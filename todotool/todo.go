package todotool

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"mime/quotedprintable"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintln(out, `todo: summarizes todo, .tasks, .backlog, .rems, and emails.`)
	fmt.Fprintln(out, ``)
	fmt.Fprintln(out, `todo entries have the format "#name summary [blockers]"`)
	fmt.Fprintln(out, `blockers can be the following:`)
	fmt.Fprintln(out, `- b:YYYY-MM-DD.HH:MM:SS: blocks on date, any prefix works.`)
	fmt.Fprintln(out, `- b:#name: task is blocked until #name exists.`)
	fmt.Fprintln(out, `- b:urgent: every other task is blocked until this clears.`)
	fmt.Fprintln(out, `- b:backlog: task is blocked until there's another backlog item.`)
}

func readfile(name string) string {
	file := name
	if !strings.HasPrefix(file, "/") {
		file = path.Join(os.Getenv("HOME"), name)
	}
	content, err := os.ReadFile(file)
	if err != nil {
		log.Fatalf("couldn't read %s: %v", file, err)
	}
	return string(content)
}

func writefile(name, content string) {
	fmt.Printf("rewriting %s.\n", name)
	file := name
	if !strings.HasPrefix(file, "/") {
		file = path.Join(os.Getenv("HOME"), name)
	}
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		log.Fatalf("couldn't rewrite %s: %s", file, err)
	}
}

func splitSubject(subject string) []string {
	r := make([]string, 0, 2)
	for {
		pf, sf, ok := strings.Cut(subject, "=?")
		if strings.TrimSpace(pf) != "" {
			r = append(r, pf)
		}
		if !ok {
			return r
		}
		fs := strings.SplitN(sf, "?", 4)
		if len(fs) < 4 || !strings.HasPrefix(fs[3], "=") {
			return append(r, sf)
		}
		r, subject = append(r, "=?"+strings.Join(fs[:3], "?")+"?="), fs[3][1:]
	}
}

// examples:
// input: =?utf-8?B?c3rFkWzFkSwgYmFuw6FuIGFs?= =?utf-8?Q?ma_di=C3=B3?= = =?utf-8?Q?al ma?= narancs
// result: szőlő, banán alma dió = al ma narancs
// input: =?UTF-8?Q?=F0=9F=93=86_Beginnen_Sie_das_Jahr_2025_mit_einem_strahlenden_L?= =?UTF-8?Q?=C3=A4cheln_=E2=80=93_und_mit_einer_einfachen_Terminbuchung!?=
// result: 📆 Beginnen Sie das Jahr 2025 mit einem strahlenden Lächeln – und mit einer einfachen Terminbuchung!
// input: =?utf-8?Q?V=C3=A1ltson Digit=C3=A1lis =C3=81llampolg=C3=A1r alkalmaz=C3=A1sra vagy =C3=9Cgyf=C3=A9lkapu+-ra, hogy elektronikusan int=C3=A9zhesse az =C3=BCgyeit!?=
// result: Váltson Digitális Állampolgár alkalmazásra vagy Ügyfélkapu+-ra, hogy elektronikusan intézhesse az ügyeit!
// input: =?iso-8859-2?Q?Fw:_Fi=F3k_megsz=FBn=E9se_/_t=F6rl=E9se_-_Account_removing?=
// result: Fw: Fiók megszûnése / törlése - Account removing
func decodeRFC2047(s string) string {
	r := strings.Builder{}
	wasQuoted := false
	for _, ss := range splitSubject(s) {
		ss = strings.TrimSpace(ss)
		space := ""
		if r.Len() > 0 {
			space = " "
		}
		if len(s) < 6 || !strings.HasPrefix(ss, "=?") || !strings.HasSuffix(ss, "?=") {
			wasQuoted = false
			r.WriteString(space + ss)
			continue
		}
		f := strings.Split(ss, "?")
		if len(f) != 5 {
			wasQuoted = false
			r.WriteString(space + ss)
			continue
		}
		var d []byte
		var err error
		if f[2] == "B" {
			d, err = base64.StdEncoding.DecodeString(f[3])
		} else if f[2] == "q" || f[2] == "Q" {
			ssrd := strings.NewReader(strings.ReplaceAll(f[3], "_", " "))
			d, err = io.ReadAll(quotedprintable.NewReader(ssrd))
		} else {
			err = errors.New("invalid encoding")
		}
		if strings.Contains(f[1], "8859") { // iso-8859 support
			var nd []byte
			for _, ch := range d {
				if ch < 128 {
					nd = append(nd, ch)
				} else {
					nd = utf8.AppendRune(nd, rune(ch))
				}
			}
			d = nd
		}
		if err != nil {
			wasQuoted = false
			r.WriteString(space + ss)
		} else {
			if wasQuoted {
				// from https://datatracker.ietf.org/doc/html/rfc2047#section-6.2:
				// when displaying a particular header field that contains multiple
				// 'encoded-word's, any 'linear-white-space' that separates a pair of
				// adjacent 'encoded-word's is ignored.
				space = ""
			}
			r.WriteString(space + string(d))
			wasQuoted = true
		}
	}
	return r.String()
}

func trimquotes(s string) string {
	return s[1 : len(s)-1]
}

// normalizedate normalizes "yyyymmdd" into a valid range.
func normalizedate(s string, item string) string {
	if len(s) < 8 {
		return s
	}
	var yyyy, mm, dd int
	if _, err := fmt.Sscanf(s, "%4d%2d%2d", &yyyy, &mm, &dd); err != nil {
		return s
	}
	t := time.Date(yyyy, time.Month(mm), dd, 0, 0, 0, 0, time.Local)
	norm := t.Format("20060102")
	if norm == s[:8] {
		return s
	}
	fmt.Printf("normalizing %q to %s (%s).\n", item, norm, t.Format("Mon"))
	return norm + s[8:]
}

func Run(ctx context.Context) error {
	// prefer running wtodo if available.
	if p, err := exec.LookPath("wtodo"); err == nil {
		cmd := exec.Command(p, os.Args[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		cmd.Run()
		return nil
	}

	flag.Usage = usage
	flag.Parse()
	now := time.Now().Format("20060102.150405")

	// invoke the flashcard app.
	cmd := exec.Command("flashcard")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("failed to run flashcard: %v\n", err)
	}

	// process the .rems file.
	rems := []string{}
	oldrems := readfile(".rems")
	newremsB := strings.Builder{}
	newremsB.Grow(len(oldrems))
	for _, line := range strings.Split(oldrems, "\n") {
		norm := line
		if len(line) >= 8 && line[0] != '#' {
			norm = normalizedate(line, line)
		}
		if len(norm) > 0 && norm[0] != '#' && norm <= now {
			rems = append(rems, norm)
		}
		newremsB.WriteString(norm)
		newremsB.WriteByte('\n')
	}
	newrems := strings.TrimSpace(newremsB.String()) + "\n"
	if newrems == "\n" {
		newrems = ""
	}
	if strings.TrimSpace(newrems) != strings.TrimSpace(oldrems) {
		writefile(".rems", newrems)
	}

	// process the todo files.
	activetasks := []string{}
	tasks := []string{}
	tasktitle := map[string]string{}
	taskready := map[string]bool{}
	alltasks := map[string]bool{} // this includes subtasks too, the ones with . in them.
	for _, file := range []string{"todo", ".tasks", ".backlog"} {
		oldcontent := readfile(file)
		newcontentB := strings.Builder{}
		newcontentB.Grow(len(oldcontent))
		for _, line := range strings.Split(readfile(file), "\n") {
			if len(line) < 2 || line[0] != '#' || line[1] < '0' {
				newcontentB.WriteString(line)
				newcontentB.WriteByte('\n')
				continue
			}
			fields := strings.Split(line, " ")
			t := fields[0]
			for i, f := range fields {
				if f == "b:urgent" {
					fmt.Println(line)
					return nil
				}
				if strings.HasPrefix(f, "b:20") {
					fields[i] = "b:" + normalizedate(f[2:], line)
				}
			}
			line = strings.TrimSpace(strings.Join(fields, " "))
			tasks = append(tasks, line)
			newcontentB.WriteString(line)
			newcontentB.WriteByte('\n')
			if _, ok := alltasks[t]; ok {
				fmt.Printf("error: %s is duplicated\n", t)
			}
			alltasks[t] = true
			if strings.IndexByte(t, '.') != -1 {
				continue
			}
			taskready[t] = file == "todo"
			tasktitle[t] = line
		}
		newcontent := strings.TrimSpace(newcontentB.String()) + "\n"
		if newcontent == "\n" {
			newcontent = ""
		}
		if strings.TrimSpace(newcontent) != strings.TrimSpace(oldcontent) {
			writefile(file, newcontent)
		}
	}
	for t := range alltasks {
		n, oldt := strings.LastIndexByte(t, '.'), ""
		for n != -1 {
			t, oldt = t[:n], t
			if _, exists := alltasks[t]; !exists {
				fmt.Printf("error: the parent for %s does not exist\n", oldt)
			}
			n = strings.LastIndexByte(t, '.')
		}
	}
	hadbacklog := false
	for _, title := range tasks {
		task := strings.Fields(title)[0]
		ready, hadblocker, isbacklog := true, false, false
		for _, token := range strings.Fields(title) {
			if !strings.HasPrefix(token, "b:") {
				continue
			}
			hadblocker = true
			blocker := token[2:]
			if strings.HasPrefix(blocker, "20") {
				if blocker > now {
					ready = false
				}
			} else if strings.HasPrefix(blocker, "#") {
				if _, ok := taskready[blocker]; ok {
					ready = false
				}
			} else if blocker == "backlog" {
				isbacklog = true
			} else {
				fmt.Printf("invalid blocker %q in %s\n", blocker, task)
			}
		}
		if hadblocker {
			if ready && isbacklog {
				ready = !hadbacklog
				hadbacklog = true
			}
			taskready[task] = ready
		}
		if taskready[task] {
			activetasks = append(activetasks, tasktitle[task])
		}
	}

	// print the reminders and tasks.
	if len(rems) > 0 {
		fmt.Printf("reminders:\n  %s\n\n", strings.Join(rems, "\n  "))
	}
	if len(activetasks) > 0 {
		fmt.Printf("tasks:\n  %s\n\n", strings.Join(activetasks, "\n  "))
	}

	// Check for events on my blog.
	eventzch := make(chan string, 1)
	go func() {
		err := func() error {
			cookieData, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".config/.iio"))
			if err != nil {
				return fmt.Errorf("todo.ReadCookieFile: %v", err)
			}
			cookies := strings.Fields(string(cookieData))
			if len(cookies) == 0 {
				return fmt.Errorf("todo.EmptyCookieFile file=%s", filepath.Join(os.Getenv("HOME"), ".config/.iio"))
			}
			ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, "GET", "https://iio.ie/eventz", nil)
			if err != nil {
				return fmt.Errorf("todo.NewEventzRequest: %v", err)
			}
			request.AddCookie(&http.Cookie{Name: "session", Value: cookies[0]})
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				return fmt.Errorf("todo.DoEventzRequest: %v", err)
			}
			body, err := io.ReadAll(response.Body)
			if err := response.Body.Close(); err != nil {
				return fmt.Errorf("todo.CloseEventzBody: %v", err)
			}
			if err != nil {
				return fmt.Errorf("todo.ReadEventzBody: %v", err)
			}
			if response.StatusCode != http.StatusOK {
				return fmt.Errorf("todo.CheckEventzStatus status=%q: %s", response.Status, bytes.TrimSpace(body))
			}
			lines := strings.Split(string(bytes.TrimSpace(body)), "\n")
			if len(lines) == 2 {
				eventzch <- ""
				return nil
			}
			eventzch <- fmt.Sprintf("blog.Eventz:\n  %s\n\n", html.UnescapeString(strings.Join(lines[1:len(lines)-1], "\n  ")))
			return nil
		}()
		if err != nil {
			eventzch <- fmt.Sprintf("todo.EventzCheckFailed: %v\n", err)
		}
	}()

	// now check emails.
	fetchRE := regexp.MustCompile(`^\* [0-9]+ FETCH`)
	type emailcfg struct {
		user, pass, inbox string
		result            chan string
	}
	emailcfgs := []*emailcfg{}
	for _, line := range strings.Split(readfile(".config/.myemails"), "\n") {
		line := strings.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 4 {
			log.Fatalf("invalid email config")
		}
		c := &emailcfg{
			user:  fields[1],
			pass:  fields[2],
			inbox: fields[3],
		}
		c.result = make(chan string)
		emailcfgs = append(emailcfgs, c)
		go func() {
			conn, err := tls.Dial("tcp", "imap.gmail.com:993", nil)
			if err != nil {
				c.result <- fmt.Sprintf("connect error for %s\n", c.user)
				return
			}
			request := fmt.Sprintf("a0 login %s %s\r\n", c.user, c.pass)
			request += fmt.Sprintf("a1 select %s\r\n", c.inbox)
			request += fmt.Sprintf("a2 fetch 1:99 (flags body.peek[header.fields (subject)])\r\n")
			request += fmt.Sprintf("a3 logout\r\n")
			if n, err := conn.Write([]byte(request)); n != len(request) || err != nil {
				c.result <- fmt.Sprintf("failed writing to %s: %v\n", c.user, err)
				return
			}
			reply, err := io.ReadAll(conn)
			if err != nil {
				c.result <- fmt.Sprintf("failed reading from %s: %v\n", c.user, err)
				return
			}
			titles := map[string]bool{}
			var title string
			var seen bool
			for _, line := range strings.Split(string(reply), "\r\n") {
				if fetchRE.MatchString(line) {
					if strings.Contains(line, `\Seen`) {
						seen = true
					}
				} else if len(line) == 0 {
					if title != "" {
						title = strings.TrimPrefix(strings.TrimSpace(decodeRFC2047(title)), "Re: ")
						v, ok := titles[title]
						if !ok {
							titles[title] = seen
						} else if v && !seen {
							titles[title] = false
						}
						title, seen = "", false
					}
				} else if strings.HasPrefix(line, "Subject: ") {
					title = " " + strings.TrimSpace(trimquotes(fmt.Sprintf("%q", line[9:])))
				} else if title != "" {
					title += " " + strings.TrimSpace(trimquotes(fmt.Sprintf("%q", line)))
				}
			}
			if len(titles) > 0 {
				sortedTitles := make([]string, 0, len(titles))
				for t := range titles {
					sortedTitles = append(sortedTitles, t)
				}
				sort.Strings(sortedTitles)
				for i, t := range sortedTitles {
					if titles[t] {
						sortedTitles[i] = "    " + t
					} else {
						sortedTitles[i] = "  u " + t
					}
				}
				c.result <- fmt.Sprintf("%s:\n%s\n", c.user, strings.Join(sortedTitles, "\n"))
			} else {
				c.result <- ""
			}
		}()
	}

	fmt.Printf("checking blog eventz...")
	blogeventz := <-eventzch
	fmt.Printf("\r\033[K%schecking email...", blogeventz)
	for _, cfg := range emailcfgs {
		r := <-cfg.result
		if len(r) > 0 {
			fmt.Printf("\r\033[K%s\nchecking email...", r)
		}
	}
	fmt.Printf("\r\033[K")
	return nil
}
