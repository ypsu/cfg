package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"
)

var logfile = path.Join(os.Getenv("HOME"), ".tlog")
var notefile = "/tmp/.tnote"

func main() {
	// usage.
	if len(os.Args) == 2 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
		fmt.Println("tlog: timestamped logging.")
		fmt.Println("")
		fmt.Println("usage:")
		fmt.Println("  tlog: start vim to enter a note to append with the current timestamp.")
		fmt.Println("  tlog -w: start a daemon that continuously alerts while the last note is stale.")
		fmt.Println("  tlog [msg...]: appends msg to the notefile with the current timestamp.")
		return
	}

	// notifier daemon.
	if len(os.Args) == 2 && os.Args[1] == "-w" {
		cmd := exec.Command("tail", "-f", logfile)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stdout
		cmd.Start()
		const targetFreshness = 29 * time.Minute
		for {
			var sleep time.Duration
			s, err := os.Stat(logfile)
			if err != nil {
				log.Fatal(err)
			}
			now := time.Now()
			sleep = s.ModTime().Add(targetFreshness).Sub(now)
			if now.Hour() <= 8 || 1830 <= now.Hour()*100+now.Minute() {
				sleep = targetFreshness
			}
			if sleep < time.Minute {
				os.Stdout.Write([]byte{7})
				sleep = time.Minute
			}
			time.Sleep(sleep)
		}
	}

	// open the tlogfile.
	// doing it early also checks that the system is tlog-ready before starting vim.
	f, err := os.OpenFile(logfile, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	// read note via vim or args.
	var note string
	if len(os.Args) == 1 {
		os.Remove(notefile)
		cmd := exec.Command("vim", "+star", notefile)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("launch vim: %v", err)
		}
		buf, err := os.ReadFile(notefile)
		if err != nil {
			log.Fatal(err)
		}
		if len(buf) == 0 {
			log.Fatal("notefile empty.")
		}
		note = string(buf)
		fmt.Print(note)
	} else {
		note = strings.Join(os.Args[1:], " ")
	}
	note = strings.TrimSpace(note)

	// append message.
	t := time.Now().Format("2006-01-02.15:04:05")
	for _, line := range strings.Split(note, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fmt.Fprintf(f, "%s %s\n", t, line)
	}

	// clear the bell.
	exec.Command("tmux", "select-window", "-t", "5", ";", "select-window", "-l").Run()
}
