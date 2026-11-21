package safety

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/gitx"
	"github.com/jfricker/cleanup/internal/match"
	"github.com/jfricker/cleanup/internal/rootguard"
	"github.com/jfricker/cleanup/internal/testfix"
)

var ctx = context.Background()

type env struct {
	t    *testing.T
	root rootguard.Root
	git  *gitx.Runner
	in   *Inspector
	del  *Deleter
}

func newEnv(t *testing.T, dirty bool) *env {
	return newEnvAt(t, testfix.Root(t), dirty, gitx.Options{})
}

func newEnvAt(t *testing.T, path string, dirty bool, o gitx.Options) *env {
	t.Helper()
	root, err := rootguard.Resolve(path, rootguard.Options{Home: "/nonexistent-home", SystemRules: []rootguard.Rule{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	o.Env = testfix.Env(t)
	git, err := gitx.New(o)
	if err != nil {
		t.Fatal(err)
	}
	in := NewInspector(root, git, Config{IncludeDirty: dirty, NeverTouch: []string{".git", ".wrangler"}})
	return &env{t: t, root: root, git: git, in: in, del: NewDeleter(in, nil)}
}

func (e *env) cand(kind match.Kind, path string, age int) match.Candidate {
	e.t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		e.t.Fatal(err)
	}
	c := match.Candidate{Kind: kind, Path: path, IsDir: fi.IsDir(), Strategy: config.StrategyRemove, AgeDays: age, Cleaner: "test"}
	// The enclosing git tree of the parent, as the walk would report it.
	for p := filepath.Dir(path); strings.HasPrefix(p, e.root.Path); p = filepath.Dir(p) {
		if testfix.Exists(filepath.Join(p, ".git")) {
			c.GitTree = p
			break
		}
		if p == e.root.Path {
			break
		}
	}
	return c
}

func (e *env) worktree(path, repo string, age int) match.Candidate {
	c := e.cand(match.Worktree, path, age)
	c.Repo, c.GitTree = repo, ""
	return c
}

func (e *env) inspect(cs ...match.Candidate) []Verdict {
	tr := e.in.CheckTracked(ctx, cs)
	vs := make([]Verdict, len(cs))
	for i, c := range cs {
		vs[i] = e.in.Inspect(ctx, c, tr)
	}
	return Reconcile(vs)
}

func (e *env) deleteAll(vs []Verdict) {
	for _, v := range vs {
		if t, ok := v.Target(); ok {
			if o := e.del.Delete(ctx, t); o.Kind != Deleted {
				e.t.Fatalf("delete %s: %v %v", t.Rel(), o.Kind, o.Err)
			}
		}
	}
}

func find(t *testing.T, vs []Verdict, path string) Verdict {
	t.Helper()
	for _, v := range vs {
		if v.Candidate.Path == path {
			return v
		}
	}
	t.Fatalf("no verdict for %s", path)
	return Verdict{}
}

func wantReason(t *testing.T, v Verdict, kind VerdictKind, code ReasonCode) {
	t.Helper()
	if v.Kind != kind || len(v.Reasons) == 0 || v.Reasons[0].Code != code {
		t.Fatalf("%s: got %v (%s), want %v %s", v.Candidate.Rel, v.Kind, v.ReasonText(), kind, reasonText[code])
	}
	if v.Selectable() {
		t.Fatalf("%s: skipped/protected verdict is selectable", v.Candidate.Path)
	}
}

// AC-R1: a retired dir containing a fresh workspace and a locked worktree is skipped
// and nothing inside it is deleted.
func TestAC_R1_ParentsNeverOverrideChildren(t *testing.T) {
	e := newEnv(t, false)
	s := testfix.ScenarioR1(t, e.root.Path)
	vs := e.inspect(e.cand(match.Retired, s.Orca, 30), e.worktree(s.Locked, s.Repo, 30))
	if v := find(t, vs, s.Orca); v.Kind != Skipped {
		t.Fatalf(".orca: %v %s", v.Kind, v.ReasonText())
	}
	wantReason(t, find(t, vs, s.Locked), Skipped, ReasonLocked)
	e.deleteAll(vs)
	for _, p := range []string{s.Orca, s.Fresh, s.Locked} {
		if !testfix.Exists(p) {
			t.Fatalf("%s was deleted", p)
		}
	}
}

func TestReconcileDowngradesAncestors(t *testing.T) {
	c := func(p string) match.Candidate { return match.Candidate{Path: p} }
	parent := Verdict{Candidate: c("/r/a"), Kind: Eligible, target: Target{v: &verified{}}}
	other := Verdict{Candidate: c("/r/ab"), Kind: Eligible, target: Target{v: &verified{}}}
	child := Verdict{Candidate: c("/r/a/b/c"), Kind: Skipped}
	out := Reconcile([]Verdict{parent, child, other})
	got := find(t, out, "/r/a")
	if got.Kind != Skipped || got.Reasons[0].Code != ReasonContainsSkipped || got.Selectable() {
		t.Fatalf("parent %+v", got)
	}
	if !find(t, out, "/r/ab").Selectable() {
		t.Fatal("sibling with a common name prefix was downgraded")
	}
}

// AC-R2: unpushed commits and stashes block a full clone; ignored files only warn.
func TestAC_R2_FullCloneWork(t *testing.T) {
	for name, tc := range map[string]struct {
		opts testfix.CloneOpts
		code ReasonCode
	}{
		"local-commit": {testfix.CloneOpts{LocalCommit: true}, ReasonLocalOnly},
		"stash":        {testfix.CloneOpts{Stash: true}, ReasonStash},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, false)
			clone := testfix.FullClone(t, e.root.Path, tc.opts)
			vs := e.inspect(e.cand(match.Retired, clone, 30))
			wantReason(t, vs[0], Skipped, tc.code)
		})
	}
	t.Run("ignored-only", func(t *testing.T) {
		e := newEnv(t, false)
		clone := testfix.FullClone(t, e.root.Path, testfix.CloneOpts{IgnoredEnv: true})
		v := e.inspect(e.cand(match.Retired, clone, 30))[0]
		if v.Kind != EligibleWithNotes || len(v.Notes) != 1 || !slices.Contains(v.Notes[0].Paths, ".env") {
			t.Fatalf("got %v %s notes=%+v", v.Kind, v.ReasonText(), v.Notes)
		}
		e.deleteAll([]Verdict{v})
		if testfix.Exists(clone) {
			t.Fatal("eligible clone not deleted")
		}
	})
}

// AC-R3: tracked build/ is protected; untracked dist/ is eligible.
func TestAC_R3_TrackedProtected(t *testing.T) {
	e := newEnv(t, false)
	s := testfix.ScenarioR3(t, e.root.Path)
	vs := e.inspect(e.cand(match.Artifact, s.TrackedBuild, 0), e.cand(match.Artifact, s.Dist, 0))
	wantReason(t, find(t, vs, s.TrackedBuild), Protected, ReasonTracked)
	if !find(t, vs, s.Dist).Selectable() {
		t.Fatalf("dist not eligible: %s", find(t, vs, s.Dist).ReasonText())
	}
	e.deleteAll(vs)
	if !testfix.Exists(s.TrackedBuild) || testfix.Exists(s.Dist) {
		t.Fatal("wrong deletion result")
	}
}

// AC-R4: fresh retired-tool files are skipped; committed ones are protected.
func TestAC_R4_RetiredTools(t *testing.T) {
	e := newEnv(t, false)
	s := testfix.ScenarioR4(t, e.root.Path)
	vs := e.inspect(e.cand(match.Retired, s.FreshRules, 30), e.cand(match.Retired, s.FreshDir, 30),
		e.cand(match.Retired, s.CommittedRules, 30))
	wantReason(t, find(t, vs, s.FreshRules), Skipped, ReasonFresh)
	wantReason(t, find(t, vs, s.FreshDir), Skipped, ReasonFresh)
	wantReason(t, find(t, vs, s.CommittedRules), Protected, ReasonTracked)
}

// AC-G3: each blocking condition alone yields its reason.
func TestAC_G3_EachCondition(t *testing.T) {
	want := map[testfix.Condition]ReasonCode{
		testfix.Uncommitted:         ReasonUncommitted,
		testfix.Untracked:           ReasonUntracked,
		testfix.Stashed:             ReasonStash,
		testfix.DetachedUnreachable: ReasonDetached,
		testfix.Locked:              ReasonLocked,
		testfix.LocalOnly:           ReasonLocalOnly,
	}
	e := newEnv(t, false)
	for cond, code := range want {
		dir := testfix.ConditionRepo(t, e.root.Path, cond.String(), cond)
		v := e.inspect(e.cand(match.Retired, dir, 30))[0]
		if v.Kind != Skipped || len(v.Reasons) != 1 || v.Reasons[0].Code != code {
			t.Errorf("%s: got %v %s", cond, v.Kind, v.ReasonText())
		}
	}
}

// AC-G4: -D relaxes only uncommitted, untracked and unreachable detached HEAD.
func TestAC_G4_IncludeDirty(t *testing.T) {
	e := newEnv(t, true)
	for cond, relaxed := range map[testfix.Condition]bool{
		testfix.Uncommitted: true, testfix.Untracked: true, testfix.DetachedUnreachable: true,
		testfix.Stashed: false, testfix.Locked: false, testfix.LocalOnly: false,
	} {
		dir := testfix.ConditionRepo(t, e.root.Path, cond.String(), cond)
		v := e.inspect(e.cand(match.Retired, dir, 30))[0]
		if relaxed {
			if v.Kind != EligibleWithNotes || !strings.Contains(v.Notes[0].Text, "--include-dirty") {
				t.Errorf("%s: got %v %s %+v", cond, v.Kind, v.ReasonText(), v.Notes)
			}
		} else if v.Kind != Skipped {
			t.Errorf("%s: got %v, want skipped", cond, v.Kind)
		}
	}
}

// AC-G5: when git refuses a worktree removal the item fails and the directory stays.
func TestAC_G5_NoFallbackWhenGitRefuses(t *testing.T) {
	path := testfix.Root(t)
	repo := testfix.Repo(t, filepath.Join(path, "repo"))
	wt := testfix.Worktree(t, repo, filepath.Join(path, "wt"), "wt")
	testfix.BackdateTree(t, wt, 40)
	testfix.BackdateTree(t, repo, 40)
	real, err := gitx.New(gitx.Options{})
	if err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = remove ] && { echo 'fatal: refused' >&2; exit 128; }; done\nexec " + real.Bin() + " \"$@\"\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	e := newEnvAt(t, path, false, gitx.Options{Bin: stub})
	v := e.inspect(e.worktree(wt, repo, 30))[0]
	tg, ok := v.Target()
	if !ok {
		t.Fatalf("worktree not eligible: %s", v.ReasonText())
	}
	o := e.del.Delete(ctx, tg)
	if o.Kind != Failed || !strings.Contains(o.Err.Error(), "git refused") {
		t.Fatalf("outcome %v %v", o.Kind, o.Err)
	}
	if !testfix.Exists(filepath.Join(wt, "README.md")) {
		t.Fatal("worktree files were removed after git refused")
	}
}

// AC-G8: deep age catches recent work where age is the only guard.
func TestAC_G8_DeepAge(t *testing.T) {
	build := func(t *testing.T, e *env) (string, string) {
		repo := testfix.Repo(t, filepath.Join(e.root.Path, "repo"))
		wt := testfix.Worktree(t, repo, filepath.Join(e.root.Path, "wt"), "wt")
		testfix.Commit(t, wt, "src/lib/x.go", "package x\n", 60)
		testfix.BackdateTree(t, wt, 60)
		testfix.BackdateTree(t, repo, 60)
		testfix.Write(t, filepath.Join(wt, "src/lib/x.go"), "package x // edited\n")
		testfix.Backdate(t, filepath.Join(wt, "src/lib/x.go"), 1)
		return repo, wt
	}
	t.Run("without -D", func(t *testing.T) {
		e := newEnv(t, false)
		repo, wt := build(t, e)
		wantReason(t, e.inspect(e.worktree(wt, repo, 30))[0], Skipped, ReasonUncommitted)
	})
	t.Run("with -D", func(t *testing.T) {
		e := newEnv(t, true)
		repo, wt := build(t, e)
		wantReason(t, e.inspect(e.worktree(wt, repo, 30))[0], Skipped, ReasonFresh)
	})
	t.Run("retired non-git dir", func(t *testing.T) {
		e := newEnv(t, false)
		dir := filepath.Join(e.root.Path, ".zed")
		testfix.Write(t, filepath.Join(dir, "a/b/c/settings.json"), "{}")
		testfix.BackdateTree(t, dir, 60)
		testfix.Backdate(t, filepath.Join(dir, "a/b/c/settings.json"), 1)
		wantReason(t, e.inspect(e.cand(match.Retired, dir, 30))[0], Skipped, ReasonFresh)
	})
}

// AC-G10: repo stashes are a note on linked worktrees and a blocker for full clones.
func TestAC_G10_Stashes(t *testing.T) {
	e := newEnv(t, false)
	repo, wt := testfix.RepoWithStashAndWorktree(t, e.root.Path)
	v := e.inspect(e.worktree(wt, repo, 30))[0]
	if v.Kind != EligibleWithNotes || !strings.Contains(v.Notes[0].Text, "repo has 1 stash") {
		t.Fatalf("linked: %v %s %+v", v.Kind, v.ReasonText(), v.Notes)
	}
	e.deleteAll([]Verdict{v})
	if testfix.Exists(wt) {
		t.Fatal("worktree not removed")
	}
	if n, err := e.git.StashCount(ctx, repo); err != nil || n != 1 {
		t.Fatalf("stash lost: %d %v", n, err)
	}
}

func TestLinkedUnpushedNote(t *testing.T) {
	e := newEnv(t, false)
	repo, wt := testfix.LinkedUnpushed(t, e.root.Path)
	v := e.inspect(e.worktree(wt, repo, 30))[0]
	if v.Kind != EligibleWithNotes || !strings.Contains(v.Notes[0].Text, "branch feat has 1 unpushed commit; kept in "+repo) {
		t.Fatalf("%v %s %+v", v.Kind, v.ReasonText(), v.Notes)
	}
}

// AC-C1: a worktree outside the root is never eligible.
func TestAC_C1_OutsideRoot(t *testing.T) {
	e := newEnv(t, false)
	repo, wt := testfix.OutsideWorktree(t, e.root.Path)
	c := match.Candidate{Kind: match.Worktree, Path: wt, IsDir: true, Repo: repo, Strategy: config.StrategyRemove}
	wantReason(t, e.inspect(c)[0], Protected, ReasonOutsideRoot)
}

// AC-C2: symlinks are never followed when deleting.
func TestAC_C2_SymlinksNotFollowed(t *testing.T) {
	e := newEnv(t, false)
	dir := filepath.Join(e.root.Path, "cache")
	keep := testfix.OutsideSymlink(t, filepath.Join(dir, "link"))
	testfix.Write(t, filepath.Join(dir, "x"), "x")
	vs := e.inspect(e.cand(match.Artifact, dir, 0))
	e.deleteAll(vs)
	if testfix.Exists(dir) || !testfix.Exists(keep) {
		t.Fatalf("dir exists=%v outside kept=%v", testfix.Exists(dir), testfix.Exists(keep))
	}

	link := filepath.Join(e.root.Path, "leaf")
	testfix.OutsideSymlink(t, link)
	c := match.Candidate{Kind: match.Artifact, Path: link, IsDir: true, Strategy: config.StrategyRemove}
	if v := e.inspect(c)[0]; v.Kind != Protected {
		t.Fatalf("symlink candidate: %v %s", v.Kind, v.ReasonText())
	}
}

// AC-C4: a parent swapped for an outside symlink between scan and delete is caught.
func TestAC_C4_SwapBetweenScanAndDelete(t *testing.T) {
	e := newEnv(t, false)
	parent := filepath.Join(e.root.Path, "p")
	target := filepath.Join(parent, "target")
	testfix.Write(t, filepath.Join(target, "x"), "x")
	v := e.inspect(e.cand(match.Artifact, target, 0))[0]
	tg, ok := v.Target()
	if !ok {
		t.Fatal(v.ReasonText())
	}
	if err := os.Rename(parent, parent+"-moved"); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(testfix.Canonical(t, t.TempDir()), "o")
	testfix.Write(t, filepath.Join(outside, "target", "precious"), "keep")
	if err := os.Symlink(outside, parent); err != nil {
		t.Fatal(err)
	}
	o := e.del.Delete(ctx, tg)
	if o.Kind != NotDeleted || o.Reason.Code != ReasonChangedSinceScan {
		t.Fatalf("outcome %v %v %v", o.Kind, o.Reason, o.Err)
	}
	if !testfix.Exists(filepath.Join(outside, "target", "precious")) {
		t.Fatal("deleted outside the root")
	}
}

// AC-D4: empty-contents keeps the directory.
func TestAC_D4_EmptyContents(t *testing.T) {
	e := newEnv(t, false)
	dir := filepath.Join(e.root.Path, "out")
	testfix.Write(t, filepath.Join(dir, "a/b"), "x")
	testfix.Write(t, filepath.Join(dir, "c"), "x")
	c := e.cand(match.Artifact, dir, 0)
	c.Strategy = config.StrategyEmptyContents
	e.deleteAll(e.inspect(c))
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("entries %v err %v", entries, err)
	}
}

func TestFailClosedAndGuards(t *testing.T) {
	e := newEnv(t, false)
	if o := e.del.Delete(ctx, Target{}); o.Kind != Failed {
		t.Fatal("zero target accepted")
	}
	dir := filepath.Join(e.root.Path, "x")
	testfix.Write(t, filepath.Join(dir, ".wrangler", "state"), "x")
	wantReason(t, e.inspect(e.cand(match.Artifact, dir, 0))[0], Skipped, ReasonContainsNeverTouch)

	bad := e.cand(match.Artifact, dir, 0)
	bad.Strategy = config.StrategyInvalid
	wantReason(t, e.inspect(bad)[0], Skipped, ReasonInspectionFailed)

	// AC-G1 at inspection level: a failing git skips every git-tree candidate.
	stub := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 128\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	clone := testfix.FullClone(t, e.root.Path, testfix.CloneOpts{})
	broken := newEnvAt(t, e.root.Path, false, gitx.Options{Bin: stub})
	wantReason(t, broken.inspect(broken.cand(match.Retired, clone, 30))[0], Skipped, ReasonInspectionFailed)
	r3 := testfix.ScenarioR3(t, e.root.Path)
	wantReason(t, broken.inspect(broken.cand(match.Artifact, r3.Dist, 0))[0], Skipped, ReasonInspectionFailed)
}

func TestRevalidationCatchesNewWork(t *testing.T) {
	e := newEnv(t, false)
	clone := testfix.FullClone(t, e.root.Path, testfix.CloneOpts{})
	tg, ok := e.inspect(e.cand(match.Retired, clone, 30))[0].Target()
	if !ok {
		t.Fatal("clean clone not eligible")
	}
	testfix.Write(t, filepath.Join(clone, "new-work.txt"), "x")
	if o := e.del.Delete(ctx, tg); o.Kind != NotDeleted {
		t.Fatalf("outcome %v", o.Kind)
	}
	if !testfix.Exists(clone) {
		t.Fatal("deleted despite new work")
	}
}

func TestProtectedPathInspect(t *testing.T) {
	e := newEnv(t, false)
	e.in.cfg.Protected = []string{"keep/proj"}
	mk := func(rel string) match.Candidate {
		p := filepath.Join(e.root.Path, rel)
		testfix.Write(t, filepath.Join(p, "x"), "x")
		return e.cand(match.Artifact, p, 0)
	}
	wantReason(t, e.inspect(mk("keep/proj"))[0], Protected, ReasonProtectedPath)
	v := e.inspect(mk("keep/proj/sub/__pycache__"))[0]
	wantReason(t, v, Protected, ReasonProtectedPath)
	if v.Reasons[0].String() != "protected path: keep/proj" {
		t.Fatalf("reason %q", v.Reasons[0])
	}
	wantReason(t, e.inspect(mk("keep"))[0], Protected, ReasonProtectedPath)
	for _, rel := range []string{"keep/proj2", "keep/pro", "other"} {
		if v := e.inspect(mk(rel))[0]; v.Kind != Eligible {
			t.Errorf("%s: %v %s", rel, v.Kind, v.ReasonText())
		}
	}
}

func TestProtectedPathRevalidateRefuses(t *testing.T) {
	e := newEnv(t, false)
	dir := filepath.Join(e.root.Path, "area", "cache")
	testfix.Write(t, filepath.Join(dir, "x"), "x")
	v := e.inspect(e.cand(match.Artifact, dir, 0))[0]
	tg, ok := v.Target()
	if !ok {
		t.Fatal(v.ReasonText())
	}
	del := NewDeleter(e.in, []string{"area"})
	o := del.Delete(context.Background(), tg)
	if o.Kind != NotDeleted || o.Reason == nil || o.Reason.Code != ReasonProtectedPath {
		t.Fatalf("outcome %v %v %v", o.Kind, o.Reason, o.Err)
	}
	if !testfix.Exists(filepath.Join(dir, "x")) {
		t.Fatal("protected target was removed")
	}
	// A shared name prefix is not protection.
	if o := NewDeleter(e.in, []string{"ar"}).Delete(context.Background(), tg); o.Kind != Deleted {
		t.Fatalf("sibling prefix: %v %v", o.Kind, o.Reason)
	}
}
