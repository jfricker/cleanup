# <Title>

**Author**: John Fricker.
**Last updated**: 2026-11-19.
**Status**: Draft.
**Approved by**: —

## Problem

<What problem, ticket, or incident prompted this cycle?>

## Proposed outcome

<What should be true once this cycle ships?>

## Affected users and systems

<Who or what this touches.>

## Scope

### In Scope (v1)

### Out of Scope (v1)

## Resolved decisions

## Constraints

## Open questions

- **Carried forward** from [cleanup v0.0.1](archive/2026-11-21-cleanup-v0-0-1-installation-and-first-run-onboarding/) (operator, 2026-11-21): a linked
  worktree that sits under a `never_touch`-named directory is still proposed as
  eligible, on the default run and with `-o worktrees`. Worktrees come from
  `git worktree list`, not the walk, and `internal/safety` checks `never_touch` names
  only inside a candidate, not in its ancestors. Needs its own change in
  `internal/safety`.
- **Carried forward** from [cleanup v1](archive/2026-11-19-cleanup-v1-a-safe-clean-go-rebuild-of-the-gleanup-prototype/)
  (deferred again in v0.0.1): SwiftPM `.build/checkouts` strictness: should
  package-manager checkouts be treated as regenerable?
