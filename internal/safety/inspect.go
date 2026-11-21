package safety

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/gitx"
	"github.com/jfricker/cleanup/internal/match"
	"github.com/jfricker/cleanup/internal/rootguard"
)

// Config tunes inspection.
type Config struct {
	IncludeDirty bool     // -D: relax GIT-2 conditions 1, 2 and 4
	NeverTouch   []string // names that must never be deleted
	Protected    []string // root-relative, slash-separated protected paths (PROT-4)
	Now          func() time.Time
}

// Inspector produces verdicts. It is safe for concurrent use.
type Inspector struct {
	root  rootguard.Root
	git   *gitx.Runner
	cfg   Config
	never map[string]bool
}

// NewInspector returns an inspector for root.
func NewInspector(root rootguard.Root, git *gitx.Runner, cfg Config) *Inspector {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	never := map[string]bool{}
	for _, n := range cfg.NeverTouch {
		if n != ".git" {
			never[n] = true
		}
	}
	return &Inspector{root: root, git: git, cfg: cfg, never: never}
}

// Tracked holds the result of the batched tracked-file check (SPEC INSP-4).
type Tracked struct {
	byPath map[string]trackedResult
}

type trackedResult struct {
	tracked bool
	err     error
}

// CheckTracked runs one `git ls-files` per enclosing work tree for all candidates
// inside it.
func (in *Inspector) CheckTracked(ctx context.Context, cands []match.Candidate) Tracked {
	t := Tracked{byPath: map[string]trackedResult{}}
	byTree := map[string][]match.Candidate{}
	for _, c := range cands {
		if c.GitTree != "" && c.GitTree != c.Path {
			byTree[c.GitTree] = append(byTree[c.GitTree], c)
		}
	}
	for tree, cs := range byTree {
		rels := make([]string, len(cs))
		for i, c := range cs {
			r, err := filepath.Rel(tree, c.Path)
			if err != nil {
				t.byPath[c.Path] = trackedResult{err: err}
				continue
			}
			rels[i] = filepath.ToSlash(r)
		}
		files, err := in.git.Tracked(ctx, tree, rels)
		for i, c := range cs {
			if _, done := t.byPath[c.Path]; done {
				continue
			}
			res := trackedResult{err: err}
			if err == nil {
				for _, f := range files {
					if f == rels[i] || strings.HasPrefix(f, rels[i]+"/") {
						res.tracked = true
						break
					}
				}
			}
			t.byPath[c.Path] = res
		}
	}
	return t
}

// Inspect runs the full pipeline for one candidate (SPEC INSP-1…6). It never
// returns an eligible verdict on any error path.
func (in *Inspector) Inspect(ctx context.Context, c match.Candidate, tr Tracked) Verdict {
	if c.Strategy == config.StrategyInvalid {
		return skipped(c, ReasonInspectionFailed, "invalid strategy")
	}

	// 1. Containment (INSP-3) and identity.
	rel, ok := relUnder(in.root.Path, c.Path)
	if !ok {
		return protected(c, ReasonOutsideRoot, "")
	}
	if p, hit := protectedMatch(in.cfg.Protected, rel); hit {
		return protected(c, ReasonProtectedPath, p)
	}
	if canon, err := filepath.EvalSymlinks(c.Path); err != nil {
		return skipped(c, ReasonInspectionFailed, "%v", err)
	} else if canon != c.Path {
		return protected(c, ReasonSymlink, "resolves to "+canon)
	}
	fi, err := in.root.FS.Lstat(rel)
	if err != nil {
		return skipped(c, ReasonInspectionFailed, "%v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return protected(c, ReasonSymlink, "")
	}
	if fi.IsDir() != c.IsDir {
		return skipped(c, ReasonInspectionFailed, "type changed")
	}
	dev, ino := identity(fi)

	// 2. Subtree scan.
	sub, err := scanSubtree(in.root.FS, rel, c.IsDir, in.never, nil)
	if err != nil {
		return skipped(c, ReasonInspectionFailed, "%v", err)
	}
	if sub.unreadable != "" {
		return skipped(c, ReasonInspectionFailed, "unreadable: %s", sub.unreadable)
	}
	if sub.neverTouch != "" {
		return skipped(c, ReasonContainsNeverTouch, "%s", sub.neverTouch)
	}

	// 3. Tracked files (INSP-4).
	if c.GitTree != "" && c.GitTree != c.Path {
		res, ok := tr.byPath[c.Path]
		if !ok {
			return skipped(c, ReasonInspectionFailed, "tracked check missing")
		}
		if res.err != nil {
			return skipped(c, ReasonInspectionFailed, "%v", res.err)
		}
		if res.tracked {
			return protected(c, ReasonTracked, "")
		}
	}

	// 4. Git rules for the candidate itself and every nested work tree (INSP-5, GIT-4).
	v := Verdict{Candidate: c, Kind: Eligible, Size: sub.size}
	var trees []snapshot
	selfTree, relaxed, force := (*treeResult)(nil), false, false
	for _, gt := range sub.gitTrees {
		abs := filepath.Join(in.root.Path, filepath.FromSlash(gt))
		tr, err := in.checkTree(ctx, abs)
		if err != nil {
			return skipped(c, ReasonInspectionFailed, "%s: %v", gt, err)
		}
		isSelf := gt == rel
		if len(tr.blockers) > 0 {
			if isSelf {
				return Verdict{Candidate: c, Kind: Skipped, Reasons: tr.blockers, Size: sub.size}
			}
			return skipped(c, ReasonContainsSkippedGitTree, "%s (%s)", gt, joinReasons(tr.blockers))
		}
		if isSelf {
			t := tr
			selfTree = &t
		}
		relaxed = relaxed || tr.relaxed
		force = force || (isSelf && tr.relaxed)
		for _, n := range tr.notes {
			if !isSelf {
				n.Text = gt + ": " + n.Text
			}
			v.Notes = append(v.Notes, n)
		}
		trees = append(trees, tr.snap)
	}
	if c.Kind == match.Worktree && (selfTree == nil || !selfTree.linked) {
		return skipped(c, ReasonInspectionFailed, "not a linked worktree")
	}

	// 5. Age (INSP-6, AGE-1, AGE-2).
	deep := relaxed || (c.Kind == match.Retired && c.GitTree == "")
	newest := sub.newest
	if !deep {
		if newest, err = shallowNewest(in.root.FS, rel, c.IsDir); err != nil {
			return skipped(c, ReasonInspectionFailed, "%v", err)
		}
	} else if selfTree != nil && relaxed {
		skip, err := in.ignoredSkipper(ctx, selfTree.snap.tree)
		if err != nil {
			return skipped(c, ReasonInspectionFailed, "%v", err)
		}
		again, err := scanSubtree(in.root.FS, rel, c.IsDir, in.never, skip)
		if err != nil {
			return skipped(c, ReasonInspectionFailed, "%v", err)
		}
		newest = again.newest
	}
	if selfTree != nil {
		ht, err := in.git.HeadTime(ctx, selfTree.snap.tree)
		if err != nil {
			return skipped(c, ReasonInspectionFailed, "%v", err)
		}
		newest = later(newest, ht)
		if ifi, err := os.Lstat(filepath.Join(selfTree.dirs.GitDir, "index")); err == nil {
			newest = later(newest, ifi.ModTime())
		}
	}
	v.Age = max(in.cfg.Now().Sub(newest), 0)
	if c.AgeDays > 0 && v.Age < time.Duration(c.AgeDays)*24*time.Hour {
		v.Kind, v.Reasons, v.Notes = Skipped, []Reason{{Code: ReasonFresh,
			Detail: fmt.Sprintf("%dd < %dd", int(v.Age.Hours()/24), c.AgeDays)}}, nil
		return v
	}

	// 6–7. Notes and target.
	if len(v.Notes) > 0 {
		v.Kind = EligibleWithNotes
	}
	v.target = Target{v: &verified{
		cand: c, rel: rel, abs: c.Path, dev: dev, ino: ino, isDir: c.IsDir,
		kind: targetKind(c), size: sub.size, trees: trees, force: force,
	}}
	return v
}

// ignoredSkipper returns a predicate matching git-ignored paths of tree, for AGE-2.
func (in *Inspector) ignoredSkipper(ctx context.Context, tree string) (func(string) bool, error) {
	st, err := in.git.Status(ctx, tree)
	if err != nil {
		return nil, err
	}
	ignored := map[string]bool{}
	for _, p := range st.Ignored {
		ignored[strings.TrimSuffix(p, "/")] = true
	}
	return func(p string) bool {
		for q := p; q != "." && q != "/" && q != ""; q = path.Dir(q) {
			if ignored[q] {
				return true
			}
		}
		return false
	}, nil
}

func joinReasons(rs []Reason) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = r.String()
	}
	return strings.Join(parts, "; ")
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// relUnder returns path relative to root (slash-separated) if strictly inside it.
func relUnder(root, p string) (string, bool) {
	r, err := filepath.Rel(root, p)
	if err != nil || r == "." || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return "", false
	}
	return filepath.ToSlash(r), true
}

// protectedMatch reports the first protected path that equals rel, contains it, or
// lies inside it (component-aware: a/b does not match a/bc).
func protectedMatch(protected []string, rel string) (string, bool) {
	for _, p := range protected {
		if rel == p || strings.HasPrefix(rel, p+"/") || strings.HasPrefix(p, rel+"/") {
			return p, true
		}
	}
	return "", false
}

func identity(fi os.FileInfo) (uint64, uint64) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev), st.Ino // Dev is int32 on darwin, uint64 on linux
	}
	return 0, 0
}
