package testfix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScenariosBuild(t *testing.T) {
	root := Root(t)

	r1 := ScenarioR1(t, root)
	if out := Git(t, r1.Repo, "worktree", "list", "--porcelain"); !strings.Contains(out, "locked") {
		t.Fatalf("R1 worktree not locked:\n%s", out)
	}

	clone := FullClone(t, root, CloneOpts{LocalCommit: true, Stash: true, IgnoredEnv: true})
	if n := Git(t, clone, "rev-list", "--count", "--branches", "--not", "--remotes"); n != "1" {
		t.Fatalf("local-only commits = %s, want 1", n)
	}
	if out := Git(t, clone, "status", "--porcelain", "--ignored"); !strings.Contains(out, "!! .env") {
		t.Fatalf(".env not ignored:\n%s", out)
	}

	r3 := ScenarioR3(t, root)
	if out := Git(t, r3.Web, "ls-files", "build"); out != "build/release.sh" {
		t.Fatalf("build not tracked: %q", out)
	}

	for c := Uncommitted; c <= LocalOnly; c++ {
		ConditionRepo(t, root, "cond-"+c.String(), c)
	}

	_, wt := LinkedUnpushed(t, root)
	if n := Git(t, wt, "rev-list", "--count", "@{upstream}..HEAD"); n != "1" {
		t.Fatalf("unpushed = %s", n)
	}
}

func TestBackdate(t *testing.T) {
	dir := t.TempDir()
	Write(t, filepath.Join(dir, "a", "b.txt"), "x")
	BackdateTree(t, dir, 10)
	fi, err := os.Stat(filepath.Join(dir, "a"))
	if err != nil {
		t.Fatal(err)
	}
	if age := time.Since(fi.ModTime()); age < 9*Day {
		t.Fatalf("dir age %v", age)
	}
}

func TestEnvHasNoParentGitVars(t *testing.T) {
	t.Setenv("GIT_DIR", "/nonexistent")
	for _, kv := range Env(t) {
		if strings.HasPrefix(kv, "GIT_DIR=") {
			t.Fatal("GIT_DIR leaked")
		}
	}
}
