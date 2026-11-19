// Package execute deletes selected targets one at a time, honours interruption
// exactly, runs git worktree prune afterwards and reports the true result
// (SPEC DEL-4…6, WT-5, LOG-1…3).
package execute

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"cleanup/internal/gitx"
	"cleanup/internal/runlog"
	"cleanup/internal/safety"
)

// Item is one target and what happened to it.
type Item struct {
	Target  safety.Target
	Outcome safety.Outcome
}

// Report is the exact result of a deletion run.
type Report struct {
	Deleted      []Item
	Skipped      []Item // changed since scan
	Failed       []Item
	Partial      []Item
	NotAttempted []safety.Target
	Bytes        int64
	Interrupted  bool
	Pruned       []string
	PruneErrors  []error
}

// Failures returns the number of failed or partial items.
func (r Report) Failures() int { return len(r.Failed) + len(r.Partial) }

// EventKind tags a DeleteEvent.
type EventKind int

// Event kinds.
const (
	ItemStarted EventKind = iota + 1
	ItemDone
	Pruning
	Finished
)

// Event reports deletion progress. Emit callbacks must not block.
type Event struct {
	Kind    EventKind
	Index   int // 0-based
	Total   int
	Target  safety.Target
	Outcome safety.Outcome
	Report  *Report // set on Finished
}

// Runner executes deletions.
type Runner struct {
	Deleter *safety.Deleter
	Git     *gitx.Runner
	Log     *runlog.Writer // must be open; nil only in tests
}

// ErrInterrupted describes an item abandoned by a hard interrupt.
var ErrInterrupted = errors.New("interrupted while deleting; may be partially deleted")

// Execute deletes targets. soft stops the run between items (first Ctrl+C); hard
// abandons the current item (second Ctrl+C). pruneRepos are repositories with gone
// worktree entries found by the scan; prune runs for them and for every repo whose
// worktree was removed, even after an interruption.
func (r Runner) Execute(hard, soft context.Context, targets []safety.Target, pruneRepos []string, emit func(Event)) Report {
	if emit == nil {
		emit = func(Event) {}
	}
	order := Order(targets)
	var rep Report
	r.log(func(l *runlog.Writer) error { return l.Start() })
	prune := slices.Clone(pruneRepos)

	for i, t := range order {
		if soft.Err() != nil || hard.Err() != nil {
			rep.Interrupted = true
			rep.NotAttempted = append(rep.NotAttempted, order[i:]...)
			break
		}
		emit(Event{Kind: ItemStarted, Index: i, Total: len(order), Target: t})
		done := make(chan safety.Outcome, 1)
		go func() { done <- r.Deleter.Delete(hard, t) }()
		var o safety.Outcome
		select {
		case o = <-done:
		case <-hard.Done():
			o = safety.Outcome{Kind: safety.Partial, Err: ErrInterrupted}
		}
		it := Item{Target: t, Outcome: o}
		switch o.Kind {
		case safety.Deleted:
			// A parent's measured size includes children already deleted in this run.
			for _, d := range rep.Deleted {
				if strings.HasPrefix(d.Target.Path(), t.Path()+"/") {
					o.Bytes -= d.Outcome.Bytes
				}
			}
			o.Bytes = max(o.Bytes, 0)
			it.Outcome = o
			rep.Deleted = append(rep.Deleted, it)
			rep.Bytes += o.Bytes
			if t.Kind() == safety.TargetWorktree && !slices.Contains(prune, t.Repo()) {
				prune = append(prune, t.Repo())
			}
		case safety.NotDeleted:
			rep.Skipped = append(rep.Skipped, it)
		case safety.Partial:
			rep.Partial = append(rep.Partial, it)
		default:
			rep.Failed = append(rep.Failed, it)
		}
		var errForLog error = o.Err
		if o.Reason != nil {
			errForLog = errors.New(o.Reason.String())
		}
		r.log(func(l *runlog.Writer) error {
			return l.Item(t.Path(), t.Candidate().Cleaner, o.Bytes, o.Kind.String(), errForLog)
		})
		emit(Event{Kind: ItemDone, Index: i, Total: len(order), Target: t, Outcome: o})
		if errors.Is(o.Err, ErrInterrupted) {
			rep.Interrupted = true
			rep.NotAttempted = append(rep.NotAttempted, order[i+1:]...)
			break
		}
	}
	for _, t := range rep.NotAttempted {
		r.log(func(l *runlog.Writer) error {
			return l.Item(t.Path(), t.Candidate().Cleaner, 0, "not attempted", nil)
		})
	}

	// Prune must run even after an interruption, so it gets its own context.
	if len(prune) > 0 {
		emit(Event{Kind: Pruning, Total: len(order)})
		pctx, cancel := context.WithTimeout(context.WithoutCancel(hard), 2*time.Minute)
		for _, repo := range prune {
			if err := r.Git.Prune(pctx, repo); err != nil {
				rep.PruneErrors = append(rep.PruneErrors, err)
			} else {
				rep.Pruned = append(rep.Pruned, repo)
			}
		}
		cancel()
	}

	outcome := "complete"
	if rep.Interrupted {
		outcome = "interrupted"
	} else if rep.Failures() > 0 {
		outcome = "completed with failures"
	}
	r.log(func(l *runlog.Writer) error { return l.End(rep.Bytes, outcome) })
	emit(Event{Kind: Finished, Total: len(order), Report: &rep})
	return rep
}

func (r Runner) log(f func(*runlog.Writer) error) {
	if r.Log != nil {
		_ = f(r.Log) // a log write failure must not stop or misreport deletion
	}
}

// Order sorts targets for deletion: worktrees first (so git can remove them), then
// deepest paths first, then by path.
func Order(ts []safety.Target) []safety.Target {
	out := slices.Clone(ts)
	slices.SortStableFunc(out, func(a, b safety.Target) int {
		aw, bw := a.Kind() == safety.TargetWorktree, b.Kind() == safety.TargetWorktree
		if aw != bw {
			if aw {
				return -1
			}
			return 1
		}
		if da, db := strings.Count(a.Path(), "/"), strings.Count(b.Path(), "/"); da != db {
			return db - da
		}
		return strings.Compare(a.Path(), b.Path())
	})
	return out
}
