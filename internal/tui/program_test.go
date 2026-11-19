package tui

import (
	"context"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/goleak"

	"cleanup/internal/execute"
	"cleanup/internal/safety"
	"cleanup/internal/scan"
)

// TUI-5 / AC-T1: quitting during the scan cancels it and leaks no goroutines.
func TestQuitDuringScanNoLeak(t *testing.T) {
	defer goleak.VerifyNone(t)
	scanStopped := make(chan struct{})
	code, err := Run(Deps{
		Options: Options{Root: "/r"},
		Scan: func(ctx context.Context, emit func(scan.Event)) (*scan.Result, error) {
			emit(scan.Event{Phase: scan.PhaseWalk, Dirs: 1})
			<-ctx.Done()
			close(scanStopped)
			return nil, ctx.Err()
		},
		Execute: func([]safety.Verdict, func(execute.Event)) (*execute.Report, string, int, error) {
			t.Fatal("execute must not run")
			return nil, "", 0, nil
		},
		SoftCancel: func() {}, HardCancel: func() {},
		programOptions: []tea.ProgramOption{tea.WithInput(strings.NewReader("q")), tea.WithOutput(io.Discard)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if code != 130 {
		t.Fatalf("exit code %d", code)
	}
	<-scanStopped
}
