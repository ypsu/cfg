// disw - display switcher.
// switches to the next connected display.
// if multiple monitors are on, it switches off one.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var flagDryrun = flag.Bool("n", false, "dryrun mode, just print the action that would happen.")

type display struct {
	name, mode string
	active     bool
	intent     bool
}

func run() error {
	flag.Parse()

	output, err := exec.Command("xrandr").Output()
	if err != nil {
		return err
	}

	var displays []display
	lines := strings.Split(string(output), "\n")
	for i := 0; i < len(lines)-1; i++ {
		var name, state, mode string
		fmt.Sscan(lines[i], &name, &state)
		if state != "connected" {
			continue
		}
		fmt.Sscan(lines[i+1], &mode)
		if mode == "3840x2400" {
			// halve the resolution until i find a better way to handle high dpi.
			mode = "1920x1200"
		}
		displays = append(displays, display{
			name:   name,
			mode:   mode,
			active: strings.Contains(lines[i+1], "*"),
			intent: strings.Contains(lines[i+1], "*"),
		})
	}

	activeCount, lastActive := 0, 0
	for i, d := range displays {
		if d.active {
			lastActive, activeCount = i, activeCount+1
		}
	}

	if activeCount >= 2 {
		displays[lastActive].intent = false
	} else {
		displays[lastActive].intent, displays[(lastActive+1)%len(displays)].intent = false, true
	}

	debug := bytes.NewBuffer(make([]byte, 0, 4096))
	fmt.Fprintf(debug, "%s\n", output)

	var args []string
	for _, d := range displays {
		if !d.intent {
			args = append(args, "--output", d.name, "--off")
		} else {
			args = append(args, "--output", d.name, "--mode", d.mode)
		}
		fmt.Fprintf(debug, "%+v\n", d)
	}
	fmt.Printf("xrandr %s\ndebug data at /tmp/disw.debug\n", strings.Join(args, " "))

	fmt.Fprintf(debug, "\nxrandr %s\n", strings.Join(args, " "))
	err = os.WriteFile("/tmp/disw.debug", debug.Bytes(), 0o644)
	if err != nil {
		return fmt.Errorf("debug data write: %v", err)
	}

	if *flagDryrun {
		return nil
	}

	output, err = exec.Command("xrandr", args...).Output()
	fmt.Printf("%s", output)
	return err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
