// Onboarding screens (SPEC ONB-1, ONB-3, TOOL-7, NFR-10). OnboardModel is a pure state
// machine over onboard.Flow: Update takes messages and returns effects instead of
// doing I/O, and a thin tea.Model adapter (onboardProgram) performs them.
//
// Contract (shared with plain.Onboard): the front end never writes the config. On
// confirmation at the review step it runs the onboard.WriteFunc once and shows the
// text it returns; on cancel (Esc, Ctrl+C) it exits onboard.ExitCancelled and the
// WriteFunc is never called. Success is (0, nil), a failing WriteFunc (2, err).
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/jfricker/cleanup/internal/onboard"
	"github.com/jfricker/cleanup/internal/scan"
)

// ErrTUIUnavailable is returned by RunOnboard when the program failed before its first
// render, so the caller can fall back to plain prompts (ONB-1).
var ErrTUIUnavailable = errors.New("terminal UI unavailable")

// OnboardEffect is an action the adapter must perform.
type OnboardEffect int

// Onboarding effects.
const (
	OnbQuit OnboardEffect = iota + 1
	OnbStartProbe
	OnbCancelProbe
	OnbWrite
)

// Onboarding messages (KeyMsg, ResizeMsg, ThemeMsg and TickMsg are shared with App).
type (
	// PasteMsg is pasted text.
	PasteMsg string
	// ProbeEventMsg carries probe progress for probe generation Gen.
	ProbeEventMsg struct {
		Gen   int
		Event scan.Event
	}
	// ProbeDoneMsg ends probe generation Gen.
	ProbeDoneMsg struct {
		Gen        int
		Footprints []scan.ToolFootprint
		Err        error
	}
	// WriteDoneMsg is the result of the WriteFunc.
	WriteDoneMsg struct {
		Summary string
		Err     error
	}
)

// lineEditor is a single-line text editor (insert, backspace, delete, left/right,
// home/end, Ctrl+U, paste).
type lineEditor struct {
	text []rune
	cur  int
}

func newEditor(s string) lineEditor {
	r := []rune(s)
	return lineEditor{text: r, cur: len(r)}
}

func (e *lineEditor) String() string { return string(e.text) }

// Insert adds s at the cursor; control characters (including newlines from a paste)
// are dropped.
func (e *lineEditor) Insert(s string) {
	var add []rune
	for _, r := range s {
		if r >= ' ' && r != 0x7f {
			add = append(add, r)
		}
	}
	e.text = slices.Insert(e.text, e.cur, add...)
	e.cur += len(add)
}

func (e *lineEditor) Backspace() {
	if e.cur > 0 {
		e.text = slices.Delete(e.text, e.cur-1, e.cur)
		e.cur--
	}
}

func (e *lineEditor) Delete() {
	if e.cur < len(e.text) {
		e.text = slices.Delete(e.text, e.cur, e.cur+1)
	}
}

func (e *lineEditor) Left() { e.cur = max(e.cur-1, 0) }

func (e *lineEditor) Right() { e.cur = min(e.cur+1, len(e.text)) }

func (e *lineEditor) Home() { e.cur = 0 }

func (e *lineEditor) End() { e.cur = len(e.text) }

func (e *lineEditor) Clear() { e.text, e.cur = nil, 0 }

// Key applies an editing key and reports whether it was one.
func (e *lineEditor) Key(k string) bool {
	switch k {
	case "backspace":
		e.Backspace()
	case "delete":
		e.Delete()
	case "left":
		e.Left()
	case "right":
		e.Right()
	case "home", "ctrl+a":
		e.Home()
	case "end", "ctrl+e":
		e.End()
	case "ctrl+u":
		e.Clear()
	case "space":
		e.Insert(" ")
	default:
		if utf8.RuneCountInString(k) == 1 {
			e.Insert(k)
			return true
		}
		return false
	}
	return true
}

// OnboardModel is the onboarding TUI state.
type OnboardModel struct {
	Flow    *onboard.Flow
	Width   int
	Height  int
	Dark    bool
	NoColor bool
	Tick    int

	ExitCode int
	Err      error

	input  lineEditor
	errMsg string

	pcursor int
	typing  bool // protected step: the add-path input is active

	probing  bool
	gen      int
	progress scan.Event
	probeErr error
	tcursor  int
	started  time.Time

	writing bool
	written bool
	summary string
}

// NewOnboard returns a model at the root step.
func NewOnboard(f *onboard.Flow) *OnboardModel {
	m := &OnboardModel{Flow: f, Width: 80, Height: 24, Dark: true}
	m.input = newEditor(f.RootDisplay())
	return m
}

// Update applies msg and returns effects for the adapter.
func (m *OnboardModel) Update(msg any) []OnboardEffect {
	switch v := msg.(type) {
	case ResizeMsg:
		m.Width, m.Height = v.Width, v.Height
	case ThemeMsg:
		m.Dark = v.Dark
	case TickMsg:
		m.Tick++
	case ProbeEventMsg:
		if v.Gen == m.gen {
			m.progress = v.Event
		}
	case ProbeDoneMsg:
		if v.Gen != m.gen || !m.probing {
			return nil
		}
		m.probing = false
		m.probeErr = v.Err
		if v.Err == nil {
			m.Flow.SetProbe(v.Footprints)
		} else {
			m.Flow.SetProbe(nil)
		}
		m.tcursor = 0
	case WriteDoneMsg:
		m.writing = false
		if v.Err != nil {
			m.Err, m.ExitCode = v.Err, 2
			return []OnboardEffect{OnbQuit}
		}
		m.written, m.summary = true, v.Summary
	case PasteMsg:
		if m.inputActive() {
			m.input.Insert(string(v))
			m.errMsg = ""
		}
	case KeyMsg:
		return m.key(string(v))
	}
	return nil
}

// inputActive reports whether keys currently edit text (so "b" is a letter).
func (m *OnboardModel) inputActive() bool {
	switch m.Flow.Step() {
	case onboard.Root:
		return true
	case onboard.Protected:
		return m.typing
	}
	return false
}

func (m *OnboardModel) cancel() []OnboardEffect {
	m.ExitCode = onboard.ExitCancelled
	return []OnboardEffect{OnbCancelProbe, OnbQuit}
}

func (m *OnboardModel) key(k string) []OnboardEffect {
	if m.writing {
		return nil
	}
	if m.written {
		switch k {
		case "enter", "esc", "q", "ctrl+c":
			return []OnboardEffect{OnbQuit}
		}
		return nil
	}
	if k == "ctrl+c" || k == "esc" {
		return m.cancel()
	}
	switch m.Flow.Step() {
	case onboard.Root:
		return m.rootKey(k)
	case onboard.Protected:
		return m.protectedKey(k)
	case onboard.Tools:
		return m.toolsKey(k)
	case onboard.Summary:
		switch k {
		case "enter":
			m.writing = true
			return []OnboardEffect{OnbWrite}
		case "shift+tab", "b":
			return m.back()
		}
	}
	return nil
}

func (m *OnboardModel) rootKey(k string) []OnboardEffect {
	if k == "enter" {
		if err := m.Flow.SetRoot(m.input.String()); err != nil {
			m.errMsg = err.Error()
			return nil
		}
		m.errMsg = ""
		return m.advance()
	}
	if m.input.Key(k) {
		m.errMsg = ""
	}
	return nil
}

func (m *OnboardModel) protectedKey(k string) []OnboardEffect {
	if m.typing {
		if k == "enter" {
			s := m.input.String()
			if s == "" {
				m.typing = false
				return nil
			}
			if err := m.Flow.AddProtected(s); err != nil {
				m.errMsg = err.Error()
				return nil
			}
			m.errMsg = ""
			m.input.Clear()
			m.pcursor = len(m.Flow.Protected()) - 1
			return nil
		}
		if m.input.Key(k) {
			m.errMsg = ""
		}
		return nil
	}
	n := len(m.Flow.Protected())
	switch k {
	case "a":
		m.typing, m.errMsg = true, ""
		m.input.Clear()
	case "d", "delete", "backspace":
		if p := m.Flow.Protected(); m.pcursor < len(p) {
			m.Flow.RemoveProtected(p[m.pcursor])
			m.pcursor = min(m.pcursor, max(n-2, 0))
		}
	case "up", "k":
		m.pcursor = max(m.pcursor-1, 0)
	case "down", "j":
		m.pcursor = min(m.pcursor+1, max(n-1, 0))
	case "enter":
		return m.advance()
	case "shift+tab", "b":
		return m.back()
	}
	return nil
}

func (m *OnboardModel) toolsKey(k string) []OnboardEffect {
	switch k {
	case "shift+tab", "b":
		return m.back()
	}
	if m.probing {
		return nil
	}
	fps := m.Flow.Footprints()
	switch k {
	case "up", "k":
		m.tcursor = max(m.tcursor-1, 0)
	case "down", "j":
		m.tcursor = min(m.tcursor+1, max(len(fps)-1, 0))
	case "space", " ", "x":
		if m.tcursor < len(fps) {
			m.Flow.ToggleTool(fps[m.tcursor].Tool.ID)
		}
	case "enter":
		return m.advance()
	}
	return nil
}

func (m *OnboardModel) advance() []OnboardEffect {
	m.Flow.Next()
	return m.enter()
}

func (m *OnboardModel) back() []OnboardEffect {
	var eff []OnboardEffect
	if m.probing {
		m.probing = false
		m.gen++ // late probe messages are ignored
		eff = append(eff, OnbCancelProbe)
	}
	m.Flow.Back()
	return append(eff, m.enter()...)
}

// enter sets up the state of the step the flow is now on.
func (m *OnboardModel) enter() []OnboardEffect {
	m.errMsg = ""
	switch m.Flow.Step() {
	case onboard.Root:
		m.input = newEditor(m.Flow.RootDisplay())
	case onboard.Protected:
		m.typing = false
		m.input.Clear()
		m.pcursor = min(m.pcursor, max(len(m.Flow.Protected())-1, 0))
	case onboard.Tools:
		m.probing, m.probeErr, m.progress = true, nil, scan.Event{}
		m.started = time.Now()
		m.gen++
		return []OnboardEffect{OnbStartProbe}
	}
	return nil
}

// OnboardDeps are the inputs of RunOnboard.
type OnboardDeps struct {
	Flow  *onboard.Flow
	Write onboard.WriteFunc

	programOptions []tea.ProgramOption                   // tests only: input/output without a terminal
	run            func(*tea.Program) (tea.Model, error) // tests only: replaces (*tea.Program).Run
}

type onboardProgram struct {
	m        *OnboardModel
	deps     OnboardDeps
	prog     *tea.Program
	rendered atomic.Bool

	mu          sync.Mutex
	cancelProbe context.CancelFunc
	wg          sync.WaitGroup
}

func (p *onboardProgram) Init() tea.Cmd {
	return tea.Batch(tick(), tea.RequestBackgroundColor)
}

func (p *onboardProgram) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var effects []OnboardEffect
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		effects = p.m.Update(ResizeMsg{Width: msg.Width, Height: msg.Height})
	case tea.BackgroundColorMsg:
		effects = p.m.Update(ThemeMsg{Dark: msg.IsDark()})
	case tea.KeyPressMsg:
		effects = p.m.Update(KeyMsg(msg.String()))
	case tea.PasteMsg:
		effects = p.m.Update(PasteMsg(msg.Content))
	case tickMsg:
		p.m.Update(TickMsg{})
		if p.m.probing {
			return p, tick()
		}
		return p, nil
	case ProbeEventMsg, ProbeDoneMsg, WriteDoneMsg:
		effects = p.m.Update(msg)
	}
	return p, p.apply(effects)
}

func (p *onboardProgram) stopProbe() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancelProbe != nil {
		p.cancelProbe()
		p.cancelProbe = nil
	}
}

func (p *onboardProgram) apply(effects []OnboardEffect) tea.Cmd {
	var cmds []tea.Cmd
	for _, e := range effects {
		switch e {
		case OnbCancelProbe:
			p.stopProbe()
		case OnbStartProbe:
			p.stopProbe()
			ctx, cancel := context.WithCancel(context.Background())
			p.mu.Lock()
			p.cancelProbe = cancel
			p.mu.Unlock()
			gen, root, prot := p.m.gen, p.m.Flow.RootDisplay(), p.m.Flow.Protected()
			probe := p.m.Flow.Deps().Probe
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				fps, err := probe(ctx, root, prot, func(ev scan.Event) { p.prog.Send(ProbeEventMsg{Gen: gen, Event: ev}) })
				if ctx.Err() == nil {
					p.prog.Send(ProbeDoneMsg{Gen: gen, Footprints: fps, Err: err})
				}
			}()
			cmds = append(cmds, tick())
		case OnbWrite:
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				s, err := p.deps.Write()
				p.prog.Send(WriteDoneMsg{Summary: s, Err: err})
			}()
		case OnbQuit:
			cmds = append(cmds, tea.Quit)
		}
	}
	return tea.Batch(cmds...)
}

func (p *onboardProgram) View() tea.View {
	p.rendered.Store(true)
	v := tea.NewView(p.m.View())
	v.AltScreen = true
	v.WindowTitle = "cleanup setup"
	return v
}

// RunOnboard runs the onboarding TUI and blocks until it exits. It returns
// (onboard.ExitCancelled, nil) on cancel with the WriteFunc never called, (0, nil)
// after a successful write, and an error wrapping ErrTUIUnavailable when the program
// failed before its first render (the caller then falls back to plain prompts).
func RunOnboard(ctx context.Context, d OnboardDeps) (int, error) {
	m := NewOnboard(d.Flow)
	m.NoColor = os.Getenv("NO_COLOR") != ""
	p := &onboardProgram{m: m, deps: d}
	p.prog = tea.NewProgram(p, d.programOptions...)
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			p.prog.Send(KeyMsg("ctrl+c"))
		case <-stop:
		}
	}()
	run := d.run
	if run == nil {
		run = (*tea.Program).Run
	}
	_, err := run(p.prog)
	close(stop)
	p.stopProbe()
	p.wg.Wait()
	if err != nil {
		if !p.rendered.Load() {
			return 2, fmt.Errorf("%w: %v", ErrTUIUnavailable, err)
		}
		return 2, err
	}
	return m.ExitCode, m.Err
}
