# 0010 — Act as the owner through a GitHub App user token

- **Status:** Accepted
- **Date:** 2026-11-14

## Context

[ADR-0008](0008-approve-and-merge-as-the-owner-from-the-desktop.md) gives
Pocket **Approve + merge** as `bketelsen`, with 2 fine-grained personal
access tokens (1 for `bketelsen`, 1 for `frostyard`) in the desktop
keyring. [ADR-0009](0009-read-check-runs-only-with-a-token-that-can.md)
found that a fine-grained token has no Checks permission: the `PocketPR`
query gets `FORBIDDEN` on each `CheckRun` node under `statusCheckRollup`,
and gates 6 and 7 cannot run. ADR-0009 refuses such a PR by name and leaves
the token to a later ADR. This is that ADR.

Facts (read on 2026-11-14):

- Every required check in frostyard/core, frostyard/pocket, frostyard/fleet,
  frostyard/snosi, frostyard/updex and bketelsen/pi-pocket-team has
  `integration_id` 15368 (GitHub Actions). So with a fine-grained token,
  almost every ready PR gets no button.
- A GitHub App's user access token acts as the user: GitHub shows the user
  with the App's badge. It reaches only what the App's permissions, the
  user's own access and the App's installations all allow. An App can have
  "Checks: read".
- A private App installs only on its owner's account. A public App can be
  installed and authorized by any account, for that account only.
- The device flow needs no client secret: `POST
  https://github.com/login/device/code` gives a `device_code`, a
  `user_code`, `verification_uri` `https://github.com/login/device`,
  `expires_in` (900 s) and `interval` (5 s); the app polls `POST
  https://github.com/login/oauth/access_token`.
- Expiring user tokens (on by default; `scope=offline_access` forces them):
  the access token lives 8 hours, the refresh token 6 months. A refresh of
  a device-flow token needs no client secret. A used refresh token and its
  old access token stop at once; a spent or expired one gives
  `bad_refresh_token`.
- `GET /user/installations` gives, with a user token, the App's
  installations that the user can reach, each with `app_slug`, `account`,
  `permissions` and `suspended_at`.
- A user token shares the user's rate limit (5,000 requests per hour).
- An App of the `frostyard` organization can be managed by every
  organization owner (`bketelsen`, `bsherman`, `KyleGospo`): its
  permissions, its secrets and keys, its webhook.

The constraints of ADR-0008 stay: actions by `bketelsen` only, the token in
the desktop keyring only and never on the team VM, no bypass, gates 1 to 10,
the 3 allowed mutations, and only a click in Pocket starts a mutation.

## Decision

**The App.** Pocket acts as the owner through the user access token of 1
GitHub App, **Pocket for bketelsen** (slug `pocket-for-bketelsen`, or the
slug GitHub gives if it refuses the name).

- **Owned by `bketelsen`**, so only he manages it. **Public**, because a
  private App of `bketelsen` cannot be installed on `frostyard`; public
  means only that its page is visible and that another account can install
  or authorize it for itself, which reaches none of the owner's
  repositories. It never goes to the Marketplace.
- **Installed on `bketelsen` and on `frostyard`**, "All repositories": the
  reach of ADR-0008's 2 tokens. A PR of a repository outside the
  installation gets the named refusal of ADR-0009 (the read's 404 names its
  call).
- **No client secret and no private key** are ever made: the device flow
  and its refresh need neither, and with no private key nobody can act as
  the App itself. No webhook. No callback URL.
- **Never on a ruleset's bypass list.** Pocket refuses to act where the
  owner can bypass a rule.
- **Separate from any automated merge-gate App.** The gate App is another
  actor (an automated grant, not the owner), has other secrets (a private
  key), other permissions (no Contents: write) and its own kill switch
  (suspending the gate must not end the owner's manual path).
- Pocket keeps the App's client ID, slug and name as constants read from
  GitHub (`gh api apps/<slug>`, whose `owner.login` must be `bketelsen`).
  Every text that names the App uses that name.

**Permissions and settings.**

| Setting | Value | Why |
| --- | --- | --- |
| Checks | Read-only | gates 6 and 7 read each check run and its app (`checkSuite.app.databaseId`) |
| Commit statuses | Read-only | gate 7 reads each `StatusContext` |
| Contents | Read and write | the merge; `GET .../branches/{b}` (classic protection) |
| Merge queues | Read and write | `enqueuePullRequest` on a merge-queue base; no doc names an endpoint for it, so Pocket does not require it at sign-in |
| Metadata | Read-only (GitHub sets it) | the rules of the branch and each ruleset's `current_user_can_bypass` |
| Pull requests | Read and write | the PR query and the approval |
| Workflows | Read and write | the merge of a PR that changes `.github/workflows/` |
| Every other repository, organization and account permission | No access | not used |
| Expire user authorization tokens | on | 8-hour access token, 6-month refresh token |
| Request user authorization (OAuth) during installation | off | Pocket signs in by device flow |
| Enable Device Flow | on | the sign-in |
| Webhook | off | Pocket needs no events |

**GitHub calls and their App permissions.** This table replaces the
"Fine-grained permission" column of ADR-0008's table and ADR-0009's row.
"Docs" means [permissions required for GitHub Apps](https://docs.github.com/en/rest/authentication/permissions-required-for-github-apps)
unless the row links another page; "closeout" means only the owner's first
use proves it.

| Call | Use | App permission | Source |
| --- | --- | --- | --- |
| `POST https://github.com/login/device/code` | Start the sign-in | none (client ID only) | [user access token docs](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app) |
| `POST https://github.com/login/oauth/access_token` | Poll the sign-in; refresh | none (client ID only) | user access token docs; [refresh docs](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens) |
| `GET /user` | The token's login | none | docs |
| `GET /user/installations` | The App's installations and their permissions | none | [installations docs](https://docs.github.com/en/rest/apps/installations) |
| `GET /repos/{o}/{r}` | Check: the token reads a ready repository | Metadata: read | docs |
| `GET /repos/{o}/{r}/rules/branches/{branch}` | Rules on the base | Metadata: read | docs |
| `GET /repos/{o}/{r}/rulesets/{id}` | `current_user_can_bypass` | Metadata: read | docs |
| `GET /repos/{o}/{r}/branches/{branch}` | Classic protection: `protection.enabled` | Contents: read | docs |
| GraphQL query `PocketPR` | The PR's facts, checks and reviews; the repository's methods | Pull requests: read; Contents: read; Checks: read; Commit statuses: read; Metadata: read | docs of the REST equivalents ([PR](https://docs.github.com/en/rest/pulls/pulls#get-a-pull-request), [check runs](https://docs.github.com/en/rest/checks/runs#list-check-runs-for-a-git-reference), [statuses](https://docs.github.com/en/rest/commits/statuses#get-the-combined-status-for-a-specific-reference)) |
| `addPullRequestReview` | Approve at the shown head | Pull requests: write | docs of [Create a review](https://docs.github.com/en/rest/pulls/reviews#create-a-review-for-a-pull-request) |
| `mergePullRequest` | Merge at the shown head | Contents: write; Workflows: write when the PR changes `.github/workflows/` | Contents: docs of [Merge a pull request](https://docs.github.com/en/rest/pulls/pulls#merge-a-pull-request); Workflows: closeout |
| `enqueuePullRequest` | Enqueue on a merge-queue base | Merge queues: write or Contents: write (no doc names it) | closeout |

**Sign-in (device flow).**

- Pocket asks for a device code with its client ID and
  `scope=offline_access`. It accepts the answer only with every field,
  `verification_uri` equal to `https://github.com/login/device`,
  `expires_in` and `interval` above 0.
- Settings > GitHub shows the user code, **Copy code**, **Open
  github.com/login/device** (Pocket's own constant URL, never the answer's),
  the time left and **Cancel**, with "Enter this code only here, now. Check
  that GitHub names <App name>."
- The poll runs on its own context until the code expires, every
  `interval` seconds: `authorization_pending` goes on at the same
  interval; `slow_down` adds 5 seconds (RFC 8628); no answer, a timeout, a 5xx, a 429 or non-JSON go
  on; `access_denied`, `expired_token`, another `error` or a 4xx stop it
  with a named reason. **Cancel**, **Sign out**, a new sign-in and exit
  cancel it.
- A success needs an access token, a refresh token, and both expiries of at
  least 900 seconds, counted from the time the request was sent.
- **Check before the save.** With the candidate token, Pocket reads `GET
  /user` (the login must be `bketelsen`, else nothing is kept) and `GET
  /user/installations` (all pages). An owner (`bketelsen`, `frostyard`) is
  **ready** when 1 installation has the App's slug, that account, no
  suspension, and at least Checks: read, Commit statuses: read, Contents:
  write, Metadata: read, Pull requests: write and Workflows: write; else it
  gets a reason, and its PRs get no button. Merge queues below write gives a
  note, not a refusal. A failed read keeps nothing and keeps the old
  sign-in. Owner readiness is checked again at start, at **Run preflight**
  and after each sign-in; the PR reads stay the final authority.
- **The sign-in generation.** Each sign-in, **Cancel** and **Sign out**
  adds 1 to it. Every credential write runs under 1 write mutex and checks
  the generation inside it: a result of an old generation saves nothing.

**1 Pocket at a time.** The keyring entry is 1 per desktop user, whatever
`POCKET_CONFIG_DIR` says. So Pocket holds an operating-system lock on
`<user config dir>/org.frostyard.Pocket/github-signin.lock` (never under
`POCKET_CONFIG_DIR`) from start to the end of the process: `flock` on Linux
and macOS, `LockFileEx` on Windows. The operating system frees it at exit,
also after a crash; no button frees it, and **Release the journal lock**
does not touch it. A second Pocket loads no sign-in, refreshes nothing and
reads nothing on GitHub ("another Pocket holds the GitHub sign-in").

**Exit.** Pocket stops giving leases, cancels the device poll, and waits at
most 35 seconds for a refresh or a keyring write in progress, so that a
pair that GitHub has already rotated is saved before the process ends. The
sign-in lock goes only with the process. Mutation results keep ADR-0008's
journal rules.

**Leases and refresh.**

- Each GitHub job (a preview, a Settings read, the preflight, 1 PR's
  attempt with its reconciliation) takes 1 lease of the access token at its
  start and releases it at its end. A batch takes 1 lease per PR.
- A lease needs at least 10 minutes of validity. A job ends within 5
  minutes, so a leased token never expires inside it.
- Else a refresh is due: no new lease starts, and when no lease is held, 1
  refresh runs, with its own 30-second limit. A refresh never cuts off a
  running job. There is no timer: a refresh happens only for a lease.
- A new pair keeps the current login and must have the same client ID. It
  is saved before any lease uses it. If the keyring does not keep it, no
  lease is given (fail closed) and Settings shows **Save again** and
  **Sign out**.
- `bad_refresh_token`, another `error`, a 4xx, or a malformed success end
  the sign-in: memory cleared, entry deleted, "Sign in to GitHub again". No
  answer (network, timeout, 5xx, 429, non-JSON) keeps the entry (the
  refresh may have landed), fails the waiting jobs, and the next lease
  tries again.
- A 401 from `api.github.com` marks the token of that lease spent (an older
  token changes nothing), so the next lease refreshes. In a batch, a 401
  stops every PR that has not started, of both owners. This replaces
  ADR-0008's "that owner's PRs".
- **Sign out** clears the pair in memory (running jobs finish), then
  deletes the entry, and says how to revoke the App on GitHub. A failed
  delete keeps every button off and offers **Delete again**.

**Hosts and secrets.** The access token goes only to
`https://api.github.com`. The `device_code` and the refresh token go only to
`https://github.com/login/device/code` and
`https://github.com/login/oauth/access_token`, in a POST form body (never a
URL), with no redirect followed and a 30-second timeout. Every error text
replaces each of the 3 with `[token]`. None goes to the Pi Pocket server,
the journal, Activity, a log or a snapshot.

**Keyring.**

- 1 entry in the service `org.frostyard.Pocket`, key `github:app`, whose
  value is JSON: `{"v":1,"client_id":"…","login":"bketelsen","access":"…","access_expires":"<RFC 3339 UTC>","refresh":"…","refresh_expires":"<RFC 3339 UTC>"}`.
  1 entry keeps the pair together: a refresh writes both at once. No file
  fallback.
- No entry: "Sign in with GitHub". An entry that does not parse, has `v`
  other than 1, an empty field, a bad or zero time, another client ID or
  another login: "the stored GitHub sign-in is not usable: <reason>: sign
  in again". A keyring that is missing, locked or failing (or
  `POCKET_NO_KEYRING`): Approve + merge is off, as in ADR-0008.
- **The personal access tokens go.** Settings > GitHub has no token field,
  and no code reads a personal access token. After a sign-in, Pocket
  deletes the old keys `github:bketelsen` and `github:frostyard`. While they
  exist and there is no sign-in, Settings asks the owner to sign in, then
  to delete the 2 old tokens on GitHub.

**Attribution and rulesets.** The approval and the merge show `bketelsen`
with the App's badge. GraphQL gives `bketelsen` for `viewer.login`, the
review author, `mergedBy.login` and `mergeQueueEntry.enqueuer.login`, so
the gates, the reconciliation and the Desk receipts do not change. The
approval is a review by `bketelsen`, who has write access, so it counts as
the ruleset's required approval, and `require_last_push_approval` holds
when another account pushed last. Pocket still reads
`current_user_can_bypass` with the App token and refuses any value other
than `never`. No doc says whether an App on a ruleset's bypass list changes
`current_user_can_bypass` for its user token; the gate refuses in either
case, and the App never goes on a bypass list, so no test of it is planned.

**What stays.** Every action reads `viewer { login }` first and stops if it
is not `bketelsen`. Gates 1 to 10, the intent check, the 3 mutations, the
no-bypass reads, the journal and the Desk receipts of ADR-0008 do not
change, nor do the named refusals of ADR-0009.

### Facts that only the owner's first use proves

The docs give these, or give nothing; the closeout after the build with
this ADR's code proves them:

- That GitHub shows the review and the merge by `bketelsen` with the App's
  badge, and that GraphQL gives `bketelsen` in each field above
  (a direct merge of a low-risk PR whose head has GitHub Actions check
  runs; this also proves Checks: read).
- Which permission `enqueuePullRequest` needs, and that a queued PR shows
  `bketelsen` as the enqueuer and merger (an enqueue on frostyard/core).
- That a merge of a PR that changes `.github/workflows/` needs Workflows:
  write and works with it.

Until a path is proven, a refusal there ends as "approved, not merged", and
the owner merges on GitHub.

## Consequences

- Until the build with this ADR's code, Pocket still asks for the 2 tokens
  of ADR-0008.
- With the App, a PR whose head has check runs passes the same gates as
  before; no gate is weaker.
- The owner makes no token and rotates nothing: he signs in once every 6
  months at most, or again after a lost refresh answer (rare).
- The keyring holds a 6-month refresh token, renewed at each refresh, as it
  held the tokens before. Someone with the owner's unlocked desktop session
  can read it. **Revoke** on GitHub (Settings > Applications > Authorized
  GitHub Apps) ends every sign-in at once; suspending an installation stops
  that account.
- **Device-code phishing.** The client ID is public. Anyone with it can
  start a device flow and ask the owner to enter the code; the token then
  goes to that person and acts as `bketelsen` with this App's permissions.
  GitHub's page names the App and its permissions before **Authorize**. The
  guards: the owner enters a code only when Pocket shows it, and Settings
  says so. The same risk exists for every public OAuth client, the GitHub
  CLI's too.
- The App's permissions are broad within each owner's repositories
  (Contents: write); the fixed login, the gates and the mutation allowlist
  limit what Pocket does with them, as in ADR-0008.
- Pocket's GitHub calls share the owner's rate limit, as with the tokens.
- More code: the device flow, the sign-in lock, leases and refresh. The
  paths of the token's storage and use stay risk paths at L2.
- Every risk that ADR-0008 accepts stays: a label change in the seconds
  before a mutation, and a bad PR with `ready-for-human` and green checks.

## Alternatives considered

- **2 fine-grained personal access tokens (ADR-0008):** a fine-grained
  token has no Checks permission, so gates 6 and 7 cannot run (ADR-0009).
- **A fine-grained token plus Actions: read:** Actions shows only GitHub
  Actions' own jobs, so gate 7 cannot see another app's check runs.
- **A classic token:** its `repo` scope reaches every repository the owner
  can see, with full write.
- **1 App shared with an automated merge-gate App:** another actor, other
  secrets and other kill switches; a shared App would put a private key
  beside the owner's reach, and suspending the gate would stop his manual
  path.
- **2 private Apps, 1 per account:** a private App of `bketelsen` cannot
  be installed on `frostyard`, so the second App would belong to
  `frostyard` (next item), and Pocket would keep 2 sign-ins.
- **1 App owned by `frostyard`:** every organization owner could change its
  permissions or make a key or a secret for it.

## References

- Shapes: [design: Pocket](../design/pocket.md#the-protocol),
  [roadmap](../plans/roadmap.md#phase-5--polish-from-daily-use)
- Builds on: [ADR-0008](0008-approve-and-merge-as-the-owner-from-the-desktop.md)
  (its identity and tokens are superseded here),
  [ADR-0009](0009-read-check-runs-only-with-a-token-that-can.md)
- GitHub: [user access tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app),
  [refreshing them](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens),
  [acting on behalf of a user](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-with-a-github-app-on-behalf-of-a-user),
  [public and private Apps](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/making-a-github-app-public-or-private),
  [permissions required for GitHub Apps](https://docs.github.com/en/rest/authentication/permissions-required-for-github-apps),
  [installations](https://docs.github.com/en/rest/apps/installations),
  [rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api)
- [RFC 8628](https://datatracker.ietf.org/doc/html/rfc8628) (device
  authorization grant)
