package tui

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jfricker/cleanup/internal/execute"
)

// TestSnapshots writes HTML renderings of each screen at 80 and 160 columns in light
// and dark themes for visual review (AC-T3). It runs only when
// CLEANUP_SNAPSHOT_DIR is set.
func TestSnapshots(t *testing.T) {
	out := os.Getenv("CLEANUP_SNAPSHOT_DIR")
	if out == "" {
		t.Skip("set CLEANUP_SNAPSHOT_DIR to write snapshots")
	}
	res := result(t)
	var index strings.Builder
	index.WriteString("<!doctype html><meta charset=utf-8><title>cleanup TUI snapshots</title><body style='font-family:sans-serif'>")
	for _, dark := range []bool{true, false} {
		for _, w := range []int{80, 160} {
			screens := map[string]func(a *App){
				"1-review": func(a *App) { keys(a, "home", "down") },
				"2-confirm": func(a *App) {
					keys(a, "a", "d", "right")
				},
				"3-deleting": func(a *App) {
					keys(a, "d", "right", "enter", "ctrl+c")
					a.Update(DeleteEventMsg(execute.Event{Kind: execute.ItemDone, Index: 1, Total: 3}))
				},
				"4-skipped": func(a *App) {
					a.collapsed[skippedKey] = false
					a.rebuild()
					keys(a, "end")
				},
			}
			for _, name := range []string{"1-review", "2-confirm", "3-deleting", "4-skipped"} {
				a := New(Options{Root: "/Users/someone/Projects", Delete: true, Cleaners: cleaners})
				a.Update(ThemeMsg{Dark: dark})
				a.Update(ResizeMsg{Width: w, Height: 24})
				a.Update(ScanDoneMsg{Result: res})
				screens[name](a)
				theme := "light"
				if dark {
					theme = "dark"
				}
				file := fmt.Sprintf("%s-%s-%d.html", name, theme, w)
				writeHTML(t, filepath.Join(out, file), a.View(), dark, w)
				fmt.Fprintf(&index, "<h3>%s</h3><iframe src=%q width=%d height=470 style='border:0'></iframe>\n", file, file, w*9+40)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(out, "index.html"), []byte(index.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

var sgr = regexp.MustCompile("\x1b\\[([0-9;]*)m")

// writeHTML converts lipgloss's truecolor SGR output to HTML on a terminal-like
// background.
func writeHTML(t *testing.T, path, ansi string, dark bool, cols int) {
	bg, fg := "#ffffff", "#1f1f1f"
	if dark {
		bg, fg = "#1e1e1e", "#d4d4d4"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<!doctype html><meta charset=utf-8><body style='margin:0;background:%s'><pre style='margin:0;padding:12px;font:15px/1.25 Menlo,monospace;color:%s;width:%dch'>", bg, fg, cols)
	open := false
	style := map[string]string{}
	flush := func() {
		if open {
			b.WriteString("</span>")
		}
		var css []string
		for k, v := range style {
			css = append(css, k+":"+v)
		}
		b.WriteString("<span style='" + strings.Join(css, ";") + "'>")
		open = true
	}
	last := 0
	for _, m := range sgr.FindAllStringSubmatchIndex(ansi, -1) {
		b.WriteString(html.EscapeString(ansi[last:m[0]]))
		last = m[1]
		codes := strings.Split(ansi[m[2]:m[3]], ";")
		for i := 0; i < len(codes); i++ {
			switch codes[i] {
			case "", "0":
				style = map[string]string{}
			case "1":
				style["font-weight"] = "bold"
			case "22":
				delete(style, "font-weight")
			case "39":
				delete(style, "color")
			case "49":
				delete(style, "background")
			case "38", "48":
				if i+4 < len(codes) && codes[i+1] == "2" {
					r, _ := strconv.Atoi(codes[i+2])
					g, _ := strconv.Atoi(codes[i+3])
					bl, _ := strconv.Atoi(codes[i+4])
					key := "color"
					if codes[i] == "48" {
						key = "background"
					}
					style[key] = fmt.Sprintf("#%02x%02x%02x", r, g, bl)
					i += 4
				}
			}
		}
		flush()
	}
	b.WriteString(html.EscapeString(ansi[last:]))
	if open {
		b.WriteString("</span>")
	}
	b.WriteString("</pre>")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
