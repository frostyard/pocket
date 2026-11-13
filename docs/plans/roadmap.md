# Plan: Roadmap

What Pocket delivers, in order: a desktop client of the owner's Pi Pocket
server that surpasses its web app at a desk. Phases 1 to 4 shipped on
2026-11-12; phase 5 is in progress.

## Phase 1 — A session, native (done)

- Sign-in, the event stream and view merging; the transcript with streaming
  Markdown, folded tools, approvals and questions; the composer; the
  inspector; the palette ([design](../design/pocket.md#a-session)).
- **Done when:** against a real server, a message sent from Pocket streams
  its reply back, and a call waiting for approval is allowed from Pocket
  (`TestLiveDemo`).

## Phase 2 — Places for the team (done)

- The Inbox, In motion, the Shift board, Numbers, the Team and agent pages,
  peeks ([design](../design/pocket.md#places)).
- **Done when:** the owner answers a Desk decision and allows a call from the
  Inbox without opening a session.

## Phase 3 — Calm, settings, and the Mac (done)

- Notification kinds, bursts, do not disturb and quiet hours; the Settings
  dialog; `mygo.json` and an app icon
  ([design](../design/pocket.md#notifications-and-settings)).
- **Done when:** `make app` on a Mac builds a `Pocket.app` that signs in and
  shows a notification.

## Phase 4 — The rest of the web app the owner uses (done)

- **Team:** the roster as cards, each agent's soul and places, and the
  lessons inbox — approve, discard, edit or forget what agents propose, and
  add the owner's own (`/api/x/team/memory`).
- **Wiki:** the homewiki module's pages, read natively (`/api/x/homewiki`).
- **Artifacts:** each session's artifacts, and the artifacts across sessions
  (`/api/artifacts`), with the page in a web view.
- **Done when:** the owner approves an agent's lesson, reads a wiki page and
  opens an artifact without the web app.

## Phase 5 — Polish from daily use

- Native plan review, criteria marks, owner actions, comments, consults,
  revision and creation ([design](../design/pocket.md#desk-plans); implemented
  2026-11-12).

- Native changes and file previews, downloads and confirmed reverts
  ([design](../design/pocket.md#a-session); implemented 2026-11-12).

- File attachments by picker, drop and clipboard, plus queued/held message
  controls ([design](../design/pocket.md#a-session); implemented 2026-11-12).
- Hover hints on the sidebar's agent rows, with frostui v0.6.0's
  `SourceRow.Tooltip` (done, 2026-11-12).
- Identity for the open session (use an agent in this session) in the
  inspector (done, 2026-11-12): choose, clear and keep a folder identity.
- Shift goal and progress in the session inspector (done, 2026-11-12).
- The terminal module's shells in a Terminal place, with Ghostty's emulator
  (done, 2026-11-12).
- Terminal history is rendered without replies on attachment
  ([ADR-0007](../adr/0007-render-terminal-history-before-attaching.md),
  [design](../design/pocket.md#the-protocol)).
- **Done when:** the owner uses Pocket for a full working day without opening
  the web app.

## Later / ideas

- The supervisor's "Needs you: T13a" line as chips linked to the Inbox.
- A first release: `.svu.yml`, `make bump`, a signed and notarized Mac build,
  and a `site/` (core ADR-0012, ADR-0070).

## Open questions

- **Read marks shared with the web app?** Pocket keeps its own pinned and
  read marks; the server stores none. Decide by the first release.

## References

- Implements: [design: Pocket](../design/pocket.md)
