package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"
)

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintln(out, "tlog: timestamped logging.")
	fmt.Fprintln(out, "every 20 minutes starts alerting to enter an update.")
	fmt.Fprintln(out, "just press enter to start vim, save the update, and quit.")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "flags:")
	flag.PrintDefaults()
}

var logfileFlag = flag.String("l", path.Join(os.Getenv("HOME"), ".tlog"), "logfile to append to.")
var notefile = "/tmp/.tnote"

// notifier continuously alerts whenever the last edit on the note file seems too old.
func notifier() {
	const targetFreshness = 29 * time.Minute
	for {
		var sleep time.Duration
		s, err := os.Stat(notefile)
		if err == nil {
			sleep = s.ModTime().Add(targetFreshness).Sub(time.Now())
		}
		if sleep < time.Minute {
			os.Stdout.Write([]byte{7})
			sleep = time.Minute
		}
		time.Sleep(sleep)
	}
}

// watchenter sends a true on the channel whenever the user presses enter.
func watchenter(ch chan<- bool) {
	buf := make([]byte, 8)
	for {
		os.Stdin.Read(buf)
		os.Stdout.WriteString("\033[A") // move the cursor back up.
		ch <- true
	}
}

func main() {
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() > 0 {
		// write to the notefile.
		logfile, err := os.OpenFile(notefile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Fprintln(logfile, strings.Join(flag.Args(), " "))
		logfile.Close()

		// wake the server up to append it to the tlogfile.
		exec.Command("killall", "-USR1", "tlog").Run()

		// clear the bell.
		exec.Command("tmux", "select-window", "-t", "4", ";", "select-window", "-l").Run()
		return
	}

	logfile, err := os.OpenFile(*logfileFlag, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}
	defer logfile.Close()

	go notifier()

	enterch := make(chan bool)
	go watchenter(enterch)

	sigch := make(chan os.Signal)
	signal.Notify(sigch, syscall.SIGUSR1)

	lastnote := ""
	for {
		select {
		case <-enterch:
			// run vim.
			os.Remove(notefile)
			cmd := exec.Command("vim", "+star", notefile)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := cmd.Run(); err != nil {
				log.Fatalf("vim failed: %v", err)
			}
		case <-sigch:
			// no need to do anything, just reread the file.
		}

		// read note.
		notebytes, err := os.ReadFile(notefile)
		if err != nil || len(notebytes) == 0 {
			continue
		}
		note := strings.TrimSpace(string(notebytes))
		if note == lastnote {
			fmt.Println("warning: skipped entering same note again.")
			continue
		}
		lastnote = note

		// log note.
		t := time.Now().Format("2006-01-02.15:04:05")
		for _, line := range strings.Split(note, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			fmt.Fprintf(logfile, "%s %s\n", t, line)
			fmt.Printf("%s %s\n", t, line)
		}
	}
}
