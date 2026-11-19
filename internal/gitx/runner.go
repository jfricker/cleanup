// Package gitx runs git as a hardened subprocess and parses its machine-readable
// output. It never decides whether anything is safe to delete; it only reports facts,
// and every failure is returned as an error so callers can fail closed.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// DefaultTimeout bounds every git invocation (SPEC GIT-1).
const DefaultTimeout = 30 * time.Second

// DefaultConcurrency caps simultaneous git processes (SPEC NFR-3).
const DefaultConcurrency = 8

// baseArgs disable repository-controlled code execution and network access for
// every call (SPEC GIT-1).
var baseArgs = []string{
	"-c", "core.fsmonitor=false",
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.untrackedCache=false",
	"-c", "protocol.allow=never",
}

// candidateBins are tried in order before falling back to PATH.
var candidateBins = []string{"/usr/bin/git", "/opt/homebrew/bin/git", "/usr/local/bin/git"}

// Options configure a Runner. Zero values select the defaults.
type Options struct {
	Bin         string        // absolute git path; resolved automatically when empty
	Timeout     time.Duration // per call
	Concurrency int           // max simultaneous git processes
	Env         []string      // base environment; os.Environ() when nil
}

// Runner executes git with a scrubbed environment, fixed hardening flags, a timeout
// and a concurrency limit. It is safe for concurrent use.
type Runner struct {
	bin     string
	env     []string
	timeout time.Duration
	sem     chan struct{}
}

// Error describes a failed git invocation.
type Error struct {
	Args     []string
	ExitCode int // -1 when the process did not exit normally
	Stderr   string
	Err      error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: exit %d: %s", strings.Join(e.Args, " "), e.ExitCode, msg)
}

func (e *Error) Unwrap() error { return e.Err }

// New builds a Runner, resolving the git binary once.
func New(o Options) (*Runner, error) {
	bin := o.Bin
	if bin == "" {
		var err error
		if bin, err = resolveBin(); err != nil {
			return nil, err
		}
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.Concurrency <= 0 {
		o.Concurrency = DefaultConcurrency
	}
	base := o.Env
	if base == nil {
		base = os.Environ()
	}
	return &Runner{
		bin:     bin,
		env:     scrubEnv(base),
		timeout: o.Timeout,
		sem:     make(chan struct{}, o.Concurrency),
	}, nil
}

func resolveBin() (string, error) {
	for _, p := range candidateBins {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	p, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("git not found: %w", err)
	}
	return p, nil
}

// scrubEnv removes every GIT_* variable (GIT_DIR and GIT_WORK_TREE override -C) and
// adds the fixed safety variables.
func scrubEnv(base []string) []string {
	out := make([]string, 0, len(base)+5)
	for _, kv := range base {
		if strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "LC_ALL=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_PAGER=cat",
		"LC_ALL=C",
	)
}

// Bin returns the resolved git binary path.
func (r *Runner) Bin() string { return r.bin }

// Run executes `git <hardening> [extra...] args...` in dir and returns stdout.
func (r *Runner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return r.run(ctx, dir, nil, args)
}

func (r *Runner) run(ctx context.Context, dir string, extra, args []string) ([]byte, error) {
	select {
	case r.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, &Error{Args: args, ExitCode: -1, Err: ctx.Err()}
	}
	defer func() { <-r.sem }()

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	full := make([]string, 0, len(baseArgs)+len(extra)+len(args))
	full = append(append(append(full, baseArgs...), extra...), args...)
	cmd := exec.CommandContext(ctx, r.bin, full...)
	cmd.Dir = dir
	cmd.Env = r.env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Kill the whole process group so helpers git spawned die too.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}
	ge := &Error{Args: args, ExitCode: -1, Stderr: stderr.String(), Err: err}
	if ctx.Err() != nil {
		ge.Err = fmt.Errorf("%w: %w", ctx.Err(), err)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.Exited() {
		ge.ExitCode = ee.ExitCode()
	}
	return stdout.Bytes(), ge
}

// exitCode returns the git exit code carried by err, or -1.
func exitCode(err error) int {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.ExitCode
	}
	return -1
}
