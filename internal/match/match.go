// Package match turns directory listings into cleanup candidates. Matchers only
// propose; they cannot delete anything. Every candidate goes through
// internal/safety before it can become a deletion target.
package match

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"

	"cleanup/internal/config"
	"cleanup/internal/walk"
)

// Kind is the source of a candidate.
type Kind int

// Candidate kinds.
const (
	Artifact Kind = iota + 1 // matched by a category rule
	Retired                  // matched by a retired tool
	Worktree                 // a registered linked worktree
)

// Candidate is a path proposed for cleanup.
type Candidate struct {
	Kind         Kind
	Cleaner      string // category id, config.RetiredID or config.WorktreesID
	CleanerLabel string
	SortOrder    int
	Rule         string // rule id, tool id, or "worktree"
	Group        string // display group (rule category, tool name)
	Badge        string
	Path         string // absolute canonical path
	Rel          string // relative to the root
	IsDir        bool
	Strategy     config.Strategy
	Warning      string
	AgeDays      int    // minimum age in days; 0 = no age gate
	GitTree      string // nearest enclosing git work tree of the candidate's parent, or ""
	Repo         string // worktrees: the main repository
	Note         string // tool note, for display
}

// OptIns are the CLI opt-in flags.
type OptIns struct {
	Venvs   bool
	Modules bool
}

func (o OptIns) allows(v config.OptIn) bool {
	switch v {
	case config.OptInNone:
		return true
	case config.OptInVenvs:
		return o.Venvs
	case config.OptInModules:
		return o.Modules
	}
	return false
}

// AncestorMarkers returns every ancestor marker used by the categories, for
// walk.Options.AncestorMarkers.
func AncestorMarkers(cats []config.Category) []string {
	var out []string
	for _, c := range cats {
		for _, r := range c.Rules {
			for _, m := range r.AncestorMarkers {
				if !slices.Contains(out, m) {
					out = append(out, m)
				}
			}
		}
	}
	return out
}

// CategoryVisitor matches one category's rules.
type CategoryVisitor struct {
	cat   config.Category
	rules []compiled
}

type compiled struct {
	config.Rule
	names map[string]bool
}

// NewCategory builds a visitor for cat, keeping only rules allowed by opts.
func NewCategory(cat config.Category, opts OptIns) *CategoryVisitor {
	v := &CategoryVisitor{cat: cat}
	for _, r := range cat.Rules {
		if !opts.allows(r.OptIn) {
			continue
		}
		c := compiled{Rule: r, names: map[string]bool{}}
		for _, n := range r.Names {
			c.names[n] = true
		}
		v.rules = append(v.rules, c)
	}
	return v
}

// Visit implements walk.Visitor.
func (v *CategoryVisitor) Visit(d *walk.Dir, out *walk.Out) {
	for _, e := range d.Entries {
		for _, r := range v.rules {
			if !kindMatches(r.Kind, e) || !nameMatches(r.names, r.Globs, e.Name()) || !markersOK(r.Rule, d, e) {
				continue
			}
			out.Candidate(e, Candidate{
				Kind: Artifact, Cleaner: v.cat.ID, CleanerLabel: v.cat.Label, SortOrder: v.cat.SortOrder,
				Rule: r.ID, Group: orDefault(r.Category, v.cat.Label), Badge: r.Badge,
				Path: filepath.Join(d.Path, e.Name()), Rel: rel(d, e), IsDir: e.IsDir(),
				Strategy: r.Strategy, Warning: r.Warning, GitTree: d.GitTree,
			})
			break
		}
	}
	for _, p := range v.cat.Prune {
		if d.Has(p) {
			out.Prune(p)
		}
	}
}

func kindMatches(k config.Kind, e fs.DirEntry) bool {
	switch k {
	case config.KindDir:
		return e.IsDir()
	case config.KindFile:
		return e.Type().IsRegular()
	}
	return false
}

func nameMatches(names map[string]bool, globs []string, name string) bool {
	if names[name] {
		return true
	}
	for _, g := range globs {
		if ok, _ := path.Match(g, name); ok {
			return true
		}
	}
	return false
}

// markersOK applies the marker requirements; each list is satisfied by any one entry.
func markersOK(r config.Rule, d *walk.Dir, e fs.DirEntry) bool {
	if len(r.SiblingMarkers) > 0 && !slices.ContainsFunc(r.SiblingMarkers, d.Has) {
		return false
	}
	if len(r.AncestorMarkers) > 0 && !slices.ContainsFunc(r.AncestorMarkers, d.HasAncestorMarker) {
		return false
	}
	if len(r.SelfMarkers) > 0 {
		base := filepath.Join(d.Path, e.Name())
		if !slices.ContainsFunc(r.SelfMarkers, func(m string) bool {
			_, err := os.Lstat(filepath.Join(base, filepath.FromSlash(m)))
			return err == nil
		}) {
			return false
		}
	}
	return true
}

// RetiredVisitor matches the directories and files of retired tools.
type RetiredVisitor struct {
	label     string
	sortOrder int
	tools     []config.Tool
}

// NewRetired builds a visitor for the tools that are retired, either in config or
// because their id is in extra (from --retired). It returns nil when none are.
func NewRetired(rt config.RetiredTools, extra []string) *RetiredVisitor {
	v := &RetiredVisitor{label: rt.Label, sortOrder: rt.SortOrder}
	for _, t := range rt.Tools {
		if t.Retired || slices.Contains(extra, t.ID) {
			v.tools = append(v.tools, t)
		}
	}
	if len(v.tools) == 0 {
		return nil
	}
	return v
}

// Tools returns the retired tools this visitor scans for.
func (v *RetiredVisitor) Tools() []config.Tool { return v.tools }

// Visit implements walk.Visitor.
func (v *RetiredVisitor) Visit(d *walk.Dir, out *walk.Out) {
	for _, e := range d.Entries {
		for _, t := range v.tools {
			if !nameMatches(nil, append(slices.Clone(t.Dirs), t.Files...), e.Name()) {
				continue
			}
			out.Candidate(e, Candidate{
				Kind: Retired, Cleaner: config.RetiredID, CleanerLabel: v.label, SortOrder: v.sortOrder,
				Rule: t.ID, Group: t.Name, Badge: "retired",
				Path: filepath.Join(d.Path, e.Name()), Rel: rel(d, e), IsDir: e.IsDir(),
				Strategy: config.StrategyRemove, AgeDays: t.AgeDays, GitTree: d.GitTree, Note: t.Note,
			})
			break
		}
	}
}

func rel(d *walk.Dir, e fs.DirEntry) string {
	if d.Rel == "." {
		return e.Name()
	}
	return d.Rel + "/" + e.Name()
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
