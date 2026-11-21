package plain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/onboard"
	"github.com/jfricker/cleanup/internal/scan"
)

type onbEnv struct {
	f     *onboard.Flow
	home  string
	root  string
	probe int
}

var onbTools = []config.Tool{
	{ID: "zed", Name: "Zed", AgeDays: 30},
	{ID: "atom", Name: "Atom", AgeDays: 14},
}

func newOnbEnv(t *testing.T, footprints []scan.ToolFootprint, warn string) *onbEnv {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "Code")
	if err := os.MkdirAll(filepath.Join(root, "clients", "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := &onbEnv{home: home, root: root}
	e.f = onboard.New(onboard.Deps{
		Home: home,
		ResolveRoot: func(p string) (string, error) {
			c := filepath.Clean(p)
			if c == home {
				return "", fmt.Errorf("refusing to scan %s: your home directory", c)
			}
			fi, err := os.Stat(c)
			if err != nil || !fi.IsDir() {
				return "", fmt.Errorf("root: %s is not a directory", c)
			}
			return c, nil
		},
		Probe: func(_ context.Context, _ string, _ []string, emit func(scan.Event)) ([]scan.ToolFootprint, error) {
			e.probe++
			emit(scan.Event{Dirs: 7})
			return footprints, nil
		},
		PathWarning: warn,
		Tools:       onbTools,
	}, onboard.Initial{Root: "~/Code", ConfigPath: "/cfg/cleanup.toml"})
	return e
}

func footprints() []scan.ToolFootprint {
	return []scan.ToolFootprint{
		{Tool: onbTools[0], Items: 3, TooRecent: 2, Size: 2048, Youngest: 5 * 24 * time.Hour},
		{Tool: onbTools[1], Items: 1, Size: 100, Youngest: 90 * 24 * time.Hour},
	}
}

func runOnb(t *testing.T, e *onbEnv, input string, write onboard.WriteFunc) (int, string, error) {
	t.Helper()
	var out bytes.Buffer
	code, err := Onboard(context.Background(), strings.NewReader(input), &out, e.f, write)
	if strings.Contains(out.String(), "\x1b") {
		t.Errorf("ANSI escape in output: %q", out.String())
	}
	return code, out.String(), err
}

func TestOnboard_AcceptDefaults(t *testing.T) {
	e := newOnbEnv(t, footprints(), "")
	calls := 0
	code, out, err := runOnb(t, e, "\n\n\n\n", func() (string, error) { calls++; return "Config written: /cfg/cleanup.toml\n", nil })
	if err != nil || code != 0 || calls != 1 {
		t.Fatalf("code=%d err=%v calls=%d\n%s", code, err, calls, out)
	}
	for _, want := range []string{"Step 1 of 4", "[~/Code]", "Step 3 of 4", "Config written: /cfg/cleanup.toml", "Write the config file?"} {
		if !strings.Contains(out, want) && !strings.Contains(out, strings.ReplaceAll(want, "~/Code", e.root)) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestAC_O5_EOFAtStep2Cancels(t *testing.T) {
	e := newOnbEnv(t, footprints(), "")
	code, out, err := runOnb(t, e, "\n", func() (string, error) { t.Fatal("write called"); return "", nil })
	if err != nil || code != 130 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if !strings.Contains(out, "Step 2 of 4") || !strings.Contains(out, "Cancelled") {
		t.Errorf("output:\n%s", out)
	}
}

func TestOnboard_BackFromToolsKeepsEntries(t *testing.T) {
	e := newOnbEnv(t, footprints(), "")
	// root default, add entry, continue, toggle tool 1, back to step 2, continue, continue, confirm.
	in := "\na clients/acme\n\n1\nb\n\n\ny\n"
	code, out, err := runOnb(t, e, in, func() (string, error) { return "ok\n", nil })
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v\n%s", code, err, out)
	}
	if got := e.f.Protected(); len(got) != 1 || got[0] != "clients/acme" {
		t.Errorf("protected = %v", got)
	}
	if !e.f.Selected("zed") {
		t.Error("toggle lost after back")
	}
	if strings.Count(out, "1. clients/acme") < 2 {
		t.Errorf("entry not re-listed after back:\n%s", out)
	}
	if e.probe != 2 {
		t.Errorf("probe ran %d times", e.probe)
	}
}

func TestOnboard_ToolsListingNote(t *testing.T) {
	e := newOnbEnv(t, footprints(), "")
	_, out, _ := runOnb(t, e, "\n\n", func() (string, error) { return "", nil })
	for _, want := range []string{"2 items too recent to clean yet (< 30 days)", "Zed", "2.0 KiB", "youngest 5d", "3 items"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "too recent to clean yet (< 14") {
		t.Error("note for tool without fresh items")
	}
}

func TestOnboard_NoToolsFound(t *testing.T) {
	e := newOnbEnv(t, nil, "")
	code, out, _ := runOnb(t, e, "\n\n\n\n", func() (string, error) { return "done\n", nil })
	if code != 0 || !strings.Contains(out, "No leftovers") {
		t.Errorf("code=%d\n%s", code, out)
	}
}

func TestOnboard_PathWarningFirst(t *testing.T) {
	e := newOnbEnv(t, nil, "/opt/x is not on PATH")
	_, out, _ := runOnb(t, e, "", func() (string, error) { return "", nil })
	if !strings.HasPrefix(out, "Warning: /opt/x is not on PATH") {
		t.Errorf("warning not first:\n%s", out)
	}
}

func TestOnboard_ValidationErrorsReprompt(t *testing.T) {
	e := newOnbEnv(t, nil, "")
	in := "~\n\na ..\nd 9\nd 1\n"
	_, out, _ := runOnb(t, e, in, func() (string, error) { return "", nil })
	for _, want := range []string{"your home directory", "contains \"..\"", `no entry "9"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestOnboard_DeclineCancels(t *testing.T) {
	e := newOnbEnv(t, nil, "")
	code, _, _ := runOnb(t, e, "\n\n\nn\n", func() (string, error) { t.Fatal("write called"); return "", nil })
	if code != 130 {
		t.Errorf("code=%d", code)
	}
}

func TestOnboard_WriteError(t *testing.T) {
	e := newOnbEnv(t, nil, "")
	boom := errors.New("boom")
	code, out, err := runOnb(t, e, "\n\n\n\n", func() (string, error) { return "", boom })
	if code != 2 || !errors.Is(err, boom) || !strings.Contains(out, "error: boom") {
		t.Errorf("code=%d err=%v\n%s", code, err, out)
	}
}

func TestOnboard_ContextCancel(t *testing.T) {
	e := newOnbEnv(t, nil, "")
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw, _ := os.Pipe()
	defer pw.Close()
	defer pr.Close()
	done := make(chan int, 1)
	go func() {
		code, _ := Onboard(ctx, pr, &bytes.Buffer{}, e.f, func() (string, error) { t.Error("write called"); return "", nil })
		done <- code
	}()
	cancel()
	select {
	case code := <-done:
		if code != 130 {
			t.Errorf("code=%d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not return promptly")
	}
}
