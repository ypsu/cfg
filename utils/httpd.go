package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func usage() {
	fmt.Fprintln(flag.CommandLine.Output(), `httpd - serve the local directory over http.
usage: httpd [-a addr]

flags:
`)
	flag.PrintDefaults()
}

var flagAddr = flag.String("a", ":8080", "the address to listen on.")

func localIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP
}

func publicIP() string {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp4", addr)
			},
		},
		Timeout: 2 * time.Second,
	}
	resp, err := client.Get("http://icanhazip.com")
	if err != nil {
		return fmt.Sprintf("publicip:%v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return fmt.Sprintf("publicip:%v", err)
	}
	return strings.TrimSpace(string(body))
}

func run() error {
	flag.Usage = usage
	flag.Parse()
	fsHandler := http.FileServer(http.Dir(""))
	handler := func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%q from %s.", r.Method+" "+r.URL.Path, r.RemoteAddr)
		fsHandler.ServeHTTP(w, r)
	}
	_, port, _ := strings.Cut(*flagAddr, ":")
	log.Printf("listening on %v (%v:%s, %v:%s).", *flagAddr, localIP(), port, publicIP(), port)
	return http.ListenAndServe(*flagAddr, http.HandlerFunc(handler))
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
