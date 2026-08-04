---
adr: 0001
title: Use ADRs for architecture decisions
status: Accepted
date: 2026-08-04
supersedes: none
superseded-by: none
---

# ADR-0001: Use ADRs for architecture decisions

## Status

Accepted

## Context

ergon reached v1.6.6 with its design rationale spread across three
places: commit message bodies, prose paragraphs inside `README.md`,
and nowhere.

Four load-bearing decisions are already in the codebase with no
durable record of why. The `lint` / `check` umbrella split, the
choice to exclude mutation and branch from the default gate, the
decision to warn rather than fail on `B/op`, and the placement of
e2e tests behind a build tag are all deliberate, all
counter-intuitive on first read, and all currently justified only
in prose written for a different purpose.

That prose has now moved into `docs/explanation/`, which fixes
discoverability but not permanence: explanation pages are edited
to stay current, so the reasoning that applied at the time of the
decision is overwritten by the reasoning that applies now. The two
are different documents, and only one of them answers "why did we
do this?"

The repository is public and accepts outside contributions. A
contributor who cannot find why a constraint exists will either
re-litigate it or route around it.

## Decision

We will record architectural decisions as ADRs in `docs/adr/`, one
decision per file, numbered and never renumbered. An ADR is never
edited in place once Accepted; a decision that changes is recorded
as a new ADR that supersedes the old one.

## Alternatives Considered

None — this is a process bootstrap.

## Consequences

**Positive:**

- Decisions acquire a permanent, searchable record that survives
  the departure of whoever made them.
- Re-litigating a settled question costs a link rather than an
  argument.
- The `Alternatives Considered` section forces the rejected
  options to be written down while they are still remembered.

**Negative:**

- Every architectural change now carries a documentation step, and
  a process that is inconvenient under deadline pressure is the
  kind that gets skipped first.
- ADRs accrete. Some will be wrong, and they stay on disk being
  wrong, because deleting them destroys the record.
- The existing decisions named in Context are not retroactively
  covered by this ADR. They remain undocumented until someone
  writes them up.

**Neutral:**

- This ratifies a practice the project was already reaching for
  informally in commit bodies, rather than introducing a new one.
