# Batch and immediate delete with persistent review screen

**Author**: John Fricker.
**Last updated**: 2026-11-22.
**Status**: Draft.
**Approved by**: —

## Problem

In `--delete` mode the TUI supports only one delete path, and it ends the session:

1. Review: the user marks items (`space`, `a`, `n`), then presses `d`.
2. Confirm modal (`internal/tui/app.go` `confirmKey`).
3. Deleting phase with progress.
4. `DeleteDoneMsg` sets `Phase = Done`; the Done screen shows the report and any of
   `q`/`esc`/`enter` quits (`app.go` `key`, case `Done`).

Consequences:

- Removing one obvious item means building a selection and going through the batch flow.
- After a delete the scan results are gone. To delete more, or to check what is left,
  the user has to rerun `cleanup` and rescan.

## Proposed outcome

- **Batch delete** stays as now: mark items, `d`, confirm, delete the selection.
- **Immediate delete** ("delete now"): one keypress on the item under the cursor deletes
  that item without building a selection.
- After either action the user returns to the scan result (Review) screen, not a
  terminal Done screen. The screen reflects the result: deleted items are gone or
  marked deleted, failed/skipped items show why, totals (reclaimable bytes, counts)
  are recomputed, and the selection is cleared of deleted items.
- The session stays open until the user quits (`q`/`esc`). A summary of what was deleted
  in the session (and the run log path) is still available, either on screen or on exit.

## Affected users and systems

- Operators running `cleanup --delete` interactively.
- `internal/tui` (`app.go` phases and keys, `view.go`, `program.go` effects).
- `internal/execute` (invoked per batch; now possibly many times per session) and
  `internal/runlog` (one log per session vs per delete).
- Exit code semantics, currently set from a single delete run.
- Plain (non-TTY) mode in `internal/plain`: unchanged unless decided otherwise.

## Scope

### In Scope (v1)

- New immediate-delete key on the current item in Review.
- Return to Review after batch or immediate delete; Review updated in place.
- Session-level summary and exit code aggregated over all deletes.
- Tests for both paths, including the return-to-Review state.

### Out of Scope (v1)

- Changes to what is selectable or to safety classification (`internal/safety` stays
  the only remover; protected/skipped items stay undeletable).
- Undo / trash.
- Plain/non-interactive mode behaviour.
- Rescanning the filesystem after delete (update from execute outcomes instead — see
  Open questions).

## Resolved decisions

- Batch delete flow (select → `d` → confirm) is kept unchanged up to the point of
  completion.
- Only selectable verdicts can be immediately deleted, same guard as `Selected()`.
- The app no longer ends on delete completion; quitting is always an explicit user action.

## Constraints

- Safety rule: only `internal/safety` removes files; enforced by test.
- Ctrl+C semantics during deletion (first press finishes current item, second abandons)
  must hold for both paths.
- Items with notes (ignored files, unpushed branches, `-D`) currently require an explicit
  choice; immediate delete must not weaken that.
- Go 1.27, no cgo; `make check` must pass.

## Open questions

1. **Key for immediate delete.** `D`, `x`, or `delete`/`backspace`? (`d` is batch.)
2. **Confirmation for immediate delete.** None, a lightweight one-line `y/n`, or the same
   modal as batch? Mandatory for items with notes?
3. **Deleted items in Review.** Remove from the list, or keep greyed out as "deleted"
   until quit?
4. **Refresh source.** Update from execute outcomes only, or offer a rescan key?
5. **Summary and run log.** One run log per session or per delete action? Show the
   cumulative report on quit, in a status line, or both?
6. **Exit code.** Worst outcome across all deletes in the session, or last action?
7. **Done screen.** Remove it entirely, or keep it reachable (e.g. shown briefly as a
   toast/banner after each delete)?
