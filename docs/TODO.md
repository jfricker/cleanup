# cleanup — TODO

Long-lived, append-only. `wrapup` adds a dated "Needs manual verification" entry each cycle; never archived or reset.

## 2026-11-19: Needs manual verification (2026-11-19-cleanup-v1-a-safe-clean-go-rebuild-of-the-gleanup-prototype)

- [ ] **TUI in a light terminal theme and another emulator.** Run `./cleanup` in Terminal.app or iTerm with a light profile. Expected: the cursor-row highlight spans the whole row, sizes are right-aligned, the footer fits at 80 columns, and every glyph and hint is readable. (Checked so far: HTML snapshots in Chrome, and a dark real-terminal run in the Claude app's terminal panel.)
- [ ] **First real deletion run.** Run `./cleanup -d` on ~/Projects, select a few `__pycache__` items, confirm. Expected: only the selected items are gone, `git status` is unchanged in the affected repos, and `~/.local/state/cleanup/runs.jsonl` holds one line per item plus a start and end record.
- [ ] **Ctrl+C during deletion in a real terminal.** Start `./cleanup -d` with many items selected, press Ctrl+C once while deleting. Expected: the current item finishes, `git worktree prune` runs, the report lists what was and wasn't deleted, exit status 130.
- [ ] **Linux.** The rootguard refusal rules for Linux are unit-tested only; run `make check` and a dry run on a Linux box. Expected: gate passes; home, its ancestors and system folders are refused as roots.
