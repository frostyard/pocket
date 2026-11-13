# Pocket

Living document. Rationale: [ADR-0004](../adr/0004-build-a-native-client-of-pi-pockets-own-api.md),
[ADR-0005](../adr/0005-organize-the-window-around-what-needs-the-owner.md),
[ADR-0006](../adr/0006-filter-notifications-in-the-client.md).
Contracts: none of its own; Pocket speaks Pi Pocket's protocol (below).
Built in: [roadmap](../plans/roadmap.md).

## Overview

Pocket is a desktop client of a [Pi Pocket](https://github.com/TannerMidd/pi-pocket)
server and its [pi-pocket-team](https://github.com/bketelsen/pi-pocket-team)
modules: a native window on MyGo and frostui that talks to the server's own
API and event stream. Its navigation is places, not sessions: what waits on
the owner, what is in motion, the team, the shift board and the numbers.

```
┌ Places ───────────┬ Main ───────────────────────────────┬ Context ─────────┐
│ ✋ Inbox        4  │ Good afternoon, Brian               │ T1439 needs you  │
│ ✦ In motion     2 │ 2 decisions · 1 call · 1 PR to merge│ Irulan · frostmail│
│ ☾ Shift board   1 │                                     │ Keeps it open    │
│ ▦ Numbers         │ ┌ T1439b from Irulan ─────────────┐ │ Pull requests    │
│ TEAM              │ │ Gmail OAuth publish needs …     │ │ Reports          │
│  Odrade ⟳ 2       │ │ [1 Done] [2 Later] [answer…]    │ │ Events           │
│  Duncan   1       │ └─────────────────────────────────┘ │                  │
│ RECENT            │ ┌ Allow bash? (Fix the flaky CI) ─┐ │                  │
│ ▸ Planning Desk   │ │ git push --force-with-lease …   │ │                  │
│                   │ │           [Open] [Deny] [Allow] │ │                  │
└───────────────────┴─────────────────────────────────────┴──────────────────┘
 ● pocket.ts.net · 1 needs you · 2 at work                     Pi Pocket 0.28.1
```

```
cmd/pocket ── main: the window, keyring, notifications, badge
internal/pocket ── HTTP client, event stream, view merging (goroutines) ─┐
internal/app ── state (UI thread) + views (frostui / MyGo)  ◄── post(fn) ─┘
internal/config ── settings file; the token in the desktop keyring
internal/icons ── the icons Pocket draws itself
```

## Design

### Places

- **Inbox** (⌘1 / Ctrl 1) — one queue of everything that waits on the owner,
  the most pressing first: calls to allow and questions waiting in sessions
  (subagents' included), the Desk's decisions, PRs ready to merge (by review
  level), a full desk (Allow 1), failed runs, found work, threads gone quiet,
  and lessons to review (which the badge does not count). A decision or report
  that names an artifact (`artifact:<id>`, as the Desk writes it) links it,
  and gets a button titled as the artifact that opens it in Artifacts: the
  id is looked up across sessions, the thread's own session first, then its
  supervisor's, then the newest. Each is answered on its card; J/K move, 1–6 answer a decision with
  that option, ⌥Y / ⌥N allow or deny. The right pane shows the chosen item's
  context: a thread's PRs, reports and events, or a session's last steps.
- **In motion** (⌘2) — every thread under way, grouped by who moves it next
  (agents at work, with the supervisor, in review, plans, watching, parked),
  each with what its session does now from the server's peek feed, and runs
  at work that no thread holds. Double-click opens the thread's session.
- **Team** (⌘3) — the lessons agents want to keep, to approve, edit or
  discard (a change to an approved one shows old and new), and the roster.
  Each agent's page has a task card, its sessions, its lessons (forget,
  edit, add one of the owner's), places and profile. Hovering an agent, in
  the sidebar or by its avatar anywhere, says its role, model, home and
  repositories.
- **Shift board** (⌘4) — the night-shift goal board: limits, rows by state,
  Pick / Unpick, Launch picks.
- **Numbers** (⌘5) — the Desk's dashboard: completion, closures, cycle time,
  workload, PRs, cost and tokens, closures by day, by repository.
- **Wiki** (⌘6) — the homewiki module's pages, read-only, rendered natively;
  links between pages stay in the wiki, and it reads again as pages change.
- **Artifacts** (⌘7) — every session's artifacts, newest first: an SVG drawn
  natively, an HTML or Markdown page in a web view signed in as the owner
  (the `pocket_auth` cookie for the server's host and its `/a/` pages
  alone), or in the browser where the system has no web view. The web view
  loads only the server's `/a/` pages, which the server sandboxes, and
  `about:blank`: any other page of the server would load signed in as the
  owner. A page that lands elsewhere all the same (a reload or a move in the
  history, which the navigation hook does not see) is blanked, and gets no
  cookie. Another web link a person clicks opens in the browser; a script's
  own navigation opens nothing, though MyGo on Linux counts a script's link
  click as a person's. Signing out, or signing in to another server, deletes
  the cookie and blanks the view.
- **Terminal** (⌘8) — shells on the server, as the owner, through
  pi-pocket-team's terminal module, drawn by Ghostty's emulator (MyGo's
  terminal plugin): tabs of the shells (four at most, the module's cap), a new
  one in the server's home or, from a session's details or ⌘⇧T, in that
  session's folder. While a terminal has the focus, the window's Ctrl keys
  that shells use (Ctrl N, P, L, B, Q, Alt Y, N) go to the shell; on macOS
  they are ⌘ keys and never reach it. What a shell prints is the server's, so
  it may not write the desktop's clipboard (OSC 52) or read it, and only the
  web and mail links it prints open with the system; a shell that exits shows
  its code, and one that goes or stops taking keys says it is disconnected.
- **Sessions** — pinned sessions and recent ones that no other session
  started; delegations nest under the session that started them. ⌘P finds
  any session.

The places that need a module (Team, the Desk's Shift board and Numbers, the
Wiki) show only when the server has it; Pocket works against a Pi Pocket
without the team modules.

### A session

Replies stream in as Markdown (`ui/markdown.Stream`, word-paced, faded in).
Thoughts fold to one line. Tool calls fold into one line per run of calls,
which opens to each call, which opens to its output or diff; a run at work,
or one waiting for the owner, opens by itself. Messages machines write
(subagent answers, `[desk]` and `[shift]` notes) fold to their first line.
Approvals and questions dock above the composer.

The composer: Enter sends, Shift Enter breaks the line, ⌘Enter steers a run
that is working, Up brings the last message back, Esc stops the run, `/`
completes commands, templates and skills, `@agent task` starts a linked
session as that agent. The right pane: model and thinking, folder, branch,
spend, speed, cache, context used, subagents, the queue.

### Notifications and settings

Every notification passes `deliver` (`internal/app/notify.go`): the kinds the
owner chose, do not disturb (now, snoozed, quiet hours; calls to allow may
come through), and bursts gathered into one. The Settings dialog
(`internal/app/settings.go`, ⌘,) holds those choices, the theme and the
server's facts. The status bar shows do not disturb while it holds.

### The protocol

- **Sign-in:** the owner's link (`/login?token=…`) carries the token; an
  invite (`/join/…`) is redeemed for one. The token goes in the keyring
  (service `org.frostyard.Pocket`) and on every request as a bearer token,
  with `X-Pocket: 1`.
- **Events:** one SSE connection per window
  (`/api/events?p=5&embedder=notify`); the session shown changes in place
  with `/api/switch`. Live messages come as deltas, a whole view brings only
  the newest rows (earlier ones load on demand), light entries are made
  whole in the next update. `pocket.View.Apply` is a port of the web app's
  `mergeView`. Peeks (`/api/peeks`) give each waiting or working session's
  last steps.
- **Team, Desk and wiki:** JSON routes under `/api/x/team/…` (agents,
  supervisor, memory), `/api/x/desk/…` and `/api/x/homewiki/…`; their `ext`
  events are signals to read again. A lesson's approval sends what the owner
  saw (`expected`), so it holds only for that text. Artifacts come from
  `/api/artifacts` and `/a/<session>/<artifact>/<version>`.
- **Terminal:** `/api/x/terminal/…`. `pocket.TermConn` is a shell as an
  `io.ReadWriteCloser` for the emulator: Read long-polls the output (25 s at
  most) from the offset it has, in the module's units (UTF-16 code units: it
  takes the server's `to`); Write posts keys in chunks numbered per window, so
  a chunk sent again lands once, and a chunk the module refuses, or that does
  not land in 30 s, ends the connection; Resize posts the size the view fits. Closing
  it stops polling, and the module ends the shell 60 s later; End ends it at
  once. The emulator's library (libghostty-vt) is packaged by `mygo build`, and
  downloaded once into the user's cache under `go run` and `go test`; it needs
  macOS 13 (`mygo.json`). A thread's session is matched
  by title. A session's agent comes from its folder: an agent's home, or a
  delegation's worktree `<checkout>--<agent>-<slug>`.
- **Threads:** the client's goroutines never touch UI state; they post
  functions to the window (`Window.Update`).

## Operational notes

- **Activity** (⌘J) logs the connection, every command Pocket sends, and any
  field the server sent in a shape Pocket does not read.
- The stream reconnects with a growing wait (the server's `retry`, doubling
  to 30 s, with jitter); a stream silent past twice the server's 15 s pings
  counts as dead. A rejected token signs out and shows the sign-in screen.
- Reading the Desk can take long: it sweeps GitHub first when nothing swept
  for 5 minutes. Pocket gives it 3 minutes and keeps the last Desk on screen
  when a read fails.
- macOS shows notifications only from a packaged app (`make app`,
  `make dev`).
- The live terminal test needs the demo server started with
  `POCKET_DEMO_MODULES=<a pi-pocket-team checkout>`, which drops the
  terminal module into its data folder and turns it on.
- The live tests (`make live`) drive the app against real Pi Pocket servers
  with a scripted model that costs nothing. In a Pi Pocket checkout (after
  `npm ci`), `node test/serve.ts 8899 127.0.0.2` serves a copy and prints an
  owner link; copy this repository's `scripts/pocket-demo.ts` to its `test/`
  and run `node test/pocket-demo.ts 8898 127.0.0.3` for the demo server (a
  model that thinks, runs tools and asks, and a call waiting for approval).
  Set `POCKET_TEST_SERVER` and `POCKET_TEST_TOKEN`, and `POCKET_DEMO_SERVER`
  and `POCKET_DEMO_TOKEN`, to their addresses and tokens.
- Settings live in the user's config folder (`pocket/settings.json`;
  `POCKET_CONFIG_DIR` picks another). `POCKET_NO_KEYRING=1` keeps the token in
  a file there instead of the keyring, for development.

## References

- Rationale: [ADR-0004](../adr/0004-build-a-native-client-of-pi-pockets-own-api.md), [ADR-0005](../adr/0005-organize-the-window-around-what-needs-the-owner.md), [ADR-0006](../adr/0006-filter-notifications-in-the-client.md)
- Repository structure: [agent-ready repository](agent-ready-repository.md)
- Built in: [roadmap](../plans/roadmap.md)
