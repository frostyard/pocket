# Pocket

Living document. Rationale: [ADR-0004](../adr/0004-build-a-native-client-of-pi-pockets-own-api.md),
[ADR-0005](../adr/0005-organize-the-window-around-what-needs-the-owner.md),
[ADR-0006](../adr/0006-filter-notifications-in-the-client.md),
[ADR-0007](../adr/0007-render-terminal-history-before-attaching.md),
[ADR-0008](../adr/0008-approve-and-merge-as-the-owner-from-the-desktop.md).
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
  level, each with Approve + merge as the owner when GitHub's gates pass:
  see [the protocol](#the-protocol)), a full desk (Allow 1), failed runs,
  found work, threads gone quiet,
  and lessons to review (which the badge does not count). Team lessons and
  Pi Pocket's own pending lessons appear together, with the latter labeled
  “Pi Pocket memory”. Approve sends the proposal shown; Discard withdraws it.
  The core queue uses the owner's private PiP id from the stream's `pip` event,
  reads again on hello and `memory` events, and requires a `pendingAll`
  response. If PiP has never been opened, the Inbox explains how to enable
  the queue; it never substitutes an ordinary session. A decision or report
  that names an artifact (`artifact:<id>`, as the Desk writes it) links it,
  and gets a button titled as the artifact that opens it in Artifacts: the
  id is looked up across sessions, the thread's own session first, then its
  supervisor's, then the newest. Decisions stay together by thread, oldest thread first, with asks in the
  server's filing order and one thread heading. Reopened asks start a fresh
  draft and notify again. Each is answered on its card; J/K move, 1–6 answer a decision with
  that option, ⌥Y / ⌥N allow or deny. The right pane shows the chosen item's
  context: a thread's PRs, reports and events, or a session's last steps.
  Events and answered words take at most two lines; hover shows the full text.
  The right pane's Details heading has its own content row below the window's
  title bar, so palette and native window controls cannot squeeze it.
  Desk Markdown paths under `reports/`, `desk/`, `plans/` and `decisions/`
  open in a read-only native dialog from asks, reports and events. The file
  route checks containment and size; the reader loads no images.
- **In motion** (⌘2) — every thread under way, grouped by who moves it next
  (agents at work, with the supervisor, in review, plans, watching, parked),
  each with what its session does now from the server's peek feed, and runs
  at work that no thread holds. Double-click opens the thread's bound session. The optional Desk `session`
  field is authoritative, even when that session is missing locally; only older
  modules without the field use the title-and-agent fallback.
  Running now reads the server task board every five seconds while this place is
  open. It shows task labels, subagent names, start times and server-reported
  stalls, with Stop for supported task kinds; scheduled messages link to their
  session. A hidden place stops polling; stale reads cannot cross sign-ins.
- **Team** (⌘3) — the lessons agents want to keep, to approve, edit or
  discard (a change to an approved one shows old and new), and the roster.
  Each agent's page has a task card, its sessions, its lessons (forget,
  edit, add one of the owner's), places and profile. Hovering an agent, in
  the sidebar or by its avatar anywhere, says its role, model, home and
  repositories. A read of the lessons that fails stays an error on the Team
  page and on an agent's page, with Try again; the lessons read before it stay
  under the error, and a redraw does not read them again.
- **Shift board** (⌘4) — the night-shift goal board: limits, rows by state,
  Pick / Unpick, Launch picks, and a supervisor choice when several are live.
  Row details show the task, objective, stop condition, worker and PR check.
  Proposed and picked rows can change their maximum rounds and deadline; those
  and waiting rows can be dropped. Failed rows reopen, and unknown rows can be
  linked to a thread or marked failed. Forms retain the revision they opened
  on. Conflicts refresh the same board; a failed supervisor read restores the
  displayed board's choice. An uncertain retry retains its operation key.
  A waiting row may carry `held: { reason, at }` when the Desk's opt-in meter
  hold holds its launch; Pocket ignores the field for now.
- **Numbers** (⌘5) — the Desk's dashboard: completion, closures, cycle time,
  workload, PRs, cost and tokens, closures by day, by repository. The 7/30/90-day
  range and GitHub owner/repository choices filter the snapshot. Delivery and PR
  tiles and repository rows open their records; thread buttons lead to In motion.
  Unattributed records have their own action. Every drill-down keeps the shown
  snapshot's scope, and an older response cannot replace a newer filter selection.
  Coverage limits and metric definitions explain what the totals omit.
  The daily closure chart has an integer count axis; hover or keyboard focus
  shows the day's date and count beneath it, including zero-closure days.
  A Subscriptions card sits above the rest when the dashboard has the optional
  `meter` field (pi-pocket-team's Desk meter); with no field, there is no card.
  Each provider row is fresh (a gauge), stale, awaiting a report, or none; its
  texts come from the server, and Pocket computes no projection.
- **Wiki** (⌘6) — the homewiki module's pages, read-only, rendered natively;
  links between pages stay in the wiki, and it reads again as pages change.
  A read that fails stays an error in the pane with Try again; until then a
  redraw does not read the wiki again. Two reads of a page can overlap, and
  only the newest one changes the pane.
- **Artifacts** (⌘7) — every session's artifacts, newest first: an SVG drawn
  natively, an HTML or Markdown page in a web view signed in as the owner
  (the `pocket_auth` cookie for the server's host and its `/a/` pages
  alone), or in the browser where the system has no web view. The system
  browser uses its own sign-in; Pocket does not sign it in. The web view
  loads only the server's `/a/` pages, which the server sandboxes, and
  `about:blank`: any other page of the server would load signed in as the
  owner. A page that lands elsewhere all the same (a reload or a move in the
  history, which the navigation hook does not see) is blanked, and gets no
  cookie. Another web link a person clicks opens in the browser; a script's
  own navigation opens nothing, though MyGo on Linux counts a script's link
  click as a person's. Signing out, or signing in to another server, deletes
  the cookie and blanks the view.
  The version menu follows the latest by default, or pins an older version per
  artifact; SVG caches, web previews and browser links all use that selection.
  An SVG body that fails to read or parse shows its error in the preview with
  Try again, kept for that artifact and version, so a redraw does not read it
  again. Read the artifacts again clears those failures, so an SVG the server
  can now send draws.
  These are standalone artifact pages. The server omits its parent-frame bridge
  for top-level documents, so per-person saved state (the `artifact-state` API)
  and follow-up messages need the web app's session iframe. The native preview
  does not expose those APIs or broaden its `/a/` cookie to provide that bridge.
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
- **Arrakis** (⌘9) — the team as one drawn scene, a desert under an eclipse,
  that fits the pane at any size and never scrolls. The eclipse's disc counts
  what the Inbox counts, and its ring is every open Desk thread by who moves
  it next (the owner, the supervisor, agents, review, watching), or without a
  Desk the sessions that move; a click on it opens the Inbox. Each named
  agent is a figure on the dunes, the supervisor under the eclipse: a
  glowglobe and a spice harvester beside it while a run is at work (small
  lights for its subagents), a thumper while it calls for the owner (a call
  to allow, a question, a Desk ask), embers for a failed run, water for a
  finished run not yet read. Under
  the name a line says the most pressing thing, from the same peek feed as In
  motion. A click chooses a figure, whose asks and sessions the right pane
  lists; a double click goes where it presses most (the Inbox at its ask, or
  its session); J/K walk the figures. Sessions of no named agent stand small
  on the far ridge while something about them moves, seven at most, and
  stand in for the team on a server without one. What changes between two
  views of the scene plays as an event (`internal/app/arrakis_action.go`): a
  carryall sets a harvester down when a run begins, after an ornithopter
  brings the task from the supervisor (for a session another session
  started) or from the eclipse; the harvester strikes spice at each step of
  the peek feed; the carryall lifts it in a blow of spice when the run
  ends, and the worm takes it when the run fails; a new call flies to the
  eclipse, which rings when the Inbox's count rises. Events are found only
  between views that both showed the place, so the desert never replays
  what it missed. A worm passes while runs are at work (an agent named
  Sheeana rides it while she works), a storm with lightning stands on the
  horizon while failed runs are unread, and a heighliner hangs in the sky
  for each PR ready to merge, three at most. **Vision** in the place's bar
  shows a made-up order going around those states instead of the team
  (`internal/app/arrakis_vision.go`), to see the desert move; it leads
  nowhere and reads nothing. The sky follows the device's clock, or stays
  day, dusk or night. An
  agent named after a person of the books wears that person's title and
  faction (`internal/app/arrakis_cast.go`). It is a view of what the window
  already knows ([ADR-0005](../adr/0005-organize-the-window-around-what-needs-the-owner.md)):
  it adds no route and asks the server for nothing
  ([ADR-0004](../adr/0004-build-a-native-client-of-pi-pockets-own-api.md)).
  The view builds an immutable scene and a `Draw` paints it from the frame
  clock (`internal/app/arrakis_scene.go`): 30 frames a second on a GPU, 8
  where the window draws on the CPU, none while the desktop asks for less
  motion or the place is out of sight. Dune and its people belong to the
  estate of Frank Herbert; this is an unofficial fan work.
- **Sessions** — pinned sessions and recent ones that no other session
  started; delegations nest under the session that started them. ⌘P finds
  any session. A pin belongs to the account signed in: another server or
  user, which numbers its sessions differently, sees none of them.

The places that need a module (Team, the Desk's Shift board and Numbers, the
Wiki) show only when the server has it; Pocket works against a Pi Pocket
without the team modules.

Thread details expose the Desk's owner actions: keep, park, wake, attach found work,
and drop with a required reason. Watches accept a date and optional time in the
server's timezone and a check of at most 200 characters. A watch can be cleared,
passed or failed; recently completed non-plan threads can be watched again.
Due checks appear in the Inbox and In motion. Actions are disabled while a request
runs, and every completion refreshes the Desk, including a rejected conflict;
an unsuccessful action keeps its typed reason.

In motion stamps the last look after reading the Desk, retaining the previous
look as the baseline for fresh row marks throughout the visit. Its folds show
other authors' PRs, queued issues and completed plans (read on demand); the
thread pane shows resolved and withdrawn asks with their reasons. Ready PRs
sort L4, L3, L2, L1/L2, L1, then unknown, oldest label first within a level.

The Inbox, session states, status counts and thread-to-session matches share a
cache between data changes. Stream events, request completions, session lists
and seen marks invalidate it; current-view revisions and replaced snapshots
also change its key. The next 30-day unread expiry invalidates it even without
a server event. Session titles are normalized once per snapshot, and each
thread match is retained until the sources change. The 500-session/100-thread
Inbox benchmarks in `internal/app/derived_test.go` measure rendering and the
derived-data work separately.

### Desk plans

Plan threads and completed-plan rows open a native plan page; Inbox plan asks
open it instead of answering before the criteria are shown. The page renders
the server's progress steps, Markdown sections, task graph, comments, consults
and findings. Each option/criterion cell is blank, yes, partly or no; saving
marks keeps the unsent answer. Answer drafts belong to the ask ID and generation,
so reopening a decision never reuses an older answer or marks.

The owner can approve the displayed revision and graph, request changes with
an optional section comment, add or dismiss comments, stop consults, reopen
answered decisions, or stop and revise an approved scope. Scope changes list
the approved graph's tasks and require confirmation; dropping requires a reason
and confirmation. A new plan selects a lead and supervisor from the Desk roster.
Replay keys stay with the same action payload across failures and change when
the form changes. A 409 reloads the plan, retains draft text and asks the owner
to review the new state before reopening the action; failed reads disable writes.
The page follows Desk change events and refreshes after an action or on request;
reads coalesce while another read or action is running. Reopening the same plan
keeps its drafts, with separate text for each form; a successful action clears
the text of the form it submitted, and an action from elsewhere on the page
leaves every saved form text alone. Unedited criterion cells
follow the latest server marks; an answer with no mark edits leaves marks alone.
New plan reuses a pending or failed creation’s replay key across dialog closure,
so reopening it cannot start a duplicate.

These owner-only routes are the Desk module's protocol, under
`/api/x/desk/plans/` and `/api/x/desk/asks/`, as required by
[ADR-0004](../adr/0004-build-a-native-client-of-pi-pockets-own-api.md).
Built in [roadmap Phase 5](../plans/roadmap.md#phase-5--polish-from-daily-use).

### A session

New Session (⌘N / Ctrl N), the sidebar's plus button and `/new` open a working
directory prompt before sending a request. The command and `/new` offer the
current session's folder; otherwise the prompt offers the server default.
The path is on the Pi Pocket server, and leaving it empty uses that server's
default. Creation errors keep the entered path available to correct.

Find (⌘F / Ctrl F) searches loaded transcript rows, including folded text,
commands and literal tool output; Previous/Next reveal the matching row.
Search earlier messages fills more of the active context on demand. The
session menu and palette also offer Fork (from the latest finished reply),
Export (complete Markdown through the native save dialog), and New context
(with confirmation; the transcript stays in history). Pending operations keep
their source session, and a late fork never moves the user away from a
different session.

Replies stream in as Markdown (`ui/markdown.Stream`, word-paced, faded in).
A streaming reply changes no stored row, so it does not build the transcript
again: the rows are built once per conversation revision, and a live update
refreshes what they hold of the live state in place (each folded call's slot
and approval, and the row for the run at work); the search text cached over
the rows stays. Thoughts fold to one line. Tool calls fold into one line per
run of calls, which opens to each call, which opens to its output or diff; a
run at work, or one waiting for the owner, opens by itself. Messages machines
write (subagent answers, `[desk]` and `[shift]` notes) fold to their first line.
A message the owner sent shows what it carried under its words: a paperclip
pill per file the server records with the message, in the file's base name with
its path in the tooltip, and a count of the pictures it stored. That list also
holds files an older server named in the message's text, each file once, and a
message that carried only attachments draws those with no empty text block.
Approvals and questions dock above the composer.

The inspector opens Changes and files in a native sheet. Changes show the
repository, added/removed counts and files edited in this session; selecting a
change loads frostui’s `DiffView`. Files has a filtered path list and an explicit
path field, with read-only text, image and folder previews. The server’s truncated
previews are labeled. Downloads use a native save dialog, preserve the bytes and
fail above 64 MB; image responses are limited to 25 MB and raster previews to 16 million pixels.
SVGs render natively; other unsupported formats offer a download. Text previews
virtualize lines, and session exports retain their Markdown save filter.
Revert requires a destructive confirmation, captures the source session and
reports busy/conflict errors without discarding the preview. New files are
explicitly described as deleted; renamed files remain for Git to undo. Viewers
do not receive file controls. The server remains the permission authority.

The composer accepts files from a native picker, file drop, or clipboard files
and PNG images. Upload chips show progress or failure and can be removed before
sending. Disk files stream from a validated open regular file; uploads have a
ten-minute deadline and cancel on chip removal or sign-out. Uploads use the server's 50 MB limit and travel as attachment references
with the message; sending waits for every upload. Drafts and uploaded references
stay with their source session across navigation; sign-out clears remote file
references. A draft or an Up-arrow history entry belongs to its account, the
server's base URL and the user signed in there: a session ID is that server's,
so sign-out keeps them in memory for the account that comes back, and neither
another server nor another user on the same server sees them. Files accompany
ordinary messages, not local slash commands or `@agent` task creation.

Queued messages show in a dock above the composer, as the web app shows them:
a count line, then one row for each waiting message with how it goes, who sent
it and what it says, without the sender's prefix. Each text takes 1 line, and the
rows scroll past the dock's height, so a long queue never squeezes the field.
While a message to a working session waits for its answer, a local Sending row
shows it; the row goes once the server lists a new message by the same person,
which the dock knows by identity, never by text. Queue rows can withdraw messages
or take the sender's own message back to the composer with its attachments.
Returned text is added ahead of any existing
draft, even if another session is now open. Edit retains the displayed text before
the server can remove it, so a timeout or sign-out keeps a recovery copy. The copy
is kept by the message's identity, not by its text, and only once: a retried Edit does not
add the same message again, while another message with the same words still does. An Edit that succeeds
clears the marker; sign-out clears every marker with the view. Stop holds the pending queue;
held messages show in the same dock, where they can be sent again, or one's own
edited or discarded.
Controls follow the current driver and sender; remote workspaces hide Attach.
These controls rely on the server for driver permissions and report races where
a message has already reached Pi.

The composer: Enter sends, Shift Enter breaks the line, ⌘Enter steers a run
that is working, Up brings the last message back, Esc stops the run, `/`
completes commands, templates and skills, `@agent task` starts a linked
session as that agent. A task being created keeps its command as a pending
state of the source session: a failure, or a sign-out that drops the answer,
puts the whole `@agent task` text back ahead of what the person typed
meanwhile, and a retry sends a new command with a new idempotency key. When
the task starts while the person still looks at the same session, view and
place where they sent it, Pocket opens the new session; the agent page counts
as that place, even though the task links to another session. Otherwise Pocket
names the new session in a toast
and stays where the person went. The right pane: model and thinking, folder, branch,
spend, speed, cache, context used, subagents. Its identity menu
reads the team's binding, chooses an agent or clears it, and can pin an
identity inferred from the folder with “Keep this identity”. A Shift section
shows the objective, stop condition, state, round count and deadline when a
night-shift goal exists. Both read on session entry and reconnect, and on
their matching module events; late answers for a session left are discarded.
Shift controls remain `/team-shift status`, `stop` and `resume` in the composer.

The session inspector opens a background-task reader with bounded output (the
latest 20,000 characters), task status and Stop. It refreshes every three seconds
while open, and discards results after a session change. A missing output does
not hide a fresh task list or keep polling that missing record. Terminal control
sequences are removed from displayed output. Stop uses the numeric
server task ID; output uses the background record ID. Subagent rows also show
peek activity and the session's last activity time.

Scheduled messages arrive through the session stream. The inspector creates them
from a time-and-message phrase, defaulting to the device's IANA timezone when
known (an empty field leaves the choice to the server), and can cancel them.
A server-provided schedule version enables Edit and Pause/Resume. Edits keep
the original version and repeat rule while changing the message or next time;
a rejected edit keeps its draft and a conflict explains how to reopen the latest
schedule. Calendar repeats set their own next time, so that field is read-only.
Mutation controls are hidden for viewers.

The session inspector can watch the agent's browser read-only. Watching is
explicit, uses authenticated GETs for its state and JPEG frames, and stops when
the session changes, the watch is closed or Pocket signs out. Long polls wait
up to 25 seconds for a newer frame; idle closed pages are checked every five
seconds. It never opens a browser page or sends input. Missing browser support
shows its server error without hiding the session.

Browser watch waits one second between frames (at most one frame per second),
reading the page state before each frame. A closed browser is checked every
five seconds. A failed read pauses until Try again; JPEG decoding runs off the
UI thread and rejects frames larger than 16 million pixels.


### Notifications and settings

Every notification passes `deliver` (`internal/app/notify.go`): the kinds the
owner chose, do not disturb (now, snoozed, quiet hours; calls to allow may
come through), and bursts gathered into one. A burst belongs to the sign-in that
started it: when its window ends the settings and the clock are read again, so a
kind turned off or do not disturb begun in the meantime holds it. Sign-out drops
it, and a click never opens another sign-in's session. Approval notification
tags expire after a day and clear on sign-out. The Settings dialog
(`internal/app/settings.go`, ⌘,) holds those choices, the theme, the zoom,
the server's facts and GitHub (the tokens, the preflight and Batches of
Approve + merge). The status bar shows do not disturb while it holds.

The zoom scales the whole window, text, icons and spacing together, as a
browser zooms a page (MyGo's `Context.SetZoom`, drawn sharp at every level):
50% to 300% in a browser's steps, saved as `zoom` in the settings (none for
100%). ⌘= (or ⌘+), ⌘- and ⌘0 step it, and a terminal's own zoom (the wheel
with Ctrl held, ⌘ on macOS, and its font size keys) steps the window's zoom
rather than its font. The window keeps its size, so a larger zoom gives the
panes less room.

### The protocol

- **Sign-in:** the owner's link (`/login?token=…`) carries the token; an
  invite (`/join/…`) is redeemed for one. The token goes in the keyring
  (service `org.frostyard.Pocket`) and on every request as a bearer token,
  with `X-Pocket: 1`. The sign-in field masks the link. Public HTTP links
  require a clear-text credential warning and confirmation before any
  request; localhost and literal private or loopback addresses are exempt.
- **Events:** one SSE connection per window
  (`/api/events?p=5&embedder=notify`); the session shown changes in place
  with `/api/switch`; until its `switched` event, that connection's session
  events are about the session left. A new connection names the session
  shown itself, so it starts with no pending switch, and a switch answer
  from an older connection or sign-in is ignored. Live messages come as deltas, a whole view brings only
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
  macOS 13 (`mygo.json`). On attachment, the first poll is rendered in a
  temporary emulator that discards replies. Its screen and scrollback snapshot
  reaches the live emulator; subsequent polls pass through unchanged. Historical
  queries therefore cannot write into the shell ([ADR-0007](../adr/0007-render-terminal-history-before-attaching.md)).
  A replay-render failure ends the attachment. A thread's session is matched
  by title. A session's agent comes from its folder: an agent's home, or a
  delegation's worktree `<checkout>--<agent>-<slug>`.
- **GitHub (Approve + merge):** the owner approves and merges a ready PR
  as `bketelsen` from the Inbox, calling `api.github.com` with his own
  fine-grained tokens from the desktop keyring, never through the server,
  under [ADR-0008](../adr/0008-approve-and-merge-as-the-owner-from-the-desktop.md).
  `internal/github` holds the client: `Preview` reads the PR query, the
  rules, rulesets and branch protection of its base, and runs the gates;
  `Act` re-reads before each of its 3 allowed mutations (approve at the
  shown head, then merge or enqueue), checks the frozen intent, and
  reconciles by 3 reads a mutation whose answer does not prove a refusal
  (no answer, a 5xx, or any 200 with `errors`: GitHub can make the change,
  then null the result); only a 4xx is a refusal, and its 1 read only
  upgrades it when the effect is there. Every read fails closed: a null
  that the Nulls rule does not allow, a null rules page or an unknown merge
  state refuses. It follows no redirect and redacts the token from every
  error; a 403 or 404 keeps GitHub's `X-Accepted-GitHub-Permissions` as the
  missing permission. The reads of Settings and of an earlier attempt send
  no mutation: `Whoami` (`GET /user`, the login and the header
  `github-authentication-token-expiration`), `CheckRepo`, `Preflight`
  (the full read of 1 ready PR, gates 1 and 2), `State` (1 PR query) and
  `Settle` (1 read, or 4 with waits: merged or queued by the owner at the
  shown head, a merge by another actor refused and never claimed, the
  owner's approval "approved, not merged", else unknown).
  `config.GitHubTokens` keeps the tokens in the keyring with no file
  fallback. Its recorded GitHub answers are in `internal/github/testdata/`.
  - **Cards** (`internal/app/merge.go`): each ready card whose `ref` parses
    and whose owner (`bketelsen`, `frostyard`) has a kept token gets a
    preview, read on entering the Inbox, after each Desk read and when a
    dialog opens, 4 at a time, kept 60 seconds, never in a frame's draw.
    An eligible card gets **Approve + merge**; a failed gate shows its
    reason; a GitHub head newer than the Desk's says "new commit since the
    Desk read". A `risk:high` card gets **Open diff** (the three-dot
    compare of the base and the shown head, opened only by Pocket's own
    URL) and no check box; its button turns on only after the opener
    reports no error at that head, and a new head locks it again. The
    Desk's `url` is never used for a GitHub call, nor opened: **Review on
    GitHub** opens `https://github.com/<owner>/<name>/pull/<n>`, built from
    the ref (any owner), and a ref that does not parse gets no button.
  - **Dialog:** the confirm dialog reads each PR again and lists its ref,
    title, head and method; its 1 button (**Approve + merge as bketelsen**,
    **Approve + merge N as bketelsen**, or **I read the diff at abc1234:
    approve + merge**) is the only path that starts a mutation. The dialog
    keeps the server and user the window showed when it opened (and each
    row's Desk thread): every attempt records that origin, and under
    another account the dialog does not start ("signed in to another
    account: close and open again"). Pocket works
    the list 1 PR at a time in Inbox order: a refusal stops only that PR, a
    401 stops that owner's PRs and marks the token bad, a rate limit stops
    the batch ("not started: rate limit until HH:MM"), and **Stop** starts
    no other PR. The end shows each result and a toast with the totals.
  - **Batches:** Settings > GitHub > Batches (off by default,
    `githubBatches` in the settings) adds check boxes, **Select all
    eligible** (the first 25) and **Approve + merge N**. `risk:high` is
    never in a batch.
  - **Results:** a merged card leaves the Inbox, and stays hidden while
    GitHub says MERGED (a `State` read at each Desk read). A queued card
    shows "In merge queue" with no button; at start and at each Desk read
    `Settle` reads every queued attempt, whether the Desk still lists its PR
    or not, and finds it merged (only with the queue's method from the
    rules: a failed rules read, or rules with no queue method, claim
    nothing, and the attempt stays queued), still queued, or out of the
    queue (the card comes back, the attempt ends as approved: "left the
    merge queue"). An unknown outcome keeps the PR
    locked, with **Check again** (reads only), **Open on GitHub** and **Try
    again** (a new attempt through the dialog). A result whose journal
    write failed shows with "journal write failed" and keeps the same lock
    and **Check again**: it wins over the merged hide and the queued view,
    and no receipt goes out. Each Desk read writes the journal again; on
    success the lock ends and the receipt is posted. GitHub work runs on
    `githubDo`, whose result reaches the window whatever the server sign-in
    is then.
  - **Journal** (`internal/app/merge_journal.go`): `operations.json` in
    the config folder (mode 0600, no token) keeps each attempt: its origin
    (server, user, thread), its intent, its last step and result, and its
    Desk receipt. Each write goes to `operations.json.tmp`, is synced and
    renamed. Pocket writes the checkpoints "about to approve" and "about to
    merge" before each mutation, and a failed write sends nothing; no other
    step replaces a checkpoint before the result is saved, so a crash after
    a merge was sent reads as "merge may be pending" (unknown, locked).
    `operations.lock` (created with `O_EXCL` at start, removed at exit)
    keeps 1 Pocket on it;
    a held lock or a file that does not parse turns the buttons off until
    the owner clicks **Release the journal lock** or **Start a new journal**
    in Settings > GitHub. A file does not parse when it is not JSON, has an
    unknown version, or holds an op or receipt that is null, has an empty or
    unknown result or state, or repeats a result of its op (checked after a
    version 1 migration, in which an attempt with a result needs a receipt
    with a known state). Then Pocket writes the marker `operations.corrupt`
    (atomic, 0600): the name of a new copy `operations.json.corrupt-<time>`
    (a free name: an earlier copy is never written over) and the SHA-256
    of the corrupt bytes. It keeps the journal corrupt across restarts.
    Then Pocket copies the file there (a synced copy, renamed); the original
    stays. Each open with the marker leaves a copy whose digest matches
    alone; else it copies `operations.json` only when its digest matches the
    marker's, and checks the digest again; else the journal is broken ("the
    kept copy of the corrupt journal is missing"). **Start a new journal**
    checks the copy's digest, saves the new empty journal, then removes the
    marker: a crash or a failed removal between the 2 leaves the journal
    corrupt with the copy whole, and a second click is safe. A marker with
    no valid digest or safe name, or a marker or copy that cannot be
    written, leaves the journal broken, and no reset runs. Each atomic
    write also syncs the folder, so the steps reach the disk in order. At
    start each unfinished or unknown attempt is settled by reads only; a batch never resumes. An ended attempt whose
    receipts all settled is kept 7 days. The file is version 2 (a list of
    receipts per attempt); a version 1 file's single receipt is migrated.
  - **Receipts:** `pocket.Client.RecordMerge` posts each result to the
    Desk's `POST /api/x/desk/threads/:id/merges` as information only, and
    only while the window is signed in to the attempt's server and user.
    An attempt keeps 1 receipt per result (the Desk keys them
    `<op>:<result>`): a later result, such as merged after queued, adds its
    own and never replaces another's. A receipt that waits shows "not
    recorded on the Desk" and goes again with the same `op` at start and at
    each Desk read for 24 hours, then gives up; a 400 or 409 is rejected. A receipt never hides or undoes the GitHub result.
  - **Settings > GitHub:** a masked field per owner with **Check** (the
    token must sign in as `bketelsen` and read a ready repository of that
    owner; only then is it saved, so with no ready PR of that owner a typed
    token is not kept, and a kept one shows only its login and expiry) and
    **Forget**; the login, expiry and
    missing permission; the read-only preflight table (repository,
    readable, bypass, classic protection, missing permission or reason),
    whose failed repositories get no button; and Batches. Without a
    keyring (or with `POCKET_NO_KEYRING`) it says "Approve + merge needs the
    desktop keyring" and no card gets a button.
- **Threads:** the client's goroutines never touch UI state; they post
  functions to the window (`Window.Update`).


## Operational notes

- **Activity** (⌘J) logs the connection, every command Pocket sends, and any
  field the server sent in a shape Pocket does not read.
- The stream reconnects with a growing wait (the server's `retry`, doubling
  to 30 s, with jitter); a stream silent past 75 seconds (five of the server's 15 s pings)
  counts as dead. A rejected token, on the stream or any ordinary route, signs out and shows
  the sign-in screen. Route answers from a previous sign-in are ignored.
- Every stream hello refreshes the team and its lessons, and the artifacts
  if already loaded. Artifacts refresh on entry and once a minute while the
  place stays open; reads requested during a read run again afterward.
- Reading the Desk can take long: it sweeps GitHub first when nothing swept
  for 5 minutes. Pocket gives it 3 minutes and keeps the last Desk on screen
  when a read fails.
- macOS shows notifications only from a packaged app (`make app`,
  `make dev`).
- On Linux, `make install` installs the build of `make app` for the user: the
  executable with libghostty-vt beside it in `~/.local/lib/pocket`, a link in
  `~/.local/bin`, and a desktop entry and hicolor icons named
  `org.frostyard.Pocket`, the app ID `cmd/pocket` gives the windows, so that
  the desktop groups a window with its launcher.
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
  `POCKET_CONFIG_DIR` picks another). `server` is the one Pocket connects to;
  the pins, the seen marks and the last session are kept per account under
  `accounts` (that server's base URL and the user), because a session ID is its
  server's. Pocket turns the pins, seen marks and last session an older version
  kept for the whole file into the entry of the server they name, at that
  server's first hello, and never gives them to another server.
  `POCKET_NO_KEYRING=1` keeps the token in a file there instead of the keyring,
  for development.

Historical Numbers records fetch a thread’s detail when it is outside the live
Desk list, so older closures and their reports remain readable in the inspector.


## References

- Rationale: [ADR-0004](../adr/0004-build-a-native-client-of-pi-pockets-own-api.md), [ADR-0005](../adr/0005-organize-the-window-around-what-needs-the-owner.md), [ADR-0006](../adr/0006-filter-notifications-in-the-client.md), [ADR-0007](../adr/0007-render-terminal-history-before-attaching.md), [ADR-0008](../adr/0008-approve-and-merge-as-the-owner-from-the-desktop.md)
- Repository structure: [agent-ready repository](agent-ready-repository.md)
- Built in: [roadmap](../plans/roadmap.md)
