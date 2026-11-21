# cleanup

Developers and experimenters during the Age of Agents are creating gigabytes of flotsdam in the projects. Cleanup is a CLI and TUI to help reclaim diskspace by finding leftovers from tools you no longer use, stale worktrees, and recreatable build artifacts.

## Install

### With Go

```bash
go install github.com/jfricker/cleanup/cmd/cleanup@latest
```

The binary lands in `$GOBIN` if set, otherwise `$(go env GOPATH)/bin`. Make sure that
directory is on your `PATH`:

```bash
export PATH="$PATH:$(go env GOPATH)/bin"
```

From a clone:

```bash
git clone https://github.com/jfricker/cleanup
cd cleanup
go install ./cmd/cleanup
```

### Release download

Download the archive for your OS and architecture
(`cleanup_<version>_<os>_<arch>.tar.gz`) and `checksums.txt` from the
[Releases page](https://github.com/jfricker/cleanup/releases), then verify and unpack:

```bash
shasum -a 256 -c checksums.txt --ignore-missing
tar xzf cleanup_<version>_<os>_<arch>.tar.gz
```

On macOS, clear the quarantine flag so Gatekeeper lets the binary run:

```bash
xattr -d com.apple.quarantine ./cleanup
```

Move `cleanup` somewhere on your `PATH`. The archive also holds `README.md` and the man
page `cleanup.1`.

### Man page

```bash
cleanup -M | mandoc -a       # from the binary (macOS)
cleanup -M | man -l -        # from the binary (Linux)
man ./cleanup.1              # from a release archive
```

### Removal

Delete the binary. Config in `~/.config/cleanup` and the run log in
`~/.local/state/cleanup` are left in place; remove them yourself if you want them gone.

## First run and `--init`

The first interactive run with no config file at `~/.config/cleanup/config.toml` starts
onboarding instead of scanning: it asks for your project root, lets you protect
directories, and lists tools you can mark as retired, then writes the config and exits.
It never deletes anything. Non-interactive runs never prompt; they print one stderr
line, `cleanup: no config yet; run "cleanup --init" to set your project root and
retired tools`, and carry on with defaults.

Re-run onboarding any time with `cleanup --init`, pre-filled from your existing config.
It needs an interactive terminal and combines only with `-c FILE` (target config),
`-r DIR` (pre-fill the root) and `-T` (plain-text prompts). Esc or Ctrl+C cancels
without writing anything.

Config keys it manages:

- `project_root` — directory to scan when no `-r` or positional root is given
  (default `~/Projects`). A missing or refused root is an error, not a fallback.
- `protected_paths` — directories, relative to the root, that are never walked,
  proposed or deleted. `cleanup -l` shows the root, its source and protected paths.

## Quick start

```bash
cleanup --init
cleanup              # interactive dry run over your project root
cleanup -d           # review, select and confirm deletions
cleanup -T -s        # plain-text dry run, including skipped items
```

`cleanup --help` lists every flag.

## Usage

- **Dry run by default.** Nothing is deleted unless you pass `-d/--delete`, then confirm
  in the TUI (focus starts on Cancel) or add `-f/--force`.
- **Cleaners.** Built-in categories: `python`, `node`, `rust`, `swift`, `gradle`,
  `cmake`, `test-artifacts`, `macos` (off by default), plus `worktrees` and `retired`.
  Narrow a run with `-o`/`-x`; add virtualenvs with `-v` and `node_modules` with `-m`.
- **Retired tools.** Only tools you mark `retired = true` in config, or pass with
  `-u cursor,zed`, are scanned. They are always age-gated (default 30 days, `-a` to
  change).
- **Config.** `cleanup -P > ~/.config/cleanup/config.toml`, then edit. The file needs
  `version = 1`; unknown keys and over-broad patterns are errors with file and line.
- **Run log.** Every `-d` run appends to `~/.local/state/cleanup/runs.jsonl`.
- **Exit status.** 0 success, 1 some deletions failed, 2 usage/config/root error,
  130 interrupted.

## Safety model

- One inspection step decides every candidate and fails closed: any error skips the
  item. Only `internal/safety` can create a deletion target or remove files, and a
  test enforces that.
- Git-tracked files are never deleted.
- A git tree is skipped for uncommitted changes, untracked files, an unreachable
  detached HEAD, lock state, and, in full clones, stashes or commits on no remote.
  Ignored files and unpushed linked-worktree branches are shown as notes.
- A folder containing a skipped or protected item is skipped as a whole.
- Symlinks are never followed; everything stays inside the canonical scan root. Home,
  its ancestors, `~/Documents`, `~/Desktop`, `~/Downloads`, `~/Library` (iCloud,
  CloudStorage) and system folders are refused as roots.
- Git runs with fsmonitor, hooks and filter drivers disabled and a scrubbed
  environment.
- Each item is re-checked just before deletion. The first Ctrl+C finishes the current
  item, runs `git worktree prune` and reports exactly what happened; the second
  abandons the current item.

## Development

- `make check` is the quality gate: gofmt, `go vet`, staticcheck, race tests,
  govulncheck and a `go mod tidy` diff.
- `make golden` regenerates CLI golden files; `make bench` runs the scan benchmark.
- `CLEANUP_SNAPSHOT_DIR=<dir> go test -run TestSnapshots ./internal/tui/` writes HTML
  renderings of the TUI (80/160 columns, light/dark) for visual review.

## Documentation

The cycle documents live in `docs/` (`INTENT.md`, `SPEC.md`, `TECH-DESIGN.md`,
`PLAN.md`, `STATE.md`); see `AGENTS.md` for the process. `docs/ARCHITECTURE.md` is the
long-lived index, and completed cycles are archived under `docs/archive/`. The
prototype review that motivated this rebuild is in `docs/reference/`.
