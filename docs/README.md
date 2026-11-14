# Documentation

Docs are split by the question they answer:

| Directory | Question | Contents |
| --- | --- | --- |
| [adr/](adr/) | **Why** did we choose this? | Architecture Decision Records — immutable once accepted; superseded, never edited |
| [design/](design/) | **How** does it fit together? | Living documents describing the current architecture |
| [specs/](specs/) | **What exactly** is the contract? | Precise, testable interface definitions |
| [plans/](plans/) | **When/in what order** do we build? | Roadmaps and phase plans; updated as work lands |

## Index

### Decisions (ADRs)

- [0001 — Record architecture decisions](adr/0001-record-architecture-decisions.md)
- [0002 — Agent-portable instruction surface](adr/0002-agent-portable-instruction-surface.md)
- [0003 — Verify gate, risk paths and corrections](adr/0003-verify-gate-risk-paths-and-corrections.md)
- [0004 — Build a native client of Pi Pocket's own API](adr/0004-build-a-native-client-of-pi-pockets-own-api.md)
- [0005 — Organize the window around what needs the owner](adr/0005-organize-the-window-around-what-needs-the-owner.md)
- [0006 — Filter notifications in the client](adr/0006-filter-notifications-in-the-client.md)
- [0007 — Render terminal history before attaching](adr/0007-render-terminal-history-before-attaching.md)
- [0008 — Approve and merge as the owner from the desktop](adr/0008-approve-and-merge-as-the-owner-from-the-desktop.md)
- [0009 — Read check runs only with a token that can](adr/0009-read-check-runs-only-with-a-token-that-can.md)

### Org-wide decisions

- [Org-wide decisions (frostyard/core ADRs)](org-adrs.md)

### Guides

- [After you create the repository on GitHub](new-repository.md)

### Design

- [Pocket](design/pocket.md) — the app: its places, a session, notifications, the protocol
- [Agent-ready repository](design/agent-ready-repository.md)

### Specs

*(none yet)*

### Plans

- [Roadmap](plans/roadmap.md)

## Conventions

- **New docs start from their category's `TEMPLATE.md`** (in each directory).
- New decision → new ADR with the next number; if it reverses an old one, mark
  the old one `Superseded by NNNN` rather than editing it.
- Design docs are updated in place to always reflect reality.
- Specs change only alongside the code that implements them.
- Cross-links between categories are mandatory in both directions — see the
  documentation rules in [AGENTS.md](../AGENTS.md) (CLAUDE.md/GEMINI.md are
  symlinks to it, ADR-0002).
- Adding a doc means adding it to the index above.
