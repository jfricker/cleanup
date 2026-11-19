package safety

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"cleanup/internal/gitx"
)

// maxIgnoredListed caps the ignored paths listed in a note (SPEC GIT-3).
const maxIgnoredListed = 20

// snapshot is the git state a target was verified against (re-checked by Delete).
type snapshot struct {
	tree      string
	oid       string
	changed   []string
	untracked []string
	stashes   int
	linked    bool
}

// treeResult is the outcome of the GIT rules for one work tree.
type treeResult struct {
	blockers []Reason
	notes    []Note
	relaxed  bool // eligible only because of --include-dirty
	linked   bool
	dirs     gitx.Dirs
	snap     snapshot
}

var lockFiles = []string{"index.lock", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG", "rebase-merge", "rebase-apply", "locked"}

// checkTree applies SPEC GIT-2, GIT-3 and GIT-5 to the work tree at tree. Any git
// error is returned; the caller turns it into a skip.
func (in *Inspector) checkTree(ctx context.Context, tree string) (treeResult, error) {
	var r treeResult
	dirs, err := in.git.Dirs(ctx, tree)
	if err != nil {
		return r, err
	}
	r.dirs, r.linked = dirs, dirs.Linked()

	for _, f := range lockFiles {
		if _, err := os.Lstat(filepath.Join(dirs.GitDir, f)); err == nil {
			r.blockers = append(r.blockers, Reason{Code: ReasonLocked, Detail: f})
		} else if !os.IsNotExist(err) {
			return r, err
		}
	}

	st, err := in.git.Status(ctx, tree)
	if err != nil {
		return r, err
	}
	stashes, err := in.git.StashCount(ctx, tree)
	if err != nil {
		return r, err
	}
	r.snap = snapshot{tree: tree, oid: st.OID, changed: st.Changed, untracked: st.Untracked, stashes: stashes, linked: r.linked}

	var relaxable []Reason
	if len(st.Changed) > 0 {
		relaxable = append(relaxable, Reason{Code: ReasonUncommitted, Detail: count(len(st.Changed), "path")})
	}
	if len(st.Untracked) > 0 {
		relaxable = append(relaxable, Reason{Code: ReasonUntracked, Detail: count(len(st.Untracked), "file")})
	}
	if st.Detached() {
		ok, err := in.git.HeadReachable(ctx, tree)
		if err != nil {
			return r, err
		}
		if !ok {
			relaxable = append(relaxable, Reason{Code: ReasonDetached, Detail: shortOID(st.OID)})
		}
	}

	repo := filepath.Dir(dirs.CommonDir)
	if r.linked {
		if stashes > 0 {
			r.notes = append(r.notes, Note{Text: fmt.Sprintf("repo has %s; kept in %s", count(stashes, "stash"), repo)})
		}
		if !st.Detached() {
			n, err := in.git.Unpushed(ctx, tree)
			if err != nil {
				return r, err
			}
			if n > 0 {
				r.notes = append(r.notes, Note{Text: fmt.Sprintf("branch %s has %s; kept in %s", st.Head, count(n, "unpushed commit"), repo)})
			}
		}
	} else {
		if stashes > 0 {
			r.blockers = append(r.blockers, Reason{Code: ReasonStash, Detail: count(stashes, "stash")})
		}
		n, err := in.git.LocalOnlyCommits(ctx, tree)
		if err != nil {
			return r, err
		}
		if n > 0 {
			r.blockers = append(r.blockers, Reason{Code: ReasonLocalOnly, Detail: count(n, "commit") + " not on any remote"})
		}
	}

	if len(relaxable) > 0 {
		if in.cfg.IncludeDirty && len(r.blockers) == 0 {
			r.relaxed = true
			var what []string
			for _, x := range relaxable {
				what = append(what, x.String())
			}
			r.notes = append(r.notes, Note{Text: "eligible only because of --include-dirty (" + strings.Join(what, "; ") + ")"})
		} else {
			r.blockers = append(relaxable, r.blockers...)
		}
	}

	if len(st.Ignored) > 0 {
		listed := slices.Clone(st.Ignored[:min(len(st.Ignored), maxIgnoredListed)])
		r.notes = append(r.notes, Note{Text: fmt.Sprintf("%s will be deleted", count(len(st.Ignored), "ignored path")), Paths: listed})
	}
	return r, nil
}

// unchanged re-checks a snapshot before deletion (SPEC DEL-2).
func (in *Inspector) unchanged(ctx context.Context, s snapshot) (bool, string, error) {
	st, err := in.git.Status(ctx, s.tree)
	if err != nil {
		return false, "", err
	}
	if st.OID != s.oid {
		return false, "HEAD moved", nil
	}
	if !slices.Equal(st.Changed, s.changed) || !slices.Equal(st.Untracked, s.untracked) {
		return false, "working tree changed", nil
	}
	n, err := in.git.StashCount(ctx, s.tree)
	if err != nil {
		return false, "", err
	}
	if n != s.stashes {
		return false, "stashes changed", nil
	}
	return true, "", nil
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if strings.HasSuffix(noun, "sh") {
		return fmt.Sprintf("%d %ses", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func shortOID(oid string) string {
	if len(oid) > 12 {
		return oid[:12]
	}
	return oid
}
