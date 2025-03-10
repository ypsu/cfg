package main

import (
	"bufio"
	"bytes"
	"compress/flate"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"syscall"
	"unsafe"
)

var echoFlag = flag.Bool("e", false, "echo the password.")

func echo(show bool) {
	if *echoFlag {
		return
	}
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

func main() {
	flag.Parse()

	// read data.
	filename := path.Join(os.Getenv("HOME"), ".contacts")
	ciphertext, err := os.ReadFile(filename)
	if err != nil {
		log.Fatalf("couldn't load datafile: %v", err)
	}

	// get password.
	fmt.Println("enter password:")
	echo(false)
	pw, err := bufio.NewReader(os.Stdin).ReadString('\n')
	echo(true)
	if err != nil {
		log.Fatalf("couldn't read password: %v", err)
	}
	pw = pw[:len(pw)-1]

	// create a cipher.
	key := sha256.Sum256([]byte(pw))
	c, err := aes.NewCipher(key[:])
	if err != nil {
		log.Fatalf("couldn't create aes cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(c)
	if err != nil {
		log.Fatalf("couldn't create gcm cipher: %v", err)
	}

	// decrypt and decompress if datafile is not empty.
	var plaintext []byte
	if len(ciphertext) > 0 {
		nonce, ciphertext := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
		compressed, err := gcm.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			log.Fatalf("couldn't decrypt: %v", err)
		}
		rd := flate.NewReader(bytes.NewBuffer(compressed))
		if plaintext, err = io.ReadAll(rd); err != nil {
			log.Fatalf("couldn't decompresss: %v", err)
		}
		if err = rd.Close(); err != nil {
			log.Fatalf("couldn't close decompressor: %v", err)
		}
	}

	// edit the decrypted file.
	tmpfile := "/dev/shm/peditdata"
	if err = os.WriteFile(tmpfile, plaintext, 0600); err != nil {
		os.Remove(tmpfile)
		log.Fatalf("couldn't write tempfile: %v", err)
	}
	vimargs := []string{
		"-u", "NONE", "-i", "NONE", "-n", "-N", "-c", "set backspace=indent,eol,start", tmpfile,
	}
	cmd := exec.Command("/usr/bin/vim", vimargs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err = cmd.Run(); err != nil {
		os.Remove(tmpfile)
		log.Fatalf("running vim failed: %v", err)
	}

	// check if there was a change.
	newtext, err := os.ReadFile(tmpfile)
	os.Remove(tmpfile)
	if err != nil {
		log.Fatalf("couldn't read tempfile: %v", err)
	}
	if bytes.Compare(newtext, plaintext) == 0 {
		fmt.Println("no changed detected, skipping re-encryption.")
		return
	}
	plaintext = newtext

	// compress.
	buf := &bytes.Buffer{}
	wr,  err := flate.NewWriter(buf, 9)
	if err != nil {
		log.Fatalf("couldn't create compressor: %v", err)
	}
	if n, err := wr.Write(plaintext); n != len(plaintext) || err != nil {
		log.Fatalf("couldn't compress: %v", err)
	}
	if err = wr.Close(); err != nil {
		log.Fatalf("couldn't close compressor: %v", err)
	}
	plaintext = buf.Bytes()

	// encrypt.
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		log.Fatalf("couldn't create a nonce: %v", err)
	}
	ciphertext = gcm.Seal(nonce, nonce, plaintext, nil)

	// write ciphertext back to the file.
	if err = os.WriteFile(filename, ciphertext, 0644); err != nil {
		log.Fatalf("couldn't rewrite the encrypted file: %v", err)
	}
	fmt.Println("done, don't forget to commit!")
}
