package worktrees

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/gitx"
	"github.com/jfricker/cleanup/internal/match"
	"github.com/jfricker/cleanup/internal/testfix"
)

func runner(t *testing.T) *gitx.Runner {
	t.Helper()
	r, err := gitx.New(gitx.Options{Env: testfix.Env(t)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

var prov = config.Provider{Enabled: true, Label: "Git Worktrees", AgeDays: 30, SortOrder: 90}

// AC-C1 (candidate side) and WT-1/WT-2.
func TestInsideOutsideAndPrunable(t *testing.T) {
	root := testfix.Root(t)
	repo, outside := testfix.OutsideWorktree(t, root)
	inside := testfix.Worktree(t, repo, filepath.Join(root, "inside-wt"), "inside")
	gone := testfix.Worktree(t, repo, filepath.Join(root, "gone-wt"), "gone")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	res := Find(context.Background(), runner(t), root, []string{repo}, prov)
	if len(res.Errors) != 0 {
		t.Fatalf("errors %v", res.Errors)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].Path != inside || res.Candidates[0].Rel != "inside-wt" {
		t.Fatalf("candidates %+v", res.Candidates)
	}
	c := res.Candidates[0]
	if c.Kind != match.Worktree || c.Repo != repo || c.AgeDays != 30 || c.Note != "inside" {
		t.Fatalf("candidate %+v", c)
	}
	if len(res.Outside) != 1 || res.Outside[0].Path != outside {
		t.Fatalf("outside %+v", res.Outside)
	}
	if len(res.PruneRepos) != 1 || res.PruneRepos[0] != repo {
		t.Fatalf("prune repos %v", res.PruneRepos)
	}
}

func TestUnder(t *testing.T) {
	for _, tc := range []struct {
		path string
		ok   bool
	}{{"/r/a", true}, {"/r", false}, {"/r2/a", false}, {"/a", false}, {"/r/../x", false}} {
		if _, ok := under("/r", filepath.Clean(tc.path)); ok != tc.ok {
			t.Errorf("under(/r, %s) = %v", tc.path, ok)
		}
	}
}
