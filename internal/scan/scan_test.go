package scan

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/goleak"

	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/gitx"
	"github.com/jfricker/cleanup/internal/match"
	"github.com/jfricker/cleanup/internal/rootguard"
	"github.com/jfricker/cleanup/internal/safety"
	"github.com/jfricker/cleanup/internal/testfix"
)

func setup(t *testing.T) (rootguard.Root, *gitx.Runner, *config.Config) {
	t.Helper()
	root, err := rootguard.Resolve(testfix.Root(t), rootguard.Options{Home: "/nonexistent", SystemRules: []rootguard.Rule{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	git, err := gitx.New(gitx.Options{Env: testfix.Env(t)})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadBytes("", nil)
	if err != nil {
		t.Fatal(err)
	}
	return root, git, cfg
}

func TestEndToEndVerdicts(t *testing.T) {
	root, git, cfg := setup(t)
	r1 := testfix.ScenarioR1(t, root.Path)
	r3 := testfix.ScenarioR3(t, root.Path)
	r4 := testfix.ScenarioR4(t, root.Path)
	r5 := testfix.ScenarioR5(t, root.Path)
	testfix.Write(t, filepath.Join(root.Path, "rs/Cargo.toml"), "[package]\n")
	testfix.Write(t, filepath.Join(root.Path, "rs/target/debug/bin"), "12345")
	repo, wtOut := testfix.OutsideWorktree(t, root.Path)
	_ = repo

	res, err := Run(context.Background(), cfg, root, git, Options{Retired: []string{"orca", "cursor"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]safety.VerdictKind{}
	for _, v := range res.Verdicts {
		kinds[v.Candidate.Path] = v.Kind
	}
	want := map[string]safety.VerdictKind{
		r1.Orca:                               safety.Skipped,
		r1.Locked:                             safety.Skipped,
		r3.TrackedBuild:                       safety.Protected,
		r3.Dist:                               safety.Eligible,
		r4.FreshRules:                         safety.Skipped,
		r4.FreshDir:                           safety.Skipped,
		r4.CommittedRules:                     safety.Protected,
		filepath.Join(root.Path, "rs/target"): safety.Eligible,
	}
	for p, k := range want {
		if kinds[p] != k {
			t.Errorf("%s: got %v want %v", p, kinds[p], k)
		}
	}
	if _, ok := kinds[r5]; ok {
		t.Errorf("unmarked target/ (R5) became a candidate")
	}
	if len(res.Outside) != 1 || res.Outside[0].Candidate.Path != wtOut || res.Outside[0].Kind != safety.Protected {
		t.Errorf("outside %+v", res.Outside)
	}
	if got := Reclaimable(res.Verdicts); got <= 0 {
		t.Errorf("reclaimable %d", got)
	}
}

func TestOnlyExcludeAndAgeOverride(t *testing.T) {
	_, _, cfg := setup(t)
	ids := func(o Options) []string {
		var out []string
		for _, c := range Active(cfg, o) {
			out = append(out, c.ID)
		}
		return out
	}
	if got := ids(Options{Only: []string{"macos", "rust"}}); len(got) != 2 || got[0] != "rust" || got[1] != "macos" {
		t.Fatalf("only: %v", got)
	}
	got := ids(Options{Exclude: []string{"node", "worktrees"}})
	for _, id := range got {
		if id == "node" || id == "worktrees" || id == "macos" {
			t.Fatalf("exclude: %v", got)
		}
	}

	root, git, cfg := setup(t)
	dir := filepath.Join(root.Path, ".zed")
	testfix.Write(t, filepath.Join(dir, "settings.json"), "{}")
	testfix.BackdateTree(t, dir, 10)
	res, err := Run(context.Background(), cfg, root, git, Options{Retired: []string{"zed"}, AgeDays: 5}, nil)
	if err != nil || len(res.Verdicts) != 1 || !res.Verdicts[0].Selectable() {
		t.Fatalf("age override: %+v %v", res.Verdicts, err)
	}
}

// TUI-5 support: cancelling a scan stops it and leaks no goroutines.
func TestCancelNoLeak(t *testing.T) {
	defer goleak.VerifyNone(t)
	root, git, cfg := setup(t)
	for i := range 50 {
		testfix.Write(t, filepath.Join(root.Path, "p", string(rune('a'+i%26))+string(rune('a'+i/26)), "__pycache__", "x.pyc"), "x")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, cfg, root, git, Options{}, func(Event) {}); err == nil {
		t.Fatal("want context error")
	}
}

func TestEmitThrottled(t *testing.T) {
	root, git, cfg := setup(t)
	for i := range 30 {
		testfix.Mkdir(t, filepath.Join(root.Path, "d", string(rune('a'+i))))
	}
	var n int
	if _, err := Run(context.Background(), cfg, root, git, Options{}, func(Event) { n++ }); err != nil {
		t.Fatal(err)
	}
	if n == 0 || n > 20 {
		t.Fatalf("emitted %d events", n)
	}
}

func TestProtectedPathNotScanned(t *testing.T) {
	root, git, cfg := setup(t)
	for _, base := range []string{"protected", "open"} {
		testfix.Write(t, filepath.Join(root.Path, base, "app/package.json"), "{}")
		testfix.Write(t, filepath.Join(root.Path, base, "app/node_modules/x/i.js"), "x")
		testfix.Write(t, filepath.Join(root.Path, base, "app/__pycache__/a.pyc"), "x")
		testfix.Write(t, filepath.Join(root.Path, base, ".cursor/rules/a.mdc"), "x")
		testfix.BackdateTree(t, filepath.Join(root.Path, base), 60)
	}
	res, err := Run(context.Background(), cfg, root, git, Options{Retired: []string{"cursor"}, Protected: []string{"protected"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var inOpen int
	for _, v := range res.Verdicts {
		switch {
		case strings.HasPrefix(v.Candidate.Rel, "protected"):
			t.Errorf("protected path reported: %s", v.Candidate.Rel)
		case strings.HasPrefix(v.Candidate.Rel, "open/"):
			inOpen++
		}
	}
	if inOpen < 2 {
		t.Errorf("expected __pycache__ and cursor footprints under open/, got %d: %+v", inOpen, res.Verdicts)
	}
}

// PROT-5: a protected repo's stale linked worktree elsewhere under the root is
// still proposed.
func TestAC_P6_ProtectedRepoWorktreeElsewhereProposed(t *testing.T) {
	root, git, cfg := setup(t)
	repo := testfix.Repo(t, filepath.Join(root.Path, "protected", "repo"))
	wt := testfix.Worktree(t, repo, filepath.Join(root.Path, "elsewhere", "wt"), "side")
	testfix.BackdateTree(t, wt, 40)
	testfix.BackdateTree(t, repo, 40)

	for _, prot := range [][]string{nil, {"protected"}} {
		res, err := Run(context.Background(), cfg, root, git, Options{Protected: prot}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.GitErrors) != 0 {
			t.Fatalf("git errors: %v", res.GitErrors)
		}
		found := false
		for _, v := range res.Verdicts {
			if v.Candidate.Path == wt {
				found = true
			}
		}
		if !found {
			t.Errorf("protected=%v: worktree %s not proposed: %+v", prot, wt, res.Verdicts)
		}
	}
}

// AC-P5: nothing at, under or containing a protected path is ever reported eligible,
// including a linked worktree sitting under it whose main repo is outside.
func TestAC_P5_ProtectedPathNothingReported(t *testing.T) {
	root, git, cfg := setup(t)
	mainRepo := testfix.Repo(t, filepath.Join(root.Path, "main"))
	testfix.Commit(t, mainRepo, "old.txt", "x", 40)
	for _, base := range []string{"protected", "open"} {
		b := filepath.Join(root.Path, base)
		testfix.Write(t, filepath.Join(b, "app/package.json"), "{}")
		testfix.Write(t, filepath.Join(b, "app/node_modules/x/i.js"), "x")
		testfix.Write(t, filepath.Join(b, "app/__pycache__/a.pyc"), "x")
		testfix.Write(t, filepath.Join(b, ".cursor/rules/a.mdc"), "x")
		wt := testfix.Worktree(t, mainRepo, filepath.Join(b, "wt"), "side-"+base)
		testfix.BackdateTree(t, b, 60)
		testfix.BackdateTree(t, wt, 40)
	}
	testfix.BackdateTree(t, mainRepo, 40) // after the worktrees exist: their admin dirs live here
	opts := Options{Retired: []string{"cursor"}, OptIns: match.OptIns{Modules: true}}
	count := func(prot []string, prefix string) int {
		o := opts
		o.Protected = prot
		res, err := Run(context.Background(), cfg, root, git, o, nil)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, v := range res.Verdicts {
			if strings.HasPrefix(v.Candidate.Rel, prefix) && v.Selectable() {
				n++
			}
		}
		return n
	}
	if n := count([]string{"protected"}, "protected"); n != 0 {
		t.Errorf("%d eligible items under protected/", n)
	}
	if n := count([]string{"protected"}, "open"); n < 4 {
		t.Errorf("open/ with protection: want >=4 eligible (pycache, node_modules, cursor, worktree), got %d", n)
	}
	if n := count(nil, "protected"); n < 4 {
		t.Errorf("unprotected control: want >=4 eligible under protected/, got %d", n)
	}
}

// Regression: with worktrees as the only active cleaner there are no walk visitors,
// and the walk used to stop at the root, so no repo was found and nothing reported.
func TestOnlyWorktreesFindsStaleWorktree(t *testing.T) {
	root, git, cfg := setup(t)
	repo := testfix.Repo(t, filepath.Join(root.Path, "a", "repo"))
	wt := testfix.Worktree(t, repo, filepath.Join(root.Path, "b", "wt"), "side")
	testfix.Write(t, filepath.Join(root.Path, "c/package.json"), "{}")
	testfix.Write(t, filepath.Join(root.Path, "c/__pycache__/a.pyc"), "x")
	// A repo under a never_touch name is not walked, so its worktree is not found.
	hidden := testfix.Repo(t, filepath.Join(root.Path, "keep", "repo"))
	hiddenWT := testfix.Worktree(t, hidden, filepath.Join(root.Path, "d", "wt"), "hidden")
	testfix.BackdateTree(t, root.Path, 40)
	cfg.NeverTouch = append(cfg.NeverTouch, "keep")

	for _, prot := range [][]string{nil, {"b"}} {
		res, err := Run(context.Background(), cfg, root, git, Options{Only: []string{config.WorktreesID}, Protected: prot}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, v := range res.Verdicts {
			if v.Candidate.Kind != match.Worktree {
				t.Errorf("non-worktree candidate %s", v.Candidate.Path)
			}
			if v.Candidate.Path == hiddenWT {
				t.Errorf("worktree of never_touch repo reported: %s", v.Candidate.Path)
			}
			got = append(got, v.Candidate.Path)
		}
		wantWT := prot == nil
		hasWT := false
		for _, v := range res.Verdicts {
			if v.Candidate.Path == wt && v.Selectable() {
				hasWT = true
			}
		}
		if hasWT != wantWT {
			t.Errorf("protected=%v: worktree eligible=%v, want %v; verdicts %v", prot, hasWT, wantWT, got)
		}
	}
}
