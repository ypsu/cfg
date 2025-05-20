package makecfg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ypsu/cfg/toollist"
	"github.com/ypsu/gosuflow"
	"golang.org/x/sync/errgroup"
)

var errActionRejected = errors.New("makecfg.ActionRejected")

func promptedrun(ctx context.Context, cond bool, prompt string, action func() error) error {
	if !cond {
		return nil
	}
	fmt.Printf("%s [y/n] ", prompt)
	response := make(chan string)
	go func() {
		var s string
		fmt.Scan(&s)
		response <- s
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("makecfg.WaitAnswer: %v", ctx.Err())
	case r := <-response:
		if r != "y" {
			return errActionRejected
		}
		return action()
	}
}

var (
	archpkgs = []string{
		"alsa-lib",
		"gcc",
		"git",
		"go",
		"go-tools",
		"libxcursor",
		"libxss",
		"inotify-tools",
		"man-db",
		"man-pages",
		"tmux",
		"vim",
	}
	debianpkgs = []string{
		"gcc",
		"git",
		"golang-golang-x-tools",
		"inotify-tools",
		"libasound-dev",
		"libasound-dev",
		"libbsd-dev",
		"libpcap-dev",
		"libreadline-dev",
		"libssl-dev",
		"libx11-dev",
		"libxcursor-dev",
		"libxext-dev",
		"libxss-dev",
		"vim-tiny",
	}
)

type workflow struct {
	LookupDirectoriesSection                struct{}
	homedir, bindir, ddir, cfgdir, trashdir string

	InstallPackagesSection struct{}
	CloneRepoSection       struct{}
	SetupYBBSection        struct{}

	LinkDotfilesSection struct{}
	dynamicDotfiles     []string

	RegenDotfilesSection struct{}
	ClearUtilsSection    struct{}
	BuildUtilsSection    struct{}
	ClearTrashSection    struct{}
}

func (wf *workflow) InstallPackages(ctx context.Context) error {
	_, err := exec.LookPath("pacman")
	if err == nil { // if this is an archlinux
		if err := exec.CommandContext(ctx, "pacman", append([]string{"-Qi"}, archpkgs...)...).Run(); err != nil {
			args := append([]string{"pacman", "-Su", "--needed"}, archpkgs...)
			fmt.Printf("makecfg.RunCommand: sudo %s\n", strings.Join(args, " "))
			cmd := exec.CommandContext(ctx, "sudo", args...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			cmd.Run()
		}
	} else { // this is presumably debian
		if err := exec.CommandContext(ctx, "dpkg", append([]string{"-l", "--no-pager"}, debianpkgs...)...).Run(); err != nil {
			args := append([]string{"apt", "install"}, debianpkgs...)
			fmt.Printf("makecfg.RunCommand: sudo %s\n", strings.Join(args, " "))
			cmd := exec.CommandContext(ctx, "sudo", args...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			cmd.Run()
		}
	}
	return nil
}

func exists(file string) bool {
	_, err := os.Stat(file)
	return err == nil
}

func (wf *workflow) LookupDirectories(ctx context.Context) error {
	// Lookup home directory.
	wf.homedir = os.Getenv("HOME")
	if wf.homedir == "" {
		return fmt.Errorf("makecfg.HomeNotFound")
	}
	if wf.homedir[0] != '/' {
		return fmt.Errorf("makecfg.UnsupportedRelativeHome dir=%s", wf.homedir)
	}

	// Lookup or create project directory.
	wf.ddir = filepath.Join(wf.homedir, ".d")
	if !exists(wf.ddir) {
		wf.ddir = filepath.Join(wf.homedir, "d")
	}
	if err := promptedrun(ctx, wf.ddir == "", "Create ~/d?", func() error {
		if err := os.Mkdir(wf.ddir, 0755); err != nil {
			return fmt.Errorf("makecfg.MkdirD: %v", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("makecfg.CreateD: %v", err)
	}

	wf.bindir = filepath.Join(wf.homedir, ".bin")
	wf.cfgdir = filepath.Join(wf.ddir, "cfg")
	wf.trashdir = filepath.Join(wf.homedir, "cfgtrash")
	os.Mkdir(wf.bindir, 0755)
	os.Mkdir(wf.trashdir, 0755)
	return nil
}

func (wf *workflow) CloneRepo(ctx context.Context) error {
	return promptedrun(ctx, !exists(wf.cfgdir), fmt.Sprintf("Clone cfg repo into %s?", wf.cfgdir), func() error {
		cmd := exec.CommandContext(ctx, "git", "clone", "https://github.com/ypsu/cfg.git", wf.cfgdir)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("makecfg.RunGitClone: %v", err)
		}
		return nil
	})
}

func (wf *workflow) SetupYBB(ctx context.Context) error {
	ybbpath := filepath.Join(wf.bindir, "ybb")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", ybbpath, filepath.Join(wf.cfgdir, "ybb"))
	cmd.Stdout, cmd.Stderr, cmd.Dir = os.Stdout, os.Stderr, wf.cfgdir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("makecfg.BuildYBB: %v", err)
	}

	for _, tool := range toollist.Tools {
		name, _, _ := strings.Cut(tool.Desc, ":")
		if name == "makecfg" {
			// This is special case, this must always run freshly built.
			// That's achieved via a pre-existing wrapper in utils.
			continue
		}

		target := filepath.Join(wf.bindir, name)
		symlink, _ := os.Readlink(target)
		if symlink == ybbpath {
			continue
		}

		if exists(target) {
			if err := os.Rename(target, filepath.Join(wf.trashdir, name)); err != nil {
				return fmt.Errorf("makecfg.TrashYBBBinary file=%s: %v", name, err)
			}
		}
		if err := os.Symlink(ybbpath, target); err != nil {
			return fmt.Errorf("makecfg.LinkYBBBinary file=%s: %v", name, err)
		}
		fmt.Printf("makecfg.LinkedYBBBinary file=%s\n", name)
	}
	return nil
}

func (wf *workflow) LinkDotfiles(ctx context.Context) error {
	dotfiles, err := filepath.Glob(filepath.Join(wf.cfgdir, "dotfiles/*"))
	if err != nil {
		return fmt.Errorf("makecfg.GlobDotfiles: %v", err)
	}
	for _, dotfile := range dotfiles {
		base := filepath.Base(dotfile)
		if filepath.Ext(base) == ".gen" {
			// Handle this in RegenDotfiles.
			wf.dynamicDotfiles = append(wf.dynamicDotfiles, dotfile)
			continue
		}
		target := filepath.Join(wf.homedir, "."+base)
		symlink, _ := os.Readlink(target)
		if symlink == dotfile {
			continue
		}

		if exists(target) {
			if err := os.Rename(target, filepath.Join(wf.trashdir, base)); err != nil {
				return fmt.Errorf("makecfg.TrashDotfile file=%s: %v", base, err)
			}
		}
		if err := os.Symlink(dotfile, target); err != nil {
			return fmt.Errorf("makecfg.LinkDotfile file=%s: %v", base, err)
		}
		fmt.Printf("makecfg.ReplacedDotfile file=%s\n", base)
	}
	return nil
}

func (wf *workflow) RegenDotfiles(ctx context.Context) error {
	for _, dotfile := range wf.dynamicDotfiles {
		base := strings.TrimSuffix(filepath.Base(dotfile), ".gen")
		targetFile := filepath.Join(wf.homedir, "."+base)
		targetContent, _ := os.ReadFile(targetFile)

		var output bytes.Buffer
		cmd := exec.CommandContext(ctx, dotfile)
		cmd.Stdout, cmd.Stderr = &output, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("makecfg.RunDotfileGenerator file=%s: %v", base, err)
		}
		newContent := output.Bytes()
		if bytes.Equal(newContent, targetContent) {
			// The output already matches expected result.
			continue
		}

		// Show diff and set content if the user accepts it.
		if exists(targetFile) {
			diffcmd := exec.CommandContext(ctx,
				"diff", "-u",
				"--label=live/"+base, "--label=head/"+base,
				targetFile, "/dev/stdin")
			diffcmd.Stdin, diffcmd.Stdout, diffcmd.Stdout = &output, os.Stdout, os.Stderr
			diffcmd.Run()
			if err := promptedrun(ctx, true, fmt.Sprintf("Update ~/.%s?", base), func() error {
				return os.WriteFile(targetFile, newContent, 0644)
			}); err != nil {
				return fmt.Errorf("makecfg.UpdateDotfile file=%s: %v", base, err)
			}
			fmt.Printf("makecfg.UpdatedDotfile file=%s\n", base)
		} else {
			if err := os.WriteFile(targetFile, newContent, 0644); err != nil {
				return fmt.Errorf("makecfg.CreateDotfile file=%s: %v", base, err)
			}
			fmt.Printf("makecfg.CreatedDotfile file=%s\n", base)
		}
	}
	return nil
}

func (wf *workflow) ClearUtils(ctx context.Context) error {
	utils, err := filepath.Glob(filepath.Join(wf.cfgdir, "utils", "*"))
	if err != nil {
		return fmt.Errorf("makecfg.GlobUtils: %v", err)
	}
	want := make(map[string]bool, len(utils))
	for _, util := range utils {
		base := filepath.Base(util)
		want[strings.TrimSuffix(base, filepath.Ext(base))] = true
	}

	// Add ybb tools.
	want["ybb"] = true
	for _, tool := range toollist.Tools {
		name, _, _ := strings.Cut(tool.Desc, ":")
		want[name] = true
	}

	// Add misc special cases.
	want["amutt"] = true
	want["yt-dlp"] = true

	var unwanted []string
	bins, err := filepath.Glob(filepath.Join(wf.bindir, "*"))
	if err != nil {
		return fmt.Errorf("makecfg.GlobBin: %v", err)
	}
	for _, bin := range bins {
		base := filepath.Base(bin)
		if !want[base] {
			unwanted = append(unwanted, base)
		}
	}
	if len(unwanted) == 0 {
		return nil
	}
	fmt.Printf("makecfg.UnwantedBinaries: %q\n", unwanted)
	return promptedrun(ctx, true, fmt.Sprintf("Delete unwanted binaries from ~/.bin?"), func() error {
		for _, bin := range unwanted {
			if err := os.Rename(filepath.Join(wf.bindir, bin), filepath.Join(wf.trashdir, bin)); err != nil {
				return fmt.Errorf("makecfg.TrashBin binary=%s: %v", bin, err)
			}
		}
		return nil
	})
}

func (wf *workflow) BuildUtils(ctx context.Context) error {
	utils, err := filepath.Glob(filepath.Join(wf.cfgdir, "utils", "*"))
	if err != nil {
		return fmt.Errorf("makecfg.GlobUtils: %v", err)
	}

	type buildcmd struct {
		name string
		args []string
	}
	var buildcmds []buildcmd
	for _, util := range utils {
		base := filepath.Base(util)
		if strings.Contains(base, "_test") {
			continue
		}
		ext := filepath.Ext(base)
		if ext == "" {
			// This should be symlinked.
			target := filepath.Join(wf.bindir, base)
			symlink, _ := os.Readlink(target)
			if symlink == util {
				continue
			}
			if exists(target) {
				if err := os.Rename(target, filepath.Join(wf.trashdir, base)); err != nil {
					return fmt.Errorf("makecfg.TrashUtil util=%s: %v", base, err)
				}
			}
			if err := os.Symlink(util, target); err != nil {
				return fmt.Errorf("makecfg.LinkUtil util=%s: %v", base, err)
			}
			fmt.Printf("makecfg.LinkedUtil util=%s\n", base)
			continue
		}

		target := filepath.Join(wf.bindir, strings.TrimSuffix(base, ext))
		utilInfo, err := os.Stat(util)
		if err != nil {
			return fmt.Errorf("makecfg.StatUtilSource: %v", err)
		}
		targetInfo, _ := os.Stat(target)
		if exists(target) && targetInfo.ModTime().After(utilInfo.ModTime()) {
			// Skip because target is up to date.
			continue
		}

		var args []string
		switch ext {
		case ".c":
			args = []string{
				"gcc", "-O2", "-std=c99",
				"-Wall", "-Wextra", "-Werror",
				"-o", target, util,
				"-lasound",
				"-lbsd",
				"-lcrypto",
				"-lm",
				"-lncurses",
				"-lpcap",
				"-lreadline",
				"-lrt",
				"-lssl",
				"-lX11",
				"-lXcursor",
				"-lXext",
				"-lXss",
			}
		case ".go":
			args = []string{"go", "build", "-o", target, util}
		default:
			return fmt.Errorf("makecfg.UnsupportedUtilType util=%s", base)
		}
		buildcmds = append(buildcmds, buildcmd{base, args})
	}

	errg, ctx := errgroup.WithContext(ctx)
	errg.SetLimit(runtime.NumCPU())
	for _, buildcmd := range buildcmds {
		errg.Go(func() error {
			fmt.Printf("makecfg.BuildingUtil util=%s\n", buildcmd.name)
			cmd := exec.CommandContext(ctx, buildcmd.args[0], buildcmd.args[1:]...)
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			if err := cmd.Run(); err != nil {
				if ctx.Err() == nil {
					// Print this only for the first command that failed, hence the context check.
					fmt.Printf("makecfg.BuildCommand util=%s: %v\n", buildcmd.name, strings.Join(buildcmd.args, " "))
				}
				return fmt.Errorf("makecfg.BuildUtil util=%s: %v", buildcmd.name, err)
			}
			return nil
		})
	}
	return errg.Wait()
}

func (wf *workflow) ClearTrash(ctx context.Context) error {
	trash, err := filepath.Glob(filepath.Join(wf.trashdir, "*"))
	if err != nil {
		return fmt.Errorf("makecfg.GlobTrash: %v", err)
	}
	if len(trash) == 0 {
		return os.Remove(wf.trashdir)
	}
	for i, p := range trash {
		trash[i] = filepath.Base(p)
	}
	fmt.Printf("makecfg.Trash: %q\n", trash)
	return promptedrun(ctx, true, fmt.Sprintf("Delete ~/cfgtrash?"), func() error {
		return os.RemoveAll(wf.trashdir)
	})
}

func Run(ctx context.Context) error {
	return gosuflow.Run(ctx, &workflow{})
}
