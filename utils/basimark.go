// Command basimark can convert between markdown and HTML representations.
// Does nothing if it detects that the input is the right format already.
//
// It can also watch a file and continuously serve it over the web.
// In web mode while basimark polls the file, the web client uses blocking connections
// to keep the traffic to minimum.
// It's implemented by two handlers:
// /preview (meant for the user) and /content (meant as an implementation detail).
// The /content has a timestamp on the first line
// which when passed as a ts parameter to /content,
// the handler will then block until the next update in the file.
// Rest of the /content handler is the generated HTML.
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

// autolinks is something like "doc|go|groups|oncall|sheets|who".
// Those will be autolinkified if they are followed by a slash.
// Leave it empty if no autolinkification is needed.
func toHTML(inputbuf []byte, autolinks []byte) []byte {
	output := &bytes.Buffer{}

	// Escape HTML characters.
	input := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#039;").Replace(string(inputbuf))

	// Linkify links.
	if len(autolinks) == 0 {
		autolinks = []byte("__autolink_placeholder__")
	}
	re := regexp.MustCompile(`\b((http(s)?://([-.a-z0-9]+)/?)|(` + string(autolinks) + `)/)(\S*)?\b`)
	input = re.ReplaceAllString(input, "<a href='http$3://$4$5/$6'>$0</a>")

	// Add a some styling.
	output.WriteString("<div style=max-width:50em>")

	// HTMLize the input markdown.
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
		} else if strings.HasPrefix(line, "# ") {
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
	return append(bytes.TrimRight(output.Bytes(), "\n"), []byte("</div>\n")...)
}

func toMarkdown(inputbuf []byte) []byte {
	// Uncomment all hidden markers (- for lists, > for quotes).
	re := regexp.MustCompile("<!-- ([^ ]*) -->")
	outputbuf := re.ReplaceAll(inputbuf, []byte("$1 "))

	// Remove HTML tags.
	re = regexp.MustCompile("<[^>]*>")
	outputbuf = re.ReplaceAll(outputbuf, []byte(""))

	// Remove spurious newlines.
	re = regexp.MustCompile("\n\n\n+")
	outputbuf = re.ReplaceAll(append(bytes.TrimSpace(outputbuf), '\n'), []byte("\n\n"))

	// Restore escaped characters.
	output := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", "\"", "&#039;", "'").Replace(string(outputbuf))
	output = strings.ReplaceAll(output, "&amp;", "&")

	return []byte(output)
}

func handlePreview(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte(`<head><title>basimark</title>
<meta name="viewport" content="width=device-width, initial-scale=1"></head>
<body><div id=hcontent></div>
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

func main() {
	// Set up flags.
	pFlag := flag.Int("p", 8080, "Port to use for the web server for -f and -t flags. The content will be on /preview.")
	rFlag := flag.Bool("r", false, "Restore the HTML back to the original text.")
	fFlag := flag.String("f", "", "File to watch and serve via web.")
	tFlag := flag.String("t", "", "Todo item to watch from my todo list.")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: basimark <input >output")
		fmt.Fprintln(os.Stderr, "input is markdown, output is html unless reversed with the -r flag.")
		fmt.Fprintln(os.Stderr, "-f and -t start a webserver instead.")
		fmt.Fprintln(os.Stderr, "Flags:")
		flag.PrintDefaults()
	}
	flag.Parse()
	if len(*tFlag) > 0 && len(*fFlag) == 0 {
		*fFlag = os.Getenv("HOME") + "/todo"
	}

	// Read autolinks if available.
	autolinks, _ := ioutil.ReadFile(os.Getenv("HOME") + "/.autolinks")
	autolinks = bytes.TrimSpace(autolinks)

	if len(*fFlag) == 0 {
		// Read input buffers.
		inputbuf, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			log.Fatal(err)
		}

		// Run the conversion.
		outbuf := inputbuf
		isHTML := bytes.HasPrefix(inputbuf, []byte("<"))
		if *rFlag && isHTML {
			outbuf = toMarkdown(inputbuf)
		} else if !*rFlag && !isHTML {
			outbuf = toHTML(inputbuf, autolinks)
		}
		ioutil.WriteFile("/dev/stdout", outbuf, 0)
		return
	}

	// Webserver stuff from now on.
	http.HandleFunc("/preview", handlePreview)
	http.HandleFunc("/content", handleContent)
	addr := fmt.Sprintf(":%d", *pFlag)
	log.Printf("preview available at %s/preview", addr)
	go func() { log.Fatal(http.ListenAndServe(addr, nil)) }()

	// Serve the content request in this file while polling the file
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
			content, err = ioutil.ReadFile(*fFlag)
			if err != nil {
				log.Fatal(err)
			}
			if len(*tFlag) > 0 {
				var curItem string
				var todoContent bytes.Buffer
				for _, line := range bytes.Split(content, []byte("\n")) {
					if bytes.HasPrefix(line, []byte("#")) {
						var item []byte
						for i := 1; i < len(line) && (unicode.IsLetter(rune(line[i])) || unicode.IsDigit(rune(line[i]))); i++ {
							item = line[1 : i+1]
						}
						if len(item) > 0 {
							curItem = string(item)
							if curItem == *tFlag {
								todoContent.Write([]byte("# "))
								todoContent.Write(line[1:])
								todoContent.WriteByte('\n')
								continue
							}
						}
					}
					if curItem == *tFlag {
						todoContent.Write(line)
						todoContent.WriteByte('\n')
					}
				}
				content = todoContent.Bytes()
			}
			content = toHTML(content, autolinks)
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
							fmt.Fprintln(r.w, "Client cancelled the request?")
							r.done <- true
							waitingRequests[i] = req
							break
						}
					}
					if !foundStaleConn {
						req.w.WriteHeader(503)
						fmt.Fprintln(req.w, "Too many pending requests.")
						req.done <- true
					}
				} else {
					waitingRequests = append(waitingRequests, req)
				}
			} else {
				req.w.WriteHeader(400)
				fmt.Fprintln(req.w, "Invalid ts (timestamp) value.")
				req.done <- true
			}
		}
	}
}
