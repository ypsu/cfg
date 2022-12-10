package main

import (
	"crypto/tls"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime/quotedprintable"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"
)

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintln(out, `todo: summarizes todo, .backlog, .rems, and emails.`)
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

// example:
// input: =?utf-8?B?c3rFkWzFkSwgYmFuw6FuIGFs?= =?utf-8?Q?ma_di=C3=B3?= narancs
// result: szőlő banán alma dió narancs
func decodeRFC2047(s string) string {
	r := strings.Builder{}
	wasQuoted := false
	for _, ss := range strings.Split(s, " ") {
		space := ""
		if r.Len() > 0 {
			space = " "
		}
		if len(s) < 6 || !strings.HasPrefix(ss, "=?") || !strings.HasSuffix(ss, "?=") {
			r.WriteString(space + ss)
			continue
		}
		f := strings.Split(ss, "?")
		if len(f) != 5 {
			r.WriteString(space + ss)
			continue
		}
		var d []byte
		var err error
		if f[2] == "B" {
			d, err = base64.StdEncoding.DecodeString(f[3])
		} else if f[2] == "Q" {
			ssrd := strings.NewReader(strings.ReplaceAll(f[3], "_", " "))
			d, err = io.ReadAll(quotedprintable.NewReader(ssrd))
		} else {
			err = errors.New("invalid encoding")
		}
		if err != nil {
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

func main() {
	flag.Usage = usage
	flag.Parse()
	now := time.Now().Format("2006-01-02.15:04:05")

	// invoke the flashcard app.
	cmd := exec.Command("flashcard")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("failed to run flashcard: %v\n", err)
	}

	// process the .rems file.
	rems := []string{}
	for _, line := range strings.Split(readfile(".rems"), "\n") {
		if len(line) > 0 && line[0] != '#' && line <= now {
			rems = append(rems, line)
		}
	}

	// process the todo files.
	activetasks := []string{}
	tasks := []string{}
	tasktitle := map[string]string{}
	taskready := map[string]bool{}
	alltasks := map[string]bool{} // this includes subtasks too, the ones with . in them.
	for _, file := range []string{"todo", ".backlog"} {
		for _, line := range strings.Split(readfile(file), "\n") {
			if len(line) < 2 || line[0] != '#' || line[1] == ' ' {
				continue
			}
			tasks = append(tasks, line)
			fields := strings.Fields(line)
			t := fields[0]
			for _, f := range fields {
				if f == "b:urgent" {
					fmt.Println(line)
					return
				}
			}
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

	// check for unread blog comments.
	blogComments := strings.Split(strings.TrimSpace(readfile("/dummy/blog/comments.log")), "\n")
	var unread []string
	for _, line := range blogComments {
		line := strings.TrimSpace(line)
		if line == "# unread:" {
			unread = make([]string, 0, 2)
		}
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		if unread == nil {
			continue
		}
		var timestamp int64
		var post string
		if _, err := fmt.Sscanf(line, "%d comment %s", &timestamp, &post); err == nil {
			unread = append(unread, post)
		}
	}
	if len(unread) > 0 {
		fmt.Printf("new blog comment(s): %s\n\n", strings.Join(unread, " "))
	}

	// now check emails.
	fetchRE := regexp.MustCompile(`^\* [0-9]+ FETCH`)
	type emailcfg struct {
		user, pass, inbox string
		result            chan string
	}
	emailcfgs := []*emailcfg{}
	for _, line := range strings.Split(readfile(".config/.myemails"), "\n") {
		if len(line) == 0 {
			continue
		}
		c := &emailcfg{}
		if _, err := fmt.Sscan(line, &c.user, &c.pass, &c.inbox); err != nil {
			log.Fatalf("invalid email config: %v\n", err)
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
			summary := ""
			print := false
			for _, line := range strings.Split(string(reply), "\r\n") {
				if fetchRE.MatchString(line) {
					print = true
					if strings.Contains(line, `\Seen`) {
						summary += "    "
					} else {
						summary += "  u "
					}
				} else if len(line) == 0 {
					if print {
						print = false
						summary += "\n"
					}
				} else if strings.HasPrefix(line, "Subject: ") {
					summary += trimquotes(fmt.Sprintf("%q", decodeRFC2047(line[9:])))
				} else if print {
					summary += trimquotes(fmt.Sprintf("%q", decodeRFC2047(line)))
				}
			}
			if len(summary) > 0 {
				c.result <- fmt.Sprintf("%s:\n%s", c.user, summary)
			} else {
				c.result <- ""
			}
		}()
	}
	fmt.Printf("checking email...")
	for _, cfg := range emailcfgs {
		r := <-cfg.result
		if len(r) > 0 {
			fmt.Printf("\r\033[K")
			fmt.Println(r)
			fmt.Printf("checking email...")
		}
	}
	fmt.Printf("\r\033[K")
}
