// Package plain renders scan results and deletion reports as stable plain text for
// non-TTY use and --no-tui (SPEC PLAIN-1…3).
package plain

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/jfricker/cleanup/internal/execute"
	"github.com/jfricker/cleanup/internal/safety"
	"github.com/jfricker/cleanup/internal/scan"
)

// Printer writes plain output.
type Printer struct {
	W           io.Writer
	ShowSkipped bool
}

// Scan prints eligible items grouped by cleaner, then (with ShowSkipped) skipped and
// protected items, then a summary. It returns the eligible verdicts in print order.
func (p Printer) Scan(res *scan.Result, cleaners []scan.Cleaner, root string, deleting bool) []safety.Verdict {
	mode := "dry run"
	if deleting {
		mode = "delete"
	}
	fmt.Fprintf(p.W, "cleanup %s: %s\n", mode, root)

	byCleaner := map[string][]safety.Verdict{}
	var blocked []safety.Verdict
	for _, v := range res.Verdicts {
		if v.Selectable() {
			byCleaner[v.Candidate.Cleaner] = append(byCleaner[v.Candidate.Cleaner], v)
		} else {
			blocked = append(blocked, v)
		}
	}
	blocked = append(blocked, res.Outside...)

	var eligible []safety.Verdict
	for _, c := range cleaners {
		vs := byCleaner[c.ID]
		if len(vs) == 0 {
			continue
		}
		var size int64
		for _, v := range vs {
			size += v.Size
		}
		fmt.Fprintf(p.W, "\n%s (%s, %s)\n", c.Label, count(len(vs), "item"), Bytes(size))
		for _, v := range vs {
			fmt.Fprintf(p.W, "  %-50s %10s\n", v.Candidate.Rel, Bytes(v.Size))
			for _, n := range v.Notes {
				fmt.Fprintf(p.W, "      note: %s\n", n.Text)
				for _, path := range n.Paths {
					fmt.Fprintf(p.W, "        %s\n", path)
				}
			}
		}
		eligible = append(eligible, vs...)
	}

	if p.ShowSkipped && len(blocked) > 0 {
		fmt.Fprintf(p.W, "\nSkipped and protected (%d)\n", len(blocked))
		for _, v := range blocked {
			fmt.Fprintf(p.W, "  %-50s %s: %s\n", v.Candidate.Rel, v.Kind, v.ReasonText())
		}
	}
	if len(res.WalkErrors) > 0 || len(res.GitErrors) > 0 {
		fmt.Fprintf(p.W, "\nScan problems (%d)\n", len(res.WalkErrors)+len(res.GitErrors))
		for _, e := range res.WalkErrors {
			fmt.Fprintf(p.W, "  %s\n", e.Error())
		}
		for _, e := range res.GitErrors {
			fmt.Fprintf(p.W, "  %s: %v\n", e.Repo, e.Err)
		}
	}

	fmt.Fprintf(p.W, "\n%s eligible, %s reclaimable", count(len(eligible), "item"), Bytes(scan.Reclaimable(res.Verdicts)))
	if len(blocked) > 0 {
		fmt.Fprintf(p.W, "; %d skipped or protected", len(blocked))
		if !p.ShowSkipped {
			fmt.Fprint(p.W, " (show with -s)")
		}
	}
	fmt.Fprintln(p.W, ".")
	if !deleting {
		fmt.Fprintln(p.W, "Dry run: nothing was deleted. Re-run with -d to delete.")
	}
	return eligible
}

// Warnings prints rule warnings and item notes before deletion (PLAIN-2).
func (p Printer) Warnings(vs []safety.Verdict) {
	var warns []string
	notes := 0
	for _, v := range vs {
		if w := v.Candidate.Warning; w != "" && !slices.Contains(warns, w) {
			warns = append(warns, w)
		}
		notes += len(v.Notes)
	}
	if len(warns) == 0 && notes == 0 {
		return
	}
	fmt.Fprintln(p.W, "\nWarnings")
	for _, w := range warns {
		fmt.Fprintf(p.W, "  ! %s\n", w)
	}
	if notes > 0 {
		fmt.Fprintf(p.W, "  ! %s attached to items above (ignored files, unpushed branches, stashes, -D)\n", count(notes, "note"))
	}
}

// Report prints the exact deletion result (DEL-6). Failures are printed only here.
func (p Printer) Report(rep execute.Report, logPath string) {
	fmt.Fprintln(p.W)
	if rep.Interrupted {
		fmt.Fprintln(p.W, "Interrupted.")
	}
	fmt.Fprintf(p.W, "Deleted %s, freed %s.\n", count(len(rep.Deleted), "item"), Bytes(rep.Bytes))
	section := func(title string, items []execute.Item, detail func(execute.Item) string) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(p.W, "%s (%d):\n", title, len(items))
		for _, it := range items {
			fmt.Fprintf(p.W, "  %s: %s\n", it.Target.Rel(), detail(it))
		}
	}
	section("Skipped, changed since scan", rep.Skipped, func(it execute.Item) string { return it.Outcome.Reason.String() })
	section("Failed", rep.Failed, func(it execute.Item) string { return errText(it.Outcome.Err) })
	section("Partially deleted", rep.Partial, func(it execute.Item) string { return errText(it.Outcome.Err) })
	if len(rep.NotAttempted) > 0 {
		fmt.Fprintf(p.W, "Not attempted (%d):\n", len(rep.NotAttempted))
		for _, t := range rep.NotAttempted {
			fmt.Fprintf(p.W, "  %s\n", t.Rel())
		}
	}
	if len(rep.Pruned) > 0 {
		fmt.Fprintf(p.W, "Pruned worktree entries in %s.\n", count(len(rep.Pruned), "repository"))
	}
	for _, err := range rep.PruneErrors {
		fmt.Fprintf(p.W, "Prune failed: %v\n", err)
	}
	if logPath != "" {
		fmt.Fprintf(p.W, "Run log: %s\n", logPath)
	}
}

func errText(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if strings.HasSuffix(noun, "y") {
		return fmt.Sprintf("%d %sies", n, strings.TrimSuffix(noun, "y"))
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// Bytes formats a size with binary units.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
