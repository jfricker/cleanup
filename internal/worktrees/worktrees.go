// Package worktrees proposes stale linked git worktrees under the scan root and
// collects repositories with gone (prunable) worktree entries (SPEC WT-1…3).
package worktrees

import (
	"context"
	"path/filepath"
	"strings"

	"cleanup/internal/config"
	"cleanup/internal/gitx"
	"cleanup/internal/match"
)

// Result is what the provider found.
type Result struct {
	Candidates []match.Candidate // linked worktrees under the root
	Outside    []match.Candidate // linked worktrees outside the root (never deletable)
	PruneRepos []string          // repositories with prunable entries
	Errors     []RepoError
}

// RepoError is a repository whose worktrees could not be listed.
type RepoError struct {
	Repo string
	Err  error
}

// Find lists the worktrees of each repository and classifies them.
func Find(ctx context.Context, git *gitx.Runner, root string, repos []string, p config.Provider) Result {
	var res Result
	for _, repo := range repos {
		wts, err := git.Worktrees(ctx, repo)
		if err != nil {
			res.Errors = append(res.Errors, RepoError{Repo: repo, Err: err})
			continue
		}
		pruned := false
		for i, wt := range wts {
			if i == 0 || wt.Bare {
				continue // the main worktree is the repository itself
			}
			if wt.Prunable {
				if !pruned {
					res.PruneRepos = append(res.PruneRepos, repo)
					pruned = true
				}
				continue
			}
			path, err := filepath.EvalSymlinks(wt.Path)
			if err != nil {
				res.Errors = append(res.Errors, RepoError{Repo: repo, Err: err})
				continue
			}
			c := match.Candidate{
				Kind: match.Worktree, Cleaner: config.WorktreesID, CleanerLabel: p.Label,
				SortOrder: p.SortOrder, Rule: "worktree", Group: filepath.Base(repo), Badge: "wt",
				Path: path, IsDir: true, Strategy: config.StrategyRemove, AgeDays: p.AgeDays, Repo: repo,
				Note: strings.TrimPrefix(wt.Branch, "refs/heads/"),
			}
			if rel, ok := under(root, path); ok {
				c.Rel = rel
				res.Candidates = append(res.Candidates, c)
			} else {
				c.Rel = path
				res.Outside = append(res.Outside, c)
			}
		}
	}
	return res
}

// under reports whether path is strictly inside root, returning the relative path.
func under(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
