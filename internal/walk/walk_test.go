package walk

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/jfricker/cleanup/internal/testfix"
)

// nameVisitor records children with a given name and prunes names in prune.
type nameVisitor struct {
	match string
	prune []string
	mu    sync.Mutex
	seen  []string
}

func (v *nameVisitor) Visit(d *Dir, out *Out) {
	v.mu.Lock()
	v.seen = append(v.seen, d.Rel)
	v.mu.Unlock()
	for _, e := range d.Entries {
		if e.Name() == v.match {
			out.Candidate(e, v.match)
		}
		if slices.Contains(v.prune, e.Name()) {
			out.Prune(e.Name())
		}
	}
}

func build(t *testing.T) string {
	root := testfix.Root(t)
	for _, p := range []string{
		"a/node_modules/pkg/index.js",
		"a/package.json",
		"a/src/dist/x.js",
		"b/target/debug/x",
		"b/Cargo.toml",
		"c/.git/HEAD",
		"c/inner/target/y",
		"c/vendor/target/z",
	} {
		testfix.Write(t, filepath.Join(root, p), "x")
	}
	return root
}

func run(t *testing.T, root string, vs []Visitor, o Options) *Result {
	t.Helper()
	o.CountReads = true
	res, err := Run(context.Background(), root, 0, vs, o)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// AC-W1: with several visitors, each directory is read exactly once.
func TestAC_W1_EachDirReadOnce(t *testing.T) {
	root := build(t)
	vs := []Visitor{
		&nameVisitor{match: "target"},
		&nameVisitor{match: "dist"},
		&nameVisitor{match: "node_modules"},
	}
	res := run(t, root, vs, Options{NeverTouch: []string{".git"}})
	for p, n := range res.Reads {
		if n != 1 {
			t.Errorf("%s read %d times", p, n)
		}
	}
	// node_modules was recorded by visitor 2 but visitors 0 and 1 still descend.
	if res.Reads[filepath.Join(root, "a/node_modules")] != 1 {
		t.Fatal("node_modules not descended by remaining visitors")
	}
	var rels []string
	for _, f := range res.Found {
		rels = append(rels, f.Rel)
	}
	want := []string{"a/node_modules", "a/src/dist", "b/target", "c/inner/target", "c/vendor/target"}
	if !slices.Equal(rels, want) {
		t.Fatalf("found %v, want %v", rels, want)
	}
	if _, ok := res.Reads[filepath.Join(root, "c/.git")]; ok {
		t.Fatal(".git was descended")
	}
}

func TestPrunePerVisitorAndGlobal(t *testing.T) {
	root := build(t)
	v := &nameVisitor{match: "target", prune: []string{"vendor", "node_modules"}}
	res := run(t, root, []Visitor{v}, Options{NeverTouch: []string{".git"}})
	if _, ok := res.Reads[filepath.Join(root, "c/vendor")]; ok {
		t.Fatal("pruned dir was read with no other visitor wanting it")
	}
	if _, ok := res.Reads[filepath.Join(root, "a/node_modules")]; ok {
		t.Fatal("pruned node_modules was read")
	}
}

func TestSymlinksNeverFollowedOrReported(t *testing.T) {
	root := build(t)
	outside := testfix.OutsideSymlink(t, filepath.Join(root, "link"))
	if err := os.Symlink(filepath.Join(root, "b/target"), filepath.Join(root, "a/target")); err != nil {
		t.Fatal(err)
	}
	res := run(t, root, []Visitor{&nameVisitor{match: "target"}, &nameVisitor{match: "link"}}, Options{})
	for p := range res.Reads {
		if p == filepath.Join(root, "link") || filepath.Dir(p) == filepath.Dir(outside) {
			t.Fatalf("followed symlink: %s", p)
		}
	}
	for _, f := range res.Found {
		if f.Rel == "a/target" || f.Rel == "link" {
			t.Fatalf("symlink reported as candidate: %s", f.Rel)
		}
	}
}

func TestNeverTouchNotReported(t *testing.T) {
	root := build(t)
	res := run(t, root, []Visitor{&nameVisitor{match: ".git"}}, Options{NeverTouch: []string{".git"}})
	if len(res.Found) != 0 {
		t.Fatalf("never_touch reported: %+v", res.Found)
	}
}

func TestGitTreesAndAncestorMarkers(t *testing.T) {
	root := build(t)
	var mu sync.Mutex
	got := map[string]bool{}
	v := visitFunc(func(d *Dir, out *Out) {
		mu.Lock()
		got[d.Rel] = d.HasAncestorMarker("package.json")
		mu.Unlock()
		if d.Rel == "c/inner" && d.GitTree != filepath.Join(root, "c") {
			t.Errorf("c/inner GitTree = %q", d.GitTree)
		}
		if d.Rel == "a" && d.GitTree != "" {
			t.Errorf("a GitTree = %q", d.GitTree)
		}
	})
	res := run(t, root, []Visitor{v}, Options{NeverTouch: []string{".git"}, AncestorMarkers: []string{"package.json"}})
	if !got["a"] || !got["a/src"] || got["b"] || got["."] {
		t.Fatalf("ancestor markers %v", got)
	}
	if len(res.GitTrees) != 1 || res.GitTrees[0].Path != filepath.Join(root, "c") || !res.GitTrees[0].IsRepo {
		t.Fatalf("git trees %+v", res.GitTrees)
	}
}

func TestUnreadableDirRecorded(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read everything")
	}
	root := build(t)
	locked := filepath.Join(root, "b", "target")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	res := run(t, root, []Visitor{&nameVisitor{match: "x"}}, Options{})
	if !slices.Contains(res.Unreadable, locked) || len(res.Errors) != 1 {
		t.Fatalf("unreadable %v errors %v", res.Unreadable, res.Errors)
	}
}

func TestCancel(t *testing.T) {
	root := build(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, root, 0, []Visitor{&nameVisitor{}}, Options{}); err == nil {
		t.Fatal("want context error")
	}
}

type visitFunc func(d *Dir, out *Out)

func (f visitFunc) Visit(d *Dir, out *Out) { f(d, out) }
