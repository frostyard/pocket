package github

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	approve = "addPullRequestReview"
	merge   = "mergePullRequest"
	enqueue = "enqueuePullRequest"
)

var (
	newHead   = strings.Repeat("e", 40)
	waits3    = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}
	waitFirst = []time.Duration{2 * time.Second}
)

type actCase struct {
	name     string
	base     func(*testing.T) *fake
	before   func(*fake)                  // before the preview
	change   func(*fake)                  // after the preview, before Act
	intent   func(*Intent)                // what the click froze
	stepFail string                       // the step whose callback fails
	onStep   func(c *Client, name string) // runs in the step callback
	kind     string                       // the result
	step     string                       // where it ended
	reason   string                       // in the reason
	method   string                       // the result's method
	commit   string                       // the result's merge commit
	muts     []string                     // the mutations sent, in order
	waits    []time.Duration
	steps    []string // the step callbacks, when the case names them
	check    func(*testing.T, *fake, Result)
	reads    int // the PR queries Act sends, when the case names them
}

// approvedBefore: the owner approved the head before, so GitHub is CLEAN.
func approvedBefore(f *fake) {
	pr := f.pr()
	pr["viewerLatestReview"] = map[string]any{"state": "APPROVED", "commit": map[string]any{"oid": f.head()}}
	pr["reviewDecision"], pr["mergeStateStatus"] = "APPROVED", "CLEAN"
}

func pending(n int) func(int) (int, string, http.Header) {
	return func(read int) (int, string, http.Header) {
		if read == n {
			return 500, `{"message":"Server Error"}`, nil
		}
		return 0, "", nil
	}
}

func TestAct(t *testing.T) {
	riskHigh := func(f *fake) { f.pr()["labels"] = labels(labelReady, labelRisk) }
	for _, tc := range []actCase{
		// Each row of the transitions table.
		{name: "read 1: gate fails: refused", base: direct, change: func(f *fake) { f.pr()["labels"] = labels() },
			kind: "refused", step: "read", reason: "no ready-for-human label"},
		{name: "read 1: login is the machine account: refused", base: direct, change: func(f *fake) { obj(f.data, "viewer")["login"] = "frostyard-machines" },
			kind: "refused", step: "read", reason: "this token is for frostyard-machines"},
		{name: "read 1: bypass now: refused", base: direct, change: func(f *fake) { f.rulesets["25664287"].(map[string]any)["current_user_can_bypass"] = "always" },
			kind: "refused", step: "read", reason: "you can bypass a rule here"},
		{name: "read 1: 401: failed", base: direct, change: func(f *fake) {
			f.prFail = func(int) (int, string, http.Header) { return 401, `{"message":"Bad credentials"}`, nil }
		}, kind: "failed", step: "read", reason: "Bad credentials", check: func(t *testing.T, _ *fake, r Result) {
			var api *APIError
			if !errors.As(r.Err, &api) || api.Status != 401 {
				t.Errorf("err %v", r.Err)
			}
		}},
		{name: "read 1: rate limit: failed", base: direct, change: func(f *fake) {
			f.restFail = func(string) (int, string, http.Header) {
				return 403, `{"message":"API rate limit exceeded"}`, http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"1794657600"}}
			}
		}, kind: "failed", step: "read", reason: "rate limit", check: func(t *testing.T, _ *fake, r Result) {
			var api *APIError
			if !errors.As(r.Err, &api) || api.Status != 403 || !api.RetryAfter.Equal(time.Unix(1794657600, 0)) {
				t.Errorf("err %#v", api)
			}
		}},
		{name: "read 1: 429 retry-after: failed", base: direct, change: func(f *fake) {
			f.prFail = func(int) (int, string, http.Header) {
				return 429, `{"message":"slow down"}`, http.Header{"Retry-After": {"60"}}
			}
		}, kind: "failed", step: "read", reason: "slow down", check: func(t *testing.T, _ *fake, r Result) {
			var api *APIError
			if !errors.As(r.Err, &api) || !api.RetryAfter.Equal(time.Date(2026, 11, 14, 12, 1, 0, 0, time.UTC)) {
				t.Errorf("err %#v", api)
			}
		}},
		{name: "read 1: 5xx: failed", base: direct, change: func(f *fake) { f.prFail = pending(1) },
			kind: "failed", step: "read", reason: "500"},
		{name: "approve skipped: approval at the shown head", base: direct, before: approvedBefore, kind: "merged", step: "merge", method: "merge", commit: mergeOid, muts: []string{merge},
			steps: []string{"about to merge", "merged"}},
		{name: "approve success, merge success: merged", base: direct, kind: "merged", step: "merge", method: "merge", commit: mergeOid,
			muts: []string{approve, merge}, steps: []string{"about to approve", "about to merge", "merged"},
			check: func(t *testing.T, f *fake, _ Result) {
				head, id := f.head(), f.pr()["id"]
				want := []map[string]any{
					{"pullRequestId": id, "commitOID": head, "event": "APPROVE"},
					{"pullRequestId": id, "expectedHeadOid": head, "mergeMethod": "MERGE"},
				}
				if !reflect.DeepEqual(f.inputs, want) {
					t.Errorf("inputs %v", f.inputs)
				}
			}},
		{name: "approve answered 4xx: failed", base: direct, before: func(f *fake) { f.mode[approve] = answer4xx },
			kind: "failed", step: "approve", reason: "Resource not accessible", muts: []string{approve}},
		{name: "approve answered a GraphQL error, no effect: unknown", base: direct, before: func(f *fake) { f.mode[approve] = answerGQL },
			kind: "unknown", step: "reconcile approve", reason: "approval may be pending; GitHub said: Head branch was modified", muts: []string{approve}, waits: waits3},
		{name: "approve: no answer (5xx), reconciled: seen", base: direct, before: func(f *fake) { f.mode[approve], f.land[approve] = answer502, true },
			kind: "merged", step: "merge", method: "merge", muts: []string{approve, merge}, waits: waitFirst},
		{name: "approve: connection dropped, reconciled: seen", base: direct, before: func(f *fake) { f.mode[approve], f.land[approve] = answerDrop, true },
			kind: "merged", step: "merge", method: "merge", muts: []string{approve, merge}, waits: waitFirst},
		{name: "approve: answer names no approval, reconciled: seen", base: direct, before: func(f *fake) { f.mode[approve] = answerEmpty },
			kind: "merged", step: "merge", method: "merge", muts: []string{approve, merge}, waits: waitFirst},
		{name: "approve: no answer, a reconcile read fails, then seen", base: direct, before: func(f *fake) {
			f.mode[approve], f.land[approve] = answer502, true
			f.prFail = pending(2)
		}, kind: "merged", step: "merge", muts: []string{approve, merge}, waits: waits3[:2]},
		{name: "approve: no answer, seen at the 3rd read", base: direct, before: func(f *fake) {
			f.mode[approve] = answer502
			f.onRead = func(n int, pr map[string]any) {
				if n == 4 {
					for _, p := range []map[string]any{pr, f.pr()} {
						p["viewerLatestReview"] = map[string]any{"state": "APPROVED", "commit": map[string]any{"oid": p["headRefOid"]}}
						p["reviewDecision"], p["mergeStateStatus"] = "APPROVED", "CLEAN"
					}
				}
			}
		}, kind: "merged", step: "merge", muts: []string{approve, merge}, waits: waits3},
		{name: "approve: no answer, not seen after 3 reads: unknown", base: direct, before: func(f *fake) { f.mode[approve] = answer502 },
			kind: "unknown", step: "reconcile approve", reason: "approval may be pending", muts: []string{approve}, waits: waits3,
			steps: []string{"about to approve", "unknown"}},
		{name: "approve: no answer, approval at another head: unknown", base: direct, before: func(f *fake) {
			f.mode[approve], f.land[approve] = answer502, true
			f.afterApprove = func(pr map[string]any) { obj(pr, "viewerLatestReview", "commit")["oid"] = newHead }
		}, kind: "unknown", step: "reconcile approve", muts: []string{approve}, waits: waits3},
		{name: "read 2: head changed after the approval: approved", base: direct, before: func(f *fake) {
			f.afterApprove = func(pr map[string]any) { pr["headRefOid"] = newHead; f.commit()["oid"] = newHead }
		}, kind: "approved", step: "read 2", reason: "is not there", muts: []string{approve}},
		{name: "read 2: review decision not approved: approved", base: direct, before: func(f *fake) {
			f.afterApprove = func(pr map[string]any) { pr["reviewDecision"] = "REVIEW_REQUIRED" }
		}, kind: "approved", step: "read 2", reason: "the review decision is review required", muts: []string{approve}},
		{name: "read 2: still BLOCKED: approved, no merge", base: direct, before: func(f *fake) {
			f.afterApprove = func(pr map[string]any) { pr["mergeStateStatus"] = "BLOCKED" }
		}, kind: "approved", step: "read 2", reason: "GitHub still blocks the merge", muts: []string{approve}},
		{name: "read 2: risk:high added after the approval: approved", base: direct, before: func(f *fake) {
			f.afterApprove = func(pr map[string]any) { pr["labels"] = labels(labelReady, labelRisk) }
		}, kind: "approved", step: "read 2", reason: "risk:high was added", muts: []string{approve}},
		{name: "read 2: a check failed after the approval: approved", base: direct, before: func(f *fake) {
			f.afterApprove = func(map[string]any) {
				f.setChecks(checkRun("Test", "COMPLETED", "FAILURE", 15368))
			}
		}, kind: "approved", step: "read 2", reason: "check Test: failure", muts: []string{approve}},
		{name: "read 2: reviewDecision null passes", base: direct, before: func(f *fake) {
			f.afterApprove = func(pr map[string]any) { pr["reviewDecision"] = nil }
		}, kind: "merged", step: "merge", muts: []string{approve, merge}},
		{name: "read 2: HTTP error: approved", base: direct, change: func(f *fake) { f.prFail = pending(2) },
			kind: "approved", step: "read 2", reason: "could not read the PR", muts: []string{approve}},
		{name: "merge answered 4xx: approved", base: direct, before: func(f *fake) { f.mode[merge] = answer4xx },
			kind: "approved", step: "merge", reason: "Resource not accessible", muts: []string{approve, merge}},
		{name: "merge answered a GraphQL error, no effect: unknown", base: direct, before: func(f *fake) { f.mode[merge] = answerGQL },
			kind: "unknown", step: "reconcile merge", reason: "merge may be pending; GitHub said: Head branch was modified", muts: []string{approve, merge}, waits: waits3},
		{name: "merge: no answer, merged by the owner at the head: merged", base: direct, before: func(f *fake) { f.mode[merge], f.land[merge] = answer502, true },
			kind: "merged", step: "reconcile merge", method: "merge", commit: mergeOid, muts: []string{approve, merge}, waits: waitFirst},
		{name: "merge: dropped, merged: merged", base: direct, before: func(f *fake) { f.mode[merge], f.land[merge] = answerDrop, true },
			kind: "merged", step: "reconcile merge", method: "merge", commit: mergeOid, muts: []string{approve, merge}, waits: waitFirst},
		{name: "merge: answer names no merge, merged: merged", base: direct, before: func(f *fake) { f.mode[merge] = answerEmpty },
			kind: "merged", step: "reconcile merge", method: "merge", commit: mergeOid, muts: []string{approve, merge}, waits: waitFirst},
		{name: "merge: no answer, merged by another actor: refused", base: direct, before: func(f *fake) {
			f.mode[merge], f.land[merge] = answer502, true
			f.afterMerge = func(pr map[string]any) { pr["mergedBy"] = map[string]any{"login": "someone-else"} }
		}, kind: "refused", step: "reconcile merge", reason: "merged by someone-else at 3721d27", muts: []string{approve, merge}, waits: waitFirst},
		{name: "merge: no answer, merged at another head: refused", base: direct, before: func(f *fake) {
			f.mode[merge], f.land[merge] = answer502, true
			f.afterMerge = func(pr map[string]any) { pr["headRefOid"] = newHead }
		}, kind: "refused", step: "reconcile merge", reason: "merged by bketelsen at eeeeeee", muts: []string{approve, merge}, waits: waitFirst},
		{name: "merge: no answer, nothing after 3 reads: unknown", base: direct, before: func(f *fake) { f.mode[merge] = answerDrop },
			kind: "unknown", step: "reconcile merge", reason: "merge may be pending", muts: []string{approve, merge}, waits: waits3,
			check: func(t *testing.T, _ *fake, r Result) {
				if !errors.Is(r.Err, ErrNoAnswer) {
					t.Errorf("err %v", r.Err)
				}
			}},
		// Merge queue.
		{name: "queue: enqueue success: queued", base: queue, kind: "queued", step: "merge", method: "queue", muts: []string{approve, enqueue},
			check: func(t *testing.T, f *fake, _ Result) {
				if want := map[string]any{"pullRequestId": f.pr()["id"], "expectedHeadOid": f.head()}; !reflect.DeepEqual(f.inputs[1], want) {
					t.Errorf("inputs %v", f.inputs)
				}
			}},
		{name: "queue: BEHIND is allowed", base: queue, before: func(f *fake) { f.pr()["mergeStateStatus"] = "BEHIND" },
			kind: "queued", step: "merge", method: "queue", muts: []string{approve, enqueue}},
		{name: "queue: BLOCKED after approval is allowed", base: queue, before: func(f *fake) {
			f.afterApprove = func(pr map[string]any) { pr["mergeStateStatus"] = "BLOCKED" }
		}, kind: "queued", step: "merge", method: "queue", muts: []string{approve, enqueue}},
		{name: "queue: DIRTY after approval: approved", base: queue, before: func(f *fake) {
			f.afterApprove = func(pr map[string]any) { pr["mergeStateStatus"] = "DIRTY" }
		}, kind: "approved", step: "read 2", reason: "conflicts", muts: []string{approve}},
		{name: "queue: enqueue answered 4xx: approved", base: queue, before: func(f *fake) { f.mode[enqueue] = answer4xx },
			kind: "approved", step: "merge", muts: []string{approve, enqueue}},
		{name: "queue: enqueue no answer, queued by the owner: queued", base: queue, before: func(f *fake) { f.mode[enqueue], f.land[enqueue] = answer502, true },
			kind: "queued", step: "reconcile merge", method: "queue", muts: []string{approve, enqueue}, waits: waitFirst},
		{name: "queue: enqueue no answer, merged by the queue for the owner: merged with the queue's method", base: queue, before: func(f *fake) {
			f.mode[enqueue], f.land[enqueue] = answer502, true
			f.afterEnqueue = func(pr map[string]any) {
				pr["state"], pr["isInMergeQueue"], pr["mergeQueueEntry"] = "MERGED", false, nil
				pr["mergedBy"] = map[string]any{"login": Login}
				pr["mergeCommit"] = map[string]any{"oid": mergeOid}
			}
		}, kind: "merged", step: "reconcile merge", method: "squash", commit: mergeOid, muts: []string{approve, enqueue}, waits: waitFirst},
		{name: "queue: enqueue no answer, queued by another: unknown", base: queue, before: func(f *fake) {
			f.mode[enqueue], f.land[enqueue] = answer502, true
			f.afterEnqueue = func(pr map[string]any) { obj(pr, "mergeQueueEntry", "enqueuer")["login"] = "someone-else" }
		}, kind: "unknown", step: "reconcile merge", muts: []string{approve, enqueue}, waits: waits3},
		{name: "queue: enqueue no answer, queued at another head: unknown", base: queue, before: func(f *fake) {
			f.mode[enqueue], f.land[enqueue] = answerDrop, true
			f.afterEnqueue = func(pr map[string]any) { obj(pr, "mergeQueueEntry", "headCommit")["oid"] = newHead }
		}, kind: "unknown", step: "reconcile merge", muts: []string{approve, enqueue}, waits: waits3},
		// Changed between the preview and the click.
		{name: "head changed since the preview: refused", base: direct, change: func(f *fake) { f.setHead(newHead) },
			kind: "refused", step: "read", reason: "new commit since you confirmed"},
		{name: "base changed since the preview: refused", base: direct, change: func(f *fake) { f.pr()["baseRefName"] = "develop" },
			kind: "refused", step: "read", reason: "the base branch changed to develop"},
		{name: "method changed since the preview: refused", base: direct, change: func(f *fake) { f.repo()["viewerDefaultMergeMethod"] = "SQUASH" },
			kind: "refused", step: "read", reason: "the merge method changed"},
		{name: "queue added since the preview: refused", base: direct, change: func(f *fake) {
			f.pr()["isMergeQueueEnabled"] = true
			f.rules = append(f.rules, queueRule("SQUASH"))
		},
			kind: "refused", step: "read", reason: "the base has a merge queue now"},
		{name: "queue removed since the preview: refused", base: queue, change: func(f *fake) {
			f.pr()["isMergeQueueEnabled"] = false
			var rules []any
			for _, r := range f.rules {
				if r.(map[string]any)["type"] != "merge_queue" {
					rules = append(rules, r)
				}
			}
			f.rules = rules
		}, kind: "refused", step: "read", reason: "the merge queue is off now"},
		{name: "risk:high added since the preview: refused", base: direct, change: riskHigh,
			kind: "refused", step: "read", reason: "risk:high was added since you confirmed"},
		{name: "method not allowed now: refused", base: direct, change: func(f *fake) { f.repo()["mergeCommitAllowed"] = false },
			kind: "refused", step: "read", reason: "is not allowed here"},
		// risk:high.
		{name: "risk:high, single, diff opened at the head: merged", base: direct, before: riskHigh,
			intent: func(in *Intent) { in.AckHead = in.Preview.Head }, kind: "merged", step: "merge", muts: []string{approve, merge}},
		{name: "risk:high, single, no diff opened: refused", base: direct, before: riskHigh,
			kind: "refused", step: "read", reason: "open the diff at 3721d27 first"},
		{name: "risk:high, single, diff at another head: refused", base: direct, before: riskHigh,
			intent: func(in *Intent) { in.AckHead = newHead }, kind: "refused", step: "read", reason: "open the diff"},
		{name: "risk:high in a batch: refused", base: direct, before: riskHigh,
			intent: func(in *Intent) { in.Mode, in.AckHead = "batch", in.Preview.Head }, kind: "refused", step: "read", reason: "never in a batch"},
		{name: "batch without risk:high: merged", base: direct, intent: func(in *Intent) { in.Mode = "batch" },
			kind: "merged", step: "merge", muts: []string{approve, merge}},
		// The journal callback.
		{name: "step fails before the approval: refused, nothing sent", base: direct, stepFail: "about to approve",
			kind: "refused", step: "approve", reason: "journal write failed", steps: []string{"about to approve", "refused"}},
		{name: "step fails before the merge: approved, no merge", base: direct, stepFail: "about to merge",
			kind: "approved", step: "merge", reason: "journal write failed", muts: []string{approve},
			steps: []string{"about to approve", "about to merge", "approved"}},
		{name: "step fails before the merge, approval skipped: approved, nothing sent", base: direct, stepFail: "about to merge",
			before: approvedBefore, kind: "approved", step: "merge", reason: "journal write failed"},
		{name: "step fails at the end: ignored", base: direct, stepFail: "merged", kind: "merged", step: "merge", muts: []string{approve, merge}},
		// Other.
		{name: "squash: the repository's default for the owner", base: direct, before: func(f *fake) { f.repo()["viewerDefaultMergeMethod"] = "SQUASH" },
			kind: "merged", step: "merge", method: "squash", muts: []string{approve, merge},
			check: func(t *testing.T, f *fake, _ Result) {
				if f.inputs[1]["mergeMethod"] != "SQUASH" {
					t.Errorf("inputs %v", f.inputs)
				}
			}},
		{name: "not eligible at the preview: refused, no request", base: direct, intent: func(in *Intent) { in.Preview.Eligible = false },
			kind: "refused", step: "read", check: func(t *testing.T, f *fake, _ Result) {
				if f.requests != 0 {
					t.Errorf("%d requests", f.requests)
				}
			}},
		{name: "unknown mode: refused", base: direct, intent: func(in *Intent) { in.Mode = "" }, kind: "refused", step: "read", reason: "unknown mode"},
		{name: "the token GitHub echoes is redacted", base: direct, before: func(f *fake) { f.mode[approve], f.echoToken = answer4xx, true },
			kind: "failed", step: "approve", reason: "[token]", muts: []string{approve}},
	} {
		t.Run(tc.name, func(t *testing.T) { runAct(t, tc) })
	}
}

// runAct previews, freezes the intent, changes the fake, and acts.
func runAct(t *testing.T, tc actCase) {
	t.Helper()
	f := tc.base(t)
	if tc.before != nil {
		tc.before(f)
	}
	c := f.client()
	p, err := c.Preview(context.Background(), f.ref)
	if err != nil || !p.Eligible {
		t.Fatalf("preview %+v, %v", p, err)
	}
	in := Intent{Preview: p, Mode: "single"}
	if tc.intent != nil {
		tc.intent(&in)
	}
	f.reset()
	if tc.change != nil {
		tc.change(f)
	}
	var steps []string
	res := c.Act(context.Background(), in, "op-1", func(name string) error {
		steps = append(steps, name)
		if tc.onStep != nil {
			tc.onStep(c, name)
		}
		if name == tc.stepFail {
			return errors.New("disk full")
		}
		return nil
	})
	if res.Op != "op-1" || res.Kind != tc.kind || res.Step != tc.step || !strings.Contains(res.Reason, tc.reason) {
		t.Fatalf("result %+v; want %s at %s with %q", res, tc.kind, tc.step, tc.reason)
	}
	if tc.method != "" && res.Method != tc.method {
		t.Errorf("method %q, want %q", res.Method, tc.method)
	}
	if tc.commit != "" && res.MergeCommit != tc.commit {
		t.Errorf("merge commit %q", res.MergeCommit)
	}
	if !reflect.DeepEqual(f.mutations, tc.muts) && (len(f.mutations) != 0 || len(tc.muts) != 0) {
		t.Errorf("mutations %v, want %v", f.mutations, tc.muts)
	}
	if !reflect.DeepEqual(f.waits, tc.waits) && (len(f.waits) != 0 || len(tc.waits) != 0) {
		t.Errorf("waits %v, want %v", f.waits, tc.waits)
	}
	if tc.steps != nil && !reflect.DeepEqual(steps, tc.steps) {
		t.Errorf("steps %v, want %v", steps, tc.steps)
	}
	if last := steps[len(steps)-1]; last != res.Kind {
		t.Errorf("the last step is %q, not the result", last)
	}
	if strings.Contains(res.Reason, testToken) || (res.Err != nil && strings.Contains(res.Err.Error(), testToken)) {
		t.Errorf("the token is in %+v", res)
	}
	if tc.reads != 0 && f.reads != tc.reads {
		t.Errorf("%d PR reads, want %d", f.reads, tc.reads)
	}
	if tc.check != nil {
		tc.check(t, f, res)
	}
}

func TestActNilStep(t *testing.T) {
	f := direct(t)
	c := f.client()
	p, err := c.Preview(context.Background(), f.ref)
	if err != nil {
		t.Fatal(err)
	}
	if res := c.Act(context.Background(), Intent{Preview: p, Mode: "single"}, "op-2", nil); res.Kind != "merged" {
		t.Fatalf("%+v", res)
	}
}

func TestActCanceledWhileReconciling(t *testing.T) {
	f := direct(t)
	f.mode[merge] = answer502
	c := f.client()
	p, err := c.Preview(context.Background(), f.ref)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.sleep = func(context.Context, time.Duration) error { cancel(); return context.Canceled }
	res := c.Act(ctx, Intent{Preview: p, Mode: "single"}, "op-3", nil)
	if res.Kind != "unknown" || res.Step != "reconcile merge" {
		t.Fatalf("%+v", res)
	}
}
