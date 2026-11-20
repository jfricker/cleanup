# cleanup — State

## Process

<!-- each stage ticks its own item here as it completes -->
- [x] 1. write-intent: intent.md approved
- [x] 2. generate-spec: spec.md approved
- [x] 3. generate-design: design.md approved
- [x] 4. generate-plan: plan.md approved
- [x] 5. implement-plan: code and tests written
- [x] 6. execute-tests: tests passing
- [x] 7. wrapup: cycle archived

## Implementation phases

<!-- written by implement-plan as steps land -->

- [x] 0. Scaffold and gate (`go.mod`, Makefile, tools pinned: staticcheck v0.8.2, govulncheck v1.9.0). Deviation: `.metaproject.json` lint is `make lint`, not `make check` (the edit hook's 60 s timeout and govulncheck's network access make `check` unsuitable per edit).
- [x] 1. Test fixtures (`internal/testfix`): scrubbed git env, backdating, builders for R1–R5, each GIT-2 condition, linked/unpushed, stash+worktree, hostile fsmonitor/filter, newline path, outside worktree/symlink.
- [x] 2. gitx: hardened runner (env scrub, -c flags, timeout + process-group kill, semaphore), filter neutralisation, porcelain v2 / worktree `-z` parsers with fuzz tests, queries. AC-G1/G2 (runner level), AC-G6, AC-G7, AC-G9 pass.
- [x] 3. config: embedded defaults (gleanup categories; `[retired_tools]` with `retired = false`), typed enums with invalid zero values, Undecoded() rejection with file:line and suggestions, merge by key/id, pattern validation, `.git` forced. AC-R5, AC-F1–F4, RET-3 pass. Port notes: gleanup's `delete_strategy` is `strategy` (SPEC CAT-2); `always_prune` replaced by `prune = ["node_modules"]` on node; per-provider `include_dirty` dropped (`-D` is CLI-only per SPEC).
- [x] 4. rootguard: canonical root, home from the password database, exact/subtree refusal rules (macOS + Linux), opens `*os.Root`. AC-C3 passes. Note: macOS temp dirs (`/private/var/...`) are refused by default, so tests pass `Options{SystemRules: []Rule{}}` and end-to-end binary tests must create roots outside `/private` (e.g. under the repo).
- [x] 5. walk: parallel single traversal, per-visitor descend masks (a prune or candidate stops only that visitor), ancestor-marker bitset, enclosing git tree, device check, unreadable dirs recorded, ReadDir counter. AC-W1 passes.
- [x] 6. match + worktrees: category visitor (names/globs, any-of sibling/ancestor/self markers, opt-ins, per-category prune), retired visitor (only retired or `--retired` tools, age gate carried), worktree provider (in-root candidates, outside-root list, prunable repos). Rule matrix, AC-R4 matcher side, AC-C1 candidate side pass.
- [x] 7. safety: unexported `Target`, fail-closed `Inspect` pipeline (containment, subtree scan, batched tracked check, GIT-2/3/5 per tree, AGE-1/2), `Reconcile`, revalidating `Delete` through `os.Root`, NFR-4 source scan (probe-verified). AC-R1–R4, AC-G1 (inspection level), AC-G3–G5, AC-G8, AC-G10, AC-C1, AC-C2, AC-C4, AC-D4, AC-E2 pass. Additions within spec intent: a candidate containing a non-`.git` never-touch entry (e.g. `.wrangler`) is skipped; an invalid strategy is skipped.
- [x] 8. scan + runlog + execute: `scan.Run` (walk → dedup → worktrees → batched tracked → inspector pool → Reconcile, throttled progress), `runlog` (XDG path, 0700/0600 JSONL), `execute.Runner` (soft/hard contexts, worktrees-then-deepest order, nested bytes counted once, prune after loop even when interrupted). AC-D1, AC-D2, AC-D3, scan cancel with goleak pass.
- [x] 9. cli + plain + main: flag table drives parsing, grouped help and man page; hidden `-y` error; positional ROOT kept for gleanup parity; ID and `--age` validation; exit codes 0/1/2/130; two-stage signal handling; plain renderer; run log opened before any deletion. AC-L1, AC-L2 (golden), end-to-end R1–R5 with `--delete --force`, AC-F3 via binary pass. Dry run over ~/Projects: 9 s, 195 eligible / 38 skipped-or-protected, all skips conservative.
- [x] 10. tui: pure `App` state machine (five phases, cleaner/project grouping, filter, detail pane, eligible-only selection with noted items not preselected, Cancel-focused confirm, soft/hard Ctrl+C in Deleting), Bubble Tea v2.1.1 + Lip Gloss v2.0.6 adapter using `Program.Send`, light/dark palette via `LightDark` plus glyphs. AC-T1, AC-T2, headless quit-during-scan goleak test, 80/160-column width checks pass. TUI tests use a real scan so no Target can be fabricated.
- [x] 11. bench + visual checks + README: `make bench` (cached fixture, 300 repos, 30 × 20k-file `node_modules`, `-m -v`) → 7.6 s/scan, 1.4× `find -type d` on this Mac (NFR-1 ≤ 15 s and ≤ 2× met). AC-T3: HTML snapshots of review/confirm/deleting/skipped at 80 and 160 columns in light and dark, reviewed in Chrome; fixed cursor-row highlight, glyph alignment, detail path wrapping and the 80-column footer. README rewritten.
- [x] execute-tests (2026-11-19): `make check` green (govulncheck: 0 reachable vulns; 2 in required modules not called). 34 of 36 ACs referenced by tests; AC-E1 verified by hand (`make build` leaves `git status` clean), AC-W2 by the bench run above. TUI dry run over ~/Projects checked in the app's real terminal (scan progress → review tree, 195 eligible); not checked in other terminal emulators or light terminal theme.

## Design invariants (regression guards)

<!-- written by implement-plan/execute-tests; appended to docs/DESIGN-INVARIANTS.md by wrapup -->

- Only `internal/safety` removes files; `TestNFR4_OnlySafetyDeletes` enforces it.
- `safety.Target` has no exported constructor; only `Inspect` builds one, and `Reconcile` clears it from any parent of a skipped/protected item.
- Every inspection error yields `skipped(inspection failed)`; no error path yields eligible.

## Open items carried into plan.md

<!-- written by implement-plan/execute-tests; resolved with the operator by wrapup -->

- SwiftPM `.build` dirs are skipped when `.build/checkouts/*` clones have local branches not on a remote (seen on ~/Projects). Safe but over-conservative; a future cycle could treat package-manager checkouts as regenerable.

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
