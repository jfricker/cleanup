package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jfricker/cleanup/internal/testfix"
)

func newRunner(t *testing.T) *Runner {
	t.Helper()
	r, err := New(Options{Env: testfix.Env(t)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

var ctx = context.Background()

func TestScrubEnv(t *testing.T) {
	env := scrubEnv([]string{"PATH=/bin", "GIT_DIR=/x", "GIT_WORK_TREE=/y", "LC_ALL=fr", "HOME=/h"})
	for _, kv := range env {
		if kv == "GIT_DIR=/x" || kv == "GIT_WORK_TREE=/y" || kv == "LC_ALL=fr" {
			t.Fatalf("leaked %s", kv)
		}
	}
	for _, want := range []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "PATH=/bin"} {
		if !slices.Contains(env, want) {
			t.Fatalf("missing %s in %v", want, env)
		}
	}
}

// AC-G6: repo-local fsmonitor never runs; parent GIT_DIR has no effect.
func TestAC_G6_HostileConfigAndEnv(t *testing.T) {
	root := testfix.Root(t)
	repo, marker := testfix.HostileFsmonitor(t, root)
	other := testfix.Repo(t, filepath.Join(root, "other"))

	r, err := New(Options{Env: append(testfix.Env(t), "GIT_DIR="+filepath.Join(other, ".git"))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Status(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if testfix.Exists(marker) {
		t.Fatal("fsmonitor command was executed")
	}
	d, err := r.Dirs(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if d.TopLevel != repo {
		t.Fatalf("GIT_DIR redirected the query: top=%s want %s", d.TopLevel, repo)
	}
}

// AC-G9: a repo-local filter driver is never executed by status.
func TestAC_G9_HostileFilter(t *testing.T) {
	repo, marker := testfix.HostileFilter(t, testfix.Root(t))
	st, err := newRunner(t).Status(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if testfix.Exists(marker) {
		t.Fatal("filter clean command was executed")
	}
	if len(st.Changed) != 0 {
		t.Fatalf("unexpected changes %v", st.Changed)
	}
}

// AC-G7: names containing newlines survive parsing.
func TestAC_G7_NewlinePath(t *testing.T) {
	repo, name := testfix.NewlineRepo(t, testfix.Root(t))
	st, err := newRunner(t).Status(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(st.Untracked, name) {
		t.Fatalf("untracked %q missing from %q", name, st.Untracked)
	}
}

func writeStub(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// AC-G1 (runner level): a failing git surfaces as an error from every query.
func TestAC_G1_StubExit128(t *testing.T) {
	r, err := New(Options{Bin: writeStub(t, "echo boom >&2; exit 128"), Env: testfix.Env(t)})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := r.Status(ctx, dir); err == nil {
		t.Fatal("Status: want error")
	}
	if _, err := r.StashCount(ctx, dir); err == nil {
		t.Fatal("StashCount: want error")
	}
	if _, err := r.HeadReachable(ctx, dir); err == nil {
		t.Fatal("HeadReachable: want error")
	}
	if _, err := r.LocalOnlyCommits(ctx, dir); err == nil {
		t.Fatal("LocalOnlyCommits: want error")
	}
	_, err = r.Tracked(ctx, dir, []string{"x"})
	var ge *Error
	if !errors.As(err, &ge) || ge.ExitCode != 128 || !strings.Contains(ge.Stderr, "boom") {
		t.Fatalf("Tracked: got %v", err)
	}
}

// AC-G2 (runner level): a hung git is killed at the timeout and reported.
func TestAC_G2_Timeout(t *testing.T) {
	r, err := New(Options{Bin: writeStub(t, "sleep 30"), Timeout: 200 * time.Millisecond, Env: testfix.Env(t)})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = r.Run(ctx, t.TempDir(), "status")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout did not kill git promptly")
	}
}

func TestQueries(t *testing.T) {
	r := newRunner(t)
	root := testfix.Root(t)

	clone := testfix.FullClone(t, root, testfix.CloneOpts{LocalCommit: true, Stash: true, IgnoredEnv: true})
	if n, err := r.LocalOnlyCommits(ctx, clone); err != nil || n != 1 {
		t.Fatalf("LocalOnlyCommits = %d, %v", n, err)
	}
	if n, err := r.StashCount(ctx, clone); err != nil || n != 1 {
		t.Fatalf("StashCount = %d, %v", n, err)
	}
	st, err := r.Status(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(st.Ignored, ".env") || len(st.Changed) != 0 || st.Head != "main" || !st.HasAB {
		t.Fatalf("status %+v", st)
	}

	det := testfix.ConditionRepo(t, root, "det", testfix.DetachedUnreachable)
	if ok, err := r.HeadReachable(ctx, det); err != nil || ok {
		t.Fatalf("HeadReachable(detached orphan) = %v, %v", ok, err)
	}
	if ok, err := r.HeadReachable(ctx, clone); err != nil || !ok {
		t.Fatalf("HeadReachable(main) = %v, %v", ok, err)
	}

	repo, wt := testfix.LinkedUnpushed(t, root)
	d, err := r.Dirs(ctx, wt)
	if err != nil || !d.Linked() {
		t.Fatalf("Dirs(linked) = %+v, %v", d, err)
	}
	if n, err := r.Unpushed(ctx, wt); err != nil || n != 1 {
		t.Fatalf("Unpushed = %d, %v", n, err)
	}
	wts, err := r.Worktrees(ctx, repo)
	if err != nil || len(wts) != 2 || wts[1].Path != wt || wts[1].Branch != "refs/heads/feat" {
		t.Fatalf("Worktrees = %+v, %v", wts, err)
	}
	if tm, err := r.HeadTime(ctx, wt); err != nil || time.Since(tm) < 80*testfix.Day {
		t.Fatalf("HeadTime = %v, %v", tm, err)
	}

	r3 := testfix.ScenarioR3(t, root)
	files, err := r.Tracked(ctx, r3.Web, []string{"build", "dist", ":(glob)*"})
	if err != nil || !slices.Equal(files, []string{"build/release.sh"}) {
		t.Fatalf("Tracked = %q, %v", files, err)
	}
}

// Fact 1 from TECH-DESIGN: ignored-only worktrees are removed without --force;
// untracked files make git refuse.
func TestWorktreeRemoveAndPrune(t *testing.T) {
	r := newRunner(t)
	root := testfix.Root(t)
	repo := testfix.Repo(t, filepath.Join(root, "r"), ".env")
	a := testfix.Worktree(t, repo, filepath.Join(root, "a"), "a")
	testfix.Write(t, filepath.Join(a, ".env"), "x")
	if err := r.WorktreeRemove(ctx, repo, a, false); err != nil {
		t.Fatalf("ignored-only remove: %v", err)
	}
	b := testfix.Worktree(t, repo, filepath.Join(root, "b"), "b")
	testfix.Write(t, filepath.Join(b, "new"), "x")
	if err := r.WorktreeRemove(ctx, repo, b, false); err == nil || !testfix.Exists(b) {
		t.Fatalf("untracked remove: err=%v exists=%v", err, testfix.Exists(b))
	}
	if err := r.Prune(ctx, repo); err != nil {
		t.Fatal(err)
	}
}
