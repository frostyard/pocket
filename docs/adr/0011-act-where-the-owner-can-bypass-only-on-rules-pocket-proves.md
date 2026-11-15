# 0011 — Act where the owner can bypass, only on rules Pocket proves

- **Status:** Accepted; amends the "No bypass, read and not assumed" rule of
  [ADR-0008](0008-approve-and-merge-as-the-owner-from-the-desktop.md) and
  the "Never on a ruleset's bypass list" line of
  [ADR-0010](0010-act-as-the-owner-through-a-github-app-user-token.md)
- **Date:** 2026-11-14

## Context

[ADR-0008](0008-approve-and-merge-as-the-owner-from-the-desktop.md) lets
Pocket approve and merge only where `current_user_can_bypass` is `never` on
every ruleset of the base. With no bypass, a merge that GitHub accepts met
every rule, so Pocket did not need to model each rule.

Facts (read on 2026-11-14):

- `bketelsen` is an owner of the `frostyard` organization and an admin of
  his repositories. He is a bypass actor with mode `always` on the rulesets
  of those bases: the organization ruleset `default branch: humans merge`
  and the repository rulesets that
  [docs/new-repository.md](../new-repository.md) creates
  (`RepositoryRole` admin, `bypass_mode: always`). With his sign-in, every
  ready card says "you can bypass a rule here (always): Pocket refuses to
  merge" (for example frostyard/core#259, frostyard/frostyard-org#126,
  frostyard/frostmail#330, frostyard/problem-intake#27). The recorded
  answers of ADR-0008 came from the machine account, which reads `never`,
  so the tests never saw this.
- A bypass actor's merge through the API skips the rules it does not meet,
  with no prompt. The 3 mutations have no admin or bypass input
  ([`schema.json`](../../internal/github/testdata/schema.json)). The `gh`
  CLI's `--admin` flag only skips its own client check on
  `mergeStateStatus` and then calls the same `mergePullRequest`; on a merge
  queue base it calls `mergePullRequest` instead of `enqueuePullRequest`
  ([cli/cli `pkg/cmd/pr/merge/merge.go`](https://github.com/cli/cli/blob/trunk/pkg/cmd/pr/merge/merge.go),
  `blockedReason`, `shouldAddToMergeQueue`). So, with a bypass, a rule
  that Pocket does not check can be skipped in silence.
- `current_user_can_bypass` has 4 values in the
  [rulesets API](https://docs.github.com/en/rest/repos/rules#get-a-repository-ruleset):
  `always`, `pull_requests_only`, `never` and `exempt`. `exempt` means the
  rules do not apply to the viewer at all.
- The rule types and parameters that apply to the bases read
  (`GET /repos/{o}/{r}/rules/branches/main`, frostyard/core,
  frostyard/frostmail, bketelsen/pi-pocket-team): `deletion`,
  `non_fast_forward`, `pull_request` (`required_approving_review_count` 1,
  `dismiss_stale_reviews_on_push`, `require_last_push_approval`,
  `required_review_thread_resolution` on frostyard/core,
  `require_extra_approval_for_unattributed_changes`, `required_reviewers`
  empty, `require_code_owner_review` false, `dismissal_restriction`,
  `allowed_merge_methods` all 3), `required_status_checks` (strict on
  frostyard/core) and `merge_queue` (frostyard/core). GitHub's
  [available rules](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets)
  list more types (restrict updates, deployments, signed commits, code
  scanning, code quality, secret scanning, workflows, file rules and
  others), and GitHub adds new ones.
- `require_extra_approval_for_unattributed_changes`: a PR that Copilot
  opens under its own App identity, not for a person, needs 1 approval more
  than the count (same page, "Additional approval for unattributed Copilot
  pull requests"). GitHub turns it on by default.
- GitHub also blocks a merge, after every required approval, while another
  open PR whose head is the same commit has a pending or rejected review
  (same page, "Require a pull request before merging"). A commit's
  [`associatedPullRequests`](https://docs.github.com/en/graphql/reference/objects#commit)
  lists open PRs only when the commit is not on the default branch, so it
  is no complete list; the repository's `pullRequests(states: OPEN)` is.
- `require_last_push_approval`: an approval counts only from someone other
  than the last person who pushed to the head branch. GraphQL has no field
  for the last pusher.
  [`GET /repos/{o}/{r}/activity`](https://docs.github.com/en/rest/repos/repos#list-repository-activities)
  with `ref=refs/heads/<head>` lists the pushes to a branch, newest first,
  each with `after` (the new SHA), `activity_type` and `actor`. Read live
  on frostyard/frostmail#330 and frostyard/core#256 (machine account): the
  newest push entry has `after` equal to the PR head and the pusher as
  `actor.login`.
- The PR's latest reviews give `authorCanPushToRepository` (write access,
  which an approval needs to count), and `reviewThreads` gives `isResolved`
  ([PullRequest](https://docs.github.com/en/graphql/reference/objects#pullrequest)).
  [`GET /repos/{o}/{r}/compare/{base}...{head}`](https://docs.github.com/en/rest/commits/commits#compare-two-commits)
  gives `behind_by`. The
  [permissions for GitHub Apps](https://docs.github.com/en/rest/authentication/permissions-required-for-github-apps)
  name Contents: read for the activity and the compare; the App of
  ADR-0010 has Contents: read and write and Pull requests: read and write,
  so no new permission is needed.

**`mergeStateStatus` and `mergeable` for a viewer who can bypass.**

- GitHub's
  [`MergeStateStatus`](https://docs.github.com/en/graphql/reference/enums#mergestatestatus)
  docs do not say whether the value depends on the viewer. `mergeable` is
  "based on the existence of merge conflicts" only
  ([PullRequest](https://docs.github.com/en/graphql/reference/objects#pullrequest)),
  so it proves no rule. A separate field, `viewerCanMergeAsAdmin`,
  "indicates whether the viewer can bypass branch protections and merge the
  pull request immediately".
- The `gh` CLI treats BLOCKED and BEHIND as the states that an admin
  overrides with `--admin` (`allowsAdminOverride`, link above). That
  suggests GitHub reports a bypass actor's PR as BLOCKED while a rule is
  not met. It is a client's assumption, not a GitHub contract.
- Read on 2026-11-14 with the machine account (which cannot bypass):
  frostyard/frostmail#330 (open) gives `mergeable` MERGEABLE,
  `mergeStateStatus` BLOCKED, `reviewDecision` REVIEW_REQUIRED and
  `viewerCanMergeAsAdmin` false. The other 3 PRs were already merged
  (`mergeStateStatus` UNKNOWN). No read with the owner's sign-in was
  possible from the team machine.

So CLEAN is not proven to mean "every rule is met" for a viewer who can
bypass: Pocket must not rely on it there.

## Decision

**Bypass that Pocket accepts.** Gate 2 accepts `never`, `always` and
`pull_requests_only`. `exempt`, a missing value and any other value refuse
as before ("you can bypass a rule here (X): Pocket refuses to merge").
Classic protection (`protection.enabled`) must still be false.

**Pocket proves each rule of a ruleset the owner can bypass.** The rules
whose ruleset says `always` or `pull_requests_only` are the rules Pocket
proves itself, by its own reads, at the preview and before each mutation.
The rules of a ruleset that says `never` stay GitHub's to enforce, as
before. Pocket does not read or trust `mergeStateStatus` as proof of these
rules.

| Rule | What Pocket proves |
| --- | --- |
| `deletion`, `non_fast_forward`, `creation` | Nothing to prove: a merge or an enqueue moves an existing branch to a descendant commit; it never deletes, creates or force-pushes it. |
| `required_linear_history` | The method is squash or rebase (for a merge queue, the queue's method). |
| `required_status_checks` | Gates 6 and 7 (every required check present, from its app, complete and green). `strict_required_status_checks_policy`: on a base with no merge queue, `behind_by` of base tip...head is 0. `do_not_enforce_on_create` applies only to a new branch. |
| `merge_queue` | Pocket enqueues, as before, and never sends `mergePullRequest` on a queue base. |
| `pull_request` | `required_approving_review_count`: the distinct approvers whose latest review approves the current head and who can push, with the owner's own approval, reach the count (at the preview the owner's coming approval counts; after it, it must be there). Only approvals at the head count, so `dismiss_stale_reviews_on_push` holds. `require_extra_approval_for_unattributed_changes`: 1 more approval when the PR's author is not a `User`. `require_last_push_approval`: the newest activity of the head branch is a push to the head SHA by an actor that is not the owner; a head branch in a fork, or an entry that does not prove it, refuses. `required_review_thread_resolution`: no review thread is unresolved. `allowed_merge_methods`: the method is in the list. `require_code_owner_review` true or a non-empty `required_reviewers` refuses (Pocket does not read CODEOWNERS or team approvals). Another open PR at the same head commit refuses (Pocket does not read its reviews). `dismissal_restriction` and `automatic_copilot_code_review_enabled` set no merge requirement. |

**Fail closed.** Any other rule type (for example `update`,
`required_deployments`, `required_signatures`, `code_scanning`,
`workflows`, a file rule or a type that GitHub adds later) in a ruleset the
owner can bypass refuses, and the reason names it: "you can bypass rule
<type> here, and Pocket does not check it itself: Pocket refuses to merge".
A parameter that Pocket does not know refuses the same way (named
`<type>.<parameter>`), unless its value is off (null, false, 0, "", [] or
{}); this holds for every rule type, also the types that need no proof. A
known parameter with an unexpected shape refuses, also 1 whose value sets
no merge requirement (`dismissal_restriction`,
`automatic_copilot_code_review_enabled`, the merge queue's parameters). A parameter that the
[rules API](https://docs.github.com/en/rest/repos/rules) marks as required
(for `pull_request`: `required_approving_review_count`,
`dismiss_stale_reviews_on_push`, `require_code_owner_review`,
`require_last_push_approval`, `required_review_thread_resolution`; for
`required_status_checks`: the list and
`strict_required_status_checks_policy`) refuses when it is missing: a
missing value is no proof that a requirement is off. A missing
`require_extra_approval_for_unattributed_changes` counts as on, its
default. A null or
truncated field in these reads (more than 100 review threads or reviews, a
null activity page, a null `behind_by`), or a head that changed between
the reads, fails the read, as the Nulls rule of ADR-0008 says.

**The reads.** Only where a ruleset says `always` or `pull_requests_only`
and no rule is unchecked: 1 GraphQL query `PocketBypass` (the text is
[`internal/github/testdata/bypass-query.graphql`](../../internal/github/testdata/bypass-query.graphql):
the repository's open PRs (100 at most: more refuses), head, head branch,
cross-repository flag, author type, review threads and latest reviews with
`authorCanPushToRepository`); `GET .../activity`
(`per_page=1`) only for `require_last_push_approval`; `GET .../compare/`
only for a strict policy on a base with no merge queue. All are reads.
Where every ruleset says `never`, Pocket sends exactly the requests it sent
before.

**What stays.** Gates 1 to 10 and the after-approval gate of ADR-0008 (with
the merge state allow list: CLEAN or HAS_HOOKS for a direct merge after the
approval), the intent check, the 3 mutations and their exact inputs, no
admin input, no weaker requirement, the `risk:high` diff gate, and the
named refusals of ADR-0009. The proof is gate 11 and runs at both stages.

**The card.** An eligible card on such a base shows "you can bypass a rule
here: Pocket checks every rule itself". The preflight shows the bypass
value and, when a rule is unchecked, its name as the reason.

## Consequences

- The owner gets the button on the bases where he is a bypass actor, which
  are his normal bases.
- Pocket now carries a model of GitHub's rules. A rule or parameter that
  GitHub adds refuses until Pocket models it: safe, but a new GitHub
  feature can turn the button off on every base until a Pocket change.
- A merge on such a base rests on Pocket's own checks, not on GitHub's
  enforcement. A mistake in `internal/github/rules.go`, `bypass.go` or
  `gates.go` can let a merge skip a rule. These paths are risk paths at L2.
- Approvals are counted at the head only, which is stricter than GitHub
  when `dismiss_stale_reviews_on_push` is false: a PR that needs 2 or more
  approvals can refuse where GitHub would merge.
- A PR whose head commit is also the head of another open PR refuses on a
  base the owner can bypass, even when the other PR has no review.
- A repository with more than 100 open PRs refuses on a base the owner
  can bypass, because Pocket cannot read all their heads in 1 page.
- Residual risk: the reads are sequential, not 1 snapshot, as in ADR-0008.
  A push between the reads changes the head, which the expected-head inputs
  of the mutations refuse.
- The owner's own view of `mergeStateStatus` on such a base is still
  unread. If GitHub reports BLOCKED after his approval when every rule is
  met, the after-approval gate refuses: safe, and the first use shows it.

## Alternatives considered

- **Remove the owner from the bypass lists:** it changes the organization
  and repository rulesets, and the owner loses his own break-glass path on
  GitHub.
- **Trust `mergeStateStatus` CLEAN for a bypass actor:** no GitHub
  contract says that CLEAN means "every rule met" for such a viewer.
- **Prove only the rule types on the bases read today, and accept others:**
  a new rule could then be skipped in silence, which is the risk this ADR
  closes.
- **Prove every rule, also where the ruleset says `never`:** GitHub
  enforces those; proving them too would refuse on rules Pocket does not
  model for no safety gain.

## References

- Shapes: [design: Pocket](../design/pocket.md#the-protocol),
  [roadmap](../plans/roadmap.md#phase-5--polish-from-daily-use)
- Builds on: [ADR-0008](0008-approve-and-merge-as-the-owner-from-the-desktop.md),
  [ADR-0009](0009-read-check-runs-only-with-a-token-that-can.md),
  [ADR-0010](0010-act-as-the-owner-through-a-github-app-user-token.md)
- Recorded answers: [`internal/github/testdata/`](../../internal/github/testdata/README.md)
- GitHub: [available rules for rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets),
  [rules API](https://docs.github.com/en/rest/repos/rules),
  [repository activities](https://docs.github.com/en/rest/repos/repos#list-repository-activities),
  [compare two commits](https://docs.github.com/en/rest/commits/commits#compare-two-commits)
