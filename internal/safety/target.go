package safety

import (
	"cleanup/internal/config"
	"cleanup/internal/match"
)

// TargetKind is how a target is removed.
type TargetKind int

// Target kinds.
const (
	TargetRemove TargetKind = iota + 1
	TargetEmptyContents
	TargetWorktree
)

// Target is a verified deletion target. It has no exported constructor and its
// fields are unexported; the zero value is rejected by Delete.
type Target struct{ v *verified }

type verified struct {
	cand     match.Candidate
	rel      string // relative to the root, slash-separated
	abs      string
	dev, ino uint64
	isDir    bool
	kind     TargetKind
	size     int64
	trees    []snapshot
	force    bool // git worktree remove --force (only with --include-dirty)
}

// Path returns the absolute path.
func (t Target) Path() string { return t.v.abs }

// Rel returns the path relative to the root.
func (t Target) Rel() string { return t.v.rel }

// Size returns the size measured at inspection.
func (t Target) Size() int64 { return t.v.size }

// Kind returns how the target is removed.
func (t Target) Kind() TargetKind { return t.v.kind }

// Candidate returns the candidate the target was verified from.
func (t Target) Candidate() match.Candidate { return t.v.cand }

// Repo returns the repository of a worktree target.
func (t Target) Repo() string { return t.v.cand.Repo }

// Valid reports whether t was produced by inspection.
func (t Target) Valid() bool { return t.v != nil }

func targetKind(c match.Candidate) TargetKind {
	switch {
	case c.Kind == match.Worktree:
		return TargetWorktree
	case c.Strategy == config.StrategyEmptyContents:
		return TargetEmptyContents
	default:
		return TargetRemove
	}
}
