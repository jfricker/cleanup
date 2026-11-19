package testfix

import (
	"os"
	"path/filepath"
	"testing"
)

// R1 is the review's subsumption scenario: a retired tool directory that contains a
// fresh workspace and a locked worktree.
type R1 struct {
	Orca   string // retired tool dir, old
	Fresh  string // fresh (0 days) workspace inside it
	Locked string // locked linked worktree inside it
	Repo   string // main repo of the locked worktree (outside .orca)
}

// ScenarioR1 builds R1 under root.
func ScenarioR1(t testing.TB, root string) R1 {
	t.Helper()
	s := R1{
		Orca:   filepath.Join(root, ".orca"),
		Repo:   filepath.Join(root, "proj"),
		Fresh:  filepath.Join(root, ".orca", "workspaces", "ws1"),
		Locked: filepath.Join(root, ".orca", "workspaces", "ws2"),
	}
	Repo(t, s.Repo)
	Write(t, filepath.Join(s.Orca, "state.json"), "{}\n")
	Worktree(t, s.Repo, s.Locked, "ws2")
	Git(t, s.Repo, "worktree", "lock", "--reason", "agent running", s.Locked)
	BackdateTree(t, s.Orca, 40)
	BackdateTree(t, s.Repo, 40)
	Write(t, filepath.Join(s.Fresh, "notes.md"), "work in progress\n")
	return s
}

// CloneOpts selects which kinds of unique local work a full clone holds.
type CloneOpts struct {
	LocalCommit bool // a commit on a local-only branch
	Stash       bool // a stash entry
	IgnoredEnv  bool // an ignored .env file
}

// FullClone builds an old full clone at root/clone whose origin lives outside root,
// with the selected local work. It returns the clone path.
func FullClone(t testing.TB, root string, o CloneOpts) string {
	t.Helper()
	upstream := Repo(t, filepath.Join(t.TempDir(), "upstream"), ".env")
	clone := Clone(t, upstream, filepath.Join(root, "clone"))
	if o.LocalCommit {
		Git(t, clone, "checkout", "-q", "-b", "local-only")
		Commit(t, clone, "feature.txt", "unpushed\n", OldDays)
		Git(t, clone, "checkout", "-q", "main")
	}
	if o.Stash {
		Write(t, filepath.Join(clone, "README.md"), "edited\n")
		Git(t, clone, "stash", "-q")
	}
	if o.IgnoredEnv {
		Write(t, filepath.Join(clone, ".env"), "SECRET=1\n")
	}
	BackdateTree(t, clone, 40)
	return clone
}

// R3 is the tracked-build scenario.
type R3 struct {
	Web          string // repo with package.json
	TrackedBuild string // committed build/ dir
	Dist         string // untracked, regenerable dist/ dir
}

// ScenarioR3 builds R3 under root.
func ScenarioR3(t testing.TB, root string) R3 {
	t.Helper()
	s := R3{Web: filepath.Join(root, "web")}
	s.TrackedBuild = filepath.Join(s.Web, "build")
	s.Dist = filepath.Join(s.Web, "dist")
	Repo(t, s.Web, "dist/")
	Commit(t, s.Web, "package.json", "{}\n", OldDays)
	Commit(t, s.Web, "build/release.sh", "#!/bin/sh\n", OldDays)
	Write(t, filepath.Join(s.Dist, "bundle.js"), "x\n")
	BackdateTree(t, s.Web, 40)
	return s
}

// R4 is the retired-tool-config scenario.
type R4 struct {
	FreshRules     string // untracked .cursorrules at age 0
	FreshDir       string // untracked .cursor/ at age 0
	CommittedRules string // committed .cursorrules in an old repo
}

// ScenarioR4 builds R4 under root.
func ScenarioR4(t testing.TB, root string) R4 {
	t.Helper()
	plain := filepath.Join(root, "scratchpad")
	s := R4{
		FreshRules: filepath.Join(plain, ".cursorrules"),
		FreshDir:   filepath.Join(plain, ".cursor"),
	}
	Write(t, s.FreshRules, "rules\n")
	Write(t, filepath.Join(s.FreshDir, "rules", "a.mdc"), "x\n")
	repo := Repo(t, filepath.Join(root, "team"))
	Commit(t, repo, ".cursorrules", "team rules\n", OldDays)
	BackdateTree(t, repo, 60)
	s.CommittedRules = filepath.Join(repo, ".cursorrules")
	return s
}

// R5Config is a user config with the review's misspelled marker key.
const R5Config = `version = 1

[categories.rust]
[[categories.rust.rules]]
id = "cargo-target"
category = "Build Targets"
kind = "dir"
names = ["target"]
require_sibling_marker = ["Cargo.toml"]
`

// ScenarioR5 creates root/notrust/target/important.db with no Cargo.toml and returns
// the target dir.
func ScenarioR5(t testing.TB, root string) string {
	t.Helper()
	dir := filepath.Join(root, "notrust", "target")
	Write(t, filepath.Join(dir, "important.db"), "data\n")
	return dir
}

// Condition is one of the blocking git conditions from SPEC GIT-2.
type Condition int

// Blocking conditions, in SPEC GIT-2 order.
const (
	Uncommitted Condition = iota + 1
	Untracked
	Stashed
	DetachedUnreachable
	Locked
	LocalOnly
)

func (c Condition) String() string {
	return [...]string{"", "uncommitted", "untracked", "stash", "detached", "locked", "local-only"}[c]
}

// ConditionRepo builds an old full clone at root/name exhibiting exactly condition c.
func ConditionRepo(t testing.TB, root, name string, c Condition) string {
	t.Helper()
	upstream := Repo(t, filepath.Join(t.TempDir(), "upstream"))
	dir := Clone(t, upstream, filepath.Join(root, name))
	switch c {
	case Uncommitted:
		Write(t, filepath.Join(dir, "README.md"), "changed\n")
	case Untracked:
		Write(t, filepath.Join(dir, "new.txt"), "new\n")
	case Stashed:
		Write(t, filepath.Join(dir, "README.md"), "changed\n")
		Git(t, dir, "stash", "-q")
	case DetachedUnreachable:
		Git(t, dir, "checkout", "-q", "--detach")
		Commit(t, dir, "orphan.txt", "x\n", OldDays)
	case Locked:
		Write(t, filepath.Join(dir, ".git", "index.lock"), "")
	case LocalOnly:
		Commit(t, dir, "local.txt", "x\n", OldDays)
	}
	BackdateTree(t, dir, 40)
	return dir
}

// LinkedUnpushed builds repo root/main-repo with an old linked worktree at
// root/wt-unpushed whose branch has one commit not on any remote. It returns
// (repo, worktree).
func LinkedUnpushed(t testing.TB, root string) (string, string) {
	t.Helper()
	upstream := Repo(t, filepath.Join(t.TempDir(), "upstream"))
	repo := Clone(t, upstream, filepath.Join(root, "main-repo"))
	wt := filepath.Join(root, "wt-unpushed")
	Git(t, repo, "worktree", "add", "-q", "--track", "-b", "feat", wt, "origin/main")
	Commit(t, wt, "feat.txt", "x\n", OldDays)
	BackdateTree(t, wt, 40)
	BackdateTree(t, repo, 40)
	return repo, wt
}

// RepoWithStashAndWorktree builds a repo with a stash in its main worktree and an old,
// clean linked worktree. It returns (repo, worktree).
func RepoWithStashAndWorktree(t testing.TB, root string) (string, string) {
	t.Helper()
	upstream := Repo(t, filepath.Join(t.TempDir(), "upstream"))
	repo := Clone(t, upstream, filepath.Join(root, "stash-repo"))
	wt := filepath.Join(root, "wt-clean")
	Git(t, repo, "worktree", "add", "-q", "--track", "-b", "side", wt, "origin/main")
	Write(t, filepath.Join(repo, "README.md"), "changed\n")
	Git(t, repo, "stash", "-q")
	BackdateTree(t, wt, 40)
	BackdateTree(t, repo, 40)
	return repo, wt
}

// HostileFsmonitor builds a repo whose config runs `touch marker` as fsmonitor and
// returns (repo, marker).
func HostileFsmonitor(t testing.TB, root string) (string, string) {
	t.Helper()
	repo := Repo(t, filepath.Join(root, "evil-fsmonitor"))
	marker := filepath.Join(t.TempDir(), Name(t))
	Git(t, repo, "config", "core.fsmonitor", "touch "+marker)
	BackdateTree(t, repo, 40)
	return repo, marker
}

// HostileFilter builds a repo with a clean filter driver that runs `touch marker`,
// and a stat-dirty tracked file so git status would invoke it. Returns (repo, marker).
func HostileFilter(t testing.TB, root string) (string, string) {
	t.Helper()
	repo := filepath.Join(root, "evil-filter")
	Mkdir(t, repo)
	Git(t, repo, "init", "-q")
	Write(t, filepath.Join(repo, ".gitattributes"), "*.txt filter=evil\n")
	Write(t, filepath.Join(repo, "a.txt"), "hello\n")
	Git(t, repo, "add", "-A")
	Git(t, repo, "commit", "-q", "-m", "init")
	marker := filepath.Join(t.TempDir(), Name(t))
	Git(t, repo, "config", "filter.evil.clean", "sh -c 'touch "+marker+"; cat'")
	BackdateTree(t, repo, 40)
	Backdate(t, filepath.Join(repo, "a.txt"), 1) // stat-dirty vs index
	return repo, marker
}

// NewlineRepo builds a repo containing an untracked file whose name has a newline.
func NewlineRepo(t testing.TB, root string) (string, string) {
	t.Helper()
	repo := Repo(t, filepath.Join(root, "newline"))
	name := "line1\nline2.txt"
	Write(t, filepath.Join(repo, name), "x\n")
	return repo, name
}

// OutsideWorktree builds a repo under root with an old linked worktree outside root.
// Returns (repo, worktree).
func OutsideWorktree(t testing.TB, root string) (string, string) {
	t.Helper()
	repo := Repo(t, filepath.Join(root, "inside-repo"))
	wt := filepath.Join(Canonical(t, t.TempDir()), "outside-wt")
	Worktree(t, repo, wt, "outside")
	BackdateTree(t, wt, 40)
	return repo, wt
}

// OutsideSymlink creates outside/keep.txt outside root and a symlink at link (inside
// root) pointing to it. Returns the outside file path.
func OutsideSymlink(t testing.TB, link string) string {
	t.Helper()
	outside := filepath.Join(Canonical(t, t.TempDir()), "outside")
	Write(t, filepath.Join(outside, "keep.txt"), "keep\n")
	Mkdir(t, filepath.Dir(link))
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(outside, "keep.txt")
}
