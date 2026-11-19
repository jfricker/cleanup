package gitx

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Dirs describes where a work tree's git data lives.
type Dirs struct {
	GitDir    string // absolute, canonical
	CommonDir string // absolute, canonical; differs from GitDir for linked worktrees
	TopLevel  string // absolute, canonical work tree root
}

// Linked reports whether the tree is a linked worktree of another repository.
func (d Dirs) Linked() bool { return d.GitDir != d.CommonDir }

// Dirs resolves the git directories of the work tree at tree.
func (r *Runner) Dirs(ctx context.Context, tree string) (Dirs, error) {
	out, err := r.Run(ctx, tree, "rev-parse", "--path-format=absolute",
		"--absolute-git-dir", "--git-common-dir", "--show-toplevel")
	if err != nil {
		return Dirs{}, err
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 3 {
		return Dirs{}, fmt.Errorf("rev-parse: unexpected output %q", truncate(string(out)))
	}
	var d Dirs
	for i, dst := range []*string{&d.GitDir, &d.CommonDir, &d.TopLevel} {
		p, err := filepath.EvalSymlinks(lines[i])
		if err != nil {
			return Dirs{}, fmt.Errorf("rev-parse: %w", err)
		}
		*dst = p
	}
	return d, nil
}

// filterArgs returns -c overrides that disable every filter driver configured for
// the repository at tree (SPEC GIT-1a). Reading config executes nothing.
func (r *Runner) filterArgs(ctx context.Context, tree string) ([]string, error) {
	out, err := r.Run(ctx, tree, "config", "--includes", "--name-only", "--get-regexp", `^filter\.`)
	if err != nil {
		if exitCode(err) == 1 { // no matching keys
			return nil, nil
		}
		return nil, err
	}
	seen := map[string]bool{}
	var args []string
	for _, key := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		first, last := strings.IndexByte(key, '.'), strings.LastIndexByte(key, '.')
		if first < 0 || last <= first {
			continue
		}
		name := key[first+1 : last]
		if seen[name] {
			continue
		}
		seen[name] = true
		for _, k := range []string{"clean=", "smudge=", "process=", "required=false"} {
			args = append(args, "-c", "filter."+name+"."+k)
		}
	}
	return args, nil
}

// Status returns porcelain v2 status for tree, including untracked and ignored files,
// with filter drivers disabled and submodules excluded (they are inspected separately).
func (r *Runner) Status(ctx context.Context, tree string) (Status, error) {
	extra, err := r.filterArgs(ctx, tree)
	if err != nil {
		return Status{}, err
	}
	out, err := r.run(ctx, tree, extra, []string{"status", "--porcelain=v2", "-z", "--branch",
		"--untracked-files=all", "--ignored=matching", "--ignore-submodules=all"})
	if err != nil {
		return Status{}, err
	}
	return ParseStatusV2(out)
}

// StashCount returns the number of stash entries in the repository of tree.
func (r *Runner) StashCount(ctx context.Context, tree string) (int, error) {
	_, err := r.Run(ctx, tree, "rev-parse", "--verify", "--quiet", "refs/stash")
	if err != nil {
		if exitCode(err) == 1 {
			return 0, nil
		}
		return 0, err
	}
	out, err := r.Run(ctx, tree, "rev-list", "--walk-reflogs", "--count", "refs/stash")
	if err != nil {
		return 0, err
	}
	return atoi(out)
}

// HeadReachable reports whether HEAD is contained in any ref other than refs/stash.
func (r *Runner) HeadReachable(ctx context.Context, tree string) (bool, error) {
	out, err := r.Run(ctx, tree, "for-each-ref", "--contains", "HEAD", "--format=%(refname)")
	if err != nil {
		return false, err
	}
	for _, ref := range strings.Split(string(out), "\n") {
		if ref != "" && ref != "refs/stash" {
			return true, nil
		}
	}
	return false, nil
}

// LocalOnlyCommits counts commits on local branches and tags not reachable from any
// remote-tracking ref. A repository without remotes counts all its commits.
func (r *Runner) LocalOnlyCommits(ctx context.Context, tree string) (int, error) {
	out, err := r.Run(ctx, tree, "rev-list", "--count", "--branches", "--tags", "--not", "--remotes")
	if err != nil {
		return 0, err
	}
	return atoi(out)
}

// Unpushed counts commits reachable from HEAD but from no remote-tracking ref.
func (r *Runner) Unpushed(ctx context.Context, tree string) (int, error) {
	out, err := r.Run(ctx, tree, "rev-list", "--count", "HEAD", "--not", "--remotes")
	if err != nil {
		return 0, err
	}
	return atoi(out)
}

// HeadTime returns the committer time of HEAD.
func (r *Runner) HeadTime(ctx context.Context, tree string) (time.Time, error) {
	out, err := r.Run(ctx, tree, "log", "-1", "--format=%ct", "HEAD")
	if err != nil {
		return time.Time{}, err
	}
	n, err := atoi(out)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(int64(n), 0), nil
}

// HeadOID returns the commit id of HEAD.
func (r *Runner) HeadOID(ctx context.Context, tree string) (string, error) {
	out, err := r.Run(ctx, tree, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// trackedChunk keeps each ls-files command line well under ARG_MAX.
const trackedChunk = 200

// Tracked returns the tracked files under the given paths (relative to the work tree
// root top), as paths relative to top. Paths are matched literally, never as globs or
// pathspec magic.
func (r *Runner) Tracked(ctx context.Context, top string, paths []string) ([]string, error) {
	var files []string
	for start := 0; start < len(paths); start += trackedChunk {
		end := min(start+trackedChunk, len(paths))
		args := append([]string{"--literal-pathspecs", "ls-files", "-z", "--"}, paths[start:end]...)
		out, err := r.Run(ctx, top, args...)
		if err != nil {
			return nil, err
		}
		files = append(files, splitNUL(out)...)
	}
	return files, nil
}

// Worktrees lists the worktrees of the repository at repo.
func (r *Runner) Worktrees(ctx context.Context, repo string) ([]Worktree, error) {
	out, err := r.Run(ctx, repo, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	return ParseWorktrees(out)
}

// WorktreeRemove runs `git worktree remove [--force] path` in repo. A refusal is
// returned as an error; callers must not fall back to removing files themselves.
func (r *Runner) WorktreeRemove(ctx context.Context, repo, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	_, err := r.Run(ctx, repo, append(args, "--", path)...)
	return err
}

// Prune runs `git worktree prune` in repo.
func (r *Runner) Prune(ctx context.Context, repo string) error {
	_, err := r.Run(ctx, repo, "worktree", "prune")
	return err
}

func atoi(b []byte) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("git: expected a number, got %q", truncate(string(b)))
	}
	return n, nil
}
