package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"strings"
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
	const targetFreshness = 20 * time.Minute
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

func main() {
	flag.Usage = usage
	flag.Parse()

	logfile, err := os.OpenFile(*logfileFlag, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}

	go notifier()

	buf := make([]byte, 8)
	for {
		// wait for enter.
		os.Stdin.Read(buf)
		os.Stdout.WriteString("\033[A") // move the cursor back up.

		// run vim.
		os.Remove(notefile)
		cmd := exec.Command("vim", notefile)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("vim failed: %v", err)
		}

		// read note.
		notebytes, err := os.ReadFile(notefile)
		if err != nil {
			log.Fatal(err)
		}
		if len(notebytes) == 0 {
			continue
		}
		note := strings.TrimSpace(string(notebytes))

		// log note.
		t := time.Now().Format("2006-01-02.15:04:05")
		fmt.Fprintf(logfile, "%s %q\n", t, note)
		fmt.Printf("%s %s\n", t, note)
	}
}
