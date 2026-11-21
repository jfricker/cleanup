package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jfricker/cleanup/internal/cli"
	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/gitx"
	"github.com/jfricker/cleanup/internal/onboard"
	"github.com/jfricker/cleanup/internal/plain"
	"github.com/jfricker/cleanup/internal/rootguard"
	"github.com/jfricker/cleanup/internal/scan"
	"github.com/jfricker/cleanup/internal/tui"
)

// runOnboarding runs first-time setup (--init or the TRIG-1 automatic trigger) and
// returns the exit code. It never scans afterwards, even with -d (TRIG-2).
func runOnboarding(opts cli.Options, e env, defPath string, defExists, auto bool) int {
	stderr := e.Stderr
	target := defPath
	exists := defExists
	if opts.ConfigPath != "" {
		target = opts.ConfigPath
		_, err := os.Stat(target)
		exists = err == nil
	}
	var existing []byte
	loadPath := ""
	if exists {
		loadPath = target
		var err error
		if existing, err = os.ReadFile(target); err != nil {
			fmt.Fprintf(stderr, "cleanup: %v\n", err)
			return exitUsage
		}
	}
	cfg, err := config.Load(loadPath)
	if err != nil {
		fmt.Fprintf(stderr, "cleanup: config error in %s:\n%v\nFix or move the file, then run \"cleanup --init\" again.\n", target, err)
		return exitUsage
	}
	home := e.Home
	if home == "" {
		if home, err = rootguard.Home(); err != nil {
			fmt.Fprintf(stderr, "cleanup: %v\n", err)
			return exitUsage
		}
	}
	rgOpts := rootguard.Options{Home: e.Home}
	git, err := gitx.New(gitx.Options{Bin: e.GitBin})
	if err != nil {
		fmt.Fprintf(stderr, "cleanup: %v\n", err)
		return exitUsage
	}
	rootIn := "~/Projects"
	switch {
	case opts.Root != "":
		rootIn = rootguard.Expand(opts.Root, home)
	case cfg.ProjectRoot != "":
		rootIn = cfg.ProjectRoot
	}
	retired := map[string]bool{}
	for _, t := range cfg.Retired.Tools {
		retired[t.ID] = t.Retired
	}
	sig := newSignals()
	defer sig.stop()

	flow := onboard.New(onboard.Deps{
		Home: home,
		ResolveRoot: func(path string) (string, error) {
			r, err := rootguard.Resolve(path, rgOpts)
			if err != nil {
				return "", err
			}
			defer r.Close()
			return r.Path, nil
		},
		Probe: func(ctx context.Context, root string, protected []string, emit func(scan.Event)) ([]scan.ToolFootprint, error) {
			r, err := rootguard.Resolve(root, rgOpts)
			if err != nil {
				return nil, err
			}
			defer r.Close()
			return scan.Probe(ctx, cfg, r, git, protected, emit)
		},
		PathWarning: pathWarning(e.Executable, e.Getenv("PATH"), e.Getenv("SHELL")),
		Tools:       cfg.Retired.Tools,
	}, onboard.Initial{
		Root: rootIn, Protected: cfg.ProtectedPaths, Retired: retired,
		AutoTriggered: auto, ConfigPath: target, Replacing: exists,
	})
	write := func() (string, error) {
		data, err := config.Plan(existing, flow.Changes())
		if err != nil {
			return "", err
		}
		backup, err := config.Write(target, data, e.Now())
		if err != nil {
			return "", err
		}
		return flow.Summary(target, backup), nil
	}

	var code int
	if opts.NoTUI || e.Getenv("TERM") == "dumb" {
		code, err = plain.Onboard(sig.Soft, e.Stdin, e.Stdout, flow, write)
	} else {
		code, err = tui.RunOnboard(sig.Soft, tui.OnboardDeps{Flow: flow, Write: write})
		if errors.Is(err, tui.ErrTUIUnavailable) {
			code, err = plain.Onboard(sig.Soft, e.Stdin, e.Stdout, flow, write)
		}
	}
	if err != nil && code == exitOK {
		code = exitUsage
	}
	return code
}

// pathWarning returns the ONB-5 text: "" when the executable's directory is on PATH,
// else a warning naming it and the line that fixes it for the user's shell.
func pathWarning(exe func() (string, error), path, shell string) string {
	p, err := exe()
	if err != nil || !filepath.IsAbs(p) {
		return ""
	}
	dir := filepath.Dir(p)
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	for _, ent := range filepath.SplitList(path) {
		if ent == "" {
			continue
		}
		if d, err := filepath.EvalSymlinks(ent); err == nil {
			ent = d
		}
		if ent == dir {
			return ""
		}
	}
	if filepath.Base(shell) == "fish" {
		return fmt.Sprintf("%s is not on your PATH, so \"cleanup\" will not be found. Add it with: fish_add_path %s", dir, dir)
	}
	rc := "~/.zshrc"
	if filepath.Base(shell) == "bash" {
		rc = "~/.bashrc"
	}
	return fmt.Sprintf("%s is not on your PATH, so \"cleanup\" will not be found. Add this line to %s: export PATH=\"%s:$PATH\"", dir, rc, dir)
}
