package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
)

func usage() {
	fmt.Fprintln(flag.CommandLine.Output(), `httpd - serve the local directory over http.
usage: httpd [-a addr]

flags:
`)
	flag.PrintDefaults()
}

var flagAddr = flag.String("a", ":8080", "the address to listen on.")

func run() error {
	flag.Usage = usage
	flag.Parse()
	fsHandler := http.FileServer(http.Dir(""))
	handler := func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%q from %s.", r.Method+" "+r.URL.Path, r.RemoteAddr)
		fsHandler.ServeHTTP(w, r)
	}
	return http.ListenAndServe(*flagAddr, http.HandlerFunc(handler))
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
