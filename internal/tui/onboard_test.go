package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/goleak"

	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/onboard"
	"github.com/jfricker/cleanup/internal/scan"
)

var onbTools = []config.Tool{
	{ID: "zed", Name: "Zed", AgeDays: 30},
	{ID: "atom", Name: "Atom", AgeDays: 14},
}

func onbFootprints() []scan.ToolFootprint {
	return []scan.ToolFootprint{
		{Tool: onbTools[0], Items: 3, TooRecent: 2, Size: 2048, Youngest: 5 * 24 * time.Hour},
		{Tool: onbTools[1], Items: 1, Size: 100, Youngest: 90 * 24 * time.Hour},
	}
}

func newOnbFlow(t *testing.T, fps []scan.ToolFootprint, warn string, probe func()) (*onboard.Flow, string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "Code")
	for _, d := range []string{"clients/acme", "other"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return onboard.New(onboard.Deps{
		Home: home,
		ResolveRoot: func(p string) (string, error) {
			c := filepath.Clean(p)
			if c == home {
				return "", fmt.Errorf("refusing to scan %s: your home directory", c)
			}
			if fi, err := os.Stat(c); err != nil || !fi.IsDir() {
				return "", fmt.Errorf("root: %s is not a directory", c)
			}
			return c, nil
		},
		Probe: func(_ context.Context, _ string, _ []string, emit func(scan.Event)) ([]scan.ToolFootprint, error) {
			if probe != nil {
				probe()
			}
			emit(scan.Event{Dirs: 7})
			return fps, nil
		},
		PathWarning: warn,
		Tools:       onbTools,
	}, onboard.Initial{Root: "~/Code", ConfigPath: "/cfg/cleanup.toml"}), root
}

func onbKeys(m *OnboardModel, ks ...string) (effects []OnboardEffect) {
	for _, k := range ks {
		effects = append(effects, m.Update(KeyMsg(k))...)
	}
	return effects
}

func hasEff(es []OnboardEffect, e OnboardEffect) bool { return slicesContains(es, e) }

func slicesContains(es []OnboardEffect, e OnboardEffect) bool {
	for _, x := range es {
		if x == e {
			return true
		}
	}
	return false
}

func TestAC_O6_EscAtStep3CancelsAndBackKeepsEntries(t *testing.T) {
	f, _ := newOnbFlow(t, onbFootprints(), "", nil)
	m := NewOnboard(f)
	onbKeys(m, "enter", "a")
	for _, r := range "clients/acme" {
		onbKeys(m, string(r))
	}
	onbKeys(m, "enter", "enter") // add, then empty line leaves typing mode
	if got := f.Protected(); len(got) != 1 || got[0] != "clients/acme" {
		t.Fatalf("protected = %v (%s)", got, m.errMsg)
	}
	onbKeys(m, "enter") // next: probe
	m.Update(ProbeDoneMsg{Gen: m.gen, Footprints: onbFootprints()})
	onbKeys(m, "space") // toggle zed
	if !f.Selected("zed") {
		t.Fatal("toggle")
	}
	// Back to step 2: entries intact.
	onbKeys(m, "shift+tab")
	if f.Step() != onboard.Protected || len(f.Protected()) != 1 || !strings.Contains(m.View(), "clients/acme") {
		t.Fatalf("back lost entries: step=%v prot=%v", f.Step(), f.Protected())
	}
	// Forward again, then Esc: cancel, 130, no write effect.
	onbKeys(m, "enter")
	m.Update(ProbeDoneMsg{Gen: m.gen, Footprints: onbFootprints()})
	if !f.Selected("zed") {
		t.Fatal("toggle lost")
	}
	eff := onbKeys(m, "esc")
	if m.ExitCode != 130 || !hasEff(eff, OnbQuit) || hasEff(eff, OnbWrite) {
		t.Fatalf("exit=%d eff=%v", m.ExitCode, eff)
	}
}

func TestOnboardModel_EscDuringProbeCancelsProbe(t *testing.T) {
	f, _ := newOnbFlow(t, nil, "", nil)
	m := NewOnboard(f)
	onbKeys(m, "enter", "enter")
	eff := onbKeys(m, "ctrl+c")
	if m.ExitCode != 130 || !hasEff(eff, OnbCancelProbe) || !hasEff(eff, OnbQuit) {
		t.Fatalf("exit=%d eff=%v", m.ExitCode, eff)
	}
}

func TestOnboardModel_BackDuringProbeIgnoresLateResult(t *testing.T) {
	f, _ := newOnbFlow(t, nil, "", nil)
	m := NewOnboard(f)
	onbKeys(m, "enter", "enter")
	stale := m.gen
	eff := onbKeys(m, "b")
	if !hasEff(eff, OnbCancelProbe) || f.Step() != onboard.Protected {
		t.Fatalf("eff=%v step=%v", eff, f.Step())
	}
	m.Update(ProbeDoneMsg{Gen: stale, Footprints: onbFootprints()})
	if len(f.Footprints()) != 0 {
		t.Error("stale probe result applied")
	}
}

func TestOnboardModel_RootValidationAndBIsALetter(t *testing.T) {
	f, root := newOnbFlow(t, nil, "", nil)
	m := NewOnboard(f)
	onbKeys(m, "ctrl+u")
	for _, r := range "~" {
		onbKeys(m, string(r))
	}
	onbKeys(m, "enter")
	if f.Step() != onboard.Root || !strings.Contains(m.errMsg, "home directory") {
		t.Fatalf("home accepted: %q", m.errMsg)
	}
	onbKeys(m, "ctrl+u")
	m.Update(PasteMsg(root + "\n"))
	onbKeys(m, "b")
	if !strings.HasSuffix(m.input.String(), "Codeb") {
		t.Fatalf("b was not typed: %q", m.input.String())
	}
	onbKeys(m, "backspace", "enter")
	if f.Step() != onboard.Protected {
		t.Fatalf("step %v %q", f.Step(), m.errMsg)
	}
}

func TestOnboardModel_ProtectedRemoveAndErrors(t *testing.T) {
	f, _ := newOnbFlow(t, nil, "", nil)
	m := NewOnboard(f)
	onbKeys(m, "enter", "a")
	m.Update(PasteMsg("nope"))
	onbKeys(m, "enter")
	if m.errMsg == "" || len(f.Protected()) != 0 {
		t.Fatal("bad path accepted")
	}
	onbKeys(m, "ctrl+u")
	m.Update(PasteMsg("other"))
	onbKeys(m, "enter", "enter") // add, leave typing
	if len(f.Protected()) != 1 {
		t.Fatal("not added")
	}
	onbKeys(m, "d")
	if len(f.Protected()) != 0 {
		t.Fatal("not removed")
	}
}

func TestOnboardModel_ReviewWriteFlow(t *testing.T) {
	f, _ := newOnbFlow(t, nil, "", nil)
	m := NewOnboard(f)
	onbKeys(m, "enter", "enter")
	m.Update(ProbeDoneMsg{Gen: m.gen})
	onbKeys(m, "enter")
	if f.Step() != onboard.Summary {
		t.Fatal("not at review")
	}
	if !hasEff(onbKeys(m, "enter"), OnbWrite) {
		t.Fatal("no write effect")
	}
	if len(onbKeys(m, "esc")) != 0 || m.ExitCode != 0 {
		t.Fatal("keys must be ignored while writing")
	}
	m.Update(WriteDoneMsg{Summary: "Config written: /cfg/cleanup.toml\n"})
	if !strings.Contains(m.View(), "Config written") {
		t.Fatal("summary not shown")
	}
	if !hasEff(onbKeys(m, "enter"), OnbQuit) || m.ExitCode != 0 {
		t.Fatal("exit after summary")
	}
	m2 := NewOnboard(f)
	m2.Update(WriteDoneMsg{Err: errors.New("boom")})
	if m2.ExitCode != 2 || m2.Err == nil {
		t.Fatal("write error not surfaced")
	}
}

func TestLineEditor(t *testing.T) {
	e := newEditor("abc")
	e.Left()
	e.Insert("X")
	if e.String() != "abXc" || e.cur != 3 {
		t.Fatalf("%q %d", e.String(), e.cur)
	}
	e.Home()
	e.Delete()
	e.Backspace() // no-op at 0
	if e.String() != "bXc" {
		t.Fatalf("%q", e.String())
	}
	e.End()
	e.Backspace()
	e.Right() // clamped
	if e.String() != "bX" || e.cur != 2 {
		t.Fatalf("%q %d", e.String(), e.cur)
	}
	e.Insert("é\n\tz") // multibyte kept, control chars dropped (paste)
	if e.String() != "bXéz" {
		t.Fatalf("%q", e.String())
	}
	e.Key("ctrl+a")
	e.Key("ctrl+e")
	e.Key("ctrl+u")
	if e.String() != "" || e.cur != 0 {
		t.Fatal("ctrl+u")
	}
	if e.Key("up") || e.Key("ctrl+x") {
		t.Fatal("non-editing key consumed")
	}
	e.Key("space")
	if e.String() != " " {
		t.Fatal("space")
	}
}

// snapshot checks NFR-10: no colour escapes under NO_COLOR, at most 80 cols x 24
// lines, drawn inside a single-line frame.
func snapshot(t *testing.T, name string, m *OnboardModel) string {
	t.Helper()
	m.NoColor = true
	m.Update(ResizeMsg{Width: 80, Height: 24})
	v := m.View()
	if strings.Contains(v, "\x1b") {
		t.Errorf("%s: escape sequence under NO_COLOR", name)
	}
	lines := strings.Split(v, "\n")
	if len(lines) != 24 || !strings.HasPrefix(lines[0], "┌") || !strings.HasPrefix(lines[23], "└") {
		t.Errorf("%s: want a 24-line frame, got %d lines starting %q", name, len(lines), lines[0])
	}
	for i, l := range lines {
		if w := len([]rune(l)); w > 80 {
			t.Errorf("%s line %d is %d cols: %q", name, i+1, w, l)
		}
	}
	if os.Getenv("CLEANUP_SNAPSHOT_DIR") != "" {
		t.Logf("--- %s\n%s", name, v)
	}
	return v
}

func TestOnboardSnapshots80x24(t *testing.T) {
	long := strings.Repeat("deeply/nested/", 12)
	f, root := newOnbFlow(t, onbFootprints(), "/opt/very/long/bin/dir/that/is/not/on/PATH/"+long+" is not on PATH; add it to your shell profile", nil)
	m := NewOnboard(f)
	if v := snapshot(t, "root", m); !strings.Contains(v, "step 1 of 4") || !strings.Contains(v, "Warning") || !strings.Contains(v, "Esc cancel") {
		t.Errorf("root:\n%s", v)
	}
	m.input.Clear()
	m.input.Insert("/" + long + long)
	m.errMsg = "root: /x/" + long + " is not a directory"
	snapshot(t, "root-long", m)
	m.errMsg = ""
	m.input = newEditor(f.RootDisplay())

	onbKeys(m, "enter")
	if v := snapshot(t, "protected-empty", m); !strings.Contains(v, "(none)") || !strings.Contains(v, "a add") {
		t.Errorf("protected:\n%s", v)
	}
	onbKeys(m, "a")
	m.Update(PasteMsg("clients/acme"))
	onbKeys(m, "enter")
	m.Update(PasteMsg("missing/" + long))
	onbKeys(m, "enter")
	if v := snapshot(t, "protected-typing", m); !strings.Contains(v, "clients/acme") || !strings.Contains(v, "empty = done") {
		t.Errorf("typing:\n%s", v)
	}
	onbKeys(m, "ctrl+u", "enter")
	for i := range 30 { // many entries: the list scrolls and stays within 24 lines
		d := fmt.Sprint("p", i)
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := f.AddProtected(d); err != nil {
			t.Fatal(err)
		}
		m.pcursor = len(f.Protected()) - 1
	}
	if v := snapshot(t, "protected-many", m); !strings.Contains(v, "> p29") {
		t.Errorf("cursor row not visible:\n%s", v)
	}

	onbKeys(m, "enter")
	if v := snapshot(t, "probing", m); !strings.Contains(v, "Scanning") || !strings.Contains(v, "directories") {
		t.Errorf("probing:\n%s", v)
	}
	m.Update(ProbeEventMsg{Gen: m.gen, Event: scan.Event{Dirs: 1234}})
	m.Update(ProbeDoneMsg{Gen: m.gen, Footprints: onbFootprints()})
	v := snapshot(t, "tools", m)
	for _, want := range []string{"[ ] Zed", "2 items too recent to clean yet (< 30 days)", "2.0 KiB", "youngest 5d", "space toggle"} {
		if !strings.Contains(v, want) {
			t.Errorf("tools missing %q:\n%s", want, v)
		}
	}
	onbKeys(m, "space")
	if !strings.Contains(snapshot(t, "tools-selected", m), "[x] Zed") {
		t.Error("selected marker")
	}
	// Many tools in a short window still fit.
	var many []scan.ToolFootprint
	for i := range 40 {
		many = append(many, scan.ToolFootprint{Tool: config.Tool{ID: fmt.Sprint("t", i), Name: fmt.Sprint("Tool", i), AgeDays: 9}, Items: 2, TooRecent: i % 2, Size: 1 << 20, Youngest: time.Hour})
	}
	f2, _ := newOnbFlow(t, many, "", nil)
	m2 := NewOnboard(f2)
	toToolsWith(t, m2, many)
	for range 35 {
		onbKeys(m2, "down")
	}
	snapshot(t, "tools-many", m2)

	f3, _ := newOnbFlow(t, nil, "", nil)
	m3 := NewOnboard(f3)
	toToolsWith(t, m3, nil)
	if v := snapshot(t, "tools-none", m3); !strings.Contains(v, "No leftovers") {
		t.Errorf("none:\n%s", v)
	}
	onbKeys(m3, "enter")
	if v := snapshot(t, "review", m3); !strings.Contains(v, "Project root:") || !strings.Contains(v, "Enter write config") || !strings.Contains(v, "/cfg/cleanup.toml") {
		t.Errorf("review:\n%s", v)
	}
	m3.Update(WriteDoneMsg{Summary: f3.Summary("/cfg/cleanup.toml", "/cfg/cleanup.toml.bak")})
	if v := snapshot(t, "summary", m3); !strings.Contains(v, "Config written: /cfg/cleanup.toml") || !strings.Contains(v, "Enter / q exit") {
		t.Errorf("summary:\n%s", v)
	}
}

func toToolsWith(t *testing.T, m *OnboardModel, fps []scan.ToolFootprint) {
	t.Helper()
	onbKeys(m, "enter", "enter")
	m.Update(ProbeDoneMsg{Gen: m.gen, Footprints: fps})
	if m.Flow.Step() != onboard.Tools || m.probing {
		t.Fatalf("not at tools: %v", m.Flow.Step())
	}
}

func TestOnboardNoColorStylesPlain(t *testing.T) {
	f, _ := newOnbFlow(t, nil, "", nil)
	m := NewOnboard(f)
	if !strings.Contains(m.View(), "\x1b") {
		t.Fatal("expected styled output with colour enabled")
	}
	m.NoColor = true
	if strings.Contains(m.View(), "\x1b") {
		t.Fatal("escape under NoColor")
	}
}

func TestRunOnboard_FallbackWhenProgramFails(t *testing.T) {
	defer goleak.VerifyNone(t)
	f, _ := newOnbFlow(t, nil, "", nil)
	code, err := RunOnboard(context.Background(), OnboardDeps{
		Flow:  f,
		Write: func() (string, error) { t.Fatal("write called"); return "", nil },
		// The program fails while starting, before its first render.
		run: func(*tea.Program) (tea.Model, error) { return nil, errors.New("no tty") },
	})
	if !errors.Is(err, ErrTUIUnavailable) || code == 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

// A failure after the first render is a real error, not a reason to fall back.
func TestRunOnboard_NoFallbackAfterRender(t *testing.T) {
	defer goleak.VerifyNone(t)
	f, _ := newOnbFlow(t, nil, "", nil)
	code, err := RunOnboard(context.Background(), OnboardDeps{
		Flow:  f,
		Write: func() (string, error) { t.Fatal("write called"); return "", nil },
		run: func(p *tea.Program) (tea.Model, error) {
			m, _ := p.Run() // renders, then quits on ctrl+c
			return m, errors.New("late failure")
		},
		programOptions: []tea.ProgramOption{tea.WithInput(strings.NewReader("\x03")), tea.WithOutput(io.Discard)},
	})
	if err == nil || errors.Is(err, ErrTUIUnavailable) || code == 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestRunOnboard_CtrlCCancels(t *testing.T) {
	defer goleak.VerifyNone(t)
	f, _ := newOnbFlow(t, nil, "", nil)
	code, err := RunOnboard(context.Background(), OnboardDeps{
		Flow:           f,
		Write:          func() (string, error) { t.Fatal("write called"); return "", nil },
		programOptions: []tea.ProgramOption{tea.WithInput(strings.NewReader("\x03")), tea.WithOutput(io.Discard)},
	})
	if err != nil || code != 130 {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestRunOnboard_WritesOnceOnConfirm(t *testing.T) {
	defer goleak.VerifyNone(t)
	var probed, wrote atomic.Int32
	f, _ := newOnbFlow(t, onbFootprints(), "", func() { probed.Add(1) })
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer pw.Close()
		_, _ = pw.Write([]byte("\r\r")) // root, protected
		for {
			select {
			case <-done:
				return
			case <-time.After(20 * time.Millisecond):
				if probed.Load() > 0 {
					// Enters are ignored while probing; then review, write, exit.
					if _, err := pw.Write([]byte("\r")); err != nil {
						return
					}
				}
			}
		}
	}()
	code, err := RunOnboard(context.Background(), OnboardDeps{
		Flow:           f,
		Write:          func() (string, error) { wrote.Add(1); return "Config written: x\n", nil },
		programOptions: []tea.ProgramOption{tea.WithInput(pr), tea.WithOutput(io.Discard)},
	})
	close(done)
	if err != nil || code != 0 || wrote.Load() != 1 {
		t.Fatalf("code=%d err=%v wrote=%d", code, err, wrote.Load())
	}
}
