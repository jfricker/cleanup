# Design invariants (regression guards)

Long-lived, append-only. `wrapup` moves STATE.md's "Design invariants (regression
guards)" section here, dated and linked to the cycle's archive, before resetting
STATE.md. Read this before `generate-spec`/`generate-design`/`generate-plan` so an
invariant established in one cycle isn't accidentally violated in the next.

## 2026-11-19: cleanup v1 ([archive](archive/2026-11-19-cleanup-v1-a-safe-clean-go-rebuild-of-the-gleanup-prototype/))

- Only `internal/safety` removes files; `TestNFR4_OnlySafetyDeletes` enforces it.
- `safety.Target` has no exported constructor; only `Inspect` builds one, and `Reconcile` clears it from any parent of a skipped/protected item.
- Every inspection error yields `skipped(inspection failed)`; no error path yields eligible.
