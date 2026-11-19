package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"cleanup/internal/cli"
	"cleanup/internal/config"
	"cleanup/internal/execute"
	"cleanup/internal/gitx"
	"cleanup/internal/match"
	"cleanup/internal/plain"
	"cleanup/internal/rootguard"
	"cleanup/internal/runlog"
	"cleanup/internal/safety"
	"cleanup/internal/scan"
)

// Exit codes (SPEC CLI-6).
const (
	exitOK          = 0
	exitFailures    = 1
	exitUsage       = 2
	exitInterrupted = 130
)

// manDate is the date stamped into the man page.
const manDate = "2026-11-19"

// tuiRunner runs the interactive TUI; nil until the TUI package is wired in.
type tuiRunner func(s *session) int

var runTUI tuiRunner

// session carries everything a run needs once arguments, config and root are valid.
type session struct {
	opts     cli.Options
	cfg      *config.Config
	root     rootguard.Root
	git      *gitx.Runner
	scanOpts scan.Options
	cleaners []scan.Cleaner
	stdout   io.Writer
	stderr   io.Writer
	signals  *signals
}

// signals turns the first SIGINT/SIGTERM into a soft cancel (and cancels a scan) and
// the second into a hard cancel (SPEC DEL-5).
type signals struct {
	Soft, Hard       context.Context
	cancelSoft, stop context.CancelFunc
	cancelHard       context.CancelFunc
	ch               chan os.Signal
}

func newSignals() *signals {
	s := &signals{ch: make(chan os.Signal, 2)}
	s.Soft, s.cancelSoft = context.WithCancel(context.Background())
	s.Hard, s.cancelHard = context.WithCancel(context.Background())
	signal.Notify(s.ch, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	s.stop = func() { signal.Stop(s.ch); close(done) }
	go func() {
		n := 0
		for {
			select {
			case <-s.ch:
				n++
				if n == 1 {
					s.cancelSoft()
				} else {
					s.cancelHard()
				}
			case <-done:
				return
			}
		}
	}()
	return s
}

// SoftCancel and HardCancel let the TUI forward Ctrl+C key presses.
func (s *signals) SoftCancel() { s.cancelSoft() }
func (s *signals) HardCancel() { s.cancelHard() }

func run(args []string, stdout, stderr io.Writer) int {
	opts, err := cli.Parse(args)
	if err != nil {
		fmt.Fprintf(stderr, "cleanup: %v\nRun 'cleanup --help' for usage.\n", err)
		return exitUsage
	}
	switch {
	case opts.Help:
		cli.Help(stdout, version)
		return exitOK
	case opts.Version:
		fmt.Fprintf(stdout, "cleanup %s\n", version)
		return exitOK
	case opts.PrintMan:
		fmt.Fprint(stdout, cli.Man(version, manDate))
		return exitOK
	case opts.PrintConfig:
		_, _ = stdout.Write(config.Defaults())
		return exitOK
	}

	cfgPath := opts.ConfigPath
	if cfgPath == "" {
		cfgPath = defaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "cleanup: config error:\n%v\n", err)
		return exitUsage
	}
	if err := cli.ValidateIDs(opts, cfg); err != nil {
		fmt.Fprintf(stderr, "cleanup: %v\n", err)
		return exitUsage
	}
	git, err := gitx.New(gitx.Options{})
	if err != nil {
		fmt.Fprintf(stderr, "cleanup: %v\n", err)
		return exitUsage
	}
	scanOpts := scan.Options{
		Only: opts.Only, Exclude: opts.Exclude, Retired: opts.Retired, AgeDays: opts.AgeDays,
		IncludeDirty: opts.IncludeDirty, OptIns: match.OptIns{Venvs: opts.Venvs, Modules: opts.Modules},
	}
	if opts.ListCategories {
		listCategories(stdout, cfg, scanOpts, cfgPath, git.Bin())
		return exitOK
	}

	rootArg := opts.Root
	if rootArg == "" {
		u, err := user.Current()
		if err != nil {
			fmt.Fprintf(stderr, "cleanup: %v\n", err)
			return exitUsage
		}
		rootArg = filepath.Join(u.HomeDir, "Projects")
	}
	root, err := rootguard.Resolve(rootArg, rootguard.Options{})
	if err != nil {
		fmt.Fprintf(stderr, "cleanup: %v\n", err)
		return exitUsage
	}
	defer root.Close()

	useTUI := runTUI != nil && !opts.NoTUI && !opts.Force && isTTY(stdout) && isTTY(os.Stdin)
	if opts.Mode == cli.Delete && !opts.Force && !useTUI {
		fmt.Fprintln(stderr, "cleanup: --delete without an interactive terminal needs -f/--force")
		return exitUsage
	}

	sig := newSignals()
	defer sig.stop()
	s := &session{opts: opts, cfg: cfg, root: root, git: git, scanOpts: scanOpts,
		cleaners: scan.Active(cfg, scanOpts), stdout: stdout, stderr: stderr, signals: sig}
	if useTUI {
		return runTUI(s)
	}
	return runPlain(s)
}

func runPlain(s *session) int {
	p := plain.Printer{W: s.stdout, ShowSkipped: s.opts.ShowSkipped}
	res, err := scan.Run(s.signals.Soft, s.cfg, s.root, s.git, s.scanOpts, nil)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(s.stderr, "cleanup: interrupted during scan; nothing was deleted")
			return exitInterrupted
		}
		fmt.Fprintf(s.stderr, "cleanup: %v\n", err)
		return exitUsage
	}
	deleting := s.opts.Mode == cli.Delete
	eligible := p.Scan(res, s.cleaners, s.root.Path, deleting)
	if !deleting {
		return exitOK
	}
	if len(eligible) == 0 {
		fmt.Fprintln(s.stdout, "Nothing to delete.")
		return exitOK
	}
	p.Warnings(eligible)
	rep, logPath, code := s.execute(res, eligible, nil)
	if code != exitOK && rep == nil {
		return code
	}
	p.Report(*rep, logPath)
	return code
}

// execute opens the run log (aborting before any deletion if it can't, LOG-2) and
// deletes the selected verdicts. It returns the report, log path and exit code.
func (s *session) execute(res *scan.Result, selected []safety.Verdict, emit func(execute.Event)) (*execute.Report, string, int) {
	dir, err := runlog.DefaultDir()
	if err == nil {
		var log *runlog.Writer
		if log, err = runlog.Open(dir, s.root.Path); err == nil {
			defer log.Close()
			targets := make([]safety.Target, 0, len(selected))
			for _, v := range selected {
				if t, ok := v.Target(); ok {
					targets = append(targets, t)
				}
			}
			r := execute.Runner{Deleter: safety.NewDeleter(res.Inspector), Git: s.git, Log: log}
			rep := r.Execute(s.signals.Hard, s.signals.Soft, targets, res.PruneRepos, emit)
			code := exitOK
			switch {
			case rep.Interrupted:
				code = exitInterrupted
			case rep.Failures() > 0:
				code = exitFailures
			}
			return &rep, log.Path(), code
		}
	}
	fmt.Fprintf(s.stderr, "cleanup: cannot open the run log, nothing was deleted: %v\n", err)
	return nil, "", exitUsage
}

func listCategories(w io.Writer, cfg *config.Config, o scan.Options, cfgPath, gitBin string) {
	active := map[string]bool{}
	for _, c := range scan.Active(cfg, o) {
		active[c.ID] = true
	}
	state := func(id string) string {
		if active[id] {
			return "on"
		}
		return "off"
	}
	fmt.Fprintln(w, "Cleaners:")
	for _, c := range cfg.Categories {
		fmt.Fprintf(w, "  %-16s %-4s %s: %s\n", c.ID, state(c.ID), c.Label, c.Description)
	}
	fmt.Fprintf(w, "  %-16s %-4s %s: %s (age %dd)\n", config.WorktreesID, state(config.WorktreesID),
		cfg.Worktrees.Label, cfg.Worktrees.Description, cfg.Worktrees.AgeDays)
	fmt.Fprintf(w, "  %-16s %-4s %s: %s\n", config.RetiredID, state(config.RetiredID), cfg.Retired.Label, cfg.Retired.Description)
	fmt.Fprintln(w, "\nRetired tools (scanned only when retired = true or listed with -u):")
	for _, t := range cfg.Retired.Tools {
		retired := "no"
		if t.Retired || slices.Contains(o.Retired, t.ID) {
			retired = "yes"
		}
		fmt.Fprintf(w, "  %-10s retired=%-3s age=%dd  %s\n", t.ID, retired, t.AgeDays, strings.Join(append(append([]string{}, t.Dirs...), t.Files...), ", "))
	}
	if cfgPath == "" {
		cfgPath = "built-in defaults"
	}
	fmt.Fprintf(w, "\nConfig: %s\nGit: %s\n", cfgPath, gitBin)
}

func defaultConfigPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	p := filepath.Join(base, "cleanup", "config.toml")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func isTTY(w any) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
