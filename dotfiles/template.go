package main

import (
	"bufio"
	"os"
)

var in, out = bufio.NewReader(os.Stdin), bufio.NewWriter(os.Stdout)

func main() {
	defer out.Flush()

}
