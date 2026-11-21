# cleanup — TODO

Long-lived, append-only. `wrapup` adds a dated "Needs manual verification" entry each cycle; never archived or reset.

## 2026-11-19: Needs manual verification (2026-11-19-cleanup-v1-a-safe-clean-go-rebuild-of-the-gleanup-prototype)

- [ ] **TUI in a light terminal theme and another emulator.** Run `./cleanup` in Terminal.app or iTerm with a light profile. Expected: the cursor-row highlight spans the whole row, sizes are right-aligned, the footer fits at 80 columns, and every glyph and hint is readable. (Checked so far: HTML snapshots in Chrome, and a dark real-terminal run in the Claude app's terminal panel.)
- [ ] **First real deletion run.** Run `./cleanup -d` on ~/Projects, select a few `__pycache__` items, confirm. Expected: only the selected items are gone, `git status` is unchanged in the affected repos, and `~/.local/state/cleanup/runs.jsonl` holds one line per item plus a start and end record.
- [ ] **Ctrl+C during deletion in a real terminal.** Start `./cleanup -d` with many items selected, press Ctrl+C once while deleting. Expected: the current item finishes, `git worktree prune` runs, the report lists what was and wasn't deleted, exit status 130.
- [ ] **Linux.** The rootguard refusal rules for Linux are unit-tested only; run `make check` and a dry run on a Linux box. Expected: gate passes; home, its ancestors and system folders are refused as roots.

## 2026-11-21: Needs manual verification (2026-11-21-cleanup-v0-0-1-installation-and-first-run-onboarding)

- [ ] **GoReleaser config (AC-I4/I5).** Install goreleaser v2, then run `goreleaser check` and `goreleaser release --snapshot --clean --skip=publish`. Expected: four archives `cleanup_<ver>_{darwin,linux}_{amd64,arm64}.tar.gz`, each with `cleanup`, `cleanup.1` and `README.md` (`tar tzf`), and `shasum -a 256 -c dist/checksums.txt --ignore-missing` passes. Then delete `dist/` and `dist-extra/`.
- [ ] **Release workflow on the tag (INST-6/7).** Push the `v0.0.1` tag. Expected: the Release workflow runs `make check` and then publishes the four archives plus `checksums.txt` to the GitHub release.
- [ ] **Install from the tag (AC-I6).** In a clean `GOPATH`, run `go install github.com/jfricker/cleanup/cmd/cleanup@v0.0.1` and then `cleanup --version`. Expected: `cleanup v0.0.1`. A downloaded release binary prints the same.
- [ ] **Fresh-user onboarding (AC-M1).** As a user with no `~/.config/cleanup`, run `cleanup` in a terminal. Expected: the TUI onboarding starts and can be completed in under 2 minutes without editing TOML; the next `cleanup` dry-runs the chosen root.
- [ ] **TUI onboarding visuals (AC-M2).** Run `cleanup --init` in a light and a dark terminal profile at 80x24, and once with `NO_COLOR=1`. Expected: every screen and key hint is readable, nothing is truncated, and there is no colour with `NO_COLOR`.
- [ ] **Quarantined release binary (AC-M3).** Download the darwin/arm64 archive in a browser, extract it, and run `xattr -d com.apple.quarantine ./cleanup` then `./cleanup --version`. Expected: it runs without a Gatekeeper block.

