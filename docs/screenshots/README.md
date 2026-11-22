# TUI screenshots and test bed

`make testbed` builds `.testbed/` with `go run ./tools/testbed`; `make screenshot`
runs the real binary in tmux against it and renders a PNG
(`scripts/tui-screenshot.sh`, `scripts/ansi2png.mjs`). The test bed refuses a
non-empty directory rather than clearing it; delete `.testbed/` yourself to rebuild.

Command used for every shot: `cleanup -c .testbed/config.toml -d -v -m -s`, 132×34, dark.

## v0 (current TUI)

| File | Shows |
|---|---|
| `v0-review.png` | Review tree grouped by cleaner, cursor on an eligible cache, detail pane |
| `v0-worktree-note.png` | Unpushed worktree: eligible-with-notes, unselected by default, note in detail |
| `v0-skipped.png` | Skipped and protected group, cursor on an out-of-root worktree |

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
