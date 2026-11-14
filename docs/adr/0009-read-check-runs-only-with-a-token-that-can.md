# 0009 — Read check runs only with a token that can

- **Status:** Accepted
- **Date:** 2026-11-14

## Context

[ADR-0008](0008-approve-and-merge-as-the-owner-from-the-desktop.md) asks the
owner for 2 fine-grained personal access tokens with "Checks: read". That
permission came from the docs of the REST check-run endpoints, not from a
read with a fine-grained token: the live reads of ADR-0008 used the machine
account's OAuth token.

On 2026-11-14 the owner made a fine-grained token for `bketelsen` and opened
a ready PR of a private repository. Pocket showed only "Resource not
accessible by personal access token". The facts:

- A fine-grained token has no Checks permission. The token form has none,
  and GitHub's list of the endpoints that a fine-grained token can call
  ([permissions required for fine-grained personal access tokens](https://docs.github.com/en/rest/authentication/permissions-required-for-fine-grained-personal-access-tokens))
  has no "Checks" section and no `check-runs` or `check-suites` endpoint.
  GitHub support: "it isn't possible to assign Checks permissions to a
  Fine-grained PAT — only GitHub Apps can access this API"
  ([community#129512](https://github.com/orgs/community/discussions/129512),
  also [community#179545](https://github.com/orgs/community/discussions/179545)).
- The GraphQL `statusCheckRollup` gives the same refusal on each `CheckRun`
  node of a private repository: HTTP 200, the node null, and an `errors` entry
  of type `FORBIDDEN` with the node's path
  ([cli/cli#12597](https://github.com/cli/cli/issues/12597)). A
  `StatusContext` node stays readable with "Commit statuses: read". A head
  with no checks gives a null rollup and no error.
- The PR query's gate 6 needs each check run's name and its app
  (`checkSuite.app.databaseId`), to compare with the rule's
  `integration_id`. Gate 7 needs every check run of the head. With a
  fine-grained token, Pocket can read neither.
- "Actions: read" lists the workflow runs and jobs of a head
  (`GET /repos/{o}/{r}/actions/runs`, `.../runs/{id}/jobs`). It shows only
  GitHub Actions' own jobs, never another app's check runs.
- A classic token with the `repo` scope reads check runs
  ([check runs](https://docs.github.com/en/rest/checks/runs#list-check-runs-for-a-git-reference)),
  and so does a GitHub App's user token with "Checks: read".
- Pocket's error said neither the call nor the field that GitHub refused.

## Decision

**The gates stay.** Gates 6 and 7 of ADR-0008 do not change. A required
check with an `integration_id` is met only by a check run whose app Pocket
read. A check run that Pocket cannot read is never absent, never green, and
never replaced by a status or an Actions job.

**A named refusal.** When the PR query's `errors` hold a `FORBIDDEN` (or "not
accessible") entry under `statusCheckRollup`, the read fails with
`ErrChecksUnreadable` ("this token cannot read the PR's check runs"), which is
also `ErrIncomplete`. As every failed read, it fails closed: the card gets no
button, the preflight row shows the reason, and an attempt stops before its
next mutation.

**Errors name the call.** Every GitHub error that Pocket shows starts with the
call: `GET <path>` (without its query string) or `GraphQL <operation>`. A 403
or 404 adds `X-Accepted-GitHub-Permissions` when GitHub sends it. A GraphQL
`errors` list shows each entry's type and path (`FORBIDDEN at
repository.pullRequest.commits.nodes.0.commit.statusCheckRollup.contexts.nodes.0:
…`), at most 3, then the count of the rest. The redaction of the token covers
all of it.

**The permissions, corrected.** This table replaces the PR query's row of
ADR-0008's table and its list of permissions. The other rows stay.

| Call | Use | Fine-grained permission | Source |
| --- | --- | --- | --- |
| GraphQL query `PocketPR` | The PR's facts, checks and reviews; the repository's methods | Pull requests: read; Contents: read; Commit statuses: read; Metadata: read. Its `CheckRun` nodes: no fine-grained permission exists (GitHub Apps and classic tokens only) | the docs above; GitHub support; cli/cli#12597 |

A fine-grained token has: Pull requests: read and write; Contents: read and
write; Workflows: read and write; Commit statuses: read; Metadata: read.
With it, Approve + merge works only on a PR whose head has no check runs
(no checks, or commit statuses only). The way Pocket reads check runs for
the other PRs (another kind of token, or a narrower read) is a decision for
a later ADR; until then those PRs get the named refusal.

## Consequences

- The owner's next failure names the call and the field, so a wrong
  permission is found from Pocket's own text.
- With fine-grained tokens, most ready PRs get no button: every repository
  with GitHub Actions checks has check runs. Approve + merge stays the
  owner's manual path on GitHub for them until the later ADR.
- No gate is weaker. A token that can read check runs passes the same gates
  as before.
- ADR-0008's table is wrong in 1 row. Its status line points here; its text
  stays, as [ADR-0001](0001-record-architecture-decisions.md) asks.

## Alternatives considered

- **Meet a required check by an Actions job, or by a status of the same
  name:** an Actions job proves GitHub Actions as its app, but not that no
  other app's check run fails (gate 7); a status proves no app at all. Each
  weakens a gate unless a later ADR shows how gate 7 still holds.
- **Read the rest of the answer and drop the unreadable check runs:** a
  failing or pending check run would pass gate 7. Refused.
- **Treat a required check with an `integration_id` as met when GitHub's
  merge state is clean:** the merge state is BLOCKED before the approval, so
  it proves nothing at the card. Refused.
- **Pick the other token now (a classic token, an Actions read path, or a
  GitHub App):** each trades scope, setup and code; the owner chooses it.

## References

- Shapes: [design: Pocket](../design/pocket.md#the-protocol),
  [roadmap](../plans/roadmap.md#phase-5--polish-from-daily-use)
- Builds on: [ADR-0008](0008-approve-and-merge-as-the-owner-from-the-desktop.md)
- Code: `internal/github/client.go` (`ErrChecksUnreadable`, the call in each
  error), `internal/github/query.go` (the refusal)
- GitHub: [fine-grained token permissions](https://docs.github.com/en/rest/authentication/permissions-required-for-fine-grained-personal-access-tokens),
  [community#129512](https://github.com/orgs/community/discussions/129512),
  [cli/cli#12597](https://github.com/cli/cli/issues/12597)
