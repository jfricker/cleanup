package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/execute"
	"github.com/jfricker/cleanup/internal/gitx"
	"github.com/jfricker/cleanup/internal/rootguard"
	"github.com/jfricker/cleanup/internal/scan"
	"github.com/jfricker/cleanup/internal/testfix"
)

// result runs a real scan over a fixture, so the verdicts (and their targets) come
// from internal/safety; there is deliberately no way to fabricate a Target.
func result(t *testing.T) *scan.Result {
	t.Helper()
	path := testfix.Root(t)
	testfix.Write(t, filepath.Join(path, "a/__pycache__/m.pyc"), strings.Repeat("x", 100))
	testfix.Write(t, filepath.Join(path, "b/Cargo.toml"), "[package]")
	testfix.Write(t, filepath.Join(path, "b/target/bin"), strings.Repeat("x", 1000))
	testfix.FullClone(t, path, testfix.CloneOpts{IgnoredEnv: true})   // eligible with a note
	testfix.Write(t, filepath.Join(path, ".zed/settings.json"), "{}") // retired, fresh → skipped
	testfix.ScenarioR3(t, path)                                       // web/build protected, web/dist eligible
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
	cfg.Retired.Tools = append(cfg.Retired.Tools, config.Tool{ID: "clone", Name: "Clone", Retired: true, AgeDays: 30, Dirs: []string{"clone"}})
	res, err := scan.Run(context.Background(), cfg, root, git, scan.Options{Retired: []string{"zed"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

var cleaners = []scan.Cleaner{{ID: "python", Label: "Python", SortOrder: 10}, {ID: "node", Label: "Node", SortOrder: 20}, {ID: "rust", Label: "Rust", SortOrder: 30}, {ID: "retired", Label: "Retired Tools", SortOrder: 95}}

func newApp(t *testing.T, del bool) *App {
	a := New(Options{Root: "/r", Delete: del, Cleaners: cleaners})
	a.Update(ResizeMsg{Width: 120, Height: 30})
	a.Update(ScanDoneMsg{Result: result(t)})
	return a
}

func keys(a *App, ks ...string) (effects []Effect) {
	for _, k := range ks {
		effects = append(effects, a.Update(KeyMsg(k))...)
	}
	return effects
}

func has(es []Effect, e Effect) bool {
	for _, x := range es {
		if x == e {
			return true
		}
	}
	return false
}

// AC-T1: drive every phase through messages.
func TestAC_T1_Phases(t *testing.T) {
	a := New(Options{Root: "/r", Delete: true, Cleaners: cleaners})
	a.Update(ScanEventMsg{Phase: scan.PhaseWalk, Dirs: 42, Path: "x/y"})
	if a.Phase != Scanning || !strings.Contains(a.View(), "42") {
		t.Fatal("scanning view")
	}
	a.Update(ScanDoneMsg{Result: result(t)})
	if a.Phase != Review {
		t.Fatal("not in review")
	}
	// Items without notes are preselected; the clone with an ignored-file note is not.
	if got := len(a.Selected()); got != 3 {
		t.Fatalf("preselected %d", got)
	}
	keys(a, "a")
	if got := len(a.Selected()); got != 4 {
		t.Fatalf("select all: %d", got)
	}
	keys(a, "n")
	if len(a.Selected()) != 0 {
		t.Fatal("select none")
	}
	// Group toggle.
	keys(a, "g")
	if !strings.Contains(a.View(), "▾ a ") {
		t.Fatalf("project grouping view:\n%s", a.View())
	}
	keys(a, "g")
	// Filter.
	keys(a, "/", "t", "a", "r", "enter")
	if len(a.rows) != 3 || a.verdicts[a.rows[1].idx].Candidate.Rel != "b/target" || a.rows[2].kind != rowEnd {
		t.Fatalf("filter rows %+v", a.rows)
	}
	keys(a, "/", "esc")
	// Select one item with space on the first item row, confirm, cancel, confirm, delete.
	keys(a, "home", "down", "space")
	if len(a.Selected()) != 1 {
		t.Fatalf("space selected %d", len(a.Selected()))
	}
	keys(a, "d")
	if a.Phase != Confirm || a.confirmDelete {
		t.Fatal("confirm must open with Cancel focused")
	}
	if es := keys(a, "enter"); a.Phase != Review || has(es, EffectStartDelete) {
		t.Fatal("enter on Cancel must go back")
	}
	keys(a, "d", "right")
	es := keys(a, "enter")
	if a.Phase != Deleting || !has(es, EffectStartDelete) {
		t.Fatal("delete did not start")
	}
	// Ctrl+C during deletion: soft, then hard; never quits by itself.
	if es := keys(a, "ctrl+c"); !has(es, EffectSoftCancel) || has(es, EffectQuit) {
		t.Fatalf("first ctrl+c: %v", es)
	}
	if !strings.Contains(a.View(), "Stopping after the current item") {
		t.Fatal("no stopping message")
	}
	if es := keys(a, "ctrl+c"); !has(es, EffectHardCancel) {
		t.Fatalf("second ctrl+c: %v", es)
	}
	a.Update(DeleteDoneMsg{Report: &execute.Report{Interrupted: true}, Code: 130})
	if a.Phase != Done || !strings.Contains(a.View(), "Interrupted.") || strings.Contains(a.View(), "nothing was deleted") {
		t.Fatalf("done view:\n%s", a.View())
	}
	if es := keys(a, "q"); !has(es, EffectQuit) {
		t.Fatal("quit from done")
	}
}

func TestQuitDuringScanCancels(t *testing.T) {
	a := New(Options{})
	es := keys(a, "ctrl+c")
	if !has(es, EffectCancelScan) || !has(es, EffectQuit) || a.ExitCode != 130 {
		t.Fatalf("effects %v code %d", es, a.ExitCode)
	}
}

func TestScanErrorQuits(t *testing.T) {
	a := New(Options{})
	if es := a.Update(ScanDoneMsg{Err: errors.New("boom")}); !has(es, EffectQuit) || a.ExitCode != 2 {
		t.Fatal("scan error")
	}
}

// AC-T2: skipped and protected items can't be selected, individually or by group.
func TestAC_T2_BlockedNotSelectable(t *testing.T) {
	a := newApp(t, true)
	a.Opts.ShowSkipped = true
	a.collapsed[skippedKey] = false
	a.rebuild()
	for i, r := range a.rows {
		if r.key == skippedKey {
			a.cursor = i
			keys(a, "space")
			if r.kind == rowItem {
				a.cursor = i
				keys(a, "space")
			}
		}
	}
	keys(a, "a")
	for _, v := range a.Selected() {
		if !v.Selectable() {
			t.Fatalf("blocked item selected: %s", v.Candidate.Rel)
		}
	}
	if len(a.Selected()) != 4 {
		t.Fatalf("selected %d", len(a.Selected()))
	}
}

func TestDryRunCannotDelete(t *testing.T) {
	a := newApp(t, false)
	keys(a, "a", "space", "d")
	if a.Phase != Review || len(a.Selected()) != 0 {
		t.Fatal("dry run allowed selection or deletion")
	}
}

func TestNarrowLayoutAndThemes(t *testing.T) {
	for _, dark := range []bool{true, false} {
		for _, w := range []int{80, 160} {
			a := newApp(t, true)
			a.Update(ThemeMsg{Dark: dark})
			a.Update(ResizeMsg{Width: w, Height: 24})
			keys(a, "home", "down")
			out := a.View()
			for _, line := range strings.Split(out, "\n") {
				if lw := len([]rune(stripANSI(line))); lw > w {
					t.Fatalf("w=%d dark=%v line too wide (%d): %q", w, dark, lw, stripANSI(line))
				}
			}
			if !strings.Contains(out, "a/__pycache__") {
				t.Fatalf("w=%d: item missing", w)
			}
		}
	}
	a := newApp(t, false)
	a.Update(ResizeMsg{Width: 80, Height: 24})
	keys(a, "home", "down", "i")
	if !strings.Contains(a.View(), "status") {
		t.Fatal("narrow detail toggle")
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			esc = true
		case esc && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'):
			esc = false
		case !esc:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Under project grouping, item rows drop the project prefix the group label implies;
// cleaner grouping and skipped items keep the full path.
func TestProjectGroupingDropsPrefix(t *testing.T) {
	a := newApp(t, true)
	a.group = ByProject
	a.rebuild()
	names := map[string]string{}
	for _, v := range a.verdicts {
		names[v.Candidate.Rel] = a.itemName(v)
	}
	if got := names["b/target"]; got != "target" {
		t.Fatalf("b/target shown as %q", got)
	}
	if got := names["web/build"]; got != "web/build" {
		t.Fatalf("skipped web/build shown as %q", got)
	}
	a.group = ByCleaner
	for _, v := range a.verdicts {
		if got := a.itemName(v); got != v.Candidate.Rel {
			t.Fatalf("cleaner grouping shows %q for %q", got, v.Candidate.Rel)
		}
	}
}

// The detail box is sized to its content, not to the pane.
func TestDetailBoxFitsContent(t *testing.T) {
	out := stripANSI(box("ab\nabcd", 40, 20, doubleBox, lipgloss.NewStyle()))
	want := "╔══════╗\n║ ab   ║\n║ abcd ║\n╚══════╝"
	if out != want {
		t.Fatalf("box:\n%s\nwant:\n%s", out, want)
	}
}
