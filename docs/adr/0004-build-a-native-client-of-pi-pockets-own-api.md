# 0004 — Build a native client of Pi Pocket's own API

- **Status:** Accepted
- **Date:** 2026-11-12

## Context

The owner runs a Pi Pocket server (with the pi-pocket-team modules) on a VM
and reaches it over a tailnet. The web app is built mobile-first, around one
session at a time; at a desk, with a team of agents at work, the owner wants a
desktop app that is faster and calmer than a browser tab. Pi Pocket offers an
app bridge for embedders that wrap its web app in a web view, and the HTTP API
and event stream its web app uses. frostyard's desktop apps (ChairLift,
Frostmail, Frostnotes) are Go programs on MyGo's native UI and frostui.

## Decision

Pocket is a native Go program on MyGo's native UI and frostui that speaks the
web app's own protocol to an unchanged Pi Pocket server:

- It signs in with the owner's sign-in link or an invite and sends the token
  as a bearer token, never as a cookie; it keeps the token in the desktop's
  keyring.
- It reads one event stream per window (`/api/events`, protocol 5), changes
  the session it shows in place (`/api/switch`), and merges `view` events as
  the web app's `mergeView` does, in `internal/pocket`.
- It reads the team modules (`/api/x/team`, `/api/x/desk`) through their JSON
  routes, and implements their views natively rather than hosting their
  browser modules.
- It asks nothing new of the server: a feature that needs a server change is a
  change to Pi Pocket or its modules first.

## Consequences

- The window opens at once, draws Markdown, diffs and lists natively, and owns
  the desktop's notifications, badge and keyring.
- Pocket must follow the web app's protocol as it changes: every route and
  field it reads is checked against the server's source, and a field in an
  unexpected shape is logged and skipped, not fatal.
- Extension browser modules do not run in Pocket. Each module the owner relies
  on needs a native view of its own; one that has none stays in the web app.
- Artifacts (sandboxed HTML) and the server's browser panel need a web view.

## Alternatives considered

- **A web-view wrapper over the app bridge** (as Pi Pocket GUI): the web app
  as it is, with native notifications; it keeps the web app's one-session
  layout, which is what the owner wants to leave.
- **Electron or Tauri:** a second runtime and toolchain beside the fleet's Go
  apps, for a web UI Pocket does not want.
- **New server routes for Pocket:** would make Pocket depend on a fork of the
  server; the web app's routes already carry what Pocket needs.

## References

- Shapes: [design: Pocket](../design/pocket.md)
- Builds on: [ADR-0001](0001-record-architecture-decisions.md)
