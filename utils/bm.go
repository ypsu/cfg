// command basimark can convert between markdown and html representations.
// does nothing if it detects that the input is the right format already.
//
// it can also watch a file and continuously serve it over the web.
// in web mode while basimark polls the file, the web client uses blocking connections
// to keep the traffic to minimum.
// it's implemented by two handlers:
// /preview (meant for the user) and /content (meant as an implementation detail).
// the /content has a timestamp on the first line
// which when passed as a ts parameter to /content,
// the handler will then block until the next update in the file.
// rest of the /content handler is the generated html.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
)

type mode int

const (
	noneMode mode = iota
	blockquoteMode
	pMode
	preMode
	ulMode
)

func close(output *bytes.Buffer, m mode) {
	switch m {
	case blockquoteMode:
		output.WriteString("</p></blockquote>")
	case pMode:
		output.WriteString("</p>")
	case preMode:
		output.WriteString("</pre>")
	case ulMode:
		output.WriteString("</li></ul>")
	}
}

func toHTML(inputbuf []byte) []byte {
	output := &bytes.Buffer{}

	// escape html characters.
	input := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#039;").Replace(string(inputbuf))

	// linkify links.
	re := regexp.MustCompile(`\bhttp(s)?://([-.a-zßáéíóöőúüű0-9]+)(/\S*)?\b(/)?`)
	input = re.ReplaceAllString(input, "<a href='http$1://$2$3$4'>$0</a>")

	// add a some styling.
	output.WriteString("<div style=max-width:50em>")

	// htmlize the input markdown.
	m := noneMode
	for ln, line := range strings.Split(input, "\n") {
		if len(line) == 0 {
			close(output, m)
			m = noneMode
		} else if line[0] == ' ' {
			if m == noneMode {
				m = preMode
				if len(strings.TrimSpace(line)) == 0 {
					line += "<pre>"
				} else {
					output.WriteString("<pre>")
				}
			}
		} else if strings.HasPrefix(line, "- ") {
			if m == noneMode {
				m = ulMode
				output.WriteString("<ul><li>")
				line = "<!-- - -->" + strings.TrimLeft(line[2:], " ")
			} else if m == ulMode {
				output.WriteString("</li><li>")
				line = "<!-- - -->" + strings.TrimLeft(line[2:], " ")
			}
		} else if strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "## ") {
			if m != noneMode {
				log.Fatalf("line %d: # must be starting its own paragraph: %s", ln+1, line)
			}
			line = "<!-- # --><p style=font-weight:bold>" + line[2:] + "</p>"
		} else if strings.HasPrefix(line, "&gt; ") {
			if m == noneMode {
				m = blockquoteMode
				output.WriteString("<blockquote style='border-left:solid 0.25em darkgray;padding:0 0.5em;margin:1em 0'><p>")
				line = "<span style=display:none>&gt; </span>" + line[5:]
			} else if m == blockquoteMode {
				line = "<!-- &gt; -->" + line[5:]
			}
		} else if m == blockquoteMode && line == "&gt;" {
			line = "</p><p><span style=display:none>&gt;</span>"
		} else {
			if m == noneMode {
				m = pMode
				output.WriteString("<p>")
			}
		}
		output.WriteString(line)
		output.WriteByte('\n')
	}
	close(output, m)
	html := bytes.ReplaceAll(output.Bytes(), []byte("</pre>\n<pre>"), []byte("\n"))
	return append(bytes.TrimRight(html, "\n"), []byte("</div>\n")...)
}

func toMarkdown(inputbuf []byte) []byte {
	// uncomment all hidden markers (- for lists, > for quotes).
	re := regexp.MustCompile("<!-- ([^ ]*) -->")
	outputbuf := re.ReplaceAll(inputbuf, []byte("$1 "))

	// remove html tags.
	re = regexp.MustCompile("<[^>]*>")
	outputbuf = re.ReplaceAll(outputbuf, []byte(""))

	// remove spurious newlines.
	re = regexp.MustCompile("\n\n\n+")
	outputbuf = re.ReplaceAll(append(bytes.TrimSpace(outputbuf), '\n'), []byte("\n\n"))

	// restore escaped characters.
	output := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", "\"", "&#039;", "'").Replace(string(outputbuf))
	output = strings.ReplaceAll(output, "&amp;", "&")

	return []byte(output)
}

func handlePreview(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte(`<head><title>basimark</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name=color-scheme content='light dark'>
<style>body{font-family:sans-serif}</style></head>
<body><div id=hcontent>this needs javascript.</div>
<script>
	let main = async _ => {
		hcontent.innerText = "loading...";
		let ts = "";
		try {
			for (let i = 0; ; i++) {
				let resp = await fetch("/content?ts=" + ts)
				let body = await resp.text();
				let m = body.match(/^([0-9]+)\n(.*)$/s);
				ts = m[1];
				hcontent.innerHTML = m[2];
			}
		} catch (e) {
			hcontent.innerText = e;
		}
	};
	main()
</script>
</body>
`))
}

type contentRequest struct {
	w    http.ResponseWriter
	req  *http.Request
	done chan<- bool
}

var requestQueue chan<- contentRequest

func handleContent(w http.ResponseWriter, req *http.Request) {
	done := make(chan bool, 1)
	requestQueue <- contentRequest{w, req, done}
	<-done
}

func readContent(fFlag, tFlag string) ([]byte, error) {
	content, err := ioutil.ReadFile(fFlag)
	if err != nil {
		return nil, err
	}
	if tFlag != "" {
		var curItem string
		var todoContent bytes.Buffer
		for _, line := range bytes.Split(content, []byte("\n")) {
			if len(line) >= 2 && line[0] == '#' && line[1] >= '0' {
				if len(line) == 1 || line[1] < '0' || 'z' < line[1] {
					continue
				}
				var item []byte
				for i := 1; i < len(line) && !unicode.IsSpace(rune(line[i])); i++ {
					item = line[1 : i+1]
				}
				if len(item) > 0 {
					curItem = string(item)
					if curItem == tFlag {
						todoContent.Write([]byte("# "))
						todoContent.Write(line[1:])
						todoContent.WriteByte('\n')
						continue
					}
				}
			}
			if curItem == tFlag {
				todoContent.Write(line)
				todoContent.WriteByte('\n')
			}
		}
		if todoContent.Len() == 0 {
			return nil, fmt.Errorf("item %q in %q not found", tFlag, fFlag)
		}
		content = todoContent.Bytes()
	}
	return content, nil
}

func main() {
	// set up flags.
	pFlag := flag.Int("p", 8080, "port to use for the web server for -f and -t flags. the content will be on /preview.")
	qFlag := flag.Bool("q", false, "write input text back in quoted form.")
	rFlag := flag.Bool("r", false, "restore the html back to the original text.")
	fFlag := flag.String("f", "", "file to watch and serve via web.")
	tFlag := flag.String("t", "", "item to watch from my todo list or the file given.")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "bm: basic markdown is a tool that transforms or serves markdown text.")
		fmt.Fprintln(os.Stderr, "usage 1: bm <input >output")
		fmt.Fprintln(os.Stderr, "usage 2: bm [file or todo item]")
		fmt.Fprintln(os.Stderr, "input is markdown, output is html unless reversed with the -r flag.")
		fmt.Fprintln(os.Stderr, "filename/todoname, -f and -t start a webserver instead.")
		fmt.Fprintln(os.Stderr, "flags:")
		flag.PrintDefaults()
	}
	flag.Parse()

	// figure out what the user wanted.
	if len(flag.Args()) > 0 {
		if len(*tFlag) > 0 || len(*fFlag) > 0 || len(flag.Args()) > 2 {
			fmt.Fprintln(os.Stderr, "error: incorrect usage.")
			flag.Usage()
			return
		}
		if flag.NArg() == 1 {
			a := flag.Args()[0]
			if _, err := os.Stat(a); err == nil {
				*fFlag = a
			} else {
				*tFlag = a
			}
		} else if flag.NArg() == 2 {
			*fFlag = flag.Args()[0]
			*tFlag = flag.Args()[1]
		}
	}
	if len(*tFlag) > 0 && len(*fFlag) == 0 {
		*fFlag = os.Getenv("HOME") + "/todo"
	}

	if len(*fFlag) == 0 {
		// read input buffers.
		inputbuf, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			log.Fatal(err)
		}

		// run the quoting conversion.
		if *qFlag {
			fmt.Printf("%q\n", string(inputbuf))
			return
		}

		// run the markdown conversion.
		outbuf := inputbuf
		isHTML := bytes.HasPrefix(inputbuf, []byte("<"))
		if *rFlag && isHTML {
			outbuf = toMarkdown(inputbuf)
		} else if !*rFlag && !isHTML {
			outbuf = toHTML(inputbuf)
		}
		ioutil.WriteFile("/dev/stdout", outbuf, 0)
		return
	}

	if *qFlag {
		content, err := readContent(*fFlag, *tFlag)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%q\n", string(content))
		return
	}

	// webserver stuff from now on.
	http.HandleFunc("/preview", handlePreview)
	http.HandleFunc("/content", handleContent)
	addr := fmt.Sprintf(":%d", *pFlag)
	log.Printf("preview available at %s/preview", addr)
	go func() { log.Fatal(http.ListenAndServe(addr, nil)) }()

	// serve the content request in this file while polling the file
	// and unblocking the waiting requests if there are some.
	var lastMod time.Time
	var content []byte
	waitingRequests := make([]contentRequest, 0, 100)
	wq := make(chan contentRequest, 100) // WorkQueue
	requestQueue = wq
	for true {
		info, err := os.Stat(*fFlag)
		if err != nil {
			log.Fatal(err)
		}
		if info.ModTime() != lastMod {
			lastMod = info.ModTime()
			content, err = readContent(*fFlag, *tFlag)
			if err != nil {
				log.Fatal(err)
			}
			content = toHTML(content)
			for _, r := range waitingRequests {
				fmt.Fprintf(r.w, "%d\n", lastMod.UnixNano())
				r.w.Write(content)
				r.done <- true
			}
			waitingRequests = waitingRequests[:0]
		}

		select {
		case <-time.After(time.Second * 1):
		case req := <-wq:
			var ts int64
			fmt.Sscanf(req.req.FormValue("ts"), "%d", &ts)
			if lastMod.UnixNano() > ts {
				fmt.Fprintf(req.w, "%d\n", lastMod.UnixNano())
				req.w.Write(content)
				req.done <- true
			} else if lastMod.UnixNano() == ts {
				if len(waitingRequests) == cap(waitingRequests) {
					foundStaleConn := false
					for i, r := range waitingRequests {
						if r.req.Context().Err() != nil {
							foundStaleConn = true
							r.w.WriteHeader(408)
							fmt.Fprintln(r.w, "client cancelled the request?")
							r.done <- true
							waitingRequests[i] = req
							break
						}
					}
					if !foundStaleConn {
						req.w.WriteHeader(503)
						fmt.Fprintln(req.w, "too many pending requests.")
						req.done <- true
					}
				} else {
					waitingRequests = append(waitingRequests, req)
				}
			} else {
				req.w.WriteHeader(400)
				fmt.Fprintln(req.w, "invalid ts (timestamp) value.")
				req.done <- true
			}
		}
	}
}
