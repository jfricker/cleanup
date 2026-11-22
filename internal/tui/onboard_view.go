package tui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/jfricker/cleanup/internal/onboard"
	"github.com/jfricker/cleanup/internal/plain"
)

// style returns a style with the palette colour, or a plain one under NO_COLOR.
// Information is never carried by colour alone (TUI-6, NFR-10).
func (m *OnboardModel) style(pick func(palette) color.Color) lipgloss.Style {
	s := lipgloss.NewStyle()
	if m.NoColor {
		return s
	}
	return s.Foreground(pick(newPalette(m.Dark)))
}

func (m *OnboardModel) accent() lipgloss.Style {
	s := m.style(func(p palette) color.Color { return p.accent })
	if m.NoColor {
		return s
	}
	return s.Bold(true)
}
func (m *OnboardModel) muted() lipgloss.Style {
	return m.style(func(p palette) color.Color { return p.muted })
}
func (m *OnboardModel) warn() lipgloss.Style {
	return m.style(func(p palette) color.Color { return p.warn })
}
func (m *OnboardModel) bad() lipgloss.Style {
	return m.style(func(p palette) color.Color { return p.bad })
}

func wrapLines(s string, w int) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		out = append(out, strings.Split(hardWrap(l, w-2, 2), "\n")...)
	}
	return out
}

// innerH is the number of rows inside the frame.
func (m *OnboardModel) innerH() int { return max(m.Height-2, 3) }

// View renders the current step inside a single-line frame, Height lines of Width
// cells.
func (m *OnboardModel) View() string {
	w := max(m.Width-2, 18)
	step := m.Flow.Step()
	titles := map[onboard.Step]string{onboard.Root: "project root", onboard.Protected: "protected directories",
		onboard.Tools: "retired tools", onboard.Summary: "review"}
	title := fmt.Sprintf("cleanup setup · step %d of 4: %s", int(step)+1, titles[step])
	if m.written {
		title = "cleanup setup · done"
	}
	var body []string
	var footer string
	switch {
	case m.written:
		body = wrapLines(m.summary, w)
		footer = "Enter / q exit"
	case step == onboard.Root:
		body, footer = m.viewRoot(w)
	case step == onboard.Protected:
		body, footer = m.viewProtected(w)
	case step == onboard.Tools:
		body, footer = m.viewTools(w)
	default:
		body, footer = m.viewReview(w)
	}
	room := max(m.innerH()-2, 1)
	if len(body) > room {
		body = body[:room]
	}
	for i, l := range body {
		body[i] = fit(l, w)
	}
	for len(body) < room {
		body = append(body, "")
	}
	content := m.accent().Render(fit(title, w)) + "\n" + strings.Join(body, "\n") + "\n" + m.muted().Render(fit(footer, w))
	return frame(m.muted(), content, w, m.innerH())
}

func (m *OnboardModel) inputLine(prefix string, w int) string {
	e := m.input
	avail := max(w-lipgloss.Width(prefix)-1, 4)
	start := 0
	if e.cur > avail-1 {
		start = e.cur - (avail - 1)
	}
	end := min(start+avail, len(e.text))
	return prefix + string(e.text[start:e.cur]) + "▏" + string(e.text[e.cur:end])
}

func (m *OnboardModel) errLine(w int) []string {
	if m.errMsg == "" {
		return nil
	}
	var out []string
	for _, l := range wrapLines("error: "+m.errMsg, w) {
		out = append(out, m.bad().Render(l))
	}
	return out
}

func (m *OnboardModel) viewRoot(w int) ([]string, string) {
	var b []string
	if pw := m.Flow.PathWarning(); pw != "" {
		for _, l := range wrapLines("Warning: "+pw, w) {
			b = append(b, m.warn().Render(l))
		}
		b = append(b, "")
	}
	b = append(b, "Directory cleanup should scan.", "Use an absolute path or one starting with ~.", "",
		m.inputLine("> ", w))
	b = append(b, m.errLine(w)...)
	return b, "Enter next · ←→ edit · Ctrl+U clear · Esc cancel"
}

func (m *OnboardModel) viewProtected(w int) ([]string, string) {
	b := []string{"Protected directories are never scanned or cleaned.", "Root: " + truncLeft(m.Flow.RootDisplay(), w-6)}
	if n := m.Flow.Notice(); n != "" {
		for _, l := range wrapLines(n, w) {
			b = append(b, m.warn().Render(l))
		}
	}
	b = append(b, "")
	prot := m.Flow.Protected()
	if len(prot) == 0 {
		b = append(b, m.muted().Render("  (none)"))
	}
	reserved := len(b) + 4
	room := max(m.innerH()-2-reserved, 1)
	off := max(m.pcursor-room+1, 0)
	for i := off; i < len(prot) && i < off+room; i++ {
		prefix := "  "
		if i == m.pcursor && !m.typing {
			prefix = "> "
		}
		b = append(b, prefix+fit(prot[i], w-2))
	}
	if m.typing {
		b = append(b, "", m.inputLine("add> ", w))
	}
	b = append(b, m.errLine(w)...)
	if m.typing {
		return b, "Enter add (empty = done) · Ctrl+U clear · Esc cancel"
	}
	return b, "a add · d remove · ↑↓ select · Enter next · b back · Esc cancel"
}

func (m *OnboardModel) viewTools(w int) ([]string, string) {
	root := m.Flow.RootDisplay()
	if m.probing {
		b := []string{"Scanning for leftovers of tools you may no longer use (read-only).", m.muted().Render(truncLeft(root, w-2)), ""}
		phase := m.progress.Phase
		if phase == "" {
			phase = "starting"
		}
		b = append(b, fmt.Sprintf(" %s %s · %d directories", m.accent().Render(spinner[m.Tick%len(spinner)]), phase, m.progress.Dirs))
		return b, "b back · Esc cancel (nothing is written)"
	}
	var b []string
	if m.probeErr != nil {
		for _, l := range wrapLines("Could not finish the scan: "+m.probeErr.Error(), w) {
			b = append(b, m.bad().Render(l))
		}
	}
	fps := m.Flow.Footprints()
	if len(fps) == 0 {
		b = append(b, "No leftovers of configured retired tools were found.", "Press Enter to continue.")
		return b, "Enter next · b back · Esc cancel"
	}
	b = append(b, "Mark tools you no longer use; their leftovers become cleanable once old enough.", "")
	room := max(m.innerH()-2-len(b), 2)
	height := func(i int) int {
		if onboard.ToolNote(fps[i]) != "" {
			return 2
		}
		return 1
	}
	off := 0
	for {
		used := 0
		for i := off; i <= m.tcursor && i < len(fps); i++ {
			used += height(i)
		}
		if used <= room || off >= m.tcursor {
			break
		}
		off++
	}
	used := 0
	for i := off; i < len(fps) && used+height(i) <= room; i++ {
		fp := fps[i]
		cur, mark := "  ", "[ ]"
		if i == m.tcursor {
			cur = "> "
		}
		if m.Flow.Selected(fp.Tool.ID) {
			mark = "[x]"
		}
		b = append(b, fmt.Sprintf("%s%s %-14s %9s %9s  youngest %s", cur, mark, fit(onboard.ToolName(fp.Tool), 14),
			plainCount(fp.Items), plain.Bytes(fp.Size), onboard.FormatAge(fp.Youngest)))
		used++
		if n := onboard.ToolNote(fp); n != "" {
			b = append(b, m.warn().Render("      "+n))
			used++
		}
	}
	return b, "space toggle · ↑↓ move · Enter next · b back · Esc cancel"
}

func plainCount(n int) string {
	if n == 1 {
		return "1 item"
	}
	return fmt.Sprintf("%d items", n)
}

func (m *OnboardModel) viewReview(w int) ([]string, string) {
	b := []string{"Ready to write your config:", ""}
	b = append(b, wrapLines(m.Flow.Review(), w)...)
	if m.writing {
		return b, "writing…"
	}
	return b, "Enter write config · b back · Esc cancel"
}
