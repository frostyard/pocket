#!/usr/bin/env bash
# Records GitHub's answers for the approve + merge protocol (ADR-0008).
#
# Read only: every call is a REST GET or a GraphQL query (no mutation).
# Usage, from this folder:
#   ./record.sh OWNER/NAME BASE PR[:LABEL] [PR[:LABEL] ...]
#   ./record.sh --schema    (writes schema.json: the 3 mutation inputs and
#                            the enums the gates read, by introspection)
# Writes OWNER-NAME/*.json. LABEL names the answer (for example 253:merged
# writes pr-253-merged.json); without it the PR's state names it.
#
# Sanitized: no response headers; branches/<base> keeps only the commit SHA
# (the commit's author and committer carry names and email addresses);
# _links is dropped. Logins, node IDs, titles and SHAs stay: they are the
# protocol's inputs.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"

if [ "${1:-}" = "--schema" ]; then
	# GitHub allows __Type.enumValues 2 times in 1 query: 1 query per type.
	{
		for t in AddPullRequestReviewInput MergePullRequestInput EnqueuePullRequestInput; do
			gh api graphql -f query="{ __type(name: \"$t\") { name inputFields { name type { kind name ofType { name } } } } }" \
				--jq '.data.__type | {name, inputs: [.inputFields[] | {name, type: (.type.name // .type.ofType.name), required: (.type.kind == "NON_NULL")}]}'
		done
		for t in MergeStateStatus MergeableState PullRequestReviewDecision PullRequestReviewState PullRequestReviewEvent \
			PullRequestMergeMethod PullRequestState CheckStatusState CheckConclusionState StatusState; do
			gh api graphql -f query="{ __type(name: \"$t\") { name enumValues(includeDeprecated: true) { name isDeprecated } } }" \
				--jq '.data.__type | {name, values: [.enumValues[] | select(.isDeprecated | not) | .name], deprecated: [.enumValues[] | select(.isDeprecated) | .name]}'
		done
	} | jq -s '.' >"$here/schema.json"
	exit 0
fi

repo="$1"
base="$2"
shift 2
owner="${repo%/*}"
name="${repo#*/}"
dir="${owner}-${name}"
mkdir -p "$here/$dir"

gh api "repos/$repo/rules/branches/$base?per_page=100" \
	| jq '.' >"$here/$dir/rules-branches-$base.json"

for id in $(jq -r '[.[].ruleset_id] | unique | .[]' "$here/$dir/rules-branches-$base.json"); do
	gh api "repos/$repo/rulesets/$id" \
		| jq 'del(._links)' >"$here/$dir/ruleset-$id.json"
done

gh api "repos/$repo/branches/$base" \
	| jq '{name, commit: {sha: .commit.sha}, protected, protection, protection_url}' \
		>"$here/$dir/branches-$base.json"

for arg in "$@"; do
	number="${arg%%:*}"
	label="${arg#*:}"
	# gh exits 1 on an answer with an `errors` list; that answer is recorded too.
	out="$(gh api graphql -F owner="$owner" -F name="$name" -F number="$number" \
		-F query=@"$here/pr-query.graphql" 2>/dev/null)" || true
	jq -e '.data' <<<"$out" >/dev/null || { echo "no answer for $repo#$number" >&2; exit 1; }
	if [ "$label" = "$arg" ]; then
		label="$(jq -r '.data.repository.pullRequest.state // "not-found" | ascii_downcase' <<<"$out")"
	fi
	jq '.' <<<"$out" >"$here/$dir/pr-$number-$label.json"
done
