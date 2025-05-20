// this firefox wrapper reduces firefox's unnecessary disk io.
// it does so by keeping the profile in /dev/shm and periodically syncing it back to disk.
// assumption 1: the profile's directory is a symlink to /dev/shm.
// assumption 2: the disk's directory name is .mozilla/firefox/${profile}.disk
package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func run() error {
	// no-op on most of my machines.
	if hostname, _ := os.Hostname(); hostname != "ipi" {
		cmd := exec.Command("/usr/bin/firefox", os.Args[1:]...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}

	// extract profile name.
	ffdir := filepath.Join(os.Getenv("HOME"), ".mozilla/firefox")
	profiles, err := os.ReadFile(filepath.Join(ffdir, "profiles.ini"))
	if err != nil {
		return err
	}
	var profile string
	profilePrefix := []byte("Default=")
	for _, line := range bytes.Split(profiles, []byte("\n")) {
		if bytes.HasPrefix(line, profilePrefix) {
			profile = string(line[len(profilePrefix):])
			break
		}
	}
	if profile == "" {
		return fmt.Errorf("couldn't find default profile in %s", ffdir)
	}
	diskdir := filepath.Join(ffdir, profile+".disk")
	memdir := filepath.Join("/dev/shm", profile)
	os.Mkdir(memdir, 0700) // in case this is the first run.

	// lock the /dev/shm version.
	fd, err := syscall.Open(memdir, syscall.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", memdir, err)
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("flock %s: %w", memdir, err)
	}

	// run the initial sync from disk to mem.
	log.Print("syncing to memory")
	tomemCmd := exec.Command("/usr/bin/rsync", "--delete", "-clr", diskdir+"/", memdir)
	tomemCmd.Stdout, tomemCmd.Stderr = os.Stdout, os.Stderr
	if err := tomemCmd.Run(); err != nil {
		return fmt.Errorf("disk to mem rsync: %w", err)
	}
	log.Print("sync to memory done")

	// start firefox.
	log.Print("starting firefox")
	cmd := exec.Command("/usr/bin/firefox", os.Args[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	ffdone := make(chan error)
	go func() { ffdone <- cmd.Run() }()

	// periodically sync until ff quits or the user quits this wrapper.
	sig, done := make(chan os.Signal), false
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	for !done {
		select {
		case <-time.After(4 * time.Hour):
			log.Print("triggering a periodic sync")
			break
		case <-ffdone:
			log.Print("firefox exited")
			done = true
		case s := <-sig:
			log.Print("received the %s signal", s)
			cmd.Process.Kill()
			time.Sleep(2 * time.Second)
			done = true
		}

		// sync back the memory to disk.
		log.Print("syncing to disk")
		tomemCmd := exec.Command("/usr/bin/rsync", "--delete", "-clr", memdir+"/", diskdir)
		tomemCmd.Stdout, tomemCmd.Stderr = os.Stdout, os.Stderr
		if err := tomemCmd.Run(); err != nil {
			log.Printf("error syncing back to disk: %v", err)
		}
		log.Print("sync to disk done")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
