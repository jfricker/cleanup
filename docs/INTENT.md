# cleanup v1: a safe, clean Go rebuild of the gleanup prototype

**Author**: John Fricker.
**Last updated**: 2026-11-19.
**Status**: Approved.
**Approved by**: John Fricker (2026-11-19).

## Problem

`~/Projects` keeps filling up with things that can be regenerated or are no longer needed:
- language build caches (`__pycache__`, `target/`, `.next`, `node_modules`, `DerivedData`, and similar)
- stale git worktrees and gone worktree registrations
- leftovers from dev tools that are no longer used

Two prototypes already exist in `~/Projects/scratch`: `cleanup` (Rust) and `gleanup` (a near
line-for-line Go port). They prove the feature set and UX: a declarative TOML category
engine, git-aware providers, defunct-tool detection, a five-phase TUI and a plain mode.

A review of both ([docs/reference/prototype-review.md](reference/prototype-review.md)) found
that the shared design deletes things it must not. Five data-loss bugs were reproduced in
both binaries:

1. **Skipped items still get deleted.** A skipped (fresh, dirty or locked) workspace is
   deleted anyway when another scanner selects its parent folder.
2. **Full clones lose work.** Unpushed commits, stashes and git-ignored files (`.env`) are
   lost; the clone is reported as "clean".
3. **Git checks fail open.** A git error counts as clean, and when `git worktree remove`
   refuses, the tool falls back to `rm -rf`.
4. **Committed files are deleted as "artifacts".** This hits git-tracked `build/` folders
   and tool config such as `.cursorrules`.
5. **Safety depends on environment and config details.**
   - "Uninstalled tool" detection depends on `$PATH` and has no age check.
   - A misspelled config key silently removes a safety marker.

The prototypes also carry structural debt:
- string-typed config that falls back to whole-folder deletion
- about ten redundant full directory walks
- a false "nothing deleted" report after Ctrl+C mid-delete
- worktrees outside the scan root offered for deletion
- untrusted repo git config executed during scans
- weak repo hygiene

The prototypes cannot safely be used with `--delete`. Fixing them in place means rewriting
the safety model anyway, and the Rust copy doubles the maintenance surface.

## Proposed outcome

A new Go project, `cleanup`, at `~/Projects/cleanup`. It reaches feature parity with gleanup
for everyday use (minus the Orca provider), and is safe to run with `--delete` on a real
`~/Projects` tree. Done means all of the following are observably true:

- **No false "safe" verdicts.** Every deletion target passes through one inspection step
  that fails closed. Only that step can produce a deletable target (a verified-target type
  the deletion code requires).
- **The five reproduced bugs are regression tests that pass.** Each scenario from the review
  is a fixture test asserting the item is skipped, protected or warned about, as decided
  below.
- **Parents never override children.** A folder that contains any skipped item is itself
  skipped, or deletes only its eligible children. It never deletes the skipped ones.
- **Git work is preserved by default.** Before any git tree is removed:
  - **These block removal** (the tree is skipped): uncommitted changes, untracked files,
    stashes, unreachable detached HEADs, lock state, and in full clones, commits not on any
    upstream.
  - **These are shown but do not block:**
    - A linked worktree whose branch has unpushed commits stays eligible, because the
      branch survives in the main repo. The review notes "branch X has N unpushed commits;
      kept in <repo>".
    - Git-ignored files stay eligible. They are listed in the review and as a warning in the
      confirmation step, so a `.env` is visible before deletion.
- **Tracked files are never deleted.** There is no override in v1.
- **Config is validated strictly.** It requires `version = 1`. Unknown keys, unknown enum
  values and over-broad patterns (e.g. `*`) are load errors that name the file and line.
- **Everything stays inside the scan root.** Every target's canonical path must sit under
  the canonical root, worktrees included. The root guard refuses all of these:
  - `/`, `$HOME`, ancestors of `$HOME`, and system folders
  - `~/Library`, `~/Documents`, `~/Desktop` and `~/Downloads`
  - cloud-sync folders (iCloud Drive, `~/Library/CloudStorage`)
- **Git runs hardened:** repo-local fsmonitor and hooks disabled, `GIT_*` environment
  scrubbed, porcelain parsed with `-z`.
- **Honest interruption and a run log.** Ctrl+C during deletion finishes the current item,
  runs pending `git worktree prune`, and reports exactly what was and wasn't deleted. Every
  `--delete` run appends deleted paths, sizes and failures to a log under
  `~/.local/state/cleanup/`.
- **One walk.** A single shared traversal feeds all matchers.
- **Clean engineering baseline.** A `make check` gate runs `gofmt`, `go vet`, `staticcheck`,
  `govulncheck` and `go test -race`, and passes. The repo has a correct `.gitignore`, a
  tidy `go.mod` and no committed binaries or build output.

## Affected users and systems

- **User:** John (single operator) running it interactively on macOS against `~/Projects`,
  and occasionally from scripts in plain mode with `--delete --force`.
- **Filesystem:**
  - everything under the chosen scan root
  - registered git worktrees whose canonical path is under that root
  - the run log under `~/.local/state/cleanup/`
- **Git repositories:** read-only inspection, `git worktree remove` and `git worktree prune`
  only.
- **Config:** `~/.config/cleanup/config.toml`, the same path the Rust prototype used. Old
  configs without `version = 1` fail to load, with a message explaining how to update them.
- **Prototypes:** `~/Projects/scratch/gleanup` (functional reference) and
  `~/Projects/scratch/cleanup` (Rust, to be retired). Neither is modified by this cycle.

## Scope

### In Scope (v1)

- **CLI surface at parity with gleanup**: dry-run default, delete, force, no-tui, root,
  config, print-config, list-categories, only/exclude, venvs, modules, age, include-dirty,
  defunct, show-skipped, group-by, print-man, help, version. Delete is `-d/--delete`; there
  is no `-y`.
- **Declarative TOML category engine** covering the built-in categories:
  - Python, Node, Rust, Swift/Xcode, Gradle, CMake, test reports
  - macOS metadata (opt-in)
  - name, glob and marker rules (self, sibling, ancestor); prune lists; opt-ins; remove or
    empty-contents strategies; warnings
- **Git worktrees provider:** stale registered worktrees and gone worktree entries, with a
  working per-provider `age_days`.
- **Retired-tools cleaner** (replaces gleanup's defunct-tool detection):
  - Only tools the user lists as retired in config are scanned. There is no PATH or app
    detection.
  - Every retired tool is age-gated.
  - Tracked files are protected.
  - Nested git trees get the full git checks.
  - `.orca` is handled here if listed.
- **Five-phase Bubble Tea TUI:**
  - live scan progress
  - collapsible review tree grouped by cleaner or project, with a filter and a detail panel
  - confirmation modal with warnings
  - live deletion progress
  - completion summary
- **Plain-text mode** for non-TTY use, with a single clear failure report.
- **Embedded default config**, overlay merge semantics, and a generated man page.
- **Run log** for every delete run.
- **Tests:**
  - unit tests per package
  - fixture-based integration tests, including the five review scenarios and the
    fail-closed git cases
  - TUI state tests
- **Engineering:** Makefile quality gate, `.gitignore`, and filled-in `AGENTS.md` quality
  gate and dev commands.

### Out of Scope (v1)

- The Orca provider (workspace and trash scanning).
- Automatic detection of uninstalled tools through PATH or app paths.
- Any override for deleting git-tracked files.
- Windows support. macOS is primary and Linux is best-effort.
- Moving items to the Trash or making deletion undoable.
- Scheduled or background runs (cron, launchd).
- Network features: telemetry, update checks, remote config.
- Packaging and distribution (Homebrew tap, release automation) beyond `go install` and
  `make build`.
- Changes to the Rust or Go prototypes, or migration tooling for their configs.
- Concurrent multi-root scans, and scanning outside a single root per run.
- New cleaner categories beyond gleanup's set.

## Resolved decisions

- **Go, not Rust.** The prototype comparison favoured Go for this kind of tool: stdlib
  coverage, layering, the race detector and fast iteration. Rust's type-safety advantage is
  recovered in Go through validated config types and an unexported verified-target type.
- **Clean rebuild, not a refactor of gleanup.** gleanup is the functional prototype and
  behavioural reference; code may be borrowed where it is sound. The safety model, config
  loading and traversal are designed fresh from the review's "Fix first" list.
- **Bubble Tea and lipgloss for the TUI.** BurntSushi/toml for config, with `Undecoded()`
  enforcement.
- **Dry run stays the default.** Deletion always needs an explicit flag, plus either
  interactive confirmation or `--force`.
- **Fail closed everywhere.** Any inspection error makes the item "skipped (inspection
  failed)". Never fall back to raw `rm -rf` after git refuses.
- **The project is managed by metaproject** and follows the seven-stage cycle in `AGENTS.md`.
- **Delete flag is `-d/--delete`.** `-y` is dropped because by convention it means "assume
  yes". Note that `-D` stays `--include-dirty`, so the spec should keep the two visually
  distinct in help text.
- **Config stays at `~/.config/cleanup/config.toml` and requires `version = 1`.** A file
  without the version fails with a clear explanation instead of loading under looser rules.
- **Retired-tools list only.** Tools are scanned only when explicitly listed as retired in
  config. This removes the PATH dependence that marked every tool as uninstalled under cron
  or sudo.
- **No tracked-file override in v1.** Tracked paths are removed with `git rm` by the user,
  never by cleanup.
- **Unpushed commits in a linked worktree's branch do not block removal.** The branch
  survives in the main repo; the review shows a note. In full clones, unpushed commits do
  block removal, because nothing else keeps them.
- **Git-ignored files warn but do not block.** They are listed in the review and in the
  confirmation warnings.
- **Orca provider dropped.** `.orca` can be cleaned through the retired-tools list, with
  full git checks on nested trees.
- **Extended root guard.** `~/Library`, `~/Documents`, `~/Desktop`, `~/Downloads` and
  cloud-sync folders (iCloud Drive, `~/Library/CloudStorage`) are refused as scan roots, in
  addition to `/`, `$HOME`, ancestors of `$HOME` and system folders.
- **Always write a run log.** Every `--delete` run appends deleted paths, sizes and failures
  to `~/.local/state/cleanup/`, whether or not it was interrupted.

## Constraints

- Single static binary; `go build` must work with no cgo.
- Must never touch anything outside the canonical scan root, including through symlinks;
  never follow symlinks during traversal or deletion. The run log is the only file written
  outside the scan root.
- Git is a runtime dependency, invoked as a subprocess. No libgit2 or go-git, unless the
  design stage shows a clear safety benefit.
- Must stay responsive on a `~/Projects` tree with hundreds of repos and large
  `node_modules` folders. Concrete performance targets are set in the spec.
- Every commit passes the full quality gate (global rule).
- UI changes are verified visually in light and dark terminal themes at narrow and wide
  sizes.

## Open questions

None open. All nine questions from the first draft were resolved with the operator on
2026-11-19 (see Resolved decisions).
