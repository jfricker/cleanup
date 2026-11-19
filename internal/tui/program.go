package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"cleanup/internal/execute"
	"cleanup/internal/safety"
	"cleanup/internal/scan"
)

// Deps are the side effects the TUI triggers.
type Deps struct {
	Options
	// Scan runs the scan; it must stop when ctx is cancelled.
	Scan func(ctx context.Context, emit func(scan.Event)) (*scan.Result, error)
	// Execute deletes the selection and returns the report, log path and exit code.
	Execute func(selected []safety.Verdict, emit func(execute.Event)) (*execute.Report, string, int, error)
	// SoftCancel and HardCancel interrupt a running deletion.
	SoftCancel, HardCancel func()

	programOptions []tea.ProgramOption // tests only: input/output without a terminal
}

type model struct {
	app        *App
	deps       Deps
	prog       *tea.Program
	cancelScan context.CancelFunc
	scanDone   chan struct{}
}

type tickMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(tick(), tea.RequestBackgroundColor)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var effects []Effect
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		effects = m.app.Update(ResizeMsg{Width: msg.Width, Height: msg.Height})
	case tea.BackgroundColorMsg:
		effects = m.app.Update(ThemeMsg{Dark: msg.IsDark()})
	case tea.KeyPressMsg:
		effects = m.app.Update(KeyMsg(msg.String()))
	case tickMsg:
		m.app.Update(TickMsg{})
		if m.app.Phase == Scanning || m.app.Phase == Deleting {
			return m, tick()
		}
		return m, nil
	case ScanEventMsg, ScanDoneMsg, DeleteEventMsg, DeleteDoneMsg:
		effects = m.app.Update(msg)
		if _, ok := msg.(DeleteDoneMsg); ok {
			return m, m.apply(effects)
		}
	}
	return m, m.apply(effects)
}

func (m *model) apply(effects []Effect) tea.Cmd {
	var cmds []tea.Cmd
	for _, e := range effects {
		switch e {
		case EffectCancelScan:
			m.cancelScan()
		case EffectSoftCancel:
			m.deps.SoftCancel()
		case EffectHardCancel:
			m.deps.HardCancel()
		case EffectStartDelete:
			sel := m.app.Selected()
			go func() {
				rep, logPath, code, err := m.deps.Execute(sel, func(ev execute.Event) { m.prog.Send(DeleteEventMsg(ev)) })
				m.prog.Send(DeleteDoneMsg{Report: rep, LogPath: logPath, Code: code, Err: err})
			}()
			cmds = append(cmds, tick())
		case EffectQuit:
			cmds = append(cmds, tea.Quit)
		}
	}
	return tea.Batch(cmds...)
}

func (m *model) View() tea.View {
	v := tea.NewView(m.app.View())
	v.AltScreen = true
	v.WindowTitle = "cleanup"
	return v
}

// Run starts the TUI and blocks until it exits. It returns the exit code.
func Run(d Deps) (int, error) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &model{app: New(d.Options), deps: d, cancelScan: cancel, scanDone: make(chan struct{})}
	m.prog = tea.NewProgram(m, d.programOptions...)
	go func() {
		defer close(m.scanDone)
		res, err := d.Scan(ctx, func(e scan.Event) { m.prog.Send(ScanEventMsg(e)) })
		if ctx.Err() == nil {
			m.prog.Send(ScanDoneMsg{Result: res, Err: err})
		}
	}()
	_, err := m.prog.Run()
	cancel()
	<-m.scanDone // the scan goroutine always finishes before Run returns (TUI-5)
	return m.app.ExitCode, err
}
