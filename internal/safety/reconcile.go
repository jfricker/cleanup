package safety

import (
	"path/filepath"
	"slices"
	"strings"
)

// Reconcile enforces "parents never override children" (SPEC INSP-7): any eligible
// verdict whose path contains a skipped or protected verdict is downgraded to
// skipped and loses its target. It runs after all inspection, so cleaner order can
// never let a parent delete a skipped child. The result is sorted by path.
func Reconcile(vs []Verdict) []Verdict {
	out := slices.Clone(vs)
	slices.SortStableFunc(out, func(a, b Verdict) int { return strings.Compare(a.Candidate.Path, b.Candidate.Path) })
	byPath := map[string][]int{}
	for i, v := range out {
		byPath[v.Candidate.Path] = append(byPath[v.Candidate.Path], i)
	}
	for _, v := range vs {
		if v.Kind != Skipped && v.Kind != Protected {
			continue
		}
		child := v.Candidate.Path
		for p := filepath.Dir(child); p != child && p != "/" && p != "."; p = filepath.Dir(p) {
			for _, i := range byPath[p] {
				if out[i].Kind == Eligible || out[i].Kind == EligibleWithNotes {
					out[i].Kind = Skipped
					out[i].Reasons = []Reason{{Code: ReasonContainsSkipped, Detail: child}}
					out[i].Notes = nil
					out[i].target = Target{}
				}
			}
			child = p
		}
	}
	return out
}
