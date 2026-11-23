# Batch and immediate delete with persistent review screen

**Author**: John Fricker.
**Last updated**: 2026-11-23.
**Status**: Approved.
**Approved by**: John Fricker (2026-11-23).

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

- **Batch delete** stays as now: mark items, `d`, confirm modal, delete the selection.
- **Immediate delete**: `D` on the item under the cursor shows an inline `y/n` prompt
  (item and size); `y` deletes that item, anything else cancels.
- After either action the user returns to the Review screen, updated in place from the
  execute outcomes: deleted items are removed from the list, failed/skipped items stay
  with the reason, totals are recomputed, deleted items leave the selection.
- A transient banner on Review reports the action (bytes freed, failures, log path).
  The Done screen is removed.
- A status line shows cumulative bytes freed and items deleted for the session.
- The session stays open until the user quits (`q`/`esc`); the full session summary is
  printed on quit.

## Affected users and systems

- Operators running `cleanup --delete` interactively.
- `internal/tui` (`app.go` phases and keys, `view.go`, `program.go` effects).
- `internal/execute` (invoked per batch; now possibly many times per session) and
  `internal/runlog` (one log per session, appended per action).
- Exit code semantics, currently set from a single delete run.
- Plain (non-TTY) mode in `internal/plain`: unchanged.

## Scope

### In Scope (v1)

- `D` immediate delete with inline `y/n` confirmation.
- Return to Review after batch or immediate delete; Review updated from outcomes.
- Removal of the Done phase; post-delete banner; cumulative status line.
- One run log per session, appended per action; session summary printed on quit.
- Exit code aggregated over the session.
- Tests for both paths, including the return-to-Review state.

### Out of Scope (v1)

- Changes to what is selectable or to safety classification (`internal/safety` stays
  the only remover; protected/skipped items stay undeletable).
- Undo / trash.
- Plain/non-interactive mode behaviour.
- Filesystem rescan after delete, automatic or on a key.

## Resolved decisions

- Batch flow (select → `d` → confirm modal) is unchanged up to completion.
- Immediate delete key is `D` (pairs with batch `d`, hard to hit by accident).
- Immediate delete asks an inline `y/n` for every item, not the batch modal.
- Only selectable verdicts can be immediately deleted, same guard as `Selected()`.
- Deleted items are removed from the Review list, not greyed out.
- Review refreshes from execute outcomes only; no rescan.
- One run log per session, appended per delete action.
- Exit code is the worst outcome across all actions in the session.
- The Done phase is replaced by a transient banner on Review.
- The app no longer ends on delete completion; quitting is always explicit.

## Constraints

- Safety rule: only `internal/safety` removes files; enforced by test.
- Ctrl+C semantics during deletion (first press finishes current item, second abandons)
  must hold for both paths.
- Items with notes (ignored files, unpushed branches, `-D`) currently require an explicit
  choice; immediate delete must not weaken that.
- Go 1.27, no cgo; `make check` must pass.

## Open questions

None.
