// print calendar on the terminal.
// i need this because `cal -3m` doesn't work on ubuntu.
// it doesn't it print it in the form i prefer anyway.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

type monthstanza [8]string

var today = time.Now().UTC().Truncate(24 * time.Hour)

var flagExtended = flag.Bool("e", false, "show extended calendar.")

func fmtmonth(year, month int) monthstanza {
	a, r := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC), monthstanza{}
	header := fmt.Sprintf("%d %s", a.Year(), a.Month())
	r[0] = fmt.Sprintf("%-21s", strings.Repeat(" ", 10-len(header)/2)+header)
	r[1] = "Mo Tu We Th Fr Sa Su "

	t := a
	for row := 2; row < 8; row++ {
		for day := time.Weekday(1); day <= 7; day++ {
			if t.Month() == a.Month() && t.Weekday() == day%7 {
				if t.Equal(today) {
					r[row] += fmt.Sprintf("\033[7m%2d\033[0m ", t.Day())
				} else {
					r[row] += fmt.Sprintf("%2d ", t.Day())
				}
				t = t.AddDate(0, 0, 1)
			} else {
				r[row] += "   "
			}
		}
	}
	return r
}

func run() error {
	flag.Parse()
	if flag.NArg() >= 2 {
		return fmt.Errorf("got %d args, want at most 1", flag.NArg())
	}
	startyear, startq := today.Year(), int(today.Month()-1)/3
	endq := startq + 2
	var y int
	fmt.Sscan(flag.Arg(0), &y)
	if y != 0 {
		startyear, startq, endq = y, 0, 4
	}
	if *flagExtended {
		startyear, startq, endq = startyear-1, 0, 12
	}
	for q := startq; q < endq; q++ {
		a, b, c, qs := fmtmonth(startyear, q*3+1), fmtmonth(startyear, q*3+2), fmtmonth(startyear, q*3+3), monthstanza{}
		for i := range qs {
			qs[i] = fmt.Sprintf("%s  %s  %s", a[i], b[i], c[i])
		}
		fmt.Println(strings.Join(qs[:], "\n"))
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
