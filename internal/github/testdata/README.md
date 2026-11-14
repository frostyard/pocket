# Recorded GitHub answers

Real GitHub answers for the approve + merge protocol of
[ADR-0008](../../../docs/adr/0008-approve-and-merge-as-the-owner-from-the-desktop.md).
The tests of `internal/github` serve them from a fake server and change
them field by field.

Recorded on 2026-11-14 with the machine account `frostyard-machines` (read
only). So `viewer.login` is `frostyard-machines`, `viewerDidAuthor` is true
on its own PRs, and `viewerDefaultMergeMethod` is the machine account's. A
test that needs the owner's view sets those fields itself.

## Files

- `pr-query.graphql`: the PR query (`PocketPR`). `internal/github/query.go`
  sends the same text.
- `record.sh`: the recorder. Every call is a REST GET or a GraphQL query.
- `schema.json`: by introspection, the inputs of `addPullRequestReview`,
  `mergePullRequest` and `enqueuePullRequest`, and the enums the gates read
  (`values` and `deprecated`).
- `<owner>-<name>/` for each repository:
  - `rules-branches-main.json`: `GET /repos/{o}/{r}/rules/branches/main`.
  - `ruleset-<id>.json`: `GET /repos/{o}/{r}/rulesets/<id>` for each
    `ruleset_id` above, with `current_user_can_bypass`.
  - `branches-main.json`: `GET /repos/{o}/{r}/branches/main`.
  - `pr-<number>-<state>.json`: the PR query's answer.

| Repository | Base | Answers |
| --- | --- | --- |
| frostyard/core | merge queue (`SQUASH`), strict checks, 1 organization and 1 repository ruleset | rules, 2 rulesets, branch, PR 253 merged by the queue |
| bketelsen/pi-pocket-team | no queue, 2 repository rulesets | rules, 2 rulesets, branch, PR 237 merged, PR 238 open (`risk:high`, before `ready-for-human`: BLOCKED, REVIEW_REQUIRED, 1 check in progress, the required check `Test` not yet run), PR 999999 not found (HTTP 200 with `errors`) |
| bketelsen/pi-pocket-config | no ruleset | rules (empty), branch, PR 55 merged (`statusCheckRollup` null) |

## Sanitized

- No response headers.
- `branches-main.json` keeps the branch name, the commit SHA and the
  protection; the commit's author and committer (names, email addresses) are
  dropped. `_links` is dropped from the rulesets.
- No token, cookie or server address. Logins, node IDs, titles and SHAs
  stay: they are the protocol's inputs, and these repositories' PRs are
  readable by their members.
- `test/github-testdata.test.mjs` checks that every file parses and holds no
  token or email address.

## Record again

From this folder, with a read-only `gh` sign-in:

```sh
./record.sh frostyard/core main 253:merged <open PR>:open
./record.sh bketelsen/pi-pocket-team main 237:merged 999999 238:open
./record.sh bketelsen/pi-pocket-config main 55:merged
./record.sh --schema
```

Then check the diff, and run `make verify`.
