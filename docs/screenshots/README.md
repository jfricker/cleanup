# TUI screenshots and test bed

`make testbed` builds `.testbed/` with `go run ./tools/testbed`; `make screenshot`
runs the real binary in tmux against it and renders a PNG
(`scripts/tui-screenshot.sh`, `scripts/ansi2png.mjs`). The test bed refuses a
non-empty directory rather than clearing it; delete `.testbed/` yourself to rebuild.

Command used for every shot: `cleanup -c .testbed/config.toml -d -v -m -s`, 132×34, dark
(v0 shots before project grouping became the default add `-g cleaner` implicitly).

## v0 (current TUI)

| File | Shows |
|---|---|
| `v0-review.png` | Review tree grouped by cleaner, cursor on an eligible cache, detail pane |
| `v0-worktree-note.png` | Unpushed worktree: eligible-with-notes, unselected by default, note in detail |
| `v0-project-group.png` | Same data with `-g project` (name-sorted groups) |
| `v0-skipped.png` | Skipped and protected group, cursor on an out-of-root worktree |

## v1 increments

| File | Change |
|---|---|
| `v1-1-project-default.png` | `--group-by` defaults to `project` (`-g cleaner` for the old view) |
| `v1-2-boxes.png`, `v1-2-boxes-item.png` | Single-line frame around the whole TUI; single-line box around each expanded group (header row is the top edge, collapsed groups unboxed); double-line box around the detail pane |
| `v1-3-short-names.png`, `v1-3-detail-group.png` | Project prefix dropped from item rows under project grouping; detail box sized to its content (capped at the pane) |
| `v1-4-detail-group.png`, `v1-4-firstrun-root.png`, `v1-4-firstrun-tools.png` | Detail box keeps the pane width, height fits content; first-run onboarding drawn in the same single-line frame, footer pinned to the bottom |
| `v1-5-footer.png`, `v1-5-footer-confirm.png`, `v1-5-footer-firstrun.png` | Key hints as highlighted key caps with full-contrast action text on every screen; selection total right-aligned; hints drop from the middle when narrow (quit always kept); confirm-modal warnings wrap with a hanging indent |

## Test bed states (`.testbed/Projects`)

| Path | Cleaner | Age | Expected verdict | Why |
|---|---|---|---|---|
| `py-api-legacy/.venv`, caches | Python | 200d | eligible | stale venv (-v), caches |
| `py-ml-active/.venv` | Python | 0d | eligible | fresh venv: venvs are not age-gated |
| `scratch-scripts/venv-3.11` | Python | 400d | eligible | venv in a non-git dir |
| `not-a-venv/venv` | — | 365d | not proposed | no `pyvenv.cfg` |
| `web-dashboard-2023/*` | Node, Test Reports | 300d | eligible | unused node_modules (-m), every artifact kind |
| `web-shop/node_modules`, `.vite` | Node | 0d | eligible | active project: node_modules not age-gated |
| `monorepo-abandoned/**` | Node | 500d | eligible | nested workspace node_modules and dist |
| `web-tracked-build/build` | Node | 90d | protected: tracked files | committed build/ |
| `rust-cli/target` | Rust | 120d | eligible | |
| `not-rust/target` | — | 365d | not proposed | no `Cargo.toml` |
| `photos-export/build` | — | 365d | not proposed | no marker |
| `swift-kit/.build`, `.swiftpm`, `DerivedData` | Swift | 60d | eligible | |
| `android-app/.gradle`, `build` | Gradle | 150d | eligible | |
| `cpp-engine/build`, `cmake-build-debug` | CMake | 80d | eligible | `CMakeCache.txt` |
| `cpp-engine/.DS_Store` | macOS | — | not proposed | cleaner off by default (`-o macos`) |
| `service-wt/stale-feature` | Worktrees | 70d | eligible | clean, pushed |
| `service-wt/unpushed-fix` | Worktrees | 55d | eligible-with-notes | 1 unpushed commit |
| `service-wt/fresh-feature` | Worktrees | 3d | skipped: fresh | |
| `service-wt/locked-agent` | Worktrees | 50d | skipped: locked | |
| `service-wt/dirty-spike` | Worktrees | 60d | skipped: uncommitted | |
| `service-wt/untracked-notes` | Worktrees | 60d | skipped: untracked | |
| `service-wt/moved-away` | Worktrees | — | not shown | dir moved to `elsewhere/` (gone entry) |
| `elsewhere/outside-wt` | Worktrees | 90d | protected: outside root | |
| `old-experiment/.cursor*`, `.aider*` | Retired | 120d | eligible | cursor, aider retired in config |
| `old-experiment/.windsurf` | — | 120d | not proposed | windsurf not retired |
| `new-idea/.cursorrules`, `.zed` | Retired | 0d | skipped: fresh | |
| `team-handbook/.cursorrules` | Retired | 60d | protected: tracked files | |
| `.orca` (locked worktree inside) | Retired | 90d | skipped: contains skipped git tree | |
| `client-work/**` | — | 200d | not shown | `protected_paths` |
| `linked-deps/node_modules` | — | — | not proposed | symlink to `elsewhere/` |

## v1 decisions (operator Q&A, 2026-11-22)

1. **Staleness**: age-gate venvs and node_modules like worktrees: recent ones are
   listed as `fresh` and are not selectable. Age comes from the project's last
   activity, with the threshold in config.
2. **Primary view**: projects sorted by reclaimable size, with their artifacts nested.
3. **Row columns**: age, kind badge, status word and size bar, alongside checkbox,
   path and size.
4. **Skipped items**: a separate tab (Eligible | Skipped) instead of the bottom group.
5. **Status explanation**: each status word comes with a plain-language explanation
   of what it means and what would change it (e.g. `fresh — used 3d ago; eligible
   after 30d`).

### v0 defects seen on the test bed

- A locked worktree's reason renders as `locked: locked`; the lock reason
  (`claude agent running`) is dropped.
- Gone worktree entries (`service-wt/moved-away`) are not surfaced, though the
  worktrees cleaner's description says they are.
- Detail pane: `path` wraps mid-word; `about` shows the bare branch name.
- Protected and outside-root items show `0 B`, which reads as "nothing to reclaim".
