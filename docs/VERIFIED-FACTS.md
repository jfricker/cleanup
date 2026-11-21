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

## 2026-11-21: cleanup v0.0.1 installation and first-run onboarding ([archive](archive/2026-11-21-cleanup-v0-0-1-installation-and-first-run-onboarding/))

- macOS `/usr/bin/man` has no `-l`; view a page from stdin with `mandoc -a`, or a file with `man ./cleanup.1` (verified 2026-11-21, Darwin 27). Linux uses `man -l -`.
- `gitx` resolves git from fixed paths (`/usr/bin`, `/opt/homebrew/bin`, `/usr/local/bin`) before PATH, so tests inject `env.GitBin` rather than a PATH wrapper.
- execute-tests (2026-11-21): `make check` green; `BenchmarkProbe` 0.39 s, `BenchmarkScan` 7.65 s (0.82x find). End-to-end with scratch `XDG_CONFIG_HOME` via a pty: non-TTY hint line, plain `--init -T` (root, protected `keep`, Cursor retired) wrote a strict-valid file, the next dry run used the config root, skipped `keep/`, and listed `old/.cursor`; `-l` showed `Root: … (from config)` and `Protected: keep ok`. Every automated SPEC AC has a `TestAC_*`; AC-T5 is `BenchmarkProbe`.
- NFR-8 (2026-11-21, darwin/arm64, NFR-1 fixture, `-benchtime 1x`): `BenchmarkProbe` 0.79 s vs `BenchmarkScan` 7.82 s; the probe only matches tool footprints and never inspects artifact trees.
