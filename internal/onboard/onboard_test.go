package onboard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/rootguard"
	"github.com/jfricker/cleanup/internal/scan"
)

// canonTemp returns a symlink-resolved temp dir.
func canonTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mkdir(t *testing.T, p ...string) string {
	t.Helper()
	d := filepath.Join(p...)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// fakeResolve mimics rootguard rejections for a fake home.
func fakeResolve(home string) func(string) (string, error) {
	return func(p string) (string, error) {
		c := filepath.Clean(p)
		switch {
		case c == home:
			return "", fmt.Errorf("refusing to scan %s: your home directory", c)
		case c == "/":
			return "", fmt.Errorf("refusing to scan /: the filesystem root")
		case strings.HasPrefix(c, filepath.Join(home, "Library")):
			return "", fmt.Errorf("refusing to scan %s: Library", c)
		}
		fi, err := os.Stat(c)
		if err != nil {
			return "", fmt.Errorf("root: %w", err)
		}
		if !fi.IsDir() {
			return "", fmt.Errorf("root: %s is not a directory", c)
		}
		return c, nil
	}
}

func newFlow(t *testing.T, in Initial, tools ...config.Tool) (*Flow, string) {
	t.Helper()
	home := canonTemp(t)
	return New(Deps{Home: home, ResolveRoot: fakeResolve(home), Tools: tools}, in), home
}

func TestAC_O2_RootValidation(t *testing.T) {
	f, home := newFlow(t, Initial{})
	mkdir(t, home, "Library", "x")
	mkdir(t, home, "Code")
	for _, in := range []string{"~", "/", "~/Library/x", "~/nonexistent", "relative/dir"} {
		err := f.SetRoot(in)
		if err == nil {
			t.Errorf("SetRoot(%q) accepted", in)
		}
	}
	if err := f.SetRoot("~"); err == nil || !strings.Contains(err.Error(), "home directory") {
		t.Errorf("home reason missing: %v", err)
	}
	f.Next()
	if f.Step() != Root {
		t.Error("Next from invalid root advanced")
	}
	if err := f.SetRoot("~/Code"); err != nil {
		t.Fatal(err)
	}
	if got := f.Changes().ProjectRoot; got != "~/Code" {
		t.Errorf("ProjectRoot = %q", got)
	}
	f.Next()
	if f.Step() != Protected {
		t.Error("did not advance")
	}
}

func TestAC_O2_RealRootguard(t *testing.T) {
	home := canonTemp(t)
	mkdir(t, home, "Library", "x")
	code := mkdir(t, home, "Code")
	d := Deps{Home: home, ResolveRoot: func(p string) (string, error) {
		r, err := rootguard.Resolve(p, rootguard.Options{Home: home, SystemRules: []rootguard.Rule{{Path: "/", Match: rootguard.Exact, Reason: "the filesystem root"}}})
		if err != nil {
			return "", err
		}
		defer r.Close()
		return r.Path, nil
	}}
	f := New(d, Initial{})
	for _, in := range []string{"~", "/", "~/Library/x", "~/nope"} {
		if err := f.SetRoot(in); err == nil {
			t.Errorf("accepted %q", in)
		}
	}
	if err := f.SetRoot("~/Code"); err != nil || f.RootDisplay() != code {
		t.Errorf("err=%v display=%s", err, f.RootDisplay())
	}
}

func TestAC_O4_ProtectedValidation(t *testing.T) {
	f, home := newFlow(t, Initial{})
	root := mkdir(t, home, "Code")
	mkdir(t, root, "clients", "acme")
	mkdir(t, root, "other")
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{"", ".", "..", "../x", "a/../b", home, "missing", "file"} {
		if err := f.AddProtected(in); err == nil {
			t.Errorf("accepted %q", in)
		}
	}
	if err := f.AddProtected("clients/acme"); err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{"clients/acme", "clients/acme/", "clients", filepath.Join(root, "clients")} {
		if err := f.AddProtected(in); err == nil {
			t.Errorf("accepted dup/nested %q", in)
		}
	}
	mkdir(t, root, "clients", "acme", "sub")
	if err := f.AddProtected("clients/acme/sub"); err == nil {
		t.Error("accepted nested child")
	}
	if err := f.AddProtected(filepath.Join(root, "other")); err != nil {
		t.Fatal(err)
	}
	if got := f.Protected(); len(got) != 2 || got[1] != "other" {
		t.Errorf("Protected = %v", got)
	}
	f.RemoveProtected("other")
	if len(f.Protected()) != 1 {
		t.Error("remove failed")
	}
}

func fp(id string) scan.ToolFootprint { return scan.ToolFootprint{Tool: config.Tool{ID: id}, Items: 1} }

func TestAC_T4_PreselectionFromInitial(t *testing.T) {
	f, _ := newFlow(t, Initial{Retired: map[string]bool{"cursor": true}}, config.Tool{ID: "cursor"}, config.Tool{ID: "aider"})
	f.SetProbe([]scan.ToolFootprint{fp("cursor"), fp("aider")})
	if !f.Selected("cursor") || f.Selected("aider") {
		t.Fatal("preselection wrong")
	}
	f.ToggleTool("cursor")
	f.ToggleTool("aider")
	if f.Selected("cursor") || !f.Selected("aider") {
		t.Fatal("toggle wrong")
	}
	ch := f.Changes().Retired
	if ch["cursor"] || !ch["aider"] {
		t.Errorf("changes %v", ch)
	}
}

func TestChangesDefaultRootOmitted(t *testing.T) {
	f, home := newFlow(t, Initial{})
	mkdir(t, home, "Projects")
	if err := f.SetRoot("~/Projects"); err != nil {
		t.Fatal(err)
	}
	if got := f.Changes().ProjectRoot; got != "" {
		t.Errorf("got %q", got)
	}
	outside := canonTemp(t)
	if err := f.SetRoot(outside); err != nil {
		t.Fatal(err)
	}
	if got := f.Changes().ProjectRoot; got != outside {
		t.Errorf("got %q want %q", got, outside)
	}
}

func TestChangesIncludesEveryTool(t *testing.T) {
	f, _ := newFlow(t, Initial{Retired: map[string]bool{"b": true}},
		config.Tool{ID: "a"}, config.Tool{ID: "b"}, config.Tool{ID: "c"})
	f.SetProbe([]scan.ToolFootprint{fp("a")})
	f.ToggleTool("a")
	f.ToggleTool("c") // not shown: ignored
	ch := f.Changes().Retired
	if len(ch) != 3 || !ch["a"] || !ch["b"] || ch["c"] {
		t.Errorf("changes %v", ch)
	}
	if _, ok := ch["c"]; !ok {
		t.Error("c missing")
	}
}

func TestRootChangeDropsMissingProtected(t *testing.T) {
	f, home := newFlow(t, Initial{})
	r1 := mkdir(t, home, "one")
	r2 := mkdir(t, home, "two")
	mkdir(t, r1, "keep")
	mkdir(t, r1, "gone")
	mkdir(t, r2, "keep")
	if err := f.SetRoot(r1); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"keep", "gone"} {
		if err := f.AddProtected(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.SetRoot(r2); err != nil {
		t.Fatal(err)
	}
	if got := f.Protected(); len(got) != 1 || got[0] != "keep" {
		t.Errorf("Protected = %v", got)
	}
	if !strings.Contains(f.Notice(), "gone") {
		t.Errorf("notice %q", f.Notice())
	}
	// failed SetRoot leaves state alone
	if err := f.SetRoot("/"); err == nil || f.RootDisplay() != r2 {
		t.Error("failed SetRoot changed state")
	}
}

func TestBackPreservesEntries(t *testing.T) {
	f, home := newFlow(t, Initial{})
	r := mkdir(t, home, "Code")
	mkdir(t, r, "x")
	_ = f.SetRoot(r)
	f.Next()
	_ = f.AddProtected("x")
	f.Next()
	f.SetProbe([]scan.ToolFootprint{fp("a")})
	f.Back()
	if f.Step() != Protected || len(f.Protected()) != 1 {
		t.Fatalf("step %v prot %v", f.Step(), f.Protected())
	}
	f.Back()
	f.Back()
	if f.Step() != Root || f.RootDisplay() != r {
		t.Error("root lost")
	}
}

func TestSummaryText(t *testing.T) {
	f, home := newFlow(t, Initial{
		Retired: map[string]bool{"cursor": true}, AutoTriggered: true,
		ConfigPath: "/cfg/config.toml", Replacing: true,
	}, config.Tool{ID: "cursor", Name: "Cursor"}, config.Tool{ID: "aider", Name: "Aider"})
	r := mkdir(t, home, "Code")
	mkdir(t, r, "acme")
	_ = f.SetRoot(r)
	_ = f.AddProtected("acme")
	s := f.Summary("/cfg/config.toml", "/cfg/config.toml.bak-1")
	for _, w := range []string{"/cfg/config.toml", "Backup: /cfg/config.toml.bak-1", r, "acme", "Cursor",
		"not preserved", "cleanup", "run your command again"} {
		if !strings.Contains(s, w) {
			t.Errorf("summary missing %q:\n%s", w, s)
		}
	}
	if strings.Contains(s, "Aider") {
		t.Error("Aider listed")
	}
	g, _ := newFlow(t, Initial{})
	if s := g.Summary("/x", ""); strings.Contains(s, "Backup") || strings.Contains(s, "not preserved") || strings.Contains(s, "again") {
		t.Errorf("unexpected lines:\n%s", s)
	}
}
