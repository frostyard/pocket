# 0008 — Approve and merge as the owner from the desktop

- **Status:** Accepted
- **Date:** 2026-11-14

## Context

The owner merges most of the team's pull requests by hand: on 2026-11-13,
about 240 PRs that the team's machine account opened and 35 Dependabot PRs,
at about 5 clicks each on GitHub. The Desk's "Ready to merge" list
(`GET /api/x/desk/threads`, `DeskView.Ready`) names each PR, and Pocket's
Inbox shows it as a card with "Review on GitHub". Pocket has no GitHub
access ([ADR-0004](0004-build-a-native-client-of-pi-pockets-own-api.md)).

Constraints:

- The machine account `frostyard-machines` opens these PRs. On 1 PR it may
  approve or merge, never both, and never approve its own PR. The owner
  (`bketelsen`) doing both steps is allowed: he is the human.
- The rulesets on the default branch of frostyard/pocket,
  bketelsen/pi-pocket-team, frostyard/core and frostyard/nsl require 1
  approval, `dismiss_stale_reviews_on_push` and `require_last_push_approval`.
  frostyard/core also has a merge queue (method `SQUASH`) and strict
  required checks. bketelsen/pi-pocket-config has no ruleset. Auto-merge is
  off in every repository read, and each allows merge, squash and rebase.
- Never an admin merge or a ruleset bypass, and never a weaker merge
  requirement. A PR with the label `risk:high` must be read by the owner
  before it merges.
- The web Desk's JavaScript comes from the team VM, where agents edit
  `web/**` and the drop-ins live, with no review. Any token that such a page
  keeps can be read by a script that the VM serves on that origin. Pocket's
  code changes only by a reviewed PR and a build that the owner installs.
- The owner's GitHub token must stay out of reach of the team VM.

## Decision

**Where.** Pocket, on the owner's desktop, gets **Approve + merge** on the
Inbox's ready cards. The web Desk keeps its link to GitHub and gets no
button.

**Identity and tokens.**

- The owner makes 2 fine-grained personal access tokens, 1 for each resource
  owner (`bketelsen`, `frostyard`), with the permissions of the table below.
- Pocket keeps them in the desktop keyring only (service
  `org.frostyard.Pocket`, keys `github:bketelsen` and `github:frostyard`),
  through a type of its own with no file fallback. With no working keyring
  (or `POCKET_NO_KEYRING`), the button stays off.
- The only accepted login is the constant `bketelsen` in `internal/github`.
  A token whose login is another one (`frostyard-machines` too) is refused
  and not saved. Each action reads `viewer { login }` first.
- The token goes only to `https://api.github.com`. The HTTP client refuses
  every redirect. The token never goes to the Pi Pocket server, a log,
  Activity, the operation journal or a snapshot; error texts pass a
  redaction.
- Only a click in Pocket's own window starts a GitHub mutation. Pocket
  builds each call from the Desk row's `ref`, parsed with
  `^[A-Za-z0-9-]+/[A-Za-z0-9._-]+#[0-9]+$`, and never from its `url`.

**No bypass, read and not assumed.** Before a card shows the button, and
before each mutation, Pocket reads with the owner's token: the rules that
apply to the base branch, each of their rulesets' `current_user_can_bypass`
(each must be `never`), and the branch's classic protection
(`protection.enabled` must be false). With no bypass, a merge or an enqueue
that GitHub accepts met every rule.

**Gates.** 1 GraphQL query per PR (the text is
[`internal/github/testdata/pr-query.graphql`](../../internal/github/testdata/pr-query.graphql))
gives the facts. The PR must be OPEN, not a draft, not authored by the
viewer, labelled `ready-for-human`, MERGEABLE, with every required check of
the rules present (a check with an `integration_id` is met only by a
CheckRun of that app), every check complete and SUCCESS, NEUTRAL or SKIPPED,
no CHANGES_REQUESTED review, an allowed `mergeStateStatus`, and an allowed
merge method. Truncated lists, a 200 answer with `errors`, or a null in a
field that may not be null: refuse.

**The head is bound.** The confirm click freezes an intent: the PR, its base
branch, the shown head (40 characters), the method, the queue flag, the mode
and the `risk:high` flag. Before each mutation Pocket reads again and checks
the gates and the intent. The approval is
`addPullRequestReview(pullRequestId, commitOID: <shown head>, event: APPROVE)`.
After a new read that sees the owner's approval at the shown head, the merge is
`mergePullRequest(pullRequestId, expectedHeadOid: <shown head>, mergeMethod)`,
or `enqueuePullRequest(pullRequestId, expectedHeadOid: <shown head>)` on a
merge-queue base. These 3 mutations, with exactly these inputs, are the only
ones Pocket can send: a test holds the allowlist.

**Merge method.** GitHub's own default for the owner in that repository,
`repository.viewerDefaultMergeMethod`, when the repository allows it; a
merge-queue base uses the queue. The confirm dialog shows the method.

**Batch.** Up to 25 eligible cards, worked 1 at a time in Inbox order, each
with its own reads; a refusal stops only that PR. A 401 stops that owner's
PRs that have not started; a rate limit stops the batch. Batches are off
until the owner turns them on in Settings > GitHub. A single PR takes 2
clicks (**Approve + merge**, **Confirm**); a batch takes 3.

**risk:high.** Never in a batch. The card shows **Open diff**
(`https://github.com/<owner>/<name>/compare/<baseRefOid>...<headRefOid>`);
once the opener reports no error, **Approve + merge** turns on for that
head only, and the confirm button names the head.

**Results and records.** Each attempt has an operation id and ends as
merged, queued, refused, approved but not merged, failed or unknown. A
mutation with no answer is reconciled by reads only, and Pocket never claims
a merge by another actor or at another head. An operation journal in
Pocket's config folder (atomic writes, no token) supports recovery after a
crash; Pocket writes each step before its mutation and sends nothing when
that write fails. Pocket posts each result to the Desk's owner route
`POST /api/x/desk/threads/:id/merges` as information only. GitHub keeps the
record of the approval and the merge, both by `bketelsen`.

### GitHub calls and their permissions

Each call, the fine-grained permission it needs, and where the fact comes
from. "Live" means read on 2026-11-14 with the machine account's token (an
OAuth token with the scopes `repo`, `read:org`, `workflow` and `gist`, not a
fine-grained PAT); its answers are in
[`internal/github/testdata/`](../../internal/github/testdata/README.md).
"Docs" means only GitHub's documentation says it.

| Call | Use | Fine-grained permission | Source |
| --- | --- | --- | --- |
| `GET /user` ([docs](https://docs.github.com/en/rest/users/users#get-the-authenticated-user)) | Check: the token's login | none | docs |
| `GET /repos/{o}/{r}` ([docs](https://docs.github.com/en/rest/repos/repos#get-a-repository)) | Check: the token reads a ready repository | Metadata: read | docs |
| `GET /repos/{o}/{r}/rules/branches/{branch}` ([docs](https://docs.github.com/en/rest/repos/rules#get-rules-for-a-branch)) | Rules on the base, `ruleset_id`, required checks, merge queue | Metadata: read | live answer; permission: docs |
| `GET /repos/{o}/{r}/rulesets/{id}` ([docs](https://docs.github.com/en/rest/repos/rules#get-a-repository-ruleset)) | `current_user_can_bypass`, also for an organization ruleset | Metadata: read | live answer; permission: docs |
| `GET /repos/{o}/{r}/branches/{branch}` ([docs](https://docs.github.com/en/rest/branches/branches#get-a-branch)) | Classic protection: `protection.enabled` | Contents: read | live answer; permission: docs |
| GraphQL query `PocketPR` ([docs](https://docs.github.com/en/graphql/reference/objects#pullrequest)) | The PR's facts, checks and reviews; the repository's methods | Pull requests: read; Contents: read; Checks: read; Commit statuses: read; Metadata: read | live answer; permissions: docs of the REST equivalents ([PR](https://docs.github.com/en/rest/pulls/pulls#get-a-pull-request), [check runs](https://docs.github.com/en/rest/checks/runs#list-check-runs-for-a-git-reference), [statuses](https://docs.github.com/en/rest/commits/statuses#get-the-combined-status-for-a-specific-reference)) |
| `addPullRequestReview` ([docs](https://docs.github.com/en/graphql/reference/mutations#addpullrequestreview)) | Approve at the shown head | Pull requests: write | inputs: live schema; permission: docs of [Create a review](https://docs.github.com/en/rest/pulls/reviews#create-a-review-for-a-pull-request) |
| `mergePullRequest` ([docs](https://docs.github.com/en/graphql/reference/mutations#mergepullrequest)) | Merge at the shown head | Contents: write; Workflows: write when the PR changes `.github/workflows/` | inputs: live schema; Contents: docs of [Merge a pull request](https://docs.github.com/en/rest/pulls/pulls#merge-a-pull-request); Workflows: [docs](https://docs.github.com/en/rest/authentication/permissions-required-for-fine-grained-personal-access-tokens#repository-permissions-for-workflows), not confirmed for a merge |
| `enqueuePullRequest` ([docs](https://docs.github.com/en/graphql/reference/mutations#enqueuepullrequest)) | Enqueue at the shown head on a merge-queue base | Contents: write (expected; no doc names it) | inputs: live schema; permission: not documented |

**Administration: read is not needed.** The rules, the rulesets and the
branch need only Metadata: read and Contents: read. The token's
permissions are therefore: Pull requests: read and write; Contents: read
and write; Workflows: read and write; Checks: read; Commit statuses: read;
Metadata: read.

### Facts read live (machine account)

- The rules, rulesets and branch answers of frostyard/core (merge queue) and
  bketelsen/pi-pocket-team (no queue), and the PR query for merged PRs of
  frostyard/core, bketelsen/pi-pocket-team and bketelsen/pi-pocket-config.
- The PR query for an open PR of bketelsen/pi-pocket-team before its
  approval: `mergeable` MERGEABLE, `mergeStateStatus` BLOCKED,
  `reviewDecision` REVIEW_REQUIRED, `viewerLatestReview` null. A required
  check that has not run yet is absent from `statusCheckRollup`, and a check
  in progress has `status` IN_PROGRESS and `conclusion` null: a check that
  is not complete, which the gates refuse, not an incomplete answer.
- `GET /repos/{o}/{r}/rulesets/{id}` answers for an organization ruleset
  (frostyard's `default branch: humans merge`) through the repository path,
  with `source_type: Organization`.
- `current_user_can_bypass` is `never` for the machine account on all 4
  rulesets read. `bypass_actors` is absent from the answer: GitHub gives it
  only to a caller with write access to the ruleset.
- `branches/{base}` says `protected: true` where only rulesets apply, with
  `protection.enabled: false`. Pocket must read `protection.enabled`, never
  `protected`.
- Required checks carry `integration_id` 15368 (GitHub Actions) in both
  repositories. The checks of a head can repeat a name (each run of
  `pr-triage` is its own check run). In the last 15 merged PRs of
  frostyard/core, bketelsen/pi-pocket-team and frostyard/pocket, no check of
  the merged head was other than SUCCESS, NEUTRAL or SKIPPED.
- A PR that frostyard/core's merge queue merged shows `mergedBy` as the
  owner who enqueued it.
- `statusCheckRollup` is null on a head with no checks
  (bketelsen/pi-pocket-config). `reviewDecision` is APPROVED there after an
  approval, although no rule asks for one.
- A query for a missing PR answers HTTP 200 with partial `data`
  (`pullRequest: null`) and an `errors` list (`NOT_FOUND`).
- Schema (introspection): `expectedHeadOid` and `commitOID` are optional
  ("if omitted, no check is performed"), so Pocket always sends them;
  `mergeMethod` defaults to MERGE, so Pocket always sends it;
  `enqueuePullRequest` also has `jump`, which Pocket never sends. No
  mutation has an admin or bypass input. `MergeStateStatus.DRAFT` is
  deprecated; a value Pocket does not know refuses.
- frostyard/core's rules also carry `required_review_thread_resolution:
  true`, and all rulesets read carry
  `require_extra_approval_for_unattributed_changes: true`. Either can keep a
  PR BLOCKED after the owner's approval: the check after the approval then
  refuses the merge.

### Facts that only GitHub's docs give, for the preflight and the first use

- The permission of each documented call (the table).
- That a ruleset binds the owner too (no bypass for an admin who is not on
  the bypass list), so `never` holds for `bketelsen`: the preflight reads it
  with his token.
- That a fine-grained PAT for `frostyard` may need the organization's
  approval
  ([docs](https://docs.github.com/en/organizations/managing-programmatic-access-to-your-organization/setting-a-personal-access-token-policy-for-your-organization)).

### Facts that neither the live reads nor the docs give

The owner's token or first use must show these:

- That `current_user_can_bypass` comes with a token that has only Metadata:
  read. A missing `current_user_can_bypass` counts as a failure, never as
  `never`.
- The answers seen with the owner's token: `viewerDidAuthor` false,
  `viewerLatestReview`, his `viewerDefaultMergeMethod` (the machine account's
  is MERGE everywhere), and `mergeable` and `mergeStateStatus` before and
  after an approval.
- That `enqueuePullRequest` needs Contents: write (no doc names its
  permission), and that a merge of a PR that changes `.github/workflows/`
  needs Workflows: write.
- That each token's expiry comes in the header
  `github-authentication-token-expiration` (no doc page found; Pocket shows
  "unknown" without it).

The read-only preflight in Settings > GitHub reads the rules, rulesets,
branch and PR query with the owner's token for each ready repository, and
the owner's first approve + merge of a low-risk PR confirms the mutations.

## Consequences

- The owner approves and merges a ready PR in 2 clicks, and up to 25 in 3,
  as himself. GitHub shows both steps by `bketelsen`.
- The token is on the owner's desktop only. A Pocket build with a changed
  `internal/github` can act as the owner: those paths are risk paths at L2,
  read before each merge.
- A web or phone user gets no button.
- The owner makes and rotates 2 tokens. Their permissions are broad within
  each owner's repositories (Contents: write); the fixed login, the gates
  and the mutation allowlist limit what Pocket does with them.
- The reads before each mutation are sequential, not 1 snapshot. A label
  change in the seconds before a mutation is accepted as residual risk;
  GitHub binds the head and enforces every rule at the merge.
- The bot can still apply `ready-for-human`. A bad PR with the label and
  green checks passes every gate, as in today's manual path.
- GitHub's answers can change. The recorded answers are the tests' base;
  `record.sh` records them again.

## Alternatives considered

- **A button on the web Desk with a browser-side token:** the page's code
  comes from the team VM and changes with no review, so the VM could read
  the token.
- **A token on the team VM, or the bot merging:** any agent could read the
  token, and the bot may not both approve and merge.
- **A GitHub App with device flow:** more setup and code for the same
  identity.
- **The `gh` CLI's token:** broad scopes, and its storage is not Pocket's.
- **No button for `risk:high`:** keeps today's 5 clicks for the PRs that need
  the most care; the diff step makes the read the normal path instead.
- **A merge method list in Pocket's settings, or 1 allowed method per
  ruleset:** more setup; GitHub's default for the owner already matches his
  habit in each repository.
- **No confirm step:** 1 click for a single PR, but no view of the head and
  the method before Pocket acts as the owner.
- **A ruleset change to merge without a human approval:** it weakens a merge
  requirement. An automated path for PRs that are not `risk:high` is a later
  decision, not part of this one.

## References

- Shapes: [design: Pocket](../design/pocket.md#the-protocol),
  [roadmap](../plans/roadmap.md#phase-5--polish-from-daily-use)
- Builds on: [ADR-0004](0004-build-a-native-client-of-pi-pockets-own-api.md),
  [ADR-0005](0005-organize-the-window-around-what-needs-the-owner.md)
- Recorded answers: [`internal/github/testdata/`](../../internal/github/testdata/README.md)
- GitHub: [rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets),
  [merge queue](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue),
  [fine-grained personal access tokens](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens)
