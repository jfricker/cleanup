package tui

import (
	"bytes"
	"fmt"
	"image/color"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"cleanup/internal/plain"
	"cleanup/internal/safety"
)

// palette holds colours for one background. State is always also shown by a glyph
// or word, never by colour alone (TUI-6).
type palette struct {
	accent, muted, ok, warn, bad, sel, selFg color.Color
}

func newPalette(dark bool) palette {
	ld := lipgloss.LightDark(dark)
	return palette{
		accent: ld(lipgloss.Color("#5A3FC0"), lipgloss.Color("#B4A2FF")),
		muted:  ld(lipgloss.Color("#5F6368"), lipgloss.Color("#9AA0A6")),
		ok:     ld(lipgloss.Color("#1E7F3C"), lipgloss.Color("#7EE2A0")),
		warn:   ld(lipgloss.Color("#8A5A00"), lipgloss.Color("#F5C451")),
		bad:    ld(lipgloss.Color("#B3261E"), lipgloss.Color("#FF8A80")),
		sel:    ld(lipgloss.Color("#DCD3FF"), lipgloss.Color("#3B2F73")),
		selFg:  ld(lipgloss.Color("#1B1340"), lipgloss.Color("#FFFFFF")),
	}
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// View renders the current phase.
func (a *App) View() string {
	p := newPalette(a.Dark)
	title := lipgloss.NewStyle().Bold(true).Foreground(p.accent)
	mode := "dry run"
	if a.Opts.Delete {
		mode = "delete"
	}
	header := title.Render("cleanup") + " " + lipgloss.NewStyle().Foreground(p.muted).Render(mode+" · "+truncLeft(a.Opts.Root, a.Width-20))
	var body string
	switch a.Phase {
	case Scanning:
		body = a.viewScanning(p)
	case Review:
		body = a.viewReview(p)
	case Confirm:
		body = a.viewConfirm(p)
	case Deleting:
		body = a.viewDeleting(p)
	case Done:
		body = a.viewDone(p)
	}
	return header + "\n" + body
}

func (a *App) viewScanning(p palette) string {
	e := a.ScanProgress
	phase := e.Phase
	if phase == "" {
		phase = "starting"
	}
	lines := []string{
		"",
		fmt.Sprintf(" %s %s", lipgloss.NewStyle().Foreground(p.accent).Render(spinner[a.Tick%len(spinner)]), phase),
		fmt.Sprintf("   directories  %d", e.Dirs),
		fmt.Sprintf("   candidates   %d", e.Candidates),
		fmt.Sprintf("   inspected    %d", e.Inspected),
		fmt.Sprintf("   elapsed      %s", time.Since(a.ScanStart).Round(time.Second)),
		"   " + lipgloss.NewStyle().Foreground(p.muted).Render(truncLeft(e.Path, a.Width-4)),
		"",
		lipgloss.NewStyle().Foreground(p.muted).Render(" q / Ctrl+C: stop the scan (nothing is deleted)"),
	}
	return strings.Join(lines, "\n")
}

func (a *App) viewReview(p palette) string {
	h := a.bodyHeight()
	wide := a.Width >= 100
	treeW := a.Width
	if wide {
		treeW = a.Width * 3 / 5
	}
	var tree []string
	for i := a.offset; i < len(a.rows) && i < a.offset+h; i++ {
		tree = append(tree, a.renderRow(p, i, treeW))
	}
	if len(a.rows) == 0 {
		tree = append(tree, lipgloss.NewStyle().Foreground(p.muted).Render("  Nothing found."))
	}
	for len(tree) < h {
		tree = append(tree, "")
	}
	treeBlock := strings.Join(tree, "\n")

	var main string
	switch {
	case wide:
		detail := lipgloss.NewStyle().Width(a.Width-treeW-3).Height(h).
			Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(p.muted).PaddingLeft(1).
			Render(a.detailText(p, a.Width-treeW-5))
		main = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(treeW).Render(treeBlock), detail)
	case a.detail:
		main = lipgloss.NewStyle().Height(h).Render(a.detailText(p, a.Width-2))
	default:
		main = treeBlock
	}
	return main + "\n" + a.footer(p)
}

func (a *App) renderRow(p palette, i, w int) string {
	r := a.rows[i]
	cursor := i == a.cursor
	var line string
	if r.kind == rowGroup {
		arrow := "▾"
		if a.collapsed[r.key] {
			arrow = "▸"
		}
		var size int64
		nsel := 0
		for _, idx := range r.items {
			size += a.verdicts[idx].Size
			if a.selected[idx] {
				nsel++
			}
		}
		sel := ""
		if a.Opts.Delete && r.key != skippedKey {
			sel = fmt.Sprintf(" %d/%d selected", nsel, len(r.items))
		}
		line = fmt.Sprintf("%s %s (%d, %s)%s", arrow, r.label, len(r.items), plain.Bytes(size), sel)
		line = fit(line, w-1)
		if !cursor {
			line = lipgloss.NewStyle().Bold(true).Render(line)
		}
	} else {
		v := a.verdicts[r.idx]
		glyph, st := a.glyph(p, r.idx, v)
		glyph = padRight(glyph, 4)
		if cursor {
			st = lipgloss.NewStyle() // the row highlight carries the colour
		}
		size := plain.Bytes(v.Size)
		name := fit(v.Candidate.Rel, w-len(size)-11)
		line = fmt.Sprintf("  %s %s %s", st.Render(glyph), padRight(name, w-len(size)-11), size)
	}
	if cursor {
		return lipgloss.NewStyle().Background(p.sel).Foreground(p.selFg).Render(padRight("›"+line, w))
	}
	return " " + line
}

func (a *App) glyph(p palette, idx int, v safety.Verdict) (string, lipgloss.Style) {
	switch {
	case v.Kind == safety.Protected:
		return "✗ ", lipgloss.NewStyle().Foreground(p.bad)
	case !v.Selectable():
		return "⊘ ", lipgloss.NewStyle().Foreground(p.warn)
	case !a.Opts.Delete && len(v.Notes) > 0:
		return "! ", lipgloss.NewStyle().Foreground(p.warn)
	case !a.Opts.Delete:
		return "• ", lipgloss.NewStyle().Foreground(p.ok)
	case a.selected[idx] && len(v.Notes) > 0:
		return "[!]", lipgloss.NewStyle().Foreground(p.warn)
	case a.selected[idx]:
		return "[✓]", lipgloss.NewStyle().Foreground(p.ok)
	case len(v.Notes) > 0:
		return "[ ]!", lipgloss.NewStyle().Foreground(p.warn)
	default:
		return "[ ]", lipgloss.NewStyle().Foreground(p.muted)
	}
}

func (a *App) detailText(p palette, w int) string {
	v, ok := a.Current()
	if !ok {
		if r, ok := a.current(); ok {
			return lipgloss.NewStyle().Bold(true).Render(r.label) + "\n\n" +
				lipgloss.NewStyle().Foreground(p.muted).Render(fmt.Sprintf("%d items", len(r.items)))
		}
		return ""
	}
	muted := lipgloss.NewStyle().Foreground(p.muted)
	c := v.Candidate
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render(fit(c.Rel, w)),
		"",
		muted.Render("status   ") + v.Kind.String(),
	}
	if len(v.Reasons) > 0 {
		lines = append(lines, muted.Render("reason   ")+wrap(v.ReasonText(), w-9))
	}
	lines = append(lines,
		muted.Render("size     ")+plain.Bytes(v.Size),
		muted.Render("age      ")+fmt.Sprintf("%dd", int(v.Age.Hours()/24)),
		muted.Render("cleaner  ")+c.CleanerLabel+" / "+c.Group,
		muted.Render("path     ")+hardWrap(c.Path, w-9, 9),
	)
	if c.Note != "" {
		lines = append(lines, muted.Render("about    ")+wrap(c.Note, w-9))
	}
	for _, n := range v.Notes {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(p.warn).Render("! ")+wrap(n.Text, w-2))
		for _, path := range n.Paths {
			lines = append(lines, "    "+fit(path, w-4))
		}
	}
	if c.Warning != "" {
		lines = append(lines, "", muted.Render(wrap(c.Warning, w)))
	}
	return strings.Join(lines, "\n")
}

func (a *App) footer(p palette) string {
	muted := lipgloss.NewStyle().Foreground(p.muted)
	if a.filtering {
		return lipgloss.NewStyle().Foreground(p.accent).Render("filter: "+a.filter+"▏") + muted.Render("  enter apply · esc clear")
	}
	keys := "↑↓ move · ←→ fold · g group · / filter · i detail · q quit"
	if a.Width < 100 {
		keys = "↑↓ ←→ · g group · / find · i info · q quit"
	}
	status := fmt.Sprintf("%d eligible", a.eligibleCount())
	if a.Opts.Delete {
		if a.Width < 100 {
			keys = "spc select · a all · d delete · / find · i info · q quit"
		} else {
			keys = "space select · a all · n none · d delete · " + keys
		}
		status = fmt.Sprintf("%d selected, %s", len(a.Selected()), plain.Bytes(a.SelectedSize()))
	}
	if a.filter != "" {
		status += " · filter: " + a.filter
	}
	return muted.Render(fit(keys, a.Width-len(status)-3)) + "  " + lipgloss.NewStyle().Bold(true).Render(status)
}

func (a *App) eligibleCount() int {
	n := 0
	for _, v := range a.verdicts {
		if v.Selectable() {
			n++
		}
	}
	return n
}

func (a *App) viewConfirm(p palette) string {
	sel := a.Selected()
	var warns []string
	var notes []string
	for _, v := range sel {
		if w := v.Candidate.Warning; w != "" && !slices.Contains(warns, w) {
			warns = append(warns, w)
		}
		for _, n := range v.Notes {
			notes = append(notes, v.Candidate.Rel+": "+n.Text)
		}
	}
	w := min(a.Width-4, 90)
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("Delete %d items (%s)?", len(sel), plain.Bytes(a.SelectedSize()))),
		"",
		"Each item is re-checked just before it is deleted. This cannot be undone.",
	}
	maxList := max(a.Height-16, 3)
	if len(warns) > 0 {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(p.warn).Render("Warnings"))
		for _, x := range warns {
			lines = append(lines, "  ! "+wrap(x, w-6))
		}
	}
	if len(notes) > 0 {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(p.warn).Render(fmt.Sprintf("Notes (%d)", len(notes))))
		for i, x := range notes {
			if i == maxList {
				lines = append(lines, fmt.Sprintf("  … and %d more (see the detail pane)", len(notes)-maxList))
				break
			}
			lines = append(lines, "  ! "+fit(x, w-6))
		}
	}
	btn := func(label string, focused bool, c color.Color) string {
		s := lipgloss.NewStyle().Padding(0, 2).Border(lipgloss.RoundedBorder()).BorderForeground(p.muted)
		if focused {
			s = s.Bold(true).BorderForeground(c).Foreground(c)
			label = "› " + label
		}
		return s.Render(label)
	}
	buttons := lipgloss.JoinHorizontal(lipgloss.Top, btn("Cancel", !a.confirmDelete, p.accent), "  ", btn("Delete", a.confirmDelete, p.bad))
	lines = append(lines, "", buttons, lipgloss.NewStyle().Foreground(p.muted).Render("←→ choose · enter confirm · y delete · n/esc back"))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.bad).Padding(1, 2).Width(w).Render(strings.Join(lines, "\n"))
	return "\n" + box
}

func (a *App) viewDeleting(p palette) string {
	total := max(a.DelTotal, 1)
	barW := max(min(a.Width-20, 60), 10)
	filled := barW * a.DelDone / total
	bar := lipgloss.NewStyle().Foreground(p.accent).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(p.muted).Render(strings.Repeat("░", barW-filled))
	lines := []string{
		"",
		fmt.Sprintf(" Deleting  %s  %d/%d", bar, a.DelDone, a.DelTotal),
		fmt.Sprintf(" freed     %s", plain.Bytes(a.DelBytes)),
		" " + lipgloss.NewStyle().Foreground(p.muted).Render(truncLeft(a.DelCurrent, a.Width-2)),
		"",
	}
	switch {
	case a.interrupts >= 2:
		lines = append(lines, lipgloss.NewStyle().Foreground(p.bad).Render(" Aborting the current item…"))
	case a.Stopping:
		lines = append(lines, lipgloss.NewStyle().Foreground(p.warn).Render(" Stopping after the current item… (Ctrl+C again to abort it)"))
	default:
		lines = append(lines, lipgloss.NewStyle().Foreground(p.muted).Render(" Ctrl+C: stop after the current item"))
	}
	return strings.Join(lines, "\n")
}

func (a *App) viewDone(p palette) string {
	if a.Err != nil {
		return "\n" + lipgloss.NewStyle().Foreground(p.bad).Render(" "+a.Err.Error()) + "\n\n press q to quit"
	}
	var buf bytes.Buffer
	if a.Report != nil {
		plain.Printer{W: &buf}.Report(*a.Report, a.LogPath)
	}
	return buf.String() + "\n" + lipgloss.NewStyle().Foreground(p.muted).Render("press q or enter to quit")
}

// fit truncates s to at most w display cells with an ellipsis.
func fit(s string, w int) string {
	if w <= 1 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// hardWrap breaks s into lines of at most w cells, indenting continuation lines.
func hardWrap(s string, w, indent int) string {
	if w < 10 {
		return s
	}
	var lines []string
	r := []rune(s)
	for len(r) > 0 {
		n := 0
		for n < len(r) && lipgloss.Width(string(r[:n+1])) <= w {
			n++
		}
		lines = append(lines, string(r[:n]))
		r = r[n:]
	}
	return strings.Join(lines, "\n"+strings.Repeat(" ", indent))
}

// truncLeft keeps the end of s (useful for paths).
func truncLeft(s string, w int) string {
	if w <= 1 || lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[1:]
	}
	return "…" + string(r)
}

func padRight(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func wrap(s string, w int) string {
	if w < 10 {
		return s
	}
	return lipgloss.NewStyle().Width(w).Render(s)
}
