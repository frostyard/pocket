<!-- readme-kind: app -->
<img src="resources/icon.png" alt="Pocket icon" width="112" align="right">

# Pocket

**A desktop home for your Pi Pocket agents.**

Pocket is a desktop app for people who run a team of agents on a [Pi Pocket](https://github.com/TannerMidd/pi-pocket) server, that puts everything waiting on you, and everything in motion, in one window. Where Pi Pocket's web app shows one session at a time, Pocket is organized around your attention, with [pi-pocket-team](https://github.com/bketelsen/pi-pocket-team)'s Desk built in.

**Status:** pre-release. It signs in to a server, streams sessions, and runs the Inbox, In motion, the team, the wiki and artifacts.

**Documentation:** [docs/README.md](docs/README.md)

## What it does

- **Inbox.** Calls to allow, Pi's questions, the Desk's decisions and PRs ready to merge, in one queue, with decisions grouped by thread in filing order, each answered on its card, keyboard first. Desk report links open in a native reader. [Design](docs/design/pocket.md#places)
- **In motion.** Every thread at work, grouped by who moves it next, with what each agent does right now. [Design](docs/design/pocket.md#places)
- **Sessions, native.** Replies stream in as Markdown, tool calls fold away, approvals dock above the composer, `@agent` starts a task. [Design](docs/design/pocket.md#a-session)
- **The team.** Lessons to approve, a page for each named agent, the shift board and the Desk's numbers. [Design](docs/design/pocket.md#places)
- **Wiki and artifacts.** The homelab wiki and every session's pages, documents and pictures, read in the app. On systems without a web view, pages open in your browser using its own sign-in. [Design](docs/design/pocket.md#places)
- **Calm notifications.** By kind, bursts gathered into one, do not disturb and quiet hours, without changing what your phone gets. [Design](docs/design/pocket.md#notifications-and-settings)

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

On Linux, `make run` builds and starts it. Paste the sign-in link your Pi Pocket launcher printed (`https://example.org/login?token=…`) or an invite link; Pocket keeps the token in the desktop's keyring.

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
