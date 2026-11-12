# 0005 — Organize the window around what needs the owner

- **Status:** Accepted
- **Date:** 2026-11-12

## Context

With a supervisor delegating to a team of named agents, the owner's server
holds about 150 sessions at a time, most of them delegations linked under one
supervisor session. The pi-pocket-team Desk tracks the work as threads, each
with the decisions it asks of the owner, its PRs and its state. The web app
lists sessions; the Desk, its dashboard and the shift board live on a page of
their own. What the owner must do at any moment is spread across both.

## Decision

Pocket's navigation is a set of places, not a session list:

- **Inbox** gathers everything that waits on the owner, from every source: tool
  calls and questions in sessions (subagents' included), the Desk's asks, PRs
  ready to merge, a full desk, failed runs, found and quiet threads. Each is
  answered on its card.
- **In motion** shows the Desk's threads by who moves them next, and runs at
  work that no thread holds, each with what it does now.
- The **Shift board**, **Numbers** and the **Team** are places of their own.
- Sessions stay reachable (the palette, pinned and recent sessions, with
  delegations nested under the session that started them), but are not the
  first thing the window shows.

## Consequences

- The owner answers most things without opening a session.
- Pocket keeps its own grouping rules for threads (as the Desk page's
  `groupOf`) and its own read marks; they can drift from the web app's.
- A thread names no session in the Desk's view: Pocket matches the session by
  title, which can miss.

## Alternatives considered

- **Mirror the web app** (a session list, the Desk as a page with tabs): known
  to the owner, and the layout Pocket exists to improve on.
- **A session list sorted by state:** keeps 150 rows as the unit of attention;
  the Desk's thread is the better unit.

## References

- Shapes: [design: Pocket](../design/pocket.md)
- Builds on: [ADR-0004](0004-build-a-native-client-of-pi-pockets-own-api.md)
