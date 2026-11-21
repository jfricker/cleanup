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
- [x] 1. Module rename (bf88081)
- [x] 2. Guards and version (459eeb3)
- [x] 3. rootguard helpers (d04e683)
- [x] 4. Config schema (e74b62e)
- [x] 5. Config writing (846541f)
- [x] 6. Walk and scan protection (a1a28b9)
- [x] 7. Safety protection (7266542)
- [x] 8. Run-time root, hint and `-l` (3ef8e3a)
- [x] 9. Probe (809f1e2)
- [x] 10. Onboarding model (c061935)
- [x] 11. Front ends (e5b2f62)
- [x] 12. Wire onboarding (6341718)
- [x] 13. Release files and docs (8c7d0e0)

## Design invariants (regression guards)

<!-- written by implement-plan/execute-tests; appended to docs/DESIGN-INVARIANTS.md by wrapup -->
- `config.Plan` leaves a tool's `retired` untouched when its id is absent from `Changes.Retired`; onboarding must pass a value for every configured tool.
- `config.Write` never removes files: fixed temp `.<name>.new` truncated and reused; backups created with `O_EXCL`.

## Open items carried into plan.md

<!-- written by implement-plan/execute-tests; resolved with the operator by wrapup -->
- Manual, not run: AC-I4/I5 (`goreleaser check`, `goreleaser release --snapshot --clean`): goreleaser is not installed locally. Release action pins: actions/checkout v7.0.1 `3d3c42e5`, actions/setup-go v7.1.0 `d8cbb804`, goreleaser/goreleaser-action v7.3.0 `a4f02ab5`.
- Manual after tagging `v0.0.1`: AC-I6, AC-M1, AC-M3. Manual TUI review: AC-M2 (light/dark terminals at 80x24); the chrome-devtools screenshot rule targets web UI and does not apply to the Bubble Tea TUI.
- Pre-existing (v1) bug, out of scope: `scan.Run` with only the worktrees cleaner active finds nothing — with no visitors the walk's initial mask is 0, so it never descends and `GitTrees` stays empty (likely affects `cleanup -o worktrees`). Reproduced from the CLI in execute-tests (2026-11-21): a repo with a linked worktree shows `1 skipped` with default cleaners but nothing at all with `-o worktrees`.

## Verified facts (do not re-investigate)

<!-- written by implement-plan/execute-tests; appended to docs/VERIFIED-FACTS.md by wrapup -->
- macOS `/usr/bin/man` has no `-l`; view a page from stdin with `mandoc -a`, or a file with `man ./cleanup.1` (verified 2026-11-21, Darwin 27). Linux uses `man -l -`.
- `gitx` resolves git from fixed paths (`/usr/bin`, `/opt/homebrew/bin`, `/usr/local/bin`) before PATH, so tests inject `env.GitBin` rather than a PATH wrapper.
- execute-tests (2026-11-21): `make check` green; `BenchmarkProbe` 0.39 s, `BenchmarkScan` 7.65 s (0.82x find). End-to-end with scratch `XDG_CONFIG_HOME` via a pty: non-TTY hint line, plain `--init -T` (root, protected `keep`, Cursor retired) wrote a strict-valid file, the next dry run used the config root, skipped `keep/`, and listed `old/.cursor`; `-l` showed `Root: … (from config)` and `Protected: keep ok`. Every automated SPEC AC has a `TestAC_*`; AC-T5 is `BenchmarkProbe`.
- NFR-8 (2026-11-21, darwin/arm64, NFR-1 fixture, `-benchtime 1x`): `BenchmarkProbe` 0.79 s vs `BenchmarkScan` 7.82 s; the probe only matches tool footprints and never inspects artifact trees.
