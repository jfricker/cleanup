package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/jfricker/cleanup/internal/cli"
	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/execute"
	"github.com/jfricker/cleanup/internal/gitx"
	"github.com/jfricker/cleanup/internal/match"
	"github.com/jfricker/cleanup/internal/plain"
	"github.com/jfricker/cleanup/internal/rootguard"
	"github.com/jfricker/cleanup/internal/runlog"
	"github.com/jfricker/cleanup/internal/safety"
	"github.com/jfricker/cleanup/internal/scan"
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
	cfgPath  string // config file in use; "" means the built-in defaults
	stdout   io.Writer
	stderr   io.Writer
	signals  *signals
}

// env bundles everything run reads from the process, so tests can drive it through
// pipes and fakes.
type env struct {
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Interactive func() bool
	Getenv      func(string) string
	Executable  func() (string, error)
	Now         func() time.Time
	// In-process test seams for onboarding; both are empty in the shipped binary.
	GitBin string // git binary; "" resolves normally
	Home   string // home directory; "" uses the password database
}

// realEnv is the env main passes.
func realEnv() env {
	return env{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		Interactive: func() bool { return isTTY(os.Stdin) && isTTY(os.Stdout) },
		Getenv:      os.Getenv, Executable: os.Executable, Now: time.Now,
	}
}

// start says how a run begins once arguments are parsed (TRIG-1..3).
type start int

const (
	startRun         start = iota // run normally
	startHint                     // run normally with defaults, after the TRIG-3 hint
	startOnboard                  // --init: onboarding, then exit
	startOnboardOnly              // TRIG-1: automatic onboarding, then exit (never scans, TRIG-2)
)

// noConfigHint is the TRIG-3 line.
const noConfigHint = `cleanup: no config yet; run "cleanup --init" to set your project root and retired tools`

// decideStart picks the start mode (info flags have already returned). --init always
// onboards. Otherwise only the default config path being absent without -c changes
// anything: interactive runs without -f/-T/-P/-M/-l/-h/-V onboard (-d allowed); other
// runs get the TRIG-3 hint, except -l, which never hints.
func decideStart(o cli.Options, defaultExists, interactive bool) start {
	if o.Init {
		return startOnboard
	}
	if o.ConfigPath != "" || defaultExists || o.ListCategories {
		return startRun
	}
	if interactive && !o.Force && !o.NoTUI && !o.PrintConfig && !o.PrintMan && !o.Help && !o.Version {
		return startOnboardOnly
	}
	return startHint
}

// rootSource says where the scan root came from (CLI-11).
type rootSource string

const (
	rootFromFlag    rootSource = "flag"
	rootFromConfig  rootSource = "config"
	rootFromDefault rootSource = "default"
)

// resolveRoot applies the precedence -r/positional > project_root > ~/Projects. A
// config-sourced root that is missing, not a directory or refused is the PROOT-5
// error; cfgFile names the config in that message.
func resolveRoot(o cli.Options, cfg *config.Config, cfgFile, home string) (string, rootSource, error) {
	if o.Root != "" {
		return o.Root, rootFromFlag, nil
	}
	if cfg.ProjectRoot == "" {
		return filepath.Join(home, "Projects"), rootFromDefault, nil
	}
	p := cfg.ProjectRoot
	fail := func(reason string) error {
		src := cfgFile
		if src == "" {
			src = "built-in defaults"
		}
		return fmt.Errorf("project_root %q from %s: %s; run \"cleanup --init\" or pass -r DIR", p, src, reason)
	}
	fi, err := os.Stat(p)
	switch {
	case err != nil:
		return "", "", fail(errors.Unwrap(err).Error())
	case !fi.IsDir():
		return "", "", fail("not a directory")
	}
	r, err := rootguard.Resolve(p, rootguard.Options{})
	if err != nil {
		var ref *rootguard.RefusedError
		if errors.As(err, &ref) {
			return "", "", fail("refused: " + ref.Reason)
		}
		return "", "", fail(err.Error())
	}
	_ = r.Close()
	return p, rootFromConfig, nil
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

func run(args []string, e env) int {
	stdout, stderr := e.Stdout, e.Stderr
	opts, err := cli.Parse(args)
	if err != nil {
		fmt.Fprintf(stderr, "cleanup: %v\nRun 'cleanup --help' for usage.\n", err)
		return exitUsage
	}
	ver := currentVersion()
	switch {
	case opts.Help:
		cli.Help(stdout, ver)
		return exitOK
	case opts.Version:
		fmt.Fprintf(stdout, "cleanup %s\n", ver)
		return exitOK
	case opts.PrintMan:
		fmt.Fprint(stdout, cli.Man(ver, manDate))
		return exitOK
	case opts.PrintConfig:
		_, _ = stdout.Write(config.Defaults())
		return exitOK
	}

	cfgPath := opts.ConfigPath
	defPath, defExists := defaultConfigPath(e.Getenv)
	if cfgPath == "" && defExists {
		cfgPath = defPath
	}
	mode := decideStart(opts, defExists, e.Interactive())
	switch mode {
	case startHint:
		fmt.Fprintln(stderr, noConfigHint)
	case startOnboard, startOnboardOnly:
		if opts.Init && !e.Interactive() {
			fmt.Fprintln(stderr, "cleanup: --init needs an interactive terminal")
			return exitUsage
		}
		return runOnboarding(opts, e, defPath, defExists, mode == startOnboardOnly)
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
		Protected: cfg.ProtectedPaths,
	}

	home, err := rootguard.Home()
	if err != nil {
		fmt.Fprintf(stderr, "cleanup: %v\n", err)
		return exitUsage
	}
	rootArg, src, err := resolveRoot(opts, cfg, cfgPath, home)
	if err != nil {
		fmt.Fprintf(stderr, "cleanup: %v\n", err)
		return exitUsage
	}
	if opts.ListCategories {
		listCategories(stdout, cfg, scanOpts, cfgPath, git.Bin(), rootArg, src)
		return exitOK
	}
	root, err := rootguard.Resolve(rootArg, rootguard.Options{})
	if err != nil {
		fmt.Fprintf(stderr, "cleanup: %v\n", err)
		return exitUsage
	}
	defer root.Close()

	useTUI := runTUI != nil && !opts.NoTUI && !opts.Force && e.Interactive()
	if opts.Mode == cli.Delete && !opts.Force && !useTUI {
		fmt.Fprintln(stderr, "cleanup: --delete without an interactive terminal needs -f/--force")
		return exitUsage
	}

	sig := newSignals()
	defer sig.stop()
	s := &session{opts: opts, cfg: cfg, root: root, git: git, scanOpts: scanOpts, cfgPath: cfgPath,
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
	// Reload the config so a protected_paths change since the scan applies (PROT-6).
	cfg, err := config.Load(s.cfgPath)
	if err != nil {
		fmt.Fprintf(s.stderr, "cleanup: config error, nothing was deleted:\n%v\n", err)
		return nil, "", exitUsage
	}
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
			r := execute.Runner{Deleter: safety.NewDeleter(res.Inspector, cfg.ProtectedPaths), Git: s.git, Log: log}
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

func listCategories(w io.Writer, cfg *config.Config, o scan.Options, cfgPath, gitBin, root string, src rootSource) {
	fmt.Fprintf(w, "Root: %s (from %s)\n", root, src)
	for _, p := range o.Protected {
		state := "missing"
		if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err == nil && fi.IsDir() {
			state = "ok"
		}
		fmt.Fprintf(w, "Protected: %s %s\n", p, state)
	}
	fmt.Fprintln(w)
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

// defaultConfigPath returns the default config location and whether it exists.
func defaultConfigPath(getenv func(string) string) (string, bool) {
	base := getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		base = filepath.Join(home, ".config")
	}
	p := filepath.Join(base, "cleanup", "config.toml")
	_, err := os.Stat(p)
	return p, err == nil
}

func isTTY(w any) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
