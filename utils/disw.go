// disw - display switcher.
// switches to the next connected display.
// if multiple monitors are on, it switches off one.

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

var flagDryrun = flag.Bool("n", false, "dryrun mode, just print the action that would happen.")

type display struct {
	name, mode string
	active     bool
	intent     bool
}

func next(xrandr string) ([]string, error) {
	var displays []display
	lines := strings.Split(xrandr, "\n")
	for i := 0; i < len(lines)-1; i++ {
		if strings.Contains(lines[i], "*") {
			d := &displays[len(displays)-1]
			d.active, d.intent = true, true
		}
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
	if len(displays) == 0 {
		return nil, fmt.Errorf("no displays found")
	}

	activeCount, lastActive, primaryScreen := 0, 0, 0
	for i, d := range displays {
		if d.active {
			lastActive, activeCount = i, activeCount+1
		}
		if d.name == "eDP-1" {
			primaryScreen = i
		}
	}

	if activeCount >= 2 {
		displays[lastActive].intent = false
	} else if activeCount == 0 {
		displays[primaryScreen].intent = true
	} else {
		displays[lastActive].intent, displays[(lastActive+1)%len(displays)].intent = false, true
	}

	var args []string
	for _, d := range displays {
		if !d.intent {
			args = append(args, "--output", d.name, "--off")
		} else {
			args = append(args, "--output", d.name, "--mode", d.mode)
		}
	}
	if slices.Contains(args, "1920x1080") {
		return args, fmt.Errorf("found resolution 1920x1080, seems wrong, not doing anything")
	}
	return args, nil
}

func run() error {
	flag.Parse()

	output, err := exec.Command("xrandr").Output()
	if err != nil {
		return err
	}

	args, err := next(string(output))
	fmt.Printf("command: xrandr %s\n", strings.Join(args, " "))
	if err != nil {
		return err
	}
	if *flagDryrun {
		fmt.Println("skipping because in dry run mode.")
		return nil
	}

	output, err = exec.Command("xrandr", args...).Output()
	fmt.Printf("%s", output)
	exec.Command("rx").Run()
	return err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v.\n", err)
		os.Exit(1)
	}
}
