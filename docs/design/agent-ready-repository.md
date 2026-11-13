# Agent-ready repository

Living document. Rationale: [ADR-0001](../adr/0001-record-architecture-decisions.md), [ADR-0002](../adr/0002-agent-portable-instruction-surface.md), [ADR-0003](../adr/0003-verify-gate-risk-paths-and-corrections.md), [core ADR-0081](https://github.com/frostyard/core/blob/main/docs/adr/0081-store-each-correction-in-a-file-of-its-own.md).  
Contracts: [repository baseline spec](https://github.com/frostyard/core/blob/main/docs/specs/repository-baseline.md), [README spec](https://github.com/frostyard/core/blob/main/docs/specs/readme.md).

## Overview

This repository uses the structure of [bketelsen/agentic-template](https://github.com/bketelsen/agentic-template): a portable instruction surface, category-based documentation, reusable skills, a verification gate and a corrections loop, for work that LLM agents build and maintain. The structure is extracted from [bespoke](https://github.com/bketelsen/bespoke), where it has held up across coding agents. In the template itself, this structure is the whole content. This repository adds a Go module, the Pocket desktop app ([design](pocket.md)), and keeps this document up to date as its structure changes.

## Design

Agents succeed when the repository answers their questions before they ask:

- **One law, every agent.** `AGENTS.md` is the canonical instruction file; `CLAUDE.md`, `GEMINI.md`, `.cursorrules`, and `.github/copilot-instructions.md` are symlinks to it, so Claude Code, Copilot, Gemini, Cursor, and whatever comes next all read the same rules. Skills live once in `.agents/skills/` (`.claude/skills` is a symlink). No drift, ever.
- **Docs split by the question they answer.** `docs/adr/` (why — immutable), `docs/design/` (how — living), `docs/specs/` (what exactly — testable contracts), `docs/plans/` (when — phases with "Done when" outcomes). An agent that needs rationale, mechanism, contract, or sequence knows exactly where to look — and where to write.
- **Templates as enforcement.** Every category has a `TEMPLATE.md`; every new doc starts from it. Structure you don't have to think about is structure that stays consistent.
- **Mandatory cross-linking.** ADRs link the designs they shape; designs link their ADRs, specs, and roadmap phase; specs link back. A doc without its links is defined as incomplete, so the graph stays navigable from any entry point.
- **Live conventions only.** AGENTS.md's code-conventions section describes the code *as it is* — rules graduate in when the enforcing code lands. Agents following stale rules produce broken work; this structure makes stale rules a category error.
- **Skills over improvisation.** Multi-step procedures that repeat become `.agents/skills/<name>/SKILL.md` files with a frontmatter description agents can select on. Explaining a procedure twice means it should become a skill.
- **A verify gate protects the repository.** `make verify` checks the Go module (tidy, vet, gofmt, the pinned lint, MyGo's build-lifetime vet, every test) and the canonical surfaces, documentation index, risk paths, corrections inbox and vendored copies. The risk-path list gives every path a level, and the caller labels pull requests with it; the corrections inbox turns blocking reviews into guards.

**Layout.** Pocket's own parts are:

| Path | What it is |
| --- | --- |
| `cmd/pocket/` | The program: the window, keyring, notifications, badge |
| `internal/pocket/` | The client of a Pi Pocket server: API, event stream, view merging |
| `internal/app/` | The window's state and views, and their tests |
| `internal/config/` | The settings file and the credentials |
| `internal/icons/` | The icons Pocket draws itself |
| `resources/` | The app icon that `mygo build` packages |
| `scripts/pocket-demo.ts` | The scripted demo server of the live tests |
| `mygo.json` | The app's name, identifier, icon and version for `mygo build` |
| `go.mod`, `go.sum` | The module: MyGo, frostui and the `mygo` tool |
| `.golangci.yml` | The lint configuration |
| `.gitignore` | Local build output (`build/`) |

The parts that the template brings are:

| Path | What it is |
| --- | --- |
| `AGENTS.md` | Canonical agent instructions (the law) |
| `CLAUDE.md` → `AGENTS.md` | Symlink for Claude Code |
| `GEMINI.md` → `AGENTS.md` | Symlink for Gemini CLI |
| `.cursorrules` → `AGENTS.md` | Symlink for Cursor |
| `.github/copilot-instructions.md` → `AGENTS.md` | Symlink for Copilot |
| `.agents/skills/` | Canonical skills; `TEMPLATE/SKILL.md` copies for new skills |
| `.claude/skills` → `.agents/skills` | Symlink |
| `docs/README.md` | Taxonomy and index; every doc gets a line |
| `docs/org-adrs.md` | Core ADRs that bind this repository |
| `docs/adr/` | Immutable decisions; why; also `TEMPLATE.md` |
| `docs/design/` | Living architecture documents; how; also `TEMPLATE.md` |
| `docs/specs/` | Testable contracts; what; also `TEMPLATE.md` |
| `docs/plans/` | Phased plans; when; also `TEMPLATE.md` |
| `Makefile` | Verify, check and CI triad, and the Go and app targets |
| `mise.toml`, `mise.lock` | Tool pins (golangci-lint, Node); `mise install` provisions them |
| `LICENSE` | MIT license text |
| `SECURITY.md` | The support policy and where to report a vulnerability |
| `scripts/` | Template and corrections gates |
| `scripts/lib/risk-paths.mjs` | Pinned risk-path validator, matcher and levels |
| `scripts/check-template.mjs` | Template verification entry point |
| `scripts/check-corrections.mjs` | Pinned corrections gate: 1 file for each record |
| `test/` | Node.js test fixtures and gate tests |
| `.memory/` | Corrections inbox: 1 file for each record in `.memory/corrections/` |
| `.github/risk-paths.json` | High-risk repository paths, with a level for each |
| `.github/dependabot.yml` | Dependency update configuration |
| `.github/pull_request_template.md` | Risk tier and verification checklist |
| `.github/workflows/` | CI and PR triage caller workflows |

## Operational notes

`mise install` provisions the pinned golangci-lint and Node.js; Go comes from go.mod's `go` line. `make verify` needs Go, golangci-lint, Node.js 22 or later, `make`, and Pango and cairo for MyGo's Tester; the template gate itself uses no package manager, `package.json`, install step or third-party dependency. Symlinks require Linux or macOS, or `core.symlinks=true` on Windows. GitHub's web renderer shows a symlink as its target path; this is cosmetic only.

The verify gate checks canonical surfaces, the documentation index, risk paths, the corrections inbox and vendored copies. A pull request that touches `.github/risk-paths.json` gets 1 level label, and `risk:high` at L1 or L2; core takes the level from the matched globs, `default_level` and the org floors, and it never lowers a level label that the pull request already has. Blocking reviews become targeted tests or `AGENTS.md` rules with a corrections record file.

## References

- Rationale: [ADR-0001](../adr/0001-record-architecture-decisions.md), [ADR-0002](../adr/0002-agent-portable-instruction-surface.md), [ADR-0003](../adr/0003-verify-gate-risk-paths-and-corrections.md), [core ADR-0081](https://github.com/frostyard/core/blob/main/docs/adr/0081-store-each-correction-in-a-file-of-its-own.md)
- Contracts: [repository baseline spec](https://github.com/frostyard/core/blob/main/docs/specs/repository-baseline.md), [README spec](https://github.com/frostyard/core/blob/main/docs/specs/readme.md)
