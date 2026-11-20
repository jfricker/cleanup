# Verified facts (do not re-investigate)

Long-lived, append-only. `wrapup` moves STATE.md's "Verified facts (do not
re-investigate)" section here, dated and linked to the cycle's archive, before
resetting STATE.md. Read this before re-deriving something a prior cycle already
confirmed.

## 2026-11-19: cleanup v1 ([archive](archive/2026-11-19-cleanup-v1-a-safe-clean-go-rebuild-of-the-gleanup-prototype/))

Verified 2026-11-19 during generate-design (git 2.54.0 Apple Git-157, go 1.27.2, macOS).
Details in TECH-DESIGN.md "Verified facts".

- `git worktree remove` (no `--force`) succeeds when the only extra content is ignored
  files; refuses (exit 128) on untracked or modified files and on locked worktrees.
- `git worktree remove` keeps the branch; stashes are repo-wide (`refs/stash` is shared
  by all worktrees) and survive worktree removal.
- `git status` runs repo-local `filter.<name>.clean` commands even with
  `core.fsmonitor=false` and `core.hooksPath=/dev/null`; `-c filter.<name>.clean=
  -c filter.<name>.process=` neutralises them. `ls-files` and `rev-list` don't run
  filters. `core.attributesFile=/dev/null` / `GIT_ATTR_NOSYSTEM` don't help.
- Plain `git status` runs repo-local `core.fsmonitor`; `-c core.fsmonitor=false` stops it.
- `GIT_DIR` in the environment overrides `git -C <dir>`.
- `rev-list --count --branches --tags --not --remotes` counts local-only commits and
  returns all commits for a repo with no remote.
- `for-each-ref --contains HEAD` is empty for an unreachable detached commit.
- `status --porcelain=v2 -z --branch --ignored=matching --untracked-files=all` emits
  `? path` / `! path` records and `# branch.ab +N -M` when an upstream exists.
- `os.Root.RemoveAll` returns "path escapes from parent" when a parent component was
  swapped for an outside symlink, and removes only the link for a symlink leaf.
