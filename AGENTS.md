# Pocket

Pocket is a native desktop client of a Pi Pocket server and its
pi-pocket-team modules, in Go on MyGo's native UI and frostui, for an owner
who runs a team of agents and wants one window for what waits on them and
what is in motion. Start at [docs/design/pocket.md](docs/design/pocket.md),
then [docs/README.md](docs/README.md).

This file (`AGENTS.md`) is the CANONICAL agent instructions — `CLAUDE.md`,
`GEMINI.md`, `.cursorrules`, and `.github/copilot-instructions.md` are symlinks
to it, and `.claude/skills` symlinks to `.agents/skills/`
([ADR-0002](docs/adr/0002-agent-portable-instruction-surface.md)). Edit only
the canonical paths; keep content tool-agnostic.

## Build, test, run

- `mise install` — the pinned golangci-lint and Node (`mise.toml`, checksums
  in `mise.lock`). Go comes from go.mod's `go` line, the only Go pin.
- `make test` — every test, headless (`env -u DISPLAY -u WAYLAND_DISPLAY`,
  `CGO_ENABLED=0`). MyGo's Tester needs Pango and cairo, no display.
- `make verify` — the gate that leaves `git status` unchanged: `go mod tidy
  -diff`, `go vet`, `go tool mygo vet .`, the gofmt check, the pinned
  golangci-lint (as linux, darwin and windows), the template gate
  (`scripts/check-template.mjs` and `test/*.test.mjs`), then `make test`.
- `make check` — `make fmt`, then `make verify`. `make ci` — what CI runs:
  `make verify`, the corrections check against `BASE`
  (`make ci BASE=origin/main`), `make race` and `make cross`.
- `make run` starts the app; `make app` packages it (`go tool mygo build`:
  `Pocket.app` and a disk image on macOS); `make dev` runs a packaged
  development build.
- `make install` (Linux only) runs `make app`, then installs for the user:
  the app in `$(PREFIX)/lib/pocket` (`PREFIX` defaults to `~/.local`), a link
  to it in `$(PREFIX)/bin`, and in `$XDG_DATA_HOME` the hicolor icons and the
  desktop entry, each named after `mygo.json`'s `identifier`, which is the
  windows' app ID (`StartupWMClass`). `make uninstall` removes those files.
- `make snapshot` writes the views as PNGs in `build/snapshots`; `make live`
  runs the live tests against real servers (README).

## Skills (follow these for common tasks)

Step-by-step procedures live in [.agents/skills/](.agents/skills/); follow
them rather than improvising, whichever agent you are. Start a new one from
`.agents/skills/TEMPLATE/SKILL.md`.

- *(none yet — add the first one when a procedure repeats)*

## Code conventions (live — the code exists)

- **Layout.** `cmd/pocket` is the program (window, keyring, notifications,
  badge). `internal/pocket` is the server's client and holds no UI:
  `client.go`, `api.go`, `desk.go`, `extras.go`, `shift.go`, `memory.go` (routes),
  `running.go` (running board, background and schedule routes), `attachments.go` (upload and queue routes), `files.go` (changes, previews and downloads), `plans.go` (Desk plan contracts), `types.go` (wire types), `terminal.go` (shell transport), `stream.go` and `sse.go` (the
  event stream), `view.go` (`View.Apply`, the port of the web app's
  `mergeView`). `internal/app` is the window: `app.go` (state, events),
  `derived.go` (cached Inbox, session states and thread matches),
  `conn.go` (the link), `frame.go` (frame, commands, palette), `sidebar.go`,
  `inbox.go`, `board_actions.go` (board details and actions), `core_memory.go` (Pi Pocket lessons), `motion.go` (In motion, Shift board, Numbers), `numbers.go` (filters and records), `desk.go`,
  `desk_actions.go` (owner thread actions), `plans.go` (plan decisions and scope controls), `new_plan.go` (plan creation), `desk_extras.go` (supplementary lists),
  `transcript.go`, `session_tools.go` (find, fork, export and new context), `session_files.go` (native diffs, previews and confirmed reverts), `new_session.go` (server directory prompt), `composer.go`, `attachments.go` (file chips and queue controls), `upload_file_*.go` (platform file opens), `pages.go`, `session_details.go` (session
  identity and shift goal), `notify.go`, `settings.go`,
  `terminals.go` (the Terminal place, over `pocket.TermConn`),
  `terminal_replay.go` (isolated history rendering), `artifacts.go`,
  `running.go` (cross-session work), `background.go` (task output), `schedules.go` (scheduled messages), `timezone.go` (device IANA zone),
  `refs.go`, `desk_files.go` (read-only Desk Markdown), `browser_watch.go` (read-only browser frames), `wiki.go`, `teampage.go`,
  `signin.go`, `webnav.go` (where the artifact web view may go, and its cookie)
  and `webpane.go` (`ArtifactWeb`: the web view's sign-in, sign-out and native
  calls, in order, over the `WebNative` that `cmd/pocket` gives it).
  `internal/config` holds settings and credentials; `internal/icons` the
  icons Pocket draws.
- **UI thread.** `App` and everything in it belong to the UI thread. Work
  that waits on the network runs on a goroutine and hands its result back
  with `a.request(what, fn, done)` (or `a.requestWithin` for a slow route),
  which posts `done` to the UI thread and logs failures to Activity. It drops
  answers from a previous sign-in and signs out on a route 401, without
  calling the old completion. Stream retries keep the authenticated client. Never
  touch `App` from a goroutine.
- **Derived data.** Server events and request completions invalidate the Inbox,
  state and thread-match caches. A new direct mutation of their sources must
  call `invalidateDerived`; `setSessions` and `markSeen` already do. Cache
  pointers belong to the current source snapshots, never to a previous sign-in.
  The transcript's rows are built again only when the view's `Rev`, its entries
  or its order change: a live-only update refreshes each folded call's slot and
  approval and the live row in place, and keeps the search cache over the rows
  (`internal/app/transcript.go`).
- **MyGo's rules** (as frostui's): no `ui.Handle` made in a view; state lives
  in structs. `Element.Selected` recolors text for an accent fill, so a card
  that shows selection by its border uses `AccessSelected`.
- **Protocol.** Speak the web app's protocol: a bearer token (never a cookie
  too), `X-Pocket: 1` on every request, `p=5` on the stream
  ([ADR-0004](docs/adr/0004-build-a-native-client-of-pi-pockets-own-api.md)).
  Check a route or field against the server's source (Pi Pocket's
  `src/server/http/`, `projection.ts`, `room.ts`; pi-pocket-team's `team.ts`,
  `desk.ts`) before relying on it. `pocket.Decode` and `Client.Drift` keep the
  rest of an answer when one field has an unexpected shape: never make such a
  field fatal.
- **Sign-in.** Mask the link field. Require confirmation before sending a
  credential over public HTTP; only localhost and literal loopback/private
  addresses bypass that warning. Never resolve DNS to waive it.
- **Desk asks.** Keep a thread's asks in server filing order. Drafts and
  notification identity use `askKey` (id and generation), so reopening a
  plan ask never reuses an earlier answer draft.
- **Notifications** go through `deliver` in `internal/app/notify.go`, never
  straight to `Options.Notify`
  ([ADR-0006](docs/adr/0006-filter-notifications-in-the-client.md)).
- **Keys a terminal needs.** A command whose Ctrl key a shell uses (history,
  clear, cursor movement, tmux's prefix, readline's Alt keys) goes in `shellKeys`
  (`internal/app/terminals.go`), which stands it aside while a terminal has
  the focus, except on macOS, where it is a ⌘ key.
- **Terminal replay.** Set `TermConn.Replay` before giving a connection to
  the emulator. Render history through the isolated reply sink; never forward
  raw history or use a timing flag to suppress asynchronously queued replies
  ([ADR-0007](docs/adr/0007-render-terminal-history-before-attaching.md)).
- **Dependencies across repositories.** Merge the dependency PR first, then pin a
  release or commit reachable from its default branch before merging this app.
  An open dependency PR may be used for testing, never as the final merge pin.
- **Every platform.** The app builds with `CGO_ENABLED=0` for darwin, linux
  and windows (`make cross`). Write keys for people with `keys()` and
  `keyName()` (⌘ and ⌥ on macOS); bind them with `ui.Cmd`, which is Cmd there
  and Ctrl elsewhere.
- **Look at views.** View tests render with `ui.NewTesterWith`; set
  `POCKET_SNAPSHOT_DIR` (or run `make snapshot`) and look at what a view
  change draws before you call it done. The live tests skip unless their
  servers are set.
- Run `make verify` before you call any change done. CI job `verify` runs
  `make ci`, which also checks that `.memory/corrections.jsonl` is append-only
  against the PR base; run `make ci BASE=origin/main` for the same check locally.
- `mise.toml` and `mise.lock` pin every tool the gate runs (ADR-0043 in core);
  run `mise install` to get those versions. Change a pin only with `mise lock`,
  in a commit of its own.

**Test reliability.** No sleeps for timing (wait on a condition or inject a
clock). No network and no wall clock in unit tests (inject them: `Options.Now`,
`Options.After`). A flaky test gets an issue, then a fix or a quarantine (skip
with the issue link) in the same PR. Never retry a job or a test until it is
green.

**Blocker to guard.** When a review (agent cross-review, owner review, or a
human) finds a blocking point, the PR that fixes it also adds a guard: a
targeted test that fails without the fix (preferred), or an `AGENTS.md` rule
when no test can catch it. The same PR appends 1 line to
`.memory/corrections.jsonl` (`evidence`: the PR and the review;
`promoted_to`: the test or the rule). See `.memory/README.md`.

**Risk paths.** `.github/risk-paths.json` is a level list (schema version 2):
every glob carries a level, and a PR that touches one gets that level label from
`.github/workflows/risk-paths.yml` (`level:L1` read every line, `level:L2` read
the diff, `level:L3` skim, `level:L4` title and CI), plus `risk:high` at L1 and
L2, which declares tier 3. Core computes the level from the matched globs, the
`default_level` and the org floors, and it never lowers a level label that the
PR already has. The same caller removes `ready-for-human` when the head moves
after the label. Add a path when you add a file of that kind (signing, auth,
publish, secrets, destructive operations, CI), with a `why` that names its
category. Here the token's storage and use (`internal/config/secret.go`,
`internal/pocket/client.go`, `cmd/pocket/main.go`), go.mod, go.sum and
`.golangci.yml` are L2, and `mygo.json` and the tool pins are L1. The gate
requires schema version 2.

**Vendored files.** `scripts/lib/risk-paths.mjs` and
`scripts/check-corrections.mjs` are copies from frostyard/core; never edit them
by hand (`make verify` checks their hashes).

## Repository boundary

- **No secrets.** No token, sign-in link, server address of the owner's or
  personal data in code, tests, fixtures, docs or snapshots. The live tests
  read their servers and tokens from the environment; the demo server makes
  its own. Examples use `example.org` and `192.0.2.0/24`.
- **No server changes here.** A feature that needs Pi Pocket or a team module
  to change is a change to that repository first.
- **Releases.** None yet. The maintainer (Brian) cuts them; agents never
  create or push a tag. The first release adds `.svu.yml` and `make bump`
  ([roadmap](docs/plans/roadmap.md)).
- **License.** MIT. The app's icons are drawn here; the kit's are Lucide's
  (frostui's `NOTICE`).

## Documentation rules (enforced)

Docs live in `docs/` in four categories. **Every new doc starts from its
category's `TEMPLATE.md`** and follows its structure:

- `docs/adr/` — why we decided. Immutable once Accepted; reversals are new
  ADRs that mark the old one Superseded. This includes ADRs 0001 to 0003 that
  came with the template. A new org-wide rule goes in a core ADR listed in
  `docs/org-adrs.md`; a new project rule goes in a new ADR.
- `docs/design/` — how it fits together. Living; updated in place to match
  reality. `docs/design/pocket.md` is the entry point.
- `docs/specs/` — exact contracts. Change only alongside implementing code.
- `docs/plans/` — order of work. Phases with "Done when" outcomes.

After a change to the app, update `docs/design/pocket.md`, `README.md` and
this file where they describe it; check a current-state claim against the
source, never against an old document.

### Cross-linking is mandatory

A doc without its required links is incomplete — do not finish a docs change
until they exist, in both directions:

- **ADR** → links every design doc/spec it shapes, and prior ADRs it builds on.
- **Design doc** → links the ADR(s) providing its rationale, the spec(s)
  pinning its contracts, and the roadmap phase that builds it.
- **Spec** → links its motivating ADR(s) and the design doc showing where it
  fits.
- **Plan** → every phase links the design docs/specs it implements; resolved
  open questions become ADRs.

When you touch a doc, verify its links still hold (targets exist, section
anchors valid) and add the back-links on the targets. Use relative paths.

### Housekeeping

- New doc ⇒ add a line to the index in [docs/README.md](docs/README.md).
- New significant decision ⇒ new ADR *first*, then update the affected design
  docs/specs in the same change.
- Convert relative dates ("next weekend") to absolute dates in all docs.
