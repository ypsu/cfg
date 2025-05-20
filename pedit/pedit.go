package pedit

import (
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"crypto/cipher"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/ypsu/gosuflow"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

type workflow struct {
	backupOnly   bool
	echoPassword bool
	file         string

	LoadSection   struct{}
	oldCiphertext []byte

	GetpassSection struct{}
	password       []byte

	DeriveCipherSection struct{}
	aead                cipher.AEAD

	DecryptSection struct{}
	oldPlaintext   []byte

	EditSection  struct{}
	unchanged    bool
	newPlaintext []byte

	EncryptSection struct{}
	newCiphertext  []byte

	SaveSection   struct{}
	BackupSection struct{}
}

func echo(show bool) {
	termios := &syscall.Termios{}
	ptr := uintptr(unsafe.Pointer(termios))
	fd := os.Stdout.Fd()
	if _, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, ptr); err != 0 {
		log.Fatalf("couldn't get terminal state: %v", err)
	}
	if show {
		termios.Lflag |= syscall.ECHO
	} else {
		termios.Lflag &^= syscall.ECHO
	}
	syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCSETS, ptr)
}

func (wf *workflow) Load(ctx context.Context) (err error) {
	wf.oldCiphertext, err = os.ReadFile(wf.file)
	return
}

func (wf *workflow) Getpass(ctx context.Context) error {
	if wf.backupOnly {
		return nil
	}

	fmt.Print("pedit.EnterPassword: ")
	if !wf.echoPassword {
		echo(false)
		defer fmt.Println()
		defer echo(true)
	}
	response := make(chan []byte)
	go func() {
		var s []byte
		s, _ = bufio.NewReader(os.Stdin).ReadSlice('\n')
		if len(s) >= 1 {
			s = s[:len(s)-1]
		}
		response <- s
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("pedit.ReadPassword: %v", ctx.Err())
	case wf.password = <-response:
	}
	return nil
}

func (wf *workflow) DeriveCipher(ctx context.Context) (err error) {
	if wf.backupOnly {
		return nil
	}
	key := argon2.IDKey([]byte("tmc4~tyőDKßVWaSa"), wf.password, 2, 256<<10, 2, chacha20poly1305.KeySize)
	wf.aead, err = chacha20poly1305.NewX(key)
	return err
}

func (wf *workflow) Decrypt(ctx context.Context) error {
	if len(wf.oldCiphertext) == 0 || wf.backupOnly {
		return nil
	}

	nonce, ciphertext := wf.oldCiphertext[:wf.aead.NonceSize()], wf.oldCiphertext[wf.aead.NonceSize():]
	compressed, err := wf.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return fmt.Errorf("pedit.AuthenticatedOpen: %v", err)
	}
	rd := flate.NewReader(bytes.NewBuffer(compressed))
	if wf.oldPlaintext, err = io.ReadAll(rd); err != nil {
		return fmt.Errorf("pedit.Decompress: %v", err)
	}
	if err = rd.Close(); err != nil {
		return fmt.Errorf("pedit.CloseDecompressor: %v", err)
	}
	return nil
}

func (wf *workflow) Edit(ctx context.Context) error {
	if wf.backupOnly {
		return nil
	}
	tmpfile := "/dev/shm/peditdata"
	defer os.Remove(tmpfile)
	if err := os.WriteFile(tmpfile, wf.oldPlaintext, 0600); err != nil {
		return fmt.Errorf("pedit.WriteTmpfile: %v", err)
	}
	vimargs := []string{"-u", "NONE", "-i", "NONE", "-n", "-N", "-c", "set backspace=indent,eol,start", tmpfile}
	cmd := exec.Command("/usr/bin/vim", vimargs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pedit.RunVim: %v", err)
	}

	newtext, err := os.ReadFile(tmpfile)
	if err != nil {
		return fmt.Errorf("pedit.ReadTmpfile: %v", err)
	}
	if bytes.Compare(wf.oldPlaintext, newtext) == 0 {
		fmt.Println("pedit.NoChangeDetected (skipping re-encryption)")
		wf.unchanged = true
		return nil
	}
	wf.newPlaintext = newtext
	return nil
}

func (wf *workflow) Encrypt(ctx context.Context) error {
	if wf.unchanged || wf.backupOnly {
		return nil
	}

	buf := &bytes.Buffer{}
	wr, err := flate.NewWriter(buf, 9)
	if err != nil {
		return fmt.Errorf("pedit.CreateCompressor: %v", err)
	}
	if n, err := wr.Write(wf.newPlaintext); n != len(wf.newPlaintext) || err != nil {
		return fmt.Errorf("pedit.Compress: %v", err)
	}
	if err = wr.Close(); err != nil {
		return fmt.Errorf("pedit.CloseCompressor: %v", err)
	}

	nonce := make([]byte, wf.aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("pedit.NewNonce: %v", err)
	}
	wf.newCiphertext = wf.aead.Seal(nonce, nonce, buf.Bytes(), nil)
	return nil
}

func (wf *workflow) Save(ctx context.Context) error {
	if wf.unchanged || wf.backupOnly {
		return nil
	}
	return os.WriteFile(wf.file, wf.newCiphertext, 0644)
}

func (wf *workflow) Backup(ctx context.Context) error {
	if wf.unchanged && !wf.backupOnly {
		return nil
	}
	succeeded := 0
	destinations, _ := os.ReadFile(wf.file + ".backup")
	for _, destination := range strings.Fields(string(destinations)) {
		fmt.Printf("pedit.Backup dst=%v\n", destination)
		cmd := exec.CommandContext(ctx, "scp", wf.file, destination)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("pedit.BackupFailed dst=%v: %v\n", destination, err)
			continue
		}
		succeeded++
	}
	if succeeded == 0 {
		fmt.Printf("pedit.UpdatedWithoutBackup\n")
	}
	return nil
}

func Run(ctx context.Context) error {
	wf := &workflow{}
	flag.BoolVar(&wf.backupOnly, "b", false, "Skip the view/update step, run the backup only.")
	flag.BoolVar(&wf.echoPassword, "e", false, "Echo the password when entering it.")
	flag.StringVar(&wf.file, "f", filepath.Join(os.Getenv("HOME"), ".contacts"), "File with the encrypted content to view/update.")
	flag.Parse()
	return gosuflow.Run(ctx, wf)
}
