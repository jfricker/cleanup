# cleanup — State

## Process

<!-- each stage ticks its own item here as it completes -->
- [x] 1. write-intent: intent.md approved
- [x] 2. generate-spec: spec.md approved
- [x] 3. generate-design: design.md approved
- [x] 4. generate-plan: plan.md approved
- [ ] 5. implement-plan: code and tests written
- [ ] 6. execute-tests: tests passing
- [ ] 7. wrapup: cycle archived

## Implementation phases

<!-- written by implement-plan as steps land -->

- [x] 0. Scaffold and gate (`go.mod`, Makefile, tools pinned: staticcheck v0.8.2, govulncheck v1.9.0). Deviation: `.metaproject.json` lint is `make lint`, not `make check` (the edit hook's 60 s timeout and govulncheck's network access make `check` unsuitable per edit).
- [x] 1. Test fixtures (`internal/testfix`): scrubbed git env, backdating, builders for R1–R5, each GIT-2 condition, linked/unpushed, stash+worktree, hostile fsmonitor/filter, newline path, outside worktree/symlink.
- [x] 2. gitx: hardened runner (env scrub, -c flags, timeout + process-group kill, semaphore), filter neutralisation, porcelain v2 / worktree `-z` parsers with fuzz tests, queries. AC-G1/G2 (runner level), AC-G6, AC-G7, AC-G9 pass.
- [x] 3. config: embedded defaults (gleanup categories; `[retired_tools]` with `retired = false`), typed enums with invalid zero values, Undecoded() rejection with file:line and suggestions, merge by key/id, pattern validation, `.git` forced. AC-R5, AC-F1–F4, RET-3 pass. Port notes: gleanup's `delete_strategy` is `strategy` (SPEC CAT-2); `always_prune` replaced by `prune = ["node_modules"]` on node; per-provider `include_dirty` dropped (`-D` is CLI-only per SPEC).
- [x] 4. rootguard: canonical root, home from the password database, exact/subtree refusal rules (macOS + Linux), opens `*os.Root`. AC-C3 passes. Note: macOS temp dirs (`/private/var/...`) are refused by default, so tests pass `Options{SystemRules: []Rule{}}` and end-to-end binary tests must create roots outside `/private` (e.g. under the repo).

## Design invariants (regression guards)

<!-- written by implement-plan/execute-tests; appended to docs/DESIGN-INVARIANTS.md by wrapup -->

## Open items carried into plan.md

<!-- written by implement-plan/execute-tests; resolved with the operator by wrapup -->

## Verified facts (do not re-investigate)

<!-- written by implement-plan/execute-tests; appended to docs/VERIFIED-FACTS.md by wrapup -->

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
