package github

import (
	"errors"
	"sort"
	"strings"
)

// The only mutations Pocket sends, each with 1 `$input` of exactly these
// keys: no admin, bypass or body input exists here.
const (
	approveMutation = `mutation PocketApprove($input: AddPullRequestReviewInput!) {
  addPullRequestReview(input: $input) {
    pullRequestReview {
      state
    }
  }
}`
	mergeMutation = `mutation PocketMerge($input: MergePullRequestInput!) {
  mergePullRequest(input: $input) {
    pullRequest {
      state
      mergeCommit {
        oid
      }
    }
  }
}`
	enqueueMutation = `mutation PocketEnqueue($input: EnqueuePullRequestInput!) {
  enqueuePullRequest(input: $input) {
    mergeQueueEntry {
      id
    }
  }
}`
)

// mutationInputs is the allowlist: each mutation's text and its input keys.
var mutationInputs = map[string][]string{
	approveMutation: {"commitOID", "event", "pullRequestId"},
	mergeMutation:   {"expectedHeadOid", "mergeMethod", "pullRequestId"},
	enqueueMutation: {"expectedHeadOid", "pullRequestId"},
}

var errNotAllowed = errors.New("refused: not 1 of Pocket's mutations")

// allowed refuses a mutation that is not 1 of the 3, or that has other
// inputs. Any text that names a mutation must be 1 of them exactly.
func allowed(query string, vars map[string]any) error {
	keys, ok := mutationInputs[query]
	if !ok {
		if strings.Contains(strings.ToLower(query), "mutation") {
			return errNotAllowed
		}
		return nil
	}
	if len(vars) != 1 {
		return errNotAllowed
	}
	input, ok := vars["input"].(map[string]any)
	if !ok || len(input) != len(keys) {
		return errNotAllowed
	}
	got := make([]string, 0, len(input))
	for k, v := range input {
		if s, isStr := v.(string); !isStr || s == "" {
			return errNotAllowed
		}
		got = append(got, k)
	}
	sort.Strings(got)
	for i := range keys {
		if got[i] != keys[i] {
			return errNotAllowed
		}
	}
	return nil
}
