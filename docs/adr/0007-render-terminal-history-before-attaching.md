# 0007 — Render terminal history before attaching

- **Status:** Accepted
- **Date:** 2026-11-12

## Context

The terminal module replays retained output on attachment. That output may
contain terminal queries from programs that no longer run. MyGo queues query
replies asynchronously on the same writer as user input, so toggling a flag
when the first read ends cannot distinguish delayed replay replies from live
input. Pocket speaks the module's existing protocol under
[ADR-0004](0004-build-a-native-client-of-pi-pockets-own-api.md).

## Decision

Pocket renders the first successful terminal poll in a temporary Ghostty
emulator whose connection discards all writes and cannot touch the desktop.
It feeds the resulting screen and scrollback snapshot to the live emulator.
Subsequent polls pass through unchanged. The temporary emulator uses the most
recent requested terminal dimensions and is closed after producing the
snapshot. A failure to render history ends the attachment rather than sending
unprocessed history to the live emulator.

## Consequences

Historical queries cannot type replies into the server's live shell. Live
queries and user input still work. Attachment allocates an extra emulator
briefly. The snapshot restores text, styles and modes; it does not replay old
bells, desktop notifications, title callbacks or clipboard operations. As with
other remote terminal snapshots, a concurrent resize can reflow the screen.
The first poll is the replay boundary; a query newly printed before that poll
is treated as history too, as the current module has no separate replay flag.

## Alternatives considered

- **Drop writes until the next read:** queued replies may be written later;
  this also drops user input during replay.
- **Filter known query escape sequences:** an incomplete list misses queries
  and a parser must handle sequences split across poll boundaries.
- **Add a server replay flag:** requires a module change and does not itself
  separate the emulator's queued replies from user input.

## References

- Shapes: [Pocket design](../design/pocket.md#the-protocol),
  [roadmap](../plans/roadmap.md#phase-5--polish-from-daily-use)
- Builds on: [ADR-0004](0004-build-a-native-client-of-pi-pockets-own-api.md)
- Fixes: [terminal replay queries](https://github.com/frostyard/pocket/issues/12)
