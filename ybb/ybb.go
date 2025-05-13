// ybb: Ypsu's BusyBox, a collection of random tools in one binary.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ypsu/cfg/gdsnap"
	"github.com/ypsu/cfg/makecfg"
	"github.com/ypsu/cfg/pedit"
	"github.com/ypsu/cfg/todotool"
	"github.com/ypsu/cfg/toollist"

	_ "embed"
)

//go:embed ybb.go
var ybbsrc string

func run(ctx context.Context) error {
	toollist.Tools = []toollist.Tool{
		{gdsnap.Run, "gdsnap: Google Drive SNAPshotter, manages backups."},
		{makecfg.Run, "makecfg: Sets up ~/.bin and other stuff."},
		{pedit.Run, "pedit: Edit a password protected file."},
		{todotool.Run, "todo: Print my active task queue."},
	}

	toolname := os.Args[0]
	if strings.HasSuffix(toolname, "/ybb") && len(os.Args) >= 2 {
		os.Args = os.Args[1:]
		toolname = filepath.Base(os.Args[0])
	}
	for _, tool := range toollist.Tools {
		tn, _, _ := strings.Cut(tool.Desc, ":")
		if tn == toolname {
			if err := tool.Fn(ctx); err != nil {
				return fmt.Errorf("goutils.Run tool=%s: %v", toolname, err)
			}
			return nil
		}
	}

	if len(os.Args) >= 2 {
		fmt.Fprintf(os.Stderr, "goutils.ToolNotFound tool=%s\n\n", toolname)
	}

	fmt.Fprintln(os.Stderr, strings.TrimPrefix(strings.SplitAfter(ybbsrc, "\n")[0], "// "))
	fmt.Fprintf(os.Stderr, "Available tools:\n")
	for _, tool := range toollist.Tools {
		fmt.Fprintf(os.Stderr, "%s\n", tool.Desc)
	}
	if len(os.Args) >= 2 {
		os.Exit(1)
	}
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
