<!-- readme-kind: app -->
<img src="resources/icon.png" alt="Pocket icon" width="112" align="right">

# Pocket

**A desktop home for your Pi Pocket agents.**

Pocket is a desktop app for people who run a team of agents on a [Pi Pocket](https://github.com/TannerMidd/pi-pocket) server, that puts everything waiting on you, and everything in motion, in one window. Where Pi Pocket's web app shows one session at a time, Pocket is organized around your attention, with [pi-pocket-team](https://github.com/bketelsen/pi-pocket-team)'s Desk built in.

**Status:** pre-release. It signs in to a server, streams sessions, and runs the Inbox, In motion, the team, the wiki and artifacts.

**Documentation:** [docs/README.md](docs/README.md)

## What it does

- **Inbox.** Calls to allow, Pi's questions, the Desk's decisions and PRs ready to merge, in one queue, with decisions grouped by thread in filing order, each answered on its card, keyboard first. Derived queues and thread matches are reused between data changes. The Details pane keeps its heading below the window controls. Desk report links open in a native reader. Review pending lessons from both named agents and Pi Pocket memory. [Design](docs/design/pocket.md#places)
- **In motion.** Every thread at work, grouped by who moves it next, with what each agent does right now. Park, wake, watch, attach or drop a thread in its detail pane; due checks wait in the Inbox. Fresh activity, queued issues, other PRs and completed plans stay visible. Running now lists live tasks and schedules across sessions, with Stop controls. [Design](docs/design/pocket.md#places)
- **Sessions, native.** Choose the server working directory before creating a session. Inspect background output and stop its task; create or cancel scheduled messages, and edit or pause them on supporting servers. Attach files by picker, drop or image/file paste; edit or withdraw queued messages and resume held messages. Replies stream in as Markdown, tool calls fold away, approvals dock above the composer, `@agent` starts a task, and a read-only browser watch shows the agent's page. Find searches loaded messages and folded tools; the session menu can fork, export Markdown or start a new context. The inspector chooses or clears the session's team identity and shows its shift goal and progress. [Design](docs/design/pocket.md#a-session)
- **The team.** Lessons to approve, a page for each named agent, the shift board with supervisor choice, row details and controls, and the Desk's numbers with range and repository/owner filters and record drill-down. Daily closures show a count scale and each day's value on hover or keyboard focus. [Design](docs/design/pocket.md#places)
- **Wiki and artifacts.** The homelab wiki and every session's pages, documents and pictures, read in the app. On systems without a web view, pages open in your browser using its own sign-in. Choose the latest or an older artifact version. The artifact list refreshes while open; reconnecting refreshes the team and its lessons. [Design](docs/design/pocket.md#places)
- **Terminal.** Server shells retain their screen on attachment without replaying historical query replies into the live shell. [Design](docs/design/pocket.md#the-protocol)
- **Calm notifications.** By kind, bursts gathered into one, do not disturb and quiet hours, without changing what your phone gets. [Design](docs/design/pocket.md#notifications-and-settings)
- **Zoom.** Everything in the window larger or smaller, 50% to 300%, from Settings or ⌘+, ⌘- and ⌘0 (Ctrl on Linux and Windows). [Design](docs/design/pocket.md#notifications-and-settings)

## Requirements

Go 1.27, and a Pi Pocket server you can reach, with its owner's sign-in link or an invite. On Linux, GTK 3, libepoxy and Pango; MyGo needs no CGO.

## Install

Pocket has no release yet: build it from source. On a Mac, build it on the Mac that runs it (the build signs it ad hoc), then open it:

```sh
git clone https://github.com/frostyard/pocket.git
cd pocket
make app
open build/mygo/darwin-*/Pocket.app
```

On Linux, `make run` builds and starts it. Paste the sign-in link your Pi Pocket launcher printed (`https://example.org/login?token=…`) or an invite link; Pocket masks the pasted link and keeps the token in the desktop's keyring. Public HTTP servers require confirmation because the token travels in clear text.

## Develop

```sh
mise install   # the pinned tools
make ci        # what CI runs
make run       # build and start the app
```

The live tests and the demo server are in [docs/design/pocket.md](docs/design/pocket.md#operational-notes).

## Where to go next

- [AGENTS.md](AGENTS.md): the rules for agents and contributors.
- [docs/README.md](docs/README.md): the decisions, designs, specs and plans.
- [SECURITY.md](SECURITY.md): report a vulnerability privately.
- [LICENSE](LICENSE): MIT.
