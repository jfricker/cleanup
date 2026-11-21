// Package onboard is the pure state machine behind first-run onboarding (ONB, PROOT,
// PROT, TOOL, CFGW-7). It performs no terminal I/O; the TUI and plain front ends
// drive it and print what it returns.
package onboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/rootguard"
	"github.com/jfricker/cleanup/internal/scan"
)

// Step is one onboarding screen.
type Step int

const (
	Root Step = iota
	Protected
	Tools
	Summary
)

const defaultRoot = "~/Projects"

// Deps are the collaborators the flow needs.
type Deps struct {
	Home string
	// ResolveRoot canonicalises a tilde-expanded absolute path and applies the
	// existence, directory and rootguard checks; its error text is shown verbatim.
	ResolveRoot func(path string) (canonical string, err error)
	Probe       func(ctx context.Context, root string, protected []string, emit func(scan.Event)) ([]scan.ToolFootprint, error)
	PathWarning string // "" when the executable's dir is on PATH (ONB-5)
	// Tools are all configured tools; Changes carries a value for every one.
	Tools []config.Tool
}

// Initial is the pre-fill from flags and the existing config merged over defaults.
type Initial struct {
	Root          string // may use "~"
	Protected     []string
	Retired       map[string]bool
	AutoTriggered bool
	ConfigPath    string
	Replacing     bool // an existing config file will be replaced
}

// Flow is the onboarding state.
type Flow struct {
	d      Deps
	in     Initial
	step   Step
	root   string // canonical; "" until a valid root is set
	prot   []string
	notice string
	fps    []scan.ToolFootprint
	toggle map[string]bool
}

// New returns a flow at the root step, pre-filled from in (ONB-2).
func New(d Deps, in Initial) *Flow {
	f := &Flow{d: d, in: in, toggle: map[string]bool{}}
	f.prot = slices.Clone(in.Protected)
	if in.Root != "" {
		if c, err := f.resolve(in.Root); err == nil {
			f.root = c
		}
	}
	return f
}

// Deps returns the flow's dependencies (used by front ends to run the probe).
func (f *Flow) Deps() Deps { return f.d }

func (f *Flow) resolve(input string) (string, error) {
	p := rootguard.Expand(strings.TrimSpace(input), f.d.Home)
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("root must be an absolute path or start with ~ (got %q)", input)
	}
	return f.d.ResolveRoot(p)
}

// Step reports the current step.
func (f *Flow) Step() Step { return f.step }

// SetRoot validates input (PROOT-2) and, on success, sets the root. Protected entries
// that no longer exist under a changed root are dropped with a notice. On error the
// flow is unchanged.
func (f *Flow) SetRoot(input string) error {
	c, err := f.resolve(input)
	if err != nil {
		return err
	}
	f.notice = ""
	if c != f.root {
		var kept, dropped []string
		for _, e := range f.prot {
			if f.isDir(c, e) {
				kept = append(kept, e)
			} else {
				dropped = append(dropped, e)
			}
		}
		f.prot = kept
		f.fps = nil
		if len(dropped) > 0 {
			f.notice = "Dropped protected paths not found under the new root: " + strings.Join(dropped, ", ")
		}
	}
	f.root = c
	return nil
}

// Notice returns a one-off message from the last root change ("" if none).
func (f *Flow) Notice() string { return f.notice }

// RootDisplay is the canonical root, or the pre-fill when none is valid yet.
func (f *Flow) RootDisplay() string {
	if f.root != "" {
		return f.root
	}
	if f.in.Root != "" {
		return f.in.Root
	}
	return defaultRoot
}

func (f *Flow) isDir(root, rel string) bool {
	fi, err := os.Stat(filepath.Join(root, rel))
	return err == nil && fi.IsDir()
}

// AddProtected validates and adds a protected directory (PROT-1/2).
func (f *Flow) AddProtected(input string) error {
	if f.root == "" {
		return fmt.Errorf("choose a valid project root first")
	}
	s := strings.TrimSpace(input)
	if s == "" {
		return fmt.Errorf("path is empty")
	}
	rel := s
	if abs := rootguard.Expand(s, f.d.Home); filepath.IsAbs(abs) {
		r, err := filepath.Rel(f.root, filepath.Clean(abs))
		if err != nil || r == ".." || strings.HasPrefix(r, "../") {
			return fmt.Errorf("%s is outside the project root %s", s, f.root)
		}
		rel = r
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == ".." {
			return fmt.Errorf("%q contains \"..\"; use a path inside the project root", s)
		}
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return fmt.Errorf("%q is the project root itself, not a directory inside it", s)
	}
	fi, err := os.Stat(filepath.Join(f.root, rel))
	if err != nil {
		return fmt.Errorf("%s does not exist under %s", rel, f.root)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", rel)
	}
	for _, e := range f.prot {
		switch {
		case e == rel:
			return fmt.Errorf("%s is already protected", rel)
		case strings.HasPrefix(rel, e+"/"):
			return fmt.Errorf("%s is already covered by protected path %s", rel, e)
		case strings.HasPrefix(e, rel+"/"):
			return fmt.Errorf("%s contains protected path %s; remove that entry first", rel, e)
		}
	}
	f.prot = append(f.prot, rel)
	return nil
}

// RemoveProtected removes an entry; unknown entries are ignored.
func (f *Flow) RemoveProtected(rel string) {
	f.prot = slices.DeleteFunc(f.prot, func(e string) bool { return e == rel })
}

// Protected returns the current entries.
func (f *Flow) Protected() []string { return slices.Clone(f.prot) }

// Next advances a step. Leaving the root step requires a valid root.
func (f *Flow) Next() {
	if f.step == Root && f.root == "" {
		return
	}
	if f.step < Summary {
		f.step++
	}
}

// Back returns to the previous step, keeping all entries.
func (f *Flow) Back() {
	if f.step > Root {
		f.step--
	}
}

// SetProbe stores the step-3 footprints.
func (f *Flow) SetProbe(fps []scan.ToolFootprint) { f.fps = slices.Clone(fps) }

// Footprints returns the footprints shown in step 3.
func (f *Flow) Footprints() []scan.ToolFootprint { return slices.Clone(f.fps) }

func (f *Flow) shown(id string) bool {
	return slices.ContainsFunc(f.fps, func(fp scan.ToolFootprint) bool { return fp.Tool.ID == id })
}

// ToggleTool flips a shown tool; ids with no footprint are ignored.
func (f *Flow) ToggleTool(id string) {
	if f.shown(id) {
		f.toggle[id] = !f.Selected(id)
	}
}

// Selected reports whether the tool is marked retired (pre-selected from Initial).
func (f *Flow) Selected(id string) bool {
	if v, ok := f.toggle[id]; ok {
		return v
	}
	return f.in.Retired[id]
}

// projectRoot formats the root per PROOT-3.
func (f *Flow) projectRoot() string {
	if f.root == "" {
		return ""
	}
	home := filepath.Clean(f.d.Home)
	out := f.root
	if f.d.Home != "" {
		if r, err := filepath.Rel(home, f.root); err == nil && r != "." && r != ".." && !strings.HasPrefix(r, "../") {
			out = "~/" + filepath.ToSlash(r)
		}
	}
	if out == defaultRoot {
		return ""
	}
	return out
}

// Changes returns the decided managed values. Retired has an entry for every tool in
// Deps.Tools: the toggled value for shown tools, the Initial value otherwise.
func (f *Flow) Changes() config.Changes {
	ret := make(map[string]bool, len(f.d.Tools))
	for _, t := range f.d.Tools {
		if f.shown(t.ID) {
			ret[t.ID] = f.Selected(t.ID)
		} else {
			ret[t.ID] = f.in.Retired[t.ID]
		}
	}
	return config.Changes{ProjectRoot: f.projectRoot(), ProtectedPaths: f.Protected(), Retired: ret}
}

// PathWarning is the ONB-5 text ("" when none).
func (f *Flow) PathWarning() string { return f.d.PathWarning }

// Summary renders the CFGW-7 text for both front ends.
func (f *Flow) Summary(written, backup string) string {
	var b strings.Builder
	if written == "" {
		written = f.in.ConfigPath
	}
	fmt.Fprintf(&b, "Config written: %s\n", written)
	if backup != "" {
		fmt.Fprintf(&b, "Backup: %s\n", backup)
	}
	fmt.Fprintf(&b, "Project root: %s\n", f.root)
	if p := f.Protected(); len(p) > 0 {
		fmt.Fprintf(&b, "Protected paths: %s\n", strings.Join(p, ", "))
	} else {
		b.WriteString("Protected paths: none\n")
	}
	var names []string
	ch := f.Changes()
	for _, t := range f.d.Tools {
		if ch.Retired[t.ID] {
			n := t.Name
			if n == "" {
				n = t.ID
			}
			names = append(names, n)
		}
	}
	if len(names) > 0 {
		fmt.Fprintf(&b, "Tools marked retired: %s\n", strings.Join(names, ", "))
	} else {
		b.WriteString("Tools marked retired: none\n")
	}
	if f.in.Replacing {
		b.WriteString("Comments and formatting in the previous file were not preserved (the backup keeps them).\n")
	}
	b.WriteString("Next, try a dry run: cleanup\n")
	if f.in.AutoTriggered {
		b.WriteString("Setup is done: run your command again.\n")
	}
	return b.String()
}
