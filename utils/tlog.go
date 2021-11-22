package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"syscall"
	"time"
)

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintln(out, "tlog: timestamped logging.")
	fmt.Fprintln(out, "starts vim which constantly autosaves its buffer to a logfile along with a timestamp.")
	fmt.Fprintln(out, "flags:")
	flag.PrintDefaults()
}

var logfileFlag = flag.String("l", path.Join(os.Getenv("HOME"), "rec/tlog"), "logfile to append to.")
var notefile = "/tmp/.tlognote"

func main() {
	flag.Usage = usage
	flag.Parse()

	noteFile, err := os.Create(notefile)
	if err != nil {
		log.Fatal(err)
	}
	noteFile.Close()

	ifd, err := syscall.InotifyInit()
	if err != nil {
		log.Fatal(err)
	}
	_, err = syscall.InotifyAddWatch(ifd, notefile, syscall.IN_CLOSE_WRITE)
	if err != nil {
		log.Fatal(err)
	}

	logfile, err := os.OpenFile(*logfileFlag, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}

	notechan := make(chan string)

	go func() {
		buf := [256]byte{}
		for {
			if _, err := syscall.Read(ifd, buf[:]); err != nil {
				log.Fatal(err)
			}
			notebytes, err := os.ReadFile(notefile)
			if err != nil {
				log.Fatal(err)
			}
			notechan <- string(notebytes)
		}
	}()

	go func() {
		cmd := exec.Command("vim", "-c", "autocmd TextChanged * silent write", notefile)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err = cmd.Run(); err != nil {
			log.Fatalf("vim failed: %v", err)
		}
		notechan <- "\000"
	}()

	lastnote := ""
	done := false
	for !done {
		note := <-notechan
		if note == "\000" {
			done = true
			note = ""
		}
		if note == lastnote {
			continue
		}
		t := time.Now().UTC().Format("2006-01-02.15:04:05")
		fmt.Fprintf(logfile, "%s %q\n", t, note)
		lastnote = note
	}
}
