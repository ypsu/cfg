package wlogtool

import (
	"os"
	"strings"
	"testing"

	"github.com/ypsu/efftesting"
)

func TestWlog(t *testing.T) {
	et := efftesting.New(t)

	f := func(s string) string { return efftesting.Stringify(plot(s)) }
	et.Expect("Empty", f(""), "wlog.EmptyFile")
	et.Expect("NoStartingMilestone", f("250901 # Work."), "wlog.MissingEstimate lineno=1: need estimate here before burndown")
	et.Expect("NoResetMilestone", f("250901 2\n250902 0\n250903 # Work."), "wlog.MissingEstimate lineno=3: need estimate here before burndown")
	et.Expect("OnlyInitialMilestone", f("# Milestone FirstMilestone\n250901 5"), `
		set title 'Work burndown'
		set terminal wxt persist enhanced font 'Noto Sans'
		set key below
		set xdata time
		set timefmt '%Y-%m-%d'
		set xlabel 'Date'
		set ylabel 'Units'
		set format x '%b %d'
		set xtics out
		set xtics time
		unset ytics
		set y2tics out
		set grid y2tics

		set label "FirstMilestone" at "2025-09-01"

		$data << EOD
		2025-09-01 0 5 5
		EOD

		plot \
		  '$data' using 1:2 with lines axes x1y2 title 'corrections', \
		  '$data' using 1:3 with lines axes x1y2 title 'estimated', \
		  '$data' using 1:4 with lines axes x1y2 title 'actual', \
	`)

	f = func(s string) string {
		s, err := plot(s)
		if err != nil {
			return err.Error()
		}
		r := ""
		for line := range strings.Lines(s) {
			if strings.HasPrefix(line, "set label") {
				r += line
			}
		}
		ps := strings.Split(s, "EOD")
		return r + "\n" + ps[1][1:]
	}

	et.Expect("Burndown", f(`
			# Milestone Plan
			250901 3
			250902   # Some comment.
		`), `
		set label "Plan" at "2025-09-01"

		2025-09-01 0 3 3
		2025-09-02 0 2 2
	`)

	et.Expect("Reset", f(`
			# Milestone First
			250901 3
			250902   # Some comment.
			250903   # Moar comment.
			# Milestone SecondMilestone: description.
			250904 5
			250904   # Moar comment.
		`), `
		set label "First" at "2025-09-01"
		set label "SecondMilestone" at "2025-09-04"

		2025-09-01 0 3 2
		2025-09-02 0 2 1
		2025-09-03 0 1 0
		2025-09-04 0 4 4
	`)

	et.Expect("Correction", f(`
			# Milestone First
			250901 3
			250902   # Some comment.
			# Milestone SecondMilestone: description.
			250903 5
			250903   # Moar comment.
			250904 3 # Oops.
			250904   # Work.
		`), `
		set label "First" at "2025-09-01"
		set label "SecondMilestone" at "2025-09-03"

		2025-09-01 0 3 1
		2025-09-02 0 2 0
		2025-09-03 0 4 3
		2025-09-04 1 2 2
	`)
}

func TestMain(m *testing.M) {
	os.Exit(efftesting.Main(m))
}
