// muttcfg prints the configuration for mutt based on a private file.
// mutt invokes this via backticks and this picks the right config based on the name of the parent process.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

var cfgFlag = flag.String("cfg", "", "the configuration to pick. automatic if empty.")

func run() error {
	cfg := *cfgFlag
	if cfg == "" {
		parent, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", os.Getppid()))
		if err != nil {
			return err
		}
		cfg = string(bytes.TrimRight(parent, "\x00"))
	}

	cfgs, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".config/.myemails"))
	if err != nil {
		return err
	}
	var match []string
	for _, line := range strings.Split(string(cfgs), "\n") {
		line := strings.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 6 {
			return errors.New("invalid email config")
		}
		if fields[0] == cfg {
			match = fields
			break
		}
	}
	if match == nil {
		return fmt.Errorf("no config for %q", cfg)
	}

	commands := []string{
		fmt.Sprintf("set imap_user = %q", match[1]),
		fmt.Sprintf("set imap_pass = %q", match[2]),
		fmt.Sprintf("set spoolfile = %q", "="+match[3]),
		fmt.Sprintf("set from = %q", match[4]),
		fmt.Sprintf("alternates %q", match[5]),
	}
	fmt.Println(strings.Join(commands, " ; "))
	return nil
}

func main() {
	flag.Parse()
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
