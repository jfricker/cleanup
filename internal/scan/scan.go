// Package scan orchestrates one dry-run pass: the single walk, candidate dedup,
// worktree discovery, batched tracked check, parallel inspection and reconcile.
package scan

import (
	"context"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/gitx"
	"github.com/jfricker/cleanup/internal/match"
	"github.com/jfricker/cleanup/internal/rootguard"
	"github.com/jfricker/cleanup/internal/safety"
	"github.com/jfricker/cleanup/internal/walk"
	"github.com/jfricker/cleanup/internal/worktrees"
)

// Options select cleaners and tune a scan.
type Options struct {
	Only         []string // cleaner ids; empty = every enabled cleaner
	Exclude      []string
	OptIns       match.OptIns
	Retired      []string // tool ids treated as retired for this run
	AgeDays      int      // >0 overrides worktree and retired-tool age gates
	IncludeDirty bool
	// Protected are root-relative, slash-separated paths the scan never walks into
	// (PROT-4).
	Protected []string
	Workers   int
	Now       func() time.Time
}

// Phase names reported in events.
const (
	PhaseWalk    = "scanning"
	PhaseGit     = "listing worktrees"
	PhaseInspect = "inspecting"
	PhaseDone    = "done"
)

// Event reports progress. Emit callbacks must not block.
type Event struct {
	Phase      string
	Dirs       int64
	Path       string
	Candidates int
	Inspected  int
}

// Result is the outcome of a scan, sorted by path.
type Result struct {
	Verdicts   []safety.Verdict // candidates under the root, reconciled
	Outside    []safety.Verdict // worktrees outside the root (always protected)
	WalkErrors []walk.Error
	GitErrors  []worktrees.RepoError
	PruneRepos []string
	Dirs       int64
	Inspector  *safety.Inspector
}

// Cleaner describes an active cleaner, for display.
type Cleaner struct {
	ID, Label string
	SortOrder int
}

// Active returns the cleaners a scan with o would run, in sort order.
func Active(cfg *config.Config, o Options) []Cleaner {
	on := func(id string, enabled bool) bool {
		if slices.Contains(o.Exclude, id) {
			return false
		}
		if len(o.Only) > 0 {
			return slices.Contains(o.Only, id)
		}
		return enabled
	}
	var out []Cleaner
	for _, c := range cfg.Categories {
		if on(c.ID, c.Enabled) {
			out = append(out, Cleaner{c.ID, c.Label, c.SortOrder})
		}
	}
	if on(config.WorktreesID, cfg.Worktrees.Enabled) {
		out = append(out, Cleaner{config.WorktreesID, cfg.Worktrees.Label, cfg.Worktrees.SortOrder})
	}
	if on(config.RetiredID, cfg.Retired.Enabled) {
		out = append(out, Cleaner{config.RetiredID, cfg.Retired.Label, cfg.Retired.SortOrder})
	}
	slices.SortStableFunc(out, func(a, b Cleaner) int { return a.SortOrder - b.SortOrder })
	return out
}

// Run performs the scan. Cancelling ctx stops it promptly; the partial result is
// returned with ctx's error.
func Run(ctx context.Context, cfg *config.Config, root rootguard.Root, git *gitx.Runner, o Options, emit func(Event)) (*Result, error) {
	if emit == nil {
		emit = func(Event) {}
	}
	progress := throttle(emit, 100*time.Millisecond)
	active := map[string]bool{}
	for _, c := range Active(cfg, o) {
		active[c.ID] = true
	}

	var visitors []walk.Visitor
	var cats []config.Category
	for _, c := range cfg.Categories {
		if active[c.ID] {
			visitors = append(visitors, match.NewCategory(c, o.OptIns))
			cats = append(cats, c)
		}
	}
	if active[config.RetiredID] {
		if rv := match.NewRetired(cfg.Retired, o.Retired); rv != nil {
			visitors = append(visitors, rv)
		}
	}

	var dirs int64
	var mu sync.Mutex
	wres, err := walk.Run(ctx, root.Path, root.Dev, visitors, walk.Options{
		NeverTouch:      cfg.NeverTouch,
		AncestorMarkers: match.AncestorMarkers(cats),
		Workers:         o.Workers,
		Protected:       o.Protected,
		OnDir: func(rel string) {
			mu.Lock()
			dirs++
			n := dirs
			mu.Unlock()
			progress(Event{Phase: PhaseWalk, Dirs: n, Path: rel}, false)
		},
	})
	res := &Result{}
	if wres != nil {
		res.WalkErrors, res.Dirs = wres.Errors, wres.Dirs
	}
	if err != nil {
		return res, err
	}

	cands := dedup(wres.Found)
	if active[config.WorktreesID] {
		emit(Event{Phase: PhaseGit, Dirs: res.Dirs, Candidates: len(cands)})
		var repos []string
		for _, g := range wres.GitTrees {
			if g.IsRepo {
				repos = append(repos, g.Path)
			}
		}
		for _, g := range wres.GitTrees {
			if g.IsRepo {
				continue
			}
			d, derr := git.Dirs(ctx, g.Path)
			if derr != nil {
				res.GitErrors = append(res.GitErrors, worktrees.RepoError{Repo: g.Path, Err: derr})
				continue
			}
			if !d.Linked() || filepath.Base(d.CommonDir) != ".git" {
				continue
			}
			main := filepath.Dir(d.CommonDir)
			if underProtected(root.Path, main, o.Protected) && !slices.Contains(repos, main) {
				repos = append(repos, main) // PROT-5
			}
		}
		gitErrs := res.GitErrors
		wt := worktrees.Find(ctx, git, root.Path, repos, cfg.Worktrees)
		res.GitErrors, res.PruneRepos = append(gitErrs, wt.Errors...), wt.PruneRepos
		for _, c := range wt.Outside {
			res.Outside = append(res.Outside, safety.OutsideRoot(c))
		}
		cands = dedup(append(asFound(cands), asFound(wt.Candidates)...))
	}
	if o.AgeDays > 0 {
		for i := range cands {
			if cands[i].Kind != match.Artifact {
				cands[i].AgeDays = o.AgeDays
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}

	in := safety.NewInspector(root, git, safety.Config{IncludeDirty: o.IncludeDirty, NeverTouch: cfg.NeverTouch, Protected: o.Protected, Now: o.Now})
	res.Inspector = in
	emit(Event{Phase: PhaseInspect, Dirs: res.Dirs, Candidates: len(cands)})
	tracked := in.CheckTracked(ctx, cands)
	verdicts := make([]safety.Verdict, len(cands))
	jobs := make(chan int)
	var wg sync.WaitGroup
	var done int
	for range workers(o.Workers) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				verdicts[i] = in.Inspect(ctx, cands[i], tracked)
				mu.Lock()
				done++
				n := done
				mu.Unlock()
				progress(Event{Phase: PhaseInspect, Dirs: res.Dirs, Candidates: len(cands), Inspected: n, Path: cands[i].Rel}, false)
			}
		}()
	}
feed:
	for i := range cands {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return res, err
	}
	res.Verdicts = safety.Reconcile(verdicts)
	progress(Event{Phase: PhaseDone, Dirs: res.Dirs, Candidates: len(cands), Inspected: len(cands)}, true)
	return res, nil
}

// dedup keeps one candidate per path, from the cleaner with the lowest sort order
// (SPEC CAT-3).
func dedup(found []walk.Found) []match.Candidate {
	best := map[string]match.Candidate{}
	for _, f := range found {
		c := f.Payload.(match.Candidate)
		if cur, ok := best[c.Path]; !ok || c.SortOrder < cur.SortOrder {
			best[c.Path] = c
		}
	}
	out := make([]match.Candidate, 0, len(best))
	for _, c := range best {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b match.Candidate) int { return strings.Compare(a.Path, b.Path) })
	return out
}

func asFound(cs []match.Candidate) []walk.Found {
	out := make([]walk.Found, len(cs))
	for i, c := range cs {
		out[i] = walk.Found{Path: c.Path, Payload: c}
	}
	return out
}

// throttle forwards at most one non-final event per interval.
func throttle(emit func(Event), every time.Duration) func(Event, bool) {
	var mu sync.Mutex
	var last time.Time
	return func(e Event, final bool) {
		mu.Lock()
		now := time.Now()
		if !final && now.Sub(last) < every {
			mu.Unlock()
			return
		}
		last = now
		mu.Unlock()
		emit(e)
	}
}

func workers(n int) int {
	if n > 0 {
		return n
	}
	return runtime.GOMAXPROCS(0)
}

// Reclaimable returns the total size of eligible verdicts, counting nested eligible
// items once (a parent's size already includes its children).
func Reclaimable(vs []safety.Verdict) int64 {
	var total int64
	var lastTop string
	sorted := slices.Clone(vs)
	slices.SortFunc(sorted, func(a, b safety.Verdict) int { return strings.Compare(a.Candidate.Path, b.Candidate.Path) })
	for _, v := range sorted {
		if !v.Selectable() {
			continue
		}
		if lastTop != "" && strings.HasPrefix(v.Candidate.Path, lastTop+"/") {
			continue
		}
		lastTop = v.Candidate.Path
		total += v.Size
	}
	return total
}

// underProtected reports whether path lies at or under one of the root-relative
// protected paths.
func underProtected(root, path string, protected []string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return false
	}
	rel = filepath.ToSlash(rel)
	for _, p := range protected {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}
