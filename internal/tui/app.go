// Package tui is the interactive five-phase interface (SPEC TUI-1…6). App is a pure
// state machine: Update takes messages and returns effects instead of doing I/O, so
// every phase is testable without a terminal. program.go adapts it to Bubble Tea.
package tui

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jfricker/cleanup/internal/execute"
	"github.com/jfricker/cleanup/internal/safety"
	"github.com/jfricker/cleanup/internal/scan"
)

// Phase is the current screen.
type Phase int

// Phases.
const (
	Scanning Phase = iota
	Review
	Confirm
	Deleting
	Done
)

// GroupBy selects the review tree's top level.
type GroupBy int

// Groupings.
const (
	ByCleaner GroupBy = iota
	ByProject
)

// Effect is an action the adapter must perform.
type Effect int

// Effects.
const (
	EffectQuit Effect = iota + 1
	EffectCancelScan
	EffectStartDelete
	EffectSoftCancel
	EffectHardCancel
)

// Messages.
type (
	// ScanEventMsg carries scan progress.
	ScanEventMsg scan.Event
	// ScanDoneMsg ends the scanning phase.
	ScanDoneMsg struct {
		Result *scan.Result
		Err    error
	}
	// DeleteEventMsg carries deletion progress.
	DeleteEventMsg execute.Event
	// DeleteDoneMsg ends the deleting phase.
	DeleteDoneMsg struct {
		Report  *execute.Report
		LogPath string
		Code    int
		Err     error
	}
	// KeyMsg is a key press, as Bubble Tea names it ("up", "ctrl+c", "a").
	KeyMsg string
	// ResizeMsg is a terminal size change.
	ResizeMsg struct{ Width, Height int }
	// TickMsg animates the scan spinner.
	TickMsg struct{}
	// ThemeMsg reports whether the terminal background is dark.
	ThemeMsg struct{ Dark bool }
)

// Options configure an App.
type Options struct {
	Root        string
	Delete      bool
	ShowSkipped bool
	GroupBy     GroupBy
	Cleaners    []scan.Cleaner
}

type rowKind int

const (
	rowGroup rowKind = iota
	rowItem
)

type row struct {
	kind  rowKind
	key   string // group key
	label string
	idx   int // verdict index for items
	items []int
}

// App is the TUI state.
type App struct {
	Opts   Options
	Phase  Phase
	Width  int
	Height int
	Dark   bool

	ScanProgress scan.Event
	ScanStart    time.Time
	Tick         int

	Result    *scan.Result
	verdicts  []safety.Verdict // eligible and blocked, including outside-root
	selected  map[int]bool
	collapsed map[string]bool
	rows      []row
	cursor    int
	offset    int
	group     GroupBy
	filter    string
	filtering bool
	detail    bool // narrow layout: show the detail pane instead of the tree

	confirmDelete bool // focus in the confirm modal; false = Cancel

	DelDone, DelTotal int
	DelCurrent        string
	DelBytes          int64
	Stopping          bool
	interrupts        int

	Report   *execute.Report
	LogPath  string
	ExitCode int
	Err      error
}

const skippedKey = "\x00skipped"

// New returns an App in the scanning phase.
func New(o Options) *App {
	return &App{Opts: o, Phase: Scanning, Dark: true, Width: 100, Height: 30, ScanStart: time.Now(),
		group: o.GroupBy, selected: map[int]bool{}, collapsed: map[string]bool{}}
}

// Update applies msg and returns effects for the adapter.
func (a *App) Update(msg any) []Effect {
	switch m := msg.(type) {
	case ResizeMsg:
		a.Width, a.Height = m.Width, m.Height
		a.clampScroll()
	case ThemeMsg:
		a.Dark = m.Dark
	case TickMsg:
		a.Tick++
	case ScanEventMsg:
		a.ScanProgress = scan.Event(m)
	case ScanDoneMsg:
		return a.scanDone(m)
	case DeleteEventMsg:
		a.deleteEvent(execute.Event(m))
	case DeleteDoneMsg:
		a.Phase, a.Report, a.LogPath, a.ExitCode, a.Err = Done, m.Report, m.LogPath, m.Code, m.Err
	case KeyMsg:
		return a.key(string(m))
	}
	return nil
}

func (a *App) scanDone(m ScanDoneMsg) []Effect {
	if m.Err != nil {
		a.Err, a.ExitCode = m.Err, 2
		return []Effect{EffectQuit}
	}
	a.Result = m.Result
	a.verdicts = append(slices.Clone(m.Result.Verdicts), m.Result.Outside...)
	if a.Opts.Delete {
		for i, v := range a.verdicts {
			// Items with notes (ignored files, unpushed branches, -D) need an explicit choice.
			if v.Selectable() && len(v.Notes) == 0 {
				a.selected[i] = true
			}
		}
	}
	a.collapsed[skippedKey] = !a.Opts.ShowSkipped
	a.Phase = Review
	a.rebuild()
	return nil
}

func (a *App) deleteEvent(e execute.Event) {
	switch e.Kind {
	case execute.ItemStarted:
		a.DelTotal, a.DelCurrent = e.Total, e.Target.Rel()
	case execute.ItemDone:
		a.DelDone, a.DelTotal = e.Index+1, e.Total
		if e.Outcome.Kind == safety.Deleted {
			a.DelBytes += e.Outcome.Bytes
		}
	case execute.Pruning:
		a.DelCurrent = "git worktree prune"
	}
}

func (a *App) key(k string) []Effect {
	if k == "ctrl+c" {
		return a.interrupt()
	}
	switch a.Phase {
	case Scanning:
		if k == "q" || k == "esc" {
			a.ExitCode = 130
			return []Effect{EffectCancelScan, EffectQuit}
		}
	case Review:
		return a.reviewKey(k)
	case Confirm:
		return a.confirmKey(k)
	case Done:
		if k == "q" || k == "esc" || k == "enter" {
			return []Effect{EffectQuit}
		}
	}
	return nil
}

// interrupt maps Ctrl+C per phase. During deletion it never claims nothing was
// deleted: the first press finishes the current item, the second abandons it.
func (a *App) interrupt() []Effect {
	switch a.Phase {
	case Scanning:
		a.ExitCode = 130
		return []Effect{EffectCancelScan, EffectQuit}
	case Deleting:
		a.interrupts++
		a.Stopping = true
		if a.interrupts == 1 {
			return []Effect{EffectSoftCancel}
		}
		return []Effect{EffectHardCancel}
	default:
		return []Effect{EffectQuit}
	}
}

func (a *App) reviewKey(k string) []Effect {
	if a.filtering {
		switch k {
		case "enter", "esc":
			a.filtering = false
			if k == "esc" {
				a.filter = ""
			}
		case "backspace":
			if a.filter != "" {
				a.filter = a.filter[:len(a.filter)-1]
			}
		case "space":
			a.filter += " "
		default:
			if len([]rune(k)) == 1 {
				a.filter += k
			}
		}
		a.rebuild()
		return nil
	}
	switch k {
	case "q", "esc":
		return []Effect{EffectQuit}
	case "up", "k":
		a.move(-1)
	case "down", "j":
		a.move(1)
	case "pgup":
		a.move(-a.pageSize())
	case "pgdown":
		a.move(a.pageSize())
	case "home":
		a.move(-len(a.rows))
	case "end":
		a.move(len(a.rows))
	case "left", "h":
		if r, ok := a.current(); ok {
			if r.kind == rowItem {
				a.cursor = a.parentRow(a.cursor)
			} else {
				a.collapsed[r.key] = true
			}
			a.rebuild()
		}
	case "right", "l":
		if r, ok := a.current(); ok && r.kind == rowGroup {
			a.collapsed[r.key] = false
			a.rebuild()
		}
	case "tab", "enter":
		if r, ok := a.current(); ok && r.kind == rowGroup {
			a.collapsed[r.key] = !a.collapsed[r.key]
			a.rebuild()
		}
	case "space":
		if a.Opts.Delete {
			a.toggleCurrent()
		}
	case "a":
		if a.Opts.Delete {
			for i, v := range a.verdicts {
				if v.Selectable() {
					a.selected[i] = true
				}
			}
		}
	case "n":
		clear(a.selected)
	case "g":
		a.group = 1 - a.group
		a.rebuild()
	case "/":
		a.filtering = true
	case "i":
		a.detail = !a.detail
	case "d":
		if a.Opts.Delete && len(a.Selected()) > 0 {
			a.Phase, a.confirmDelete = Confirm, false
		}
	}
	return nil
}

func (a *App) confirmKey(k string) []Effect {
	switch k {
	case "left", "right", "tab", "h", "l":
		a.confirmDelete = !a.confirmDelete
	case "y":
		return a.startDelete()
	case "n", "esc", "q":
		a.Phase = Review
	case "enter":
		if a.confirmDelete {
			return a.startDelete()
		}
		a.Phase = Review
	}
	return nil
}

func (a *App) startDelete() []Effect {
	a.Phase, a.DelTotal = Deleting, len(a.Selected())
	return []Effect{EffectStartDelete}
}

// Selected returns the selected verdicts. Only selectable verdicts can be selected,
// so a skipped or protected item can never reach deletion (AC-T2).
func (a *App) Selected() []safety.Verdict {
	var out []safety.Verdict
	for i, v := range a.verdicts {
		if a.selected[i] && v.Selectable() {
			out = append(out, v)
		}
	}
	return out
}

// SelectedSize returns the total size of the selection, counting nested items once.
func (a *App) SelectedSize() int64 { return scan.Reclaimable(a.Selected()) }

func (a *App) toggleCurrent() {
	r, ok := a.current()
	if !ok {
		return
	}
	idxs := []int{r.idx}
	if r.kind == rowGroup {
		idxs = r.items
	}
	var selectable []int
	all := true
	for _, i := range idxs {
		if a.verdicts[i].Selectable() {
			selectable = append(selectable, i)
			all = all && a.selected[i]
		}
	}
	for _, i := range selectable {
		if all {
			delete(a.selected, i)
		} else {
			a.selected[i] = true
		}
	}
}

// rebuild recomputes the visible rows from verdicts, grouping, filter and collapse
// state.
func (a *App) rebuild() {
	type grp struct {
		key, label string
		order      int
		items      []int
	}
	groups := map[string]*grp{}
	var keys []string
	add := func(key, label string, order, idx int) {
		g, ok := groups[key]
		if !ok {
			g = &grp{key: key, label: label, order: order}
			groups[key] = g
			keys = append(keys, key)
		}
		g.items = append(g.items, idx)
	}
	labels := map[string]scan.Cleaner{}
	for _, c := range a.Opts.Cleaners {
		labels[c.ID] = c
	}
	filter := strings.ToLower(a.filter)
	for i, v := range a.verdicts {
		if filter != "" && !strings.Contains(strings.ToLower(v.Candidate.Rel), filter) {
			continue
		}
		if !v.Selectable() {
			add(skippedKey, "Skipped and protected", 1<<30, i)
			continue
		}
		if a.group == ByProject {
			p := project(v.Candidate.Rel)
			add("p:"+p, p, 0, i)
		} else {
			c := labels[v.Candidate.Cleaner]
			label := c.Label
			if label == "" {
				label = v.Candidate.CleanerLabel
			}
			add("c:"+v.Candidate.Cleaner, label, c.SortOrder, i)
		}
	}
	slices.SortStableFunc(keys, func(x, y string) int {
		gx, gy := groups[x], groups[y]
		if gx.order != gy.order {
			return gx.order - gy.order
		}
		return strings.Compare(gx.label, gy.label)
	})
	a.rows = a.rows[:0]
	for _, k := range keys {
		g := groups[k]
		a.rows = append(a.rows, row{kind: rowGroup, key: k, label: g.label, items: g.items})
		if a.collapsed[k] {
			continue
		}
		for _, i := range g.items {
			a.rows = append(a.rows, row{kind: rowItem, key: k, idx: i})
		}
	}
	a.clampScroll()
}

func project(rel string) string {
	if i := strings.IndexByte(rel, '/'); i > 0 {
		return rel[:i]
	}
	if filepath.IsAbs(rel) {
		return "(outside root)"
	}
	return "."
}

func (a *App) current() (row, bool) {
	if a.cursor < 0 || a.cursor >= len(a.rows) {
		return row{}, false
	}
	return a.rows[a.cursor], true
}

// Current returns the verdict under the cursor, if the cursor is on an item.
func (a *App) Current() (safety.Verdict, bool) {
	r, ok := a.current()
	if !ok || r.kind != rowItem {
		return safety.Verdict{}, false
	}
	return a.verdicts[r.idx], true
}

func (a *App) parentRow(i int) int {
	for ; i > 0 && a.rows[i].kind != rowGroup; i-- {
	}
	return i
}

func (a *App) move(d int) {
	a.cursor += d
	a.clampScroll()
}

func (a *App) pageSize() int { return max(a.bodyHeight()-1, 1) }

func (a *App) bodyHeight() int { return max(a.Height-4, 3) }

func (a *App) clampScroll() {
	a.cursor = min(max(a.cursor, 0), max(len(a.rows)-1, 0))
	h := a.bodyHeight()
	if a.cursor < a.offset {
		a.offset = a.cursor
	}
	if a.cursor >= a.offset+h {
		a.offset = a.cursor - h + 1
	}
	a.offset = max(a.offset, 0)
}
