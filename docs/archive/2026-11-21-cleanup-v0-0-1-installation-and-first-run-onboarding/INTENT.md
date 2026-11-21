# cleanup v0.0.1: installation and first-run onboarding

**Author**: John Fricker.
**Last updated**: 2026-11-21.
**Status**: Complete.
**Approved by**: John Fricker (2026-11-20).

## Problem

cleanup today works only for someone who builds it from the repo and already knows its
defaults:

- **No install path.** The only route is `make build` → `./cleanup` in the checkout.
  There is no `make install`, no `go install` guidance, and the man page is reachable
  only via `cleanup -M | man -l -`.
- **The project root is hard-coded.** `~/Projects` is the default; any other layout
  needs `-r` on every run. Nothing records which root the user actually wants scanned.
- **No way to protect specific directories.** `never_touch` matches folder *names*
  anywhere under the root, so it can't protect one particular project without hiding
  every folder with the same name.
- **Retired tools are inert by default.** Every entry in `[retired_tools]` ships with
  `retired = false`, and cleanup deliberately does not detect tools from `PATH` or installed
  apps. So the `retired` cleaner finds nothing until the user discovers
  `cleanup -P > ~/.config/cleanup/config.toml`, edits TOML by hand, and keeps
  `version = 1`. In practice nobody does this, and leftover `.cursor/`, `.windsurf/`,
  `.aider.*` etc. stay on disk.

## Proposed outcome

A new user can install cleanup in one command and, on first run (or via
`cleanup --init`), answer a short guided setup that writes a valid
`$XDG_CONFIG_HOME/cleanup/config.toml` (default `~/.config/cleanup/config.toml`).
Observably:

1. **Install.** Any of:
   - `go install github.com/jfricker/cleanup/cmd/cleanup@latest` (requires the module
     path to be `github.com/jfricker/cleanup` and a `v0.0.1` tag);
   - from a clone, `go install ./cmd/cleanup`;
   - download a release binary from GitHub.

   `go install` writes to `$GOBIN` (default `$(go env GOPATH)/bin`). Installing does
   not run onboarding; the first interactive run does.
2. The first interactive run with no config file starts onboarding instead of
   scanning; `cleanup --init` re-runs it any time. Non-TTY and `-f` runs never prompt:
   they use defaults and print a one-line hint to run `cleanup --init`.
3. **Step 1 — Project root.** Onboarding shows the project root (default `~/Projects`)
   and lets the user change it. The choice is validated with the existing rootguard
   rules (a refused root is rejected with the same reason cleanup prints today) and saved as
   `project_root`. `-r` still overrides it per run.
4. **Step 2 — Protected directories.** The user adds directories under the project root
   that must never be scanned or deleted. They are saved as root-relative paths in
   `protected_paths`, pruned from the scan and refused again at delete time.
5. **Step 3 — Old dev tools.** Onboarding runs a read-only scan of the project root for
   the tool footprints listed in `[retired_tools]` and shows every tool found, newest
   first, with item count, total size and most-recent modification age. Leftovers
   younger than `age_days` carry a note that they won't be cleaned yet. The user marks
   the tools they no longer use; those are saved as `retired = true`. Tools with no
   footprint are not shown. Nothing is deleted.
6. Onboarding ends with a summary of what was written and the dry-run command to try
   next. The written file passes the existing strict config validation.

## Affected users and systems

- **Users**: the operator on macOS (primary) and Linux; anyone installing from the repo.
- **Code**: `cmd/cleanup` (first-run detection, `--init` flag), `internal/cli`
  (flags, help, man), `internal/config` (`project_root`, `protected_paths`, writing a
  user file), `internal/rootguard` (reused for root validation), `internal/walk`
  (pruning protected paths) and `internal/match` (read-only tool-footprint probe),
  `internal/safety` (refusing protected paths at delete time), `internal/tui`
  (onboarding screens), `internal/plain` (prompt fallback and non-TTY hint),
  `go.mod` and every internal import (module rename), release CI, `README.md`, help
  and man golden files, `.goreleaser.yaml` and `.github/workflows/release.yml`.
- **Files on disk**: `~/.config/cleanup/config.toml` (created or replaced with a
  timestamped backup), `$GOBIN/cleanup` or the downloaded release binary.

## Scope

### In Scope (v0.0.1)

- Install via `go install` (remote and from a clone) and GitHub release binaries;
  README install section (no man page install; use `cleanup -M`). The Makefile stays
  developer-only (AGENTS.md).
- First-run detection (interactive, no config file) and `cleanup --init` (re-run, using
  existing values as defaults).
- Three onboarding steps: project root, protected directories, old dev tools.
- New optional config fields `project_root` and `protected_paths`, honoured at run time.
- Writing config: minimal user file (only values that differ from the embedded
  defaults), atomic write, existing file backed up before `--init` replaces it.
- TUI onboarding plus a plain prompt fallback; non-interactive runs never block.
- Release binaries on GitHub.
- Tag the release `v0.0.1`.

### Out of Scope (v0.0.1)

- Homebrew tap, code signing/notarization, Linux packages.
- `cleanup --init` does not install the binary.
- Detecting tools from `PATH`, `/Applications` or package managers (existing decision stands).
- Adding new tool definitions during onboarding (users can still edit config).
- Multiple project roots in one run.
- Scheduled/automatic runs (launchd/cron).
- Any deletion during onboarding.
- SwiftPM `.build/checkouts` strictness (deferred; see Open questions).

## Resolved decisions

- **Versioning**: `v0.0.1` is the canonical version for this release. (Operator,
  2026-11-20.)
- **Init entry point**: `cleanup --init` (a flag, not a subcommand). (Operator,
  2026-11-20.)
- **Naming**: the first step is "Project root"; default
  `~/Projects`, user-definable. Protected directories are a separate second step.
  (Operator, 2026-11-20.) Tilde is always expanded.
- **Protected directories are paths, not names**: a new `protected_paths` list of
  root-relative paths. `never_touch` stays for names and onboarding does not edit it.
  (Operator, 2026-11-20.)
- **Schema**: keep the integer schema `version = 1` (separate from the `v0.0.1`
  release version, which `--version` prints) and add `project_root` and
  `protected_paths` as optional fields; no migration. Strict unknown-key checking still
  applies. Bump the schema version only when a field changes meaning or is removed.
  (Operator, 2026-11-20.)
- **Old dev tools list**: show every configured tool found under the root with its age;
  leftovers younger than `age_days` are flagged, not hidden. Age gating at run time is
  unchanged. (Operator, 2026-11-20.)
- **Install**: `go install` or release binary, no sudo, no Makefile target;
  `cleanup --init` writes config only and never installs. (Operator, 2026-11-20.)
- **Trigger**: onboarding starts automatically on the first interactive run with no
  config file; otherwise defaults plus a hint. (Operator, 2026-11-20.)
- **Release binaries**: GoReleaser in GitHub Actions on a pushed `v*` tag; darwin and
  linux, arm64 and amd64, `CGO_ENABLED=0`; tar.gz archives including `cleanup.1`, plus
  `checksums.txt`. Not notarized: the README documents
  `xattr -d com.apple.quarantine`. (Operator, 2026-11-20.)
- **PATH**: the README names the `go install` target directory and the `PATH` line;
  onboarding also warns when the running binary's directory is not on `PATH`.
  (Operator, 2026-11-20.)
- **`-c FILE`**: `--init -c FILE` writes FILE. A plain run whose `-c` file is missing is
  an error as today; only a missing default file triggers first-run onboarding.
  (Operator, 2026-11-20.)
- **Auto-trigger**: only when stdin and stdout are both TTYs, the default config file is
  missing, and none of `-f`, `-P`, `-M`, `-h`, `--version`, `--plain`, `-c` is given.
  `-d` also triggers it; the run then ends after onboarding without deleting.
  (Operator, 2026-11-20.)
- **Plain fallback**: interactive runs use line-by-line prompts instead of the TUI when
  `--plain` is passed with `--init`, `TERM=dumb`, or the TUI fails to start.
  (Operator, 2026-11-20.)
- **Backup**: `--init` backs up an existing file as `config.toml.bak-<YYYYMMDDTHHMMSS>`
  in the same directory, never overwritten, and prints the path. (Operator, 2026-11-20.)
- **Preserving user edits**: `--init` changes only `project_root`, `protected_paths`
  and each tool's `retired` flag; every other key is carried over. Comments are lost on
  rewrite (the backup keeps them). (Operator, 2026-11-20.)
- **Un-retiring**: unmarking a tool removes its `retired` override (or the whole entry
  if nothing else is set), falling back to the default `false`. (Operator, 2026-11-20.)
- **Protected paths and worktrees**: protection follows location on disk. Nothing at or
  under a protected path is proposed, including linked worktrees there; a worktree
  elsewhere whose main repo is protected is still proposed. (Operator, 2026-11-20.)
- **Step 3 probe scope**: uses the same pruning as a real scan, including `never_touch`
  and the protected paths entered in step 2. (Operator, 2026-11-20.)
- **Step 3 age**: the newest mtime anywhere in the footprint outside `.git`, the same
  measure the run-time age gate uses (`internal/safety`). (Operator, 2026-11-20.)
- **Stale root**: a saved `project_root` that no longer exists or is refused by
  rootguard is a usage error naming the root and the config file and suggesting
  `cleanup --init` or `-r DIR`; no fallback to `~/Projects`. (Operator, 2026-11-20.)
- Onboarding never deletes; it only probes and writes config. The safety model is
  unchanged.
- Root validation reuses `internal/rootguard` so onboarding and runtime agree.
- Tool suggestions come only from footprints found under the root, matched against the
  configured `[retired_tools]` list, consistent with the existing "no PATH detection" rule.

## Constraints

- Go 1.27, `CGO_ENABLED=0`, `make check` must stay green; only `internal/safety` removes
  files (enforced by test).
- Config stays strict (unknown keys error); a `protected_paths` entry outside the
  project root, absolute, or containing `..` is a config error.
- The onboarding probe must be read-only and reasonably fast on `~/Projects` (reuse the
  single parallel walk; NFR-1 benchmark as reference).
- Must not hang CI, pipes or `-f` runs waiting for input.

## Open questions

- **Deferred** (operator, 2026-11-20), carried forward from
  [cleanup v1](archive/2026-11-19-cleanup-v1-a-safe-clean-go-rebuild-of-the-gleanup-prototype/):
  SwiftPM `.build` dirs are skipped when `.build/checkouts/*` clones have local
  branches not on a remote (seen on ~/Projects). Safe but over-conservative; should
  package-manager checkouts be treated as regenerable? Not part of this cycle.
