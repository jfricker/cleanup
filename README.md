# cleanup

Developers and experimenters during the Age of Agents are creating gigabytes of flotsdam in the projects. Cleanup is a CLI and TUI to help reclaim diskspace by finding leftovers from tools you no longer use, stale worktrees, and recreatable build artifacts.

![cleanup-demo](https://user-images.githubusercontent.com/tbd)

## Features

## Install with go (quickest)

If you have a [Go environment](https://go.dev/doc/install) configured, the
fastest way is to install a version directly from releases. Check the [Releases
page](https://github.com/digitalocean/doctl/releases) for the latest `-beta`
tag and use it in place of `v<version>-beta.<n>`:

```
go install github.com/digitalocean/doctl/cmd/doctl@v0.1
```

This drops the `doctl` binary in `$(go env GOPATH)/bin`; make sure that
directory is on your `PATH`.

### Pre-Compiled binary

You can also download the binary for your operating system directly from the [Releases page](https://github.com/jfricker/cleanup/releases).

### from source

```bash
git clone https://github.com/jfricker/cleanup
cd cleanup
make install
```

## Quick Start
```bash
make build
./cleanup --init
./cleanup            # interactive dry run over [Your Project Root]
./cleanup -d         # review, select and confirm deletions
./cleanup -T -s      # plain-text dry run, including skipped items
```

`make help` lists the development targets. `./cleanup --help` lists every flag and
`./cleanup -M | man -l -` shows the man page.

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
