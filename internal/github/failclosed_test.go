package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestActFailsClosed: every answer that does not prove a fact keeps Pocket
// from claiming it, and from any mutation it does not prove safe.
func TestActFailsClosed(t *testing.T) {
	for _, tc := range []actCase{
		// A null rules page is no proof of "no rules".
		{name: "rules page null: failed, nothing sent", base: direct, change: func(f *fake) { f.rulesRaw = "null" },
			kind: "failed", step: "read", reason: "a page of rules is null"},
		// A null pagination flag or nested field is no proof either.
		{name: "labels hasNextPage null: failed, nothing sent", base: direct, change: func(f *fake) {
			obj(f.pr(), "labels", "pageInfo")["hasNextPage"] = nil
		}, kind: "failed", step: "read", reason: "labels.pageInfo.hasNextPage is null"},
		{name: "viewerLatestReview commit oid null: failed, nothing sent", base: direct, change: func(f *fake) {
			f.pr()["viewerLatestReview"] = map[string]any{"state": "APPROVED", "commit": map[string]any{"oid": nil}}
		}, kind: "failed", step: "read", reason: "viewerLatestReview.commit.oid is null"},
		// A merge state Pocket does not know refuses on a queue base.
		{name: "queue: unknown merge state after the approval: approved, no enqueue", base: queue, before: func(f *fake) {
			f.afterApprove = func(pr map[string]any) { pr["mergeStateStatus"] = "FUTURE_STATE" }
		}, kind: "approved", step: "read 2", reason: "the merge state is FUTURE_STATE", muts: []string{approve}},
		{name: "queue: UNSTABLE after the approval: approved, no enqueue", base: queue, before: func(f *fake) {
			f.afterApprove = func(pr map[string]any) { pr["mergeStateStatus"] = "UNSTABLE" }
		}, kind: "approved", step: "read 2", reason: "a check is failing", muts: []string{approve}},

		// `errors` with a non-null result: no proof of a refusal, reconcile.
		{name: "approve: errors with data, landed: reconciled", base: direct, before: func(f *fake) { f.mode[approve], f.land[approve] = answerGQLData, true },
			kind: "merged", step: "merge", muts: []string{approve, merge}, waits: waitFirst},
		{name: "merge: errors with data, landed: reconciled", base: direct, before: func(f *fake) { f.mode[merge], f.land[merge] = answerGQLData, true },
			kind: "merged", step: "reconcile merge", method: "merge", commit: mergeOid, muts: []string{approve, merge}, waits: waitFirst},
		{name: "enqueue: errors with data, landed: reconciled", base: queue, before: func(f *fake) { f.mode[enqueue], f.land[enqueue] = answerGQLData, true },
			kind: "queued", step: "reconcile merge", method: "queue", muts: []string{approve, enqueue}, waits: waitFirst},
		{name: "approve: errors with data, no effect: unknown", base: direct, before: func(f *fake) { f.mode[approve] = answerGQLData },
			kind: "unknown", step: "reconcile approve", reason: "approval may be pending", muts: []string{approve}, waits: waits3},
		{name: "merge: errors with data, no effect: unknown", base: direct, before: func(f *fake) { f.mode[merge] = answerGQLData },
			kind: "unknown", step: "reconcile merge", reason: "merge may be pending", muts: []string{approve, merge}, waits: waits3},
		{name: "enqueue: errors with data, no effect: unknown", base: queue, before: func(f *fake) { f.mode[enqueue] = answerGQLData },
			kind: "unknown", step: "reconcile merge", muts: []string{approve, enqueue}, waits: waits3},
		{name: "approve: no data and no errors, no effect: unknown", base: direct, before: func(f *fake) { f.mode[approve] = answerNoData },
			kind: "unknown", step: "reconcile approve", muts: []string{approve}, waits: waits3},
		{name: "merge: data that does not decode, landed: reconciled", base: direct, before: func(f *fake) { f.mode[merge], f.land[merge] = answerBadData, true },
			kind: "merged", step: "reconcile merge", muts: []string{approve, merge}, waits: waitFirst},

		// A clean refusal or a 4xx: 1 read at once, which only upgrades.
		{name: "approve: clean refusal, landed: upgraded", base: direct, before: func(f *fake) { f.mode[approve], f.land[approve] = answerGQL, true },
			kind: "merged", step: "merge", muts: []string{approve, merge}, reads: 3},
		{name: "approve: 4xx, landed: upgraded", base: direct, before: func(f *fake) { f.mode[approve], f.land[approve] = answer4xx, true },
			kind: "merged", step: "merge", muts: []string{approve, merge}, reads: 3},
		{name: "merge: clean refusal, landed: merged", base: direct, before: func(f *fake) { f.mode[merge], f.land[merge] = answerGQL, true },
			kind: "merged", step: "merge", method: "merge", commit: mergeOid, muts: []string{approve, merge}, reads: 3},
		{name: "merge: 4xx, landed: merged", base: direct, before: func(f *fake) { f.mode[merge], f.land[merge] = answer4xx, true },
			kind: "merged", step: "merge", method: "merge", muts: []string{approve, merge}, reads: 3},
		{name: "enqueue: clean refusal, landed: queued", base: queue, before: func(f *fake) { f.mode[enqueue], f.land[enqueue] = answerGQL, true },
			kind: "queued", step: "merge", method: "queue", muts: []string{approve, enqueue}, reads: 3},
		{name: "merge: clean refusal, merged by another actor: refused", base: direct, before: func(f *fake) {
			f.mode[merge], f.land[merge] = answerGQL, true
			f.afterMerge = func(pr map[string]any) { pr["mergedBy"] = map[string]any{"login": "someone-else"} }
		}, kind: "refused", step: "merge", reason: "merged by someone-else at 3721d27", muts: []string{approve, merge}, reads: 3},
		{name: "approve: clean refusal, no effect: failed after 1 read", base: direct, before: func(f *fake) { f.mode[approve] = answerGQL },
			kind: "failed", step: "approve", reason: "Head branch was modified", muts: []string{approve}, reads: 2},
		{name: "approve: 4xx, no effect: failed after 1 read", base: direct, before: func(f *fake) { f.mode[approve] = answer4xx },
			kind: "failed", step: "approve", reason: "Resource not accessible", muts: []string{approve}, reads: 2},
		{name: "merge: clean refusal, no effect: approved after 1 read", base: direct, before: func(f *fake) { f.mode[merge] = answerGQL },
			kind: "approved", step: "merge", reason: "Head branch was modified", muts: []string{approve, merge}, reads: 3},
		{name: "enqueue: 4xx, no effect: approved after 1 read", base: queue, before: func(f *fake) { f.mode[enqueue] = answer4xx },
			kind: "approved", step: "merge", reason: "Resource not accessible", muts: []string{approve, enqueue}, reads: 3},
		{name: "merge: clean refusal, the verification read fails: approved", base: direct, before: func(f *fake) {
			f.mode[merge], f.land[merge] = answerGQL, true
			f.prFail = pending(3)
		}, kind: "approved", step: "merge", reason: "Head branch was modified", muts: []string{approve, merge}, reads: 3},
	} {
		t.Run(tc.name, func(t *testing.T) { runAct(t, tc) })
	}
}

// TestGraphQLCleanRefusal: only `errors` with no result is a clean refusal;
// each answer with `errors` is still ErrIncomplete for a read.
func TestGraphQLCleanRefusal(t *testing.T) {
	for body, clean := range map[string]bool{
		`{"errors":[{"message":"no"}]}`:                                    true,
		`{"data":null,"errors":[{"message":"no"}]}`:                        true,
		`{"data":{"mergePullRequest":null},"errors":[{"message":"no"}]}`:   true,
		`{"data":{"a":null,"b":null},"errors":[{"message":"no"}]}`:         true,
		`{"data":{"mergePullRequest":{}},"errors":[{"message":"no"}]}`:     false,
		`{"data":{"a":null,"b":{"x":1}},"errors":[{"message":"no"}]}`:      false,
		`{"data":{},"errors":[{"message":"no"}]}`:                          false,
		`{"data":[],"errors":[{"message":"no"}]}`:                          false,
		`{"data":{"mergePullRequest":false},"errors":[{"message":"no"}]}`:  false,
		`{"data":{"mergePullRequest":"done"},"errors":[{"message":"no"}]}`: false,
	} {
		c := New(testToken)
		c.BaseURL = "https://api.example.org"
		c.HTTP.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
		})
		var out json.RawMessage
		err := c.GraphQL(context.Background(), prQuery, nil, &out)
		if !errors.Is(err, ErrIncomplete) || errors.Is(err, errRefused) != clean || !strings.Contains(err.Error(), "no") {
			t.Errorf("%s: %v, clean %v", body, err, errors.Is(err, errRefused))
		}
	}
}

// TestRulesRefusesUnprovenAnswers: only an explicit [] means no rules, and a
// rule that does not say what it requires refuses.
func TestRulesRefusesUnprovenAnswers(t *testing.T) {
	rsc := func(params any) []any {
		r := map[string]any{"type": "required_status_checks", "ruleset_id": 26867289.0}
		if params != "missing" {
			r["parameters"] = params
		}
		return []any{r}
	}
	for name, tc := range map[string]struct {
		raw   string
		rules []any
		text  string
	}{
		"page null":                  {raw: "null", text: "a page of rules is null"},
		"page an object":             {raw: `{"message":"x"}`, text: "GitHub gave an incomplete answer"},
		"rule type empty":            {rules: []any{map[string]any{"type": "", "ruleset_id": 1.0}}, text: "a rule has no type"},
		"rule type missing":          {rules: []any{map[string]any{"ruleset_id": 1.0}}, text: "a rule has no type"},
		"check parameters null":      {rules: rsc(nil), text: "the required checks"},
		"check parameters missing":   {rules: rsc("missing"), text: "the required checks rule has no parameters"},
		"check list null":            {rules: rsc(map[string]any{"required_status_checks": nil}), text: "lists no checks"},
		"check list missing":         {rules: rsc(map[string]any{}), text: "lists no checks"},
		"check list holds a null":    {rules: rsc(map[string]any{"required_status_checks": []any{nil}}), text: "a required check has no name"},
		"queue parameters null":      {rules: []any{map[string]any{"type": "merge_queue", "ruleset_id": 1.0, "parameters": nil}}, text: "names no method"},
		"queue method null":          {rules: []any{map[string]any{"type": "merge_queue", "ruleset_id": 1.0, "parameters": map[string]any{"merge_method": nil}}}, text: "names no method"},
		"queue parameters missing":   {rules: []any{map[string]any{"type": "merge_queue", "ruleset_id": 1.0}}, text: "names no method"},
		"a null rule in a list":      {rules: []any{nil}, text: "a rule names no ruleset"},
		"ruleset id of a wrong type": {raw: `[{"type":"deletion","ruleset_id":"x"}]`, text: "GitHub gave an incomplete answer"},
	} {
		t.Run(name, func(t *testing.T) {
			f := direct(t)
			f.rulesRaw = tc.raw
			if tc.rules != nil {
				b, _ := json.Marshal(tc.rules)
				f.rulesRaw = string(b)
			}
			_, err := f.client().Rules(context.Background(), f.ref, "main")
			if !errors.Is(err, ErrIncomplete) || !strings.Contains(err.Error(), tc.text) {
				t.Fatalf("err %v, want %q", err, tc.text)
			}
		})
	}
	f := direct(t)
	f.rulesRaw = "[]"
	if r, err := f.client().Rules(context.Background(), f.ref, "main"); err != nil || r.Bypass != "never" || len(r.Required) != 0 {
		t.Fatalf("an explicit []: %+v, %v", r, err)
	}
}

// TestPreviewRefusesNestedNulls: a pagination flag is never null, and a
// nullable object that is there has its fields.
func TestPreviewRefusesNestedNulls(t *testing.T) {
	merged := func(f *fake) {
		pr := f.pr()
		pr["mergedBy"] = map[string]any{"login": Login}
		pr["mergeCommit"] = map[string]any{"oid": mergeOid}
	}
	entry := func(f *fake) {
		f.pr()["mergeQueueEntry"] = map[string]any{"enqueuer": map[string]any{"login": Login}, "headCommit": map[string]any{"oid": f.head()}}
	}
	review := func(f *fake) {
		f.pr()["viewerLatestReview"] = map[string]any{"state": "COMMENTED", "commit": map[string]any{"oid": f.head()}}
	}
	for _, tc := range []struct {
		name   string
		change func(*fake)
		text   string
	}{
		{"labels hasNextPage null", func(f *fake) { obj(f.pr(), "labels", "pageInfo")["hasNextPage"] = nil }, "labels.pageInfo.hasNextPage is null"},
		{"labels hasNextPage missing", func(f *fake) { delete(obj(f.pr(), "labels", "pageInfo"), "hasNextPage") }, "labels.pageInfo.hasNextPage is null"},
		{"reviews hasNextPage null", func(f *fake) { obj(f.pr(), "latestOpinionatedReviews", "pageInfo")["hasNextPage"] = nil }, "latestOpinionatedReviews.pageInfo.hasNextPage is null"},
		{"contexts hasNextPage null", func(f *fake) { obj(f.commit(), "statusCheckRollup", "contexts", "pageInfo")["hasNextPage"] = nil }, "contexts.pageInfo.hasNextPage is null"},
		{"viewerLatestReview state null", func(f *fake) { review(f); obj(f.pr(), "viewerLatestReview")["state"] = nil }, "viewerLatestReview.state is null"},
		{"viewerLatestReview commit null", func(f *fake) { review(f); obj(f.pr(), "viewerLatestReview")["commit"] = nil }, "viewerLatestReview.commit is null"},
		{"viewerLatestReview commit oid null", func(f *fake) { review(f); obj(f.pr(), "viewerLatestReview", "commit")["oid"] = nil }, "viewerLatestReview.commit.oid is null"},
		{"app databaseId null", func(f *fake) {
			c := checkRun("Test", "COMPLETED", "SUCCESS", 15368)
			obj(c, "checkSuite", "app")["databaseId"] = nil
			f.setChecks(c)
		}, "the app of check Test has no databaseId"},
		{"mergedBy login null", func(f *fake) { merged(f); obj(f.pr(), "mergedBy")["login"] = nil }, "mergedBy.login is null"},
		{"mergeCommit oid null", func(f *fake) { merged(f); obj(f.pr(), "mergeCommit")["oid"] = nil }, "mergeCommit.oid is null"},
		{"mergeQueueEntry enqueuer null", func(f *fake) { entry(f); obj(f.pr(), "mergeQueueEntry")["enqueuer"] = nil }, "mergeQueueEntry.enqueuer is null"},
		{"mergeQueueEntry enqueuer login null", func(f *fake) { entry(f); obj(f.pr(), "mergeQueueEntry", "enqueuer")["login"] = nil }, "mergeQueueEntry.enqueuer.login is null"},
		{"mergeQueueEntry headCommit null", func(f *fake) { entry(f); obj(f.pr(), "mergeQueueEntry")["headCommit"] = nil }, "mergeQueueEntry.headCommit is null"},
		{"mergeQueueEntry headCommit oid null", func(f *fake) { entry(f); obj(f.pr(), "mergeQueueEntry", "headCommit")["oid"] = nil }, "mergeQueueEntry.headCommit.oid is null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := direct(t)
			tc.change(f)
			_, err := f.client().Preview(context.Background(), f.ref)
			if !errors.Is(err, ErrIncomplete) || !strings.Contains(err.Error(), tc.text) {
				t.Fatalf("err %v, want %q", err, tc.text)
			}
		})
	}
	// The same objects, whole, read.
	for name, change := range map[string]func(*fake){"review": review, "merged": merged, "entry": entry} {
		f := direct(t)
		change(f)
		if _, err := f.client().Preview(context.Background(), f.ref); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
