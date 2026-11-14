package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
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

		// A 200 with `errors` and a null result proves no refusal: GitHub can
		// make the change, then null the result. Reconcile with 3 reads.
		{name: "approve: errors, null result, effect at the 2nd reconcile read: reconciled", base: direct, before: func(f *fake) {
			f.mode[approve] = answerGQL
			f.onRead = landAt(f, 3, approveEffect)
		}, kind: "merged", step: "merge", muts: []string{approve, merge}, waits: waits3[:2]},
		{name: "merge: errors, null result, effect at the 3rd reconcile read: merged", base: direct, before: func(f *fake) {
			f.mode[merge] = answerGQL
			f.onRead = landAt(f, 5, mergeEffect)
		}, kind: "merged", step: "reconcile merge", method: "merge", commit: mergeOid, muts: []string{approve, merge}, waits: waits3},
		{name: "enqueue: errors, null result, effect at the 2nd reconcile read: queued", base: queue, before: func(f *fake) {
			f.mode[enqueue] = answerGQL
			f.onRead = landAt(f, 4, enqueueEffect)
		}, kind: "queued", step: "reconcile merge", method: "queue", muts: []string{approve, enqueue}, waits: waits3[:2]},
		{name: "approve: errors, null result, no effect: unknown with GitHub's message", base: direct, before: func(f *fake) { f.mode[approve] = answerGQL },
			kind: "unknown", step: "reconcile approve", reason: "approval may be pending; GitHub said: Head branch was modified. Review and try the merge again.",
			muts: []string{approve}, waits: waits3, reads: 4},
		{name: "merge: errors, null result, no effect: unknown with GitHub's message", base: direct, before: func(f *fake) { f.mode[merge] = answerGQL },
			kind: "unknown", step: "reconcile merge", reason: "merge may be pending; GitHub said: Head branch was modified. Review and try the merge again.",
			muts: []string{approve, merge}, waits: waits3, reads: 5},
		{name: "enqueue: errors, null result, no effect: unknown", base: queue, before: func(f *fake) { f.mode[enqueue] = answerGQL },
			kind: "unknown", step: "reconcile merge", reason: "merge may be pending; GitHub said:", muts: []string{approve, enqueue}, waits: waits3},
		{name: "approve: errors, null result, every reconcile read fails: unknown", base: direct, before: func(f *fake) {
			f.mode[approve], f.land[approve] = answerGQL, true
			f.prFail = failFrom(2)
		}, kind: "unknown", step: "reconcile approve", reason: "approval may be pending", muts: []string{approve}, waits: waits3},
		{name: "merge: errors, null result, every reconcile read fails: unknown", base: direct, before: func(f *fake) {
			f.mode[merge], f.land[merge] = answerGQL, true
			f.prFail = failFrom(3)
		}, kind: "unknown", step: "reconcile merge", reason: "merge may be pending", muts: []string{approve, merge}, waits: waits3},
		{name: "merge: errors, null result, merged by another actor: refused", base: direct, before: func(f *fake) {
			f.mode[merge], f.land[merge] = answerGQL, true
			f.afterMerge = func(pr map[string]any) { pr["mergedBy"] = map[string]any{"login": "someone-else"} }
		}, kind: "refused", step: "reconcile merge", reason: "merged by someone-else at 3721d27", muts: []string{approve, merge}, waits: waitFirst},
		{name: "merge: a redirect, no effect: unknown", base: direct, before: func(f *fake) { f.mode[merge] = answerRedirect },
			kind: "unknown", step: "reconcile merge", reason: "merge may be pending", muts: []string{approve, merge}, waits: waits3},

		// A 4xx: 1 read at once, which only upgrades.
		{name: "approve: 4xx, landed: upgraded", base: direct, before: func(f *fake) { f.mode[approve], f.land[approve] = answer4xx, true },
			kind: "merged", step: "merge", muts: []string{approve, merge}, reads: 3},
		{name: "merge: 4xx, landed: merged", base: direct, before: func(f *fake) { f.mode[merge], f.land[merge] = answer4xx, true },
			kind: "merged", step: "merge", method: "merge", commit: mergeOid, muts: []string{approve, merge}, reads: 3},
		{name: "enqueue: 4xx, landed: queued", base: queue, before: func(f *fake) { f.mode[enqueue], f.land[enqueue] = answer4xx, true },
			kind: "queued", step: "merge", method: "queue", muts: []string{approve, enqueue}, reads: 3},
		{name: "merge: 4xx, merged by another actor: refused", base: direct, before: func(f *fake) {
			f.mode[merge], f.land[merge] = answer4xx, true
			f.afterMerge = func(pr map[string]any) { pr["mergedBy"] = map[string]any{"login": "someone-else"} }
		}, kind: "refused", step: "merge", reason: "merged by someone-else at 3721d27", muts: []string{approve, merge}, reads: 3},
		{name: "approve: 4xx, no effect: failed after 1 read", base: direct, before: func(f *fake) { f.mode[approve] = answer4xx },
			kind: "failed", step: "approve", reason: "Resource not accessible", muts: []string{approve}, reads: 2},
		{name: "merge: 4xx, no effect: approved after 1 read", base: direct, before: func(f *fake) { f.mode[merge] = answer4xx },
			kind: "approved", step: "merge", reason: "Resource not accessible", muts: []string{approve, merge}, reads: 3},
		{name: "enqueue: 4xx, no effect: approved after 1 read", base: queue, before: func(f *fake) { f.mode[enqueue] = answer4xx },
			kind: "approved", step: "merge", reason: "Resource not accessible", muts: []string{approve, enqueue}, reads: 3},
		{name: "merge: 4xx, the verification read fails: approved", base: direct, before: func(f *fake) {
			f.mode[merge], f.land[merge] = answer4xx, true
			f.prFail = pending(3)
		}, kind: "approved", step: "merge", reason: "Resource not accessible", muts: []string{approve, merge}, reads: 3},
		{name: "a reason over 300 runes is cut to 300", base: direct, before: func(f *fake) {
			f.mode[approve], f.message = answer4xx, strings.Repeat("é", 400)
		}, kind: "failed", step: "approve", muts: []string{approve}, check: func(t *testing.T, _ *fake, r Result) {
			if n := utf8.RuneCountInString(r.Reason); n != maxReason || !utf8.ValidString(r.Reason) {
				t.Errorf("reason of %d runes", n)
			}
		}},

		// Refused before any request: nothing changed on GitHub, no read.
		{name: "approve not sent: failed, no read", base: direct, onStep: clearTokenAt("about to approve"),
			kind: "failed", step: "approve", reason: "no GitHub token", reads: 1},
		{name: "merge not sent: approved, no read", base: direct, onStep: clearTokenAt("about to merge"),
			kind: "approved", step: "merge", reason: "no GitHub token", muts: []string{approve}, reads: 2},
	} {
		t.Run(tc.name, func(t *testing.T) { runAct(t, tc) })
	}
}

// TestGraphQLErrors: every 200 with `errors` is ErrIncomplete with GitHub's
// messages, whatever its data.
func TestGraphQLErrors(t *testing.T) {
	for _, body := range []string{
		`{"errors":[{"message":"no"}]}`,
		`{"data":null,"errors":[{"message":"no"}]}`,
		`{"data":{"mergePullRequest":null},"errors":[{"message":"no"}]}`,
		`{"data":{"mergePullRequest":{}},"errors":[{"message":"no"}]}`,
		`{"data":{},"errors":[{"message":"no"}]}`,
	} {
		c := New(testToken)
		c.BaseURL = "https://api.example.org"
		c.HTTP.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
		})
		var out json.RawMessage
		err := c.GraphQL(context.Background(), prQuery, nil, &out)
		var g *gqlErrors
		if !errors.Is(err, ErrIncomplete) || !errors.As(err, &g) || g.msg != "no" {
			t.Errorf("%s: %v", body, err)
		}
	}
}

func TestCapReason(t *testing.T) {
	if s := strings.Repeat("a", maxReason); capReason(s) != s {
		t.Fatal("a reason of 300 runes changed")
	}
	if got := capReason(strings.Repeat("ü", maxReason+1)); utf8.RuneCountInString(got) != maxReason || !utf8.ValidString(got) {
		t.Fatalf("%d runes", utf8.RuneCountInString(got))
	}
}

// landAt makes a mutation's effect visible from PR read n on, as GitHub
// can show it some seconds after an answer.
func landAt(f *fake, n int, effect func(*fake)) func(int, map[string]any) {
	return func(read int, pr map[string]any) {
		if read != n {
			return
		}
		effect(f)
		for k, v := range copyJSON(f.pr()) {
			pr[k] = v
		}
	}
}

func approveEffect(f *fake) {
	pr := f.pr()
	pr["viewerLatestReview"] = map[string]any{"state": "APPROVED", "commit": map[string]any{"oid": f.head()}}
	pr["reviewDecision"], pr["mergeStateStatus"] = "APPROVED", "CLEAN"
}

func mergeEffect(f *fake) {
	pr := f.pr()
	pr["state"], pr["mergedBy"], pr["mergeCommit"] = "MERGED", map[string]any{"login": Login}, map[string]any{"oid": mergeOid}
}

func enqueueEffect(f *fake) {
	pr := f.pr()
	pr["isInMergeQueue"] = true
	pr["mergeQueueEntry"] = map[string]any{"enqueuer": map[string]any{"login": Login}, "headCommit": map[string]any{"oid": f.head()}}
}

// failFrom fails every PR read from read n on.
func failFrom(n int) func(int) (int, string, http.Header) {
	return func(read int) (int, string, http.Header) {
		if read >= n {
			return 500, `{"message":"Server Error"}`, nil
		}
		return 0, "", nil
	}
}

// clearTokenAt drops the token just before a mutation, so it goes nowhere.
func clearTokenAt(step string) func(*Client, string) {
	return func(c *Client, name string) {
		if name == step {
			c.Token = ""
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

// TestAnswerOf: only a 4xx proves a refusal, only an error before the
// request proves nothing was sent; every other error is unproven.
func TestAnswerOf(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	bg := context.Background()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		want int
	}{
		{"not allowed", bg, errNotAllowed, answerNotSent},
		{"no token", bg, notSent("no GitHub token"), answerNotSent},
		{"403", bg, &APIError{Status: 403}, answerRefused},
		{"409, redacted", bg, Redact(&APIError{Status: 409, Message: testToken}, testToken), answerRefused},
		{"422 after the context ended", canceled, &APIError{Status: 422}, answerUnproven},
		{"redirect", bg, &APIError{Status: 307}, answerUnproven},
		{"errors with a null result", bg, &gqlErrors{msg: "x"}, answerUnproven},
		{"no answer", bg, ErrNoAnswer, answerUnproven},
		{"incomplete", bg, incomplete("no data"), answerUnproven},
		{"other", bg, errors.New("x"), answerUnproven},
	} {
		if got := answerOf(tc.ctx, tc.err); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}
