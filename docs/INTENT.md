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

- Carried forward from [cleanup v1](archive/2026-11-19-cleanup-v1-a-safe-clean-go-rebuild-of-the-gleanup-prototype/): SwiftPM `.build` dirs are skipped when `.build/checkouts/*` clones have local branches not on a remote (seen on ~/Projects). Safe but over-conservative; should package-manager checkouts be treated as regenerable?
