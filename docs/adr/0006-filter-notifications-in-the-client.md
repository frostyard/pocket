# 0006 — Filter notifications in the client

- **Status:** Accepted
- **Date:** 2026-11-12

## Context

Pi Pocket sends an embedder's connection each notification it would push to
the person (`embedder-notify`), by the person's server-side preferences, which
also drive Web Push to their phone. With a team's delegated runs finishing all
day, "Pi finished" notifications arrive in bursts that are mostly not the
owner's own work. The Desk's decisions reach the owner through no server
notification of their own in Pocket.

## Decision

Pocket decides what it shows, in `internal/app/notify.go`, with settings of
its own, and leaves the server's preferences alone:

- By kind (calls and questions, Desk decisions, finished runs, chat, limits,
  watched PRs); finished runs default to the sessions the owner started or
  pinned, not delegations.
- A burst shows as one: the first notification at once, what follows within
  10 seconds as one summary.
- Do not disturb, a snooze and daily quiet hours hold notifications; calls
  waiting for approval may come through.
- New Desk asks notify through the same path.

## Consequences

- The owner tunes the desktop without changing what their phone receives.
- Two places decide notifications; Pocket's Settings say so, and point at the
  server's.
- Held notifications are not replayed: the Inbox holds what still waits.

## Alternatives considered

- **Change the server's preferences from Pocket:** would change the phone's
  notifications too.
- **Show every notification:** the owner found the volume overwhelming.

## References

- Shapes: [design: Pocket](../design/pocket.md)
- Builds on: [ADR-0004](0004-build-a-native-client-of-pi-pockets-own-api.md), [ADR-0005](0005-organize-the-window-around-what-needs-the-owner.md)
