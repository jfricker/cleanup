# cleanup — State

## Process

<!-- each stage ticks its own item here as it completes -->
- [x] 1. write-intent: intent.md approved
- [x] 2. generate-spec: spec.md approved
- [x] 3. generate-design: design.md approved
- [x] 4. generate-plan: plan.md approved
- [ ] 5. implement-plan: code and tests written
- [ ] 6. execute-tests: tests passing
- [ ] 7. wrapup: cycle archived

## Implementation phases

<!-- written by implement-plan as steps land -->
- [x] 1. Module rename (bf88081)
- [x] 2. Guards and version (459eeb3)
- [x] 3. rootguard helpers (d04e683)
- [x] 4. Config schema (e74b62e)
- [x] 5. Config writing (846541f)
- [ ] 6. Walk and scan protection
- [ ] 7. Safety protection
- [ ] 8. Run-time root, hint and `-l`
- [ ] 9. Probe
- [ ] 10. Onboarding model
- [ ] 11. Front ends
- [ ] 12. Wire onboarding
- [ ] 13. Release files and docs

## Design invariants (regression guards)

<!-- written by implement-plan/execute-tests; appended to docs/DESIGN-INVARIANTS.md by wrapup -->
- `config.Plan` leaves a tool's `retired` untouched when its id is absent from `Changes.Retired`; onboarding must pass a value for every configured tool.
- `config.Write` never removes files: fixed temp `.<name>.new` truncated and reused; backups created with `O_EXCL`.

## Open items carried into plan.md

<!-- written by implement-plan/execute-tests; resolved with the operator by wrapup -->
- Pre-existing (v1) bug, out of scope: `scan.Run` with only the worktrees cleaner active finds nothing — with no visitors the walk's initial mask is 0, so it never descends and `GitTrees` stays empty (likely affects `cleanup -o worktrees`). Reported during step 6; not yet reproduced from the CLI.

## Verified facts (do not re-investigate)

<!-- written by implement-plan/execute-tests; appended to docs/VERIFIED-FACTS.md by wrapup -->
