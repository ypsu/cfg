// wlog: the "work log" parses wlog files and plots the burndown graph of the completed work.
//
// The data is in a file called "wlog" in the current or some parent directory.
// Each line in the datafile can have the following format:
//
// - # Some comment describing the high level goal.
// - YYMMDD UnitsRemaining  # Resets the current milestone's estimated work left to this number.
// - YYMMDD                 # This is a burndown entry that documents the completion of one unit of work.
// - # Milestone CamelCaseName: This special comment marks the previous milestone completed and puts a label on the plot.
//
// Tips:
//
//   - Make the milestones short and measurable. Keep task breakdowns somewhere else.
//   - Use past tense when describing the milestones.
//   - Avoid needless precision. Always round estimates to one of 3, 8, 15, 32, 63.
//   - Each burndown session is about ~30 min of effort, a pomodoro interval.
//   - Focus on completing the milestones.
//     Do the distractions after the milestone is reached (this is why the need to be short).
package wlogtool

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "embed"
)

type logentry struct {
	lineno   int
	date     time.Time
	estimate int // remaining work's estimate +1, 1 means milestone completed, 0 if it's a burndown entry
	name     string

	// Computed fields.
	estLeft   int // estimate of the remaining work at this point
	totalLeft int // the actual remaining work of the milestone
}

//go:embed wlog.go
var usageString string

func usage() {
	for line := range strings.Lines(usageString) {
		if !strings.HasPrefix(line, "//") {
			break
		}
		fmt.Fprint(flag.CommandLine.Output(), strings.TrimPrefix(strings.TrimPrefix(line, "//"), " "))
	}
	fmt.Fprint(flag.CommandLine.Output(), "\nFlags:\n")
	flag.PrintDefaults()
}

func Run(ctx context.Context) error {
	flagFile := flag.String("file", "", "The data file. Default is wlog in the current or ancestor directory.")
	flag.Usage = usage
	flag.Parse()

	// Find and read the logfile.
	var logdata string
	if *flagFile != "" {
		data, err := os.ReadFile(*flagFile)
		if err != nil {
			return fmt.Errorf("wlog.ReadDatafile: %v", err)
		}
		logdata = string(data)
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("wlog.Getwd: %v", err)
		}
		for wd != "/" {
			data, err := os.ReadFile(filepath.Join(wd, "wlog"))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("wlog.ReadFile: %v", err)
			}
			if err != nil {
				wd = filepath.Dir(wd)
				continue
			}
			logdata = string(data)
			break
		}
		if wd == "/" {
			return fmt.Errorf("wlog.LogfileNotFound: make sure you have a wlog file somewhere")
		}
	}

	output, err := plot(logdata)
	if err != nil {
		return fmt.Errorf("wlog.Plot: %v", err)
	}
	os.Stdout.WriteString(output)
	return nil
}

func plot(logdata string) (string, error) {
	// Parse the log entries.
	var entries []logentry
	var lineno int
	needEstimate, milestoneName := true, ""
	for line := range strings.Lines(logdata) {
		lineno++
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "#" && fields[1] == "Milestone" {
			milestoneName = strings.TrimSuffix(fields[2], ":")
			if len(entries) > 0 {
				e := entries[len(entries)-1]
				e.estimate = 1
				entries = append(entries, e)
			}
		}
		if fields[0][0] == '#' {
			continue
		}
		if len(fields) < 2 {
			return "", fmt.Errorf("wlog.SplitLine lineno=%d line=%q gotfields=%d: burndown entries need a comment about the work completed", lineno, line, len(fields))
		}
		if len(fields) >= 3 && fields[1][0] != '#' && fields[2][0] != '#' {
			return "", fmt.Errorf("wlog.NonComment lineno=%d line=%q: only a # comment can be after data", lineno, line)
		}
		date, err := strconv.Atoi(fields[0])
		if err != nil {
			return "", fmt.Errorf("wlog.ParseDateNumber: %v", err)
		}
		y, m, d := 2000+date/100/100, date/100%100, date%100
		e := logentry{date: time.Date(y, time.January+time.Month(m)-1, d, 0, 0, 0, 0, time.UTC)}
		if fields[1][0] != '#' {
			e.estimate, err = strconv.Atoi(fields[1])
			if err != nil {
				return "", fmt.Errorf("wlog.ParseEstimate lineno=%d line=%q: %v", lineno, line, err)
			}
			e.estimate++
			needEstimate = e.estimate == 1
			if len(entries) == 0 || entries[len(entries)-1].estimate == 1 {
				e.name = milestoneName
			}
		} else if needEstimate {
			return "", fmt.Errorf("wlog.MissingEstimate lineno=%d: need estimate here before burndown", lineno)
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("wlog.EmptyFile")
	}

	// Compute the per day stats.
	var lastdate time.Time
	var rem, left int
	for i := range entries {
		e := &entries[i]
		if e.date.Before(lastdate) {
			return "", fmt.Errorf("wlog.NonChronologicalOrder lineno=%d", e.lineno)
		}
		lastdate = e.date
		if e.estimate > 1 {
			rem = e.estimate - 1
		} else if e.estimate == 0 {
			rem--
		}
		e.estLeft = rem
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := &entries[i]
		if i+1 == len(entries) {
			left = e.estLeft
		} else if entries[i].estimate == 1 {
			left = 0
		}
		e.totalLeft = left
		if e.estimate == 0 {
			left++
		}
	}

	w := &bytes.Buffer{}
	w.WriteString("set title 'Work burndown'\n")
	w.WriteString("set terminal wxt persist enhanced font 'Noto Sans'\n")
	w.WriteString("set key below\n")
	w.WriteString("set xdata time\n")
	w.WriteString("set timefmt '%Y-%m-%d'\n")
	w.WriteString("set xlabel 'Date'\n")
	w.WriteString("set ylabel 'Units'\n")
	w.WriteString("set format x '%b %d'\n")
	w.WriteString("set xtics out\n")
	w.WriteString("set xtics time\n")
	w.WriteString("unset ytics\n")
	w.WriteString("set y2tics out\n")
	w.WriteString("set grid y2tics\n")
	w.WriteString("\n")

	for _, e := range entries {
		if e.name != "" {
			fmt.Fprintf(w, "set label %q at %q\n", e.name, e.date.Format(time.DateOnly))
		}
	}
	w.WriteString("\n")

	fmt.Fprintln(w, "$data << EOD")
	corrections := -1
	for i, e := range entries {
		if e.estimate == 1 {
			corrections = -1
		} else if e.estimate >= 2 {
			corrections++
		}
		if i+1 == len(entries) || !e.date.Equal(entries[i+1].date) {
			fmt.Fprintf(w, "%s %d %d %d\n", e.date.Format(time.DateOnly), max(0, corrections), e.estLeft, e.totalLeft)
		}
	}
	fmt.Fprintln(w, "EOD")

	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "plot \\")
	fmt.Fprintf(w, "  '$data' using 1:2 with lines axes x1y2 title 'corrections', \\\n")
	fmt.Fprintf(w, "  '$data' using 1:3 with lines axes x1y2 title 'estimated', \\\n")
	fmt.Fprintf(w, "  '$data' using 1:4 with lines axes x1y2 title 'actual', \\\n")
	return w.String(), nil
}
