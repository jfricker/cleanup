// Package safety is the only code that decides whether something may be deleted and
// the only code that deletes. Matchers propose candidates; Inspect turns each into a
// Verdict; only eligible verdicts carry a Target, and Target can only be built here.
// Any error during inspection skips the item (fail closed).
package safety

import (
	"fmt"
	"strings"
	"time"

	"github.com/jfricker/cleanup/internal/match"
)

// VerdictKind classifies an inspected candidate.
type VerdictKind int

// Verdict kinds.
const (
	Eligible VerdictKind = iota + 1
	EligibleWithNotes
	Skipped
	Protected
)

func (k VerdictKind) String() string {
	switch k {
	case Eligible:
		return "eligible"
	case EligibleWithNotes:
		return "eligible-with-notes"
	case Skipped:
		return "skipped"
	case Protected:
		return "protected"
	}
	return "invalid"
}

// ReasonCode identifies why an item is skipped or protected.
type ReasonCode int

// Reason codes.
const (
	ReasonOutsideRoot ReasonCode = iota + 1
	ReasonSymlink
	ReasonTracked
	ReasonInspectionFailed
	ReasonFresh
	ReasonUncommitted
	ReasonUntracked
	ReasonStash
	ReasonDetached
	ReasonLocked
	ReasonLocalOnly
	ReasonContainsSkippedGitTree
	ReasonContainsSkipped
	ReasonContainsNeverTouch
	ReasonChangedSinceScan
)

var reasonText = map[ReasonCode]string{
	ReasonOutsideRoot:            "outside root",
	ReasonSymlink:                "symlink",
	ReasonTracked:                "tracked files",
	ReasonInspectionFailed:       "inspection failed",
	ReasonFresh:                  "fresh",
	ReasonUncommitted:            "uncommitted changes",
	ReasonUntracked:              "untracked files",
	ReasonStash:                  "stashes",
	ReasonDetached:               "unreachable detached HEAD",
	ReasonLocked:                 "locked",
	ReasonLocalOnly:              "unpushed commits",
	ReasonContainsSkippedGitTree: "contains skipped git tree",
	ReasonContainsSkipped:        "contains skipped item",
	ReasonContainsNeverTouch:     "contains never-touch path",
	ReasonChangedSinceScan:       "changed since scan",
}

// Reason is a typed explanation with optional detail.
type Reason struct {
	Code   ReasonCode
	Detail string
}

func (r Reason) String() string {
	if r.Detail == "" {
		return reasonText[r.Code]
	}
	return reasonText[r.Code] + ": " + r.Detail
}

// Note is information shown with an eligible item; it does not block deletion.
type Note struct {
	Text  string
	Paths []string // e.g. the first ignored files
}

// Verdict is the inspection result for one candidate.
type Verdict struct {
	Candidate match.Candidate
	Kind      VerdictKind
	Reasons   []Reason
	Notes     []Note
	Size      int64
	Age       time.Duration
	target    Target
}

// Target returns the deletion target of an eligible verdict.
func (v Verdict) Target() (Target, bool) {
	return v.target, v.target.v != nil && (v.Kind == Eligible || v.Kind == EligibleWithNotes)
}

// Selectable reports whether the verdict may be selected for deletion.
func (v Verdict) Selectable() bool {
	_, ok := v.Target()
	return ok
}

// ReasonText joins the reasons for display.
func (v Verdict) ReasonText() string {
	parts := make([]string, len(v.Reasons))
	for i, r := range v.Reasons {
		parts[i] = r.String()
	}
	return strings.Join(parts, "; ")
}

func skipped(c match.Candidate, code ReasonCode, format string, args ...any) Verdict {
	return Verdict{Candidate: c, Kind: Skipped, Reasons: []Reason{{Code: code, Detail: fmt.Sprintf(format, args...)}}}
}

func protected(c match.Candidate, code ReasonCode, detail string) Verdict {
	return Verdict{Candidate: c, Kind: Protected, Reasons: []Reason{{Code: code, Detail: detail}}}
}

// OutsideRoot builds the protected verdict for a candidate outside the scan root.
func OutsideRoot(c match.Candidate) Verdict { return protected(c, ReasonOutsideRoot, "") }
