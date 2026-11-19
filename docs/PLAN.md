# cleanup v1: a safe, clean Go rebuild of the gleanup prototype — Implementation Plan

**Author**: John Fricker.
**Derived from**: [SPEC.md](SPEC.md), [TECH-DESIGN.md](TECH-DESIGN.md) (approved 2026-11-19).
**Last updated**: 2026-11-19.
**Status**: Approved.
**Approved by**: John Fricker (2026-11-19), via plan-mode approval.

## Context

INTENT, SPEC (amended) and TECH-DESIGN are approved. This plan sequences the
greenfield Go build so each step lands with `make check` passing (global rule: no commit
unless lint, build and full tests pass). Implementation (`implement-plan`) starts only on
the operator's separate go-ahead.

## Steps

Each step ends with `make check` green, one commit (Co-Authored-By trailer), and
STATE.md "Implementation phases" updated.

0. **Scaffold and gate**: `go.mod` (`module cleanup`, `go 1.27`), `tool` directives for
   `honnef.co/go/tools/cmd/staticcheck` (v0.8.x) and `golang.org/x/vuln/cmd/govulncheck`;
   `Makefile` (`help build check test bench golden clean`, `CGO_ENABLED=0`);
   `.gitignore` (`/cleanup`, `/bin/`, `*.test`, `*.out`, `/dist/`);
   `cmd/cleanup/main.go` printing version; fill `AGENTS.md` quality gate (`make check`)
   and dev commands; set `.metaproject.json` `"commands": {"lint": "make check"}`.
1. **Test fixtures**: `internal/testfix`, with a scrubbed-env git helper, fixed
   author/dates (`GIT_COMMITTER_DATE`), and `Backdate(path, days)` via `os.Chtimes`.
   Builders for: the five review scenarios (R1–R5), each GIT-2 condition, a linked
   worktree with an unpushed branch, a repo stash, a malicious fsmonitor, a malicious
   filter driver, a newline path, an out-of-root worktree, and an outside symlink.
2. **gitx**: `runner.go` (binary resolution, env scrub, base `-c` args, timeout +
   `Setpgid`, semaphore 8), `filters.go` (GIT-1a), `parse.go` (porcelain v2 `-z`,
   worktree list `-z`) with fuzz tests, `queries.go` (table in TECH-DESIGN). Tests:
   AC-G6, AC-G7, AC-G9, stub-git exit 128 and timeout (AC-G1/G2 at runner level).
3. **config**: `defaults.toml` ported from
   `~/Projects/scratch/gleanup/internal/config/defaults.toml` (drop `[providers.orca]`;
   rename `[defunct_tools]` → `[retired_tools]` with `retired = false` and `age_days`,
   no `binaries`/`app_paths`/`status`; add `version = 1`); `raw.go`, `types.go` (typed
   enums, zero = invalid), `load.go` (Undecoded, version, merge, validate, `.git`
   forced), `lines.go` (file:line + Levenshtein suggestions). Tests: AC-R5, AC-F1–F4,
   merge semantics, RET-3.
4. **rootguard**: `Resolve` with canonical home from `os/user`, refusal table (macOS +
   Linux by `runtime.GOOS`), opens `*os.Root`. Tests: AC-C3 with a fake home injected via
   an internal option.
5. **walk**: parallel `ReadDir` walker, `DirView`, `Visitor`/`Decision`, persistent
   ancestor-marker set, device check, error list, ReadDir counter. Tests: AC-W1, symlinks
   never followed, prune/never_touch, unreadable dir recorded.
6. **match + worktrees**: category matcher (names/globs/markers/opt-ins/strategy),
   retired matcher, git-tree discovery visitor; `internal/worktrees` provider (in-root
   filter, prune actions, per-provider age). Tests: rule matrix per built-in category,
   AC-C1 (candidate side), WT-3.
7. **safety**: `target.go` (unexported `Target`), `inspect.go` (pipeline steps 1–7),
   `subtree.go` (size, nested trees, deep mtime), `git.go` (GIT-2/3/5 rules using gitx;
   stash = blocker for full clones, note for linked), `age.go` (AGE-1/2),
   `reconcile.go` (INSP-7), `delete.go` (revalidate + `os.Root` actions +
   `WorktreeRemove`). `safety/nfr4_test.go` scans the source tree for `os.Remove*`
   outside `internal/safety` (runlog allowed only `OpenFile`/`MkdirAll`). Tests:
   AC-R1–R4, AC-G3–G5, AC-G8, AC-G10, AC-C1, AC-C2, AC-C4, AC-D4.
8. **scan + runlog + execute**: `scan.Run` (walk → dedup → batched `Tracked` → inspector
   pool → Reconcile, coalescing emitter); `runlog` (XDG path, 0700/0600, JSONL);
   `execute.Run` (soft/hard contexts, sequential order, prune after loop, Report).
   Tests: AC-D1 (signal via contexts), AC-D2, AC-D3, scan cancel leaks nothing (goleak).
9. **cli + plain + main**: flag table (borrow the `modeFlag`/`commaListFlag` patterns
   from `~/Projects/scratch/gleanup/cmd/gleanup/cli.go`), hidden `-y` error, grouped
   help, man page generator (adapt `~/Projects/scratch/gleanup/cmd/gleanup/man.go` to the
   flag table), ID validation, exit codes, signal wiring (first → soft, second → hard),
   TTY detection, plain renderer. Tests: AC-L1, AC-L2 golden files, end-to-end binary
   tests (`TestMain` builds once) over the R1–R5 fixtures with `--delete --force`.
10. **tui**: pure `App` (phases, tree, selection of eligible items only, filter,
    grouping, detail, confirm focus = Cancel) returning `Effect`s; Bubble Tea v2
    adapter; palette with light/dark variants + glyphs; layouts for ≥100 and <100
    columns. Reference layout and rendering from
    `~/Projects/scratch/gleanup/internal/tui/` but fix #7 (Ctrl+C in Deleting = soft
    cancel). Tests: AC-T1, AC-T2, goleak.
11. **bench + manual checks + README**: `make bench` fixture generator (300 repos,
    30×20k-file `node_modules`), AC-W2 run on this Mac; AC-T3 screenshots at 80/160
    columns in light and dark terminal themes; README usage and safety model.

## Files touched

All new, under `~/Projects/cleanup`:

- Root: `go.mod`, `go.sum`, `Makefile`, `.gitignore`, `README.md`, `AGENTS.md` (edit),
  `.metaproject.json` (edit), `docs/STATE.md` (progress).
- `cmd/cleanup/`: main, signals, exit codes.
- `internal/{cli,config,rootguard,walk,match,worktrees,gitx,safety,scan,execute,runlog,plain,tui,testfix}/`:
  one package per TECH-DESIGN component, `*_test.go` alongside, golden files under
  `testdata/`.

## Sequencing

- 0 → 1 → {2, 3, 4} → 5 → 6 → 7 → 8 → {9, 10} → 11.
- 2, 3 and 4 are independent of each other (they need only 0 and 1); 9 and 10 both need
  8. They share `go.mod`/`go.sum`, so they run sequentially in one session to avoid
  module-file conflicts.
- `safety` (7) is the critical path. Nothing user-visible deletes until 7–9 are in.

### Dependencies (pinned at step 0)

Bubble Tea, Lip Gloss and Bubbles **v2** (`charm.land/bubbletea/v2`,
`charm.land/lipgloss/v2`, `charm.land/bubbles/v2`; latest on 2026-11-19: v2.1.2, v2.1.1,
v2.2.1), `github.com/BurntSushi/toml` v1.6.0, `github.com/spf13/pflag` v1.1.0,
`go.uber.org/goleak` v1.3.0. Pin the newest release that is at least 14 days old
(several v2 patches are days old). Run `govulncheck` after pinning.

## Risks / rollback

- **Greenfield, one commit per step**: rollback is `git revert` of the step. The
  prototypes are untouched.
- **Bubble Tea v2 API** differs from gleanup's v1 code: reuse layout ideas only, not
  code. Isolate it in the adapter so `App` tests don't depend on it.
- **BurntSushi line numbers** are heuristic: the fallback prints the key path without a
  line.
- **Git version drift**: fixture tests encode verified behaviour, so changes surface as
  test failures, not silent unsafety.
- **NFR-1 miss**: profile the walk first (likely `ReadDir` concurrency or the subtree
  scan). The targets are tuned, not hard-failing in `make check`.
- **Sandbox**: git worktree operations may need to run unsandboxed (global rule).

## Verification plan

- `make check` after every step (gofmt, vet, staticcheck, govulncheck, `go test -race`).
- Every SPEC acceptance criterion maps to a named test (`TestAC_R1_…` etc.).
  `execute-tests` greps for each AC ID to confirm coverage.
- End-to-end: run the built binary in dry run on a copy of the prototype-review fixture
  and on `~/Projects` (dry run only), compare with gleanup's output for parity of
  matches, and confirm the five data-loss cases are now skipped or protected.
- Manual: AC-T3 terminal screenshots and the AC-W2 benchmark on this Mac.
