package execute

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/gitx"
	"github.com/jfricker/cleanup/internal/rootguard"
	"github.com/jfricker/cleanup/internal/runlog"
	"github.com/jfricker/cleanup/internal/safety"
	"github.com/jfricker/cleanup/internal/scan"
	"github.com/jfricker/cleanup/internal/testfix"
)

type fixture struct {
	root rootguard.Root
	git  *gitx.Runner
	res  *scan.Result
	log  *runlog.Writer
	dir  string
}

func setup(t *testing.T, build func(root string)) *fixture {
	t.Helper()
	path := testfix.Root(t)
	build(path)
	root, err := rootguard.Resolve(path, rootguard.Options{Home: "/nonexistent", SystemRules: []rootguard.Rule{}})
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
	res, err := scan.Run(context.Background(), cfg, root, git, scan.Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "state")
	log, err := runlog.Open(dir, root.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	return &fixture{root: root, git: git, res: res, log: log, dir: dir}
}

func (f *fixture) targets(t *testing.T) []safety.Target {
	var ts []safety.Target
	for _, v := range f.res.Verdicts {
		if tg, ok := v.Target(); ok {
			ts = append(ts, tg)
		}
	}
	if len(ts) == 0 {
		t.Fatal("no eligible targets")
	}
	return ts
}

func (f *fixture) runner() Runner {
	return Runner{Deleter: safety.NewDeleter(f.res.Inspector, nil), Git: f.git, Log: f.log}
}

func readLog(t *testing.T, dir string) []runlog.Record {
	t.Helper()
	fh, err := os.Open(filepath.Join(dir, runlog.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	var out []runlog.Record
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		var r runlog.Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func caches(t *testing.T, n int) func(string) {
	return func(root string) {
		for i := range n {
			testfix.Write(t, filepath.Join(root, "proj", string(rune('a'+i)), "__pycache__", "m.pyc"), "1234567890")
		}
	}
}

// AC-D1: an interrupt finishes the current item, still prunes, and reports and logs
// exactly what happened.
func TestAC_D1_Interrupt(t *testing.T) {
	var repo string
	f := setup(t, func(root string) {
		caches(t, 4)(root)
		repo = testfix.Repo(t, filepath.Join(root, "repo"))
		gone := testfix.Worktree(t, repo, filepath.Join(root, "gone"), "gone")
		if err := os.RemoveAll(gone); err != nil {
			t.Fatal(err)
		}
	})
	if len(f.res.PruneRepos) != 1 {
		t.Fatalf("prune repos %v", f.res.PruneRepos)
	}
	soft, stop := context.WithCancel(context.Background())
	defer stop()
	ts := f.targets(t)
	rep := f.runner().Execute(context.Background(), soft, ts, f.res.PruneRepos, func(e Event) {
		if e.Kind == ItemDone && e.Index == 0 {
			stop() // first Ctrl+C arrives while item 0 is finishing
		}
	})
	if !rep.Interrupted || len(rep.Deleted) != 1 || len(rep.NotAttempted) != len(ts)-1 {
		t.Fatalf("report: interrupted=%v deleted=%d not=%d", rep.Interrupted, len(rep.Deleted), len(rep.NotAttempted))
	}
	if testfix.Exists(rep.Deleted[0].Target.Path()) {
		t.Fatal("deleted item still exists")
	}
	for _, nt := range rep.NotAttempted {
		if !testfix.Exists(nt.Path()) {
			t.Fatalf("not-attempted item %s is gone", nt.Path())
		}
	}
	if len(rep.Pruned) != 1 || rep.Pruned[0] != repo {
		t.Fatalf("prune did not run after interrupt: %v %v", rep.Pruned, rep.PruneErrors)
	}
	recs := readLog(t, f.dir)
	if recs[0].Type != "start" || recs[len(recs)-1].Type != "end" || recs[len(recs)-1].Outcome != "interrupted" {
		t.Fatalf("log %+v", recs)
	}
	var deleted, notAttempted int
	for _, r := range recs {
		switch r.Outcome {
		case "deleted":
			deleted++
		case "not attempted":
			notAttempted++
		}
	}
	if deleted != 1 || notAttempted != len(ts)-1 {
		t.Fatalf("log counts deleted=%d not=%d", deleted, notAttempted)
	}
}

// AC-D2: one failure does not stop the others and is recorded once.
func TestAC_D2_FailureContinues(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	var locked string
	f := setup(t, func(root string) {
		caches(t, 3)(root)
		locked = filepath.Join(root, "proj", "a", "__pycache__", "sub")
		testfix.Write(t, filepath.Join(locked, "f.pyc"), "x")
	})
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	rep := f.runner().Execute(context.Background(), context.Background(), f.targets(t), nil, nil)
	if rep.Failures() != 1 || len(rep.Deleted) != 2 {
		t.Fatalf("failures=%d deleted=%d", rep.Failures(), len(rep.Deleted))
	}
}

// AC-D3: every run logs start, items and end; an unwritable log dir is an error.
func TestAC_D3_RunLog(t *testing.T) {
	f := setup(t, caches(t, 2))
	rep := f.runner().Execute(context.Background(), context.Background(), f.targets(t), nil, nil)
	recs := readLog(t, f.dir)
	if len(recs) != 4 || recs[0].Type != "start" || recs[3].Type != "end" || recs[3].Bytes != rep.Bytes || recs[1].Run != f.log.RunID() {
		t.Fatalf("log %+v", recs)
	}
	fi, err := os.Stat(filepath.Join(f.dir, runlog.FileName))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("log mode %v %v", fi.Mode(), err)
	}

	if os.Geteuid() != 0 {
		ro := t.TempDir()
		if err := os.Chmod(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
		if _, err := runlog.Open(filepath.Join(ro, "state"), "/r"); err == nil {
			t.Fatal("want error for an unwritable state dir")
		}
	}
}

func TestOrderAndNestedBytes(t *testing.T) {
	f := setup(t, func(root string) {
		testfix.Write(t, filepath.Join(root, "web/package.json"), "{}")
		testfix.Write(t, filepath.Join(root, "web/dist/a.js"), "12345")
		testfix.Write(t, filepath.Join(root, "web/dist/__pycache__/m.pyc"), "1234567890")
	})
	ts := f.targets(t)
	ordered := Order(ts)
	if len(ordered) != 2 || filepath.Base(ordered[0].Path()) != "__pycache__" {
		t.Fatalf("order %v", ordered)
	}
	rep := f.runner().Execute(context.Background(), context.Background(), ts, nil, nil)
	if rep.Bytes != 15 || len(rep.Deleted) != 2 {
		t.Fatalf("bytes %d deleted %d", rep.Bytes, len(rep.Deleted))
	}
}
