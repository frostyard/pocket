package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBypassQueryTextIsTheRecordedQuery holds bypassQuery to the text the
// recording script sent, as preview_test.go holds the PR query.
func TestBypassQueryTextIsTheRecordedQuery(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "bypass-query.graphql"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimRight(bypassQuery, "\n") != strings.TrimRight(string(b), "\n") {
		t.Fatal("bypass.go's bypass query differs from testdata/bypass-query.graphql")
	}
}

// TestQueueExitQueryTextIsTheRecordedQuery holds queueExitQuery to the text the
// recording script sent, as bypass_test.go holds the bypass query.
func TestQueueExitQueryTextIsTheRecordedQuery(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "queue-exit-query.graphql"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimRight(queueExitQuery, "\n") != strings.TrimRight(string(b), "\n") {
		t.Fatal("reads.go's queue-exit query differs from testdata/queue-exit-query.graphql")
	}
}

// prParams is a pull_request rule's parameters, every requirement off,
// changed by them.
func prParams(with map[string]any) map[string]any {
	p := map[string]any{
		"required_approving_review_count":                 0,
		"dismiss_stale_reviews_on_push":                   false,
		"required_reviewers":                              []any{},
		"require_code_owner_review":                       false,
		"require_last_push_approval":                      false,
		"required_review_thread_resolution":               false,
		"require_extra_approval_for_unattributed_changes": false,
		"allowed_merge_methods":                           []any{"merge", "squash", "rebase"},
	}
	for k, v := range with {
		p[k] = v
	}
	return p
}

// rule is 1 rule of the direct base's bypassable ruleset (25664287).
func rule(typ string, params map[string]any) map[string]any {
	r := map[string]any{"type": typ, "ruleset_id": 25664287.0}
	if params != nil {
		r["parameters"] = params
	}
	return r
}

// addOpenPR adds an open PR of the repository, at head, to the bypass
// answer.
func addOpenPR(f *fake, number int, head string) {
	f.onBypass = func(repo map[string]any) {
		open := obj(repo, "pullRequests")
		open["nodes"] = append(open["nodes"].([]any), map[string]any{
			"number": number, "headRefOid": head,
		})
	}
}

// addBypassReview adds an opinionated review to the recorded bypass answer,
// for the reads after this 1.
func (f *fake) addBypassReview(state, login, oid string, canPush bool) {
	reviews := obj(f.bypassPR, "data", "repository", "pullRequest", "latestOpinionatedReviews")
	reviews["nodes"] = append(reviews["nodes"].([]any), map[string]any{
		"state": state, "authorCanPushToRepository": canPush,
		"author": map[string]any{"login": login}, "commit": map[string]any{"oid": oid},
	})
}

// noProofReads holds the reads to what a base with no bypass sends: none of
// the 2 REST reads of the proof.
func noProofReads(t *testing.T, f *fake) {
	t.Helper()
	for _, p := range f.gets {
		if strings.Contains(p, "/activity") || strings.Contains(p, "/compare/") {
			t.Errorf("a proof read: %s", p)
		}
	}
}

// activity is 1 entry of the branch activity page: a push of head behind
// the direct base's branch, by actor.
func activity(head, kind, actor string) string {
	b, _ := json.Marshal([]any{map[string]any{
		"ref": "refs/heads/sheeana/t1759-t1-merge-receipts", "after": head,
		"activity_type": kind, "actor": map[string]any{"login": actor},
	}})
	return string(b)
}

// bypassCase is 1 base the owner can bypass, and what the gates say of it.
type bypassCase struct {
	name string
	base func(*testing.T) *fake
	// mode is every ruleset's bypass; "" is "always".
	mode   string
	rules  []any
	change func(*fake)
	// reason is in the card's refusal; a name Pocket does not check is
	// enough in it.
	reason string
	method string
}

func TestBypassGates(t *testing.T) {
	queueBase := func(t *testing.T) *fake { return queue(t) }
	// The queue's ruleset is the 1 the owner can bypass: 24434288 stays never.
	queueAlways := func(f *fake) { f.rulesets["21022424"].(map[string]any)["current_user_can_bypass"] = "always" }
	for _, tc := range []bypassCase{
		// Each rule type Pocket proves.
		{name: "linear history with merge", rules: []any{rule("required_linear_history", nil)},
			reason: "the rules need a linear history: no merge commit"},
		{name: "linear history with squash", method: "squash", rules: []any{rule("required_linear_history", nil)},
			change: func(f *fake) { f.repo()["viewerDefaultMergeMethod"] = "SQUASH" }},
		{name: "allowed methods without the method", rules: []any{rule("pull_request", prParams(map[string]any{
			"allowed_merge_methods": []any{"SQUASH"},
		}))}, reason: "the rules do not allow the merge method"},
		{name: "allowed methods with the method, in another case", method: "merge", rules: []any{rule("pull_request", prParams(map[string]any{
			"allowed_merge_methods": []any{"MERGE"},
		}))}},
		{name: "threads, 2 unresolved", rules: []any{rule("pull_request", prParams(map[string]any{
			"required_review_thread_resolution": true,
		}))}, change: func(f *fake) {
			obj(f.bypassPR, "data", "repository", "pullRequest", "reviewThreads")["nodes"] = []any{
				map[string]any{"isResolved": false}, map[string]any{"isResolved": false},
			}
		}, reason: "2 review threads are not resolved"},
		{name: "threads, 1 unresolved", rules: []any{rule("pull_request", prParams(map[string]any{
			"required_review_thread_resolution": true,
		}))}, change: func(f *fake) {
			obj(f.bypassPR, "data", "repository", "pullRequest", "reviewThreads")["nodes"] = []any{
				map[string]any{"isResolved": false}, map[string]any{"isResolved": true},
			}
		}, reason: "1 review thread is not resolved"},
		{name: "threads, none unresolved", method: "merge", rules: []any{rule("pull_request", prParams(map[string]any{
			"required_review_thread_resolution": true,
		}))}},
		{name: "strict, behind", rules: []any{rule("required_status_checks", map[string]any{
			"strict_required_status_checks_policy": true,
			"required_status_checks":               []any{},
		})}, change: func(f *fake) { f.behind = 3 }, reason: "the PR is behind its base"},
		{name: "strict, not behind", method: "merge", rules: []any{rule("required_status_checks", map[string]any{
			"strict_required_status_checks_policy": true,
			"required_status_checks":               []any{},
		})}},
		{name: "strict, do_not_enforce_on_create", method: "merge", rules: []any{rule("required_status_checks", map[string]any{
			"strict_required_status_checks_policy": true,
			"do_not_enforce_on_create":             true,
			"required_status_checks":               []any{},
		})}},
		{name: "strict on a queue base reads no compare", base: queueBase, method: "queue",
			change: queueAlways},
		// The last push.
		{name: "last push by the owner", rules: []any{rule("pull_request", prParams(map[string]any{
			"require_last_push_approval": true,
		}))}, change: func(f *fake) {
			f.activityRaw = activity(f.head(), "push", Login)
		}, reason: "you pushed last, so your approval does not count"},
		{name: "last push by another", method: "merge", rules: []any{rule("pull_request", prParams(map[string]any{
			"require_last_push_approval": true,
		}))}},
		{name: "last push, the activity names another commit", rules: []any{rule("pull_request", prParams(map[string]any{
			"require_last_push_approval": true,
		}))}, change: func(f *fake) {
			f.activityRaw = activity(newHead, "push", "frostyard-machines")
		}, reason: "Pocket cannot prove who pushed last"},
		{name: "last push, no activity", rules: []any{rule("pull_request", prParams(map[string]any{
			"require_last_push_approval": true,
		}))}, change: func(f *fake) { f.activityRaw = "[]" }, reason: "Pocket cannot prove who pushed last"},
		{name: "last push, another kind of event", rules: []any{rule("pull_request", prParams(map[string]any{
			"require_last_push_approval": true,
		}))}, change: func(f *fake) {
			f.activityRaw = activity(f.head(), "branch_deletion", "frostyard-machines")
		}, reason: "Pocket cannot prove who pushed last"},
		{name: "last push, cross repository", rules: []any{rule("pull_request", prParams(map[string]any{
			"require_last_push_approval": true,
		}))}, change: func(f *fake) {
			obj(f.bypassPR, "data", "repository", "pullRequest")["isCrossRepository"] = true
		}, reason: "Pocket cannot prove who pushed last"},
		// The approvals.
		{name: "2 approvals, 1 other at the head", method: "merge", rules: []any{rule("pull_request", prParams(map[string]any{
			"required_approving_review_count": 2,
		}))}, change: func(f *fake) { f.addBypassReview("APPROVED", "someone", f.head(), true) }},
		{name: "2 approvals, none other at the head", rules: []any{rule("pull_request", prParams(map[string]any{
			"required_approving_review_count": 2,
		}))}, reason: "the rules need 2 approvals at the head; Pocket counts 1"},
		{name: "an approval at an old head does not count", rules: []any{rule("pull_request", prParams(map[string]any{
			"required_approving_review_count": 2,
		}))}, change: func(f *fake) { f.addBypassReview("APPROVED", "someone", newHead, true) },
			reason: "Pocket counts 1"},
		{name: "an approval by someone who cannot push does not count", rules: []any{rule("pull_request", prParams(map[string]any{
			"required_approving_review_count": 2,
		}))}, change: func(f *fake) { f.addBypassReview("APPROVED", "someone", f.head(), false) },
			reason: "Pocket counts 1"},
		{name: "an approval and a change requested: the latest is the change", rules: []any{rule("pull_request", prParams(map[string]any{
			"required_approving_review_count": 2,
		}))}, change: func(f *fake) { f.addBypassReview("CHANGES_REQUESTED", "someone", f.head(), true) },
			reason: "Pocket counts 1"},
		{name: "extra approval for an unattributed change", rules: []any{rule("pull_request", prParams(map[string]any{
			"required_approving_review_count":                 1,
			"require_extra_approval_for_unattributed_changes": true,
		}))}, change: func(f *fake) {
			obj(f.bypassPR, "data", "repository", "pullRequest", "author")["__typename"] = "Bot"
		}, reason: "the rules need 2 approvals at the head; Pocket counts 1"},
		{name: "no extra approval for a user", method: "merge", rules: []any{rule("pull_request", prParams(map[string]any{
			"required_approving_review_count":                 1,
			"require_extra_approval_for_unattributed_changes": true,
		}))}},
		// Rules and keys Pocket does not check.
		{name: "code owner review", rules: []any{rule("pull_request", prParams(map[string]any{
			"require_code_owner_review": true,
		}))}, reason: "you can bypass rule pull_request.require_code_owner_review here, and Pocket does not check it itself"},
		{name: "named reviewers", rules: []any{rule("pull_request", prParams(map[string]any{
			"required_reviewers": []any{map[string]any{"id": 1}},
		}))}, reason: "you can bypass rule pull_request.required_reviewers here"},
		{name: "an unknown key that is on", rules: []any{rule("pull_request", prParams(map[string]any{
			"some_future_setting": true,
		}))}, reason: "you can bypass rule pull_request.some_future_setting here"},
		{name: "an unknown key that is off", method: "merge", rules: []any{rule("pull_request", prParams(map[string]any{
			"some_future_setting": false,
			"another_one":         nil,
			"a_list":              []any{},
			"an_object":           map[string]any{},
			"a_count":             0,
			"a_text":              "",
		}))}},
		{name: "a known key of the wrong type", rules: []any{rule("pull_request", prParams(map[string]any{
			"require_last_push_approval": "true",
		}))}, reason: "you can bypass rule pull_request.require_last_push_approval here"},
		{name: "no parameters to read", rules: []any{rule("pull_request", nil)},
			reason: "you can bypass rule pull_request here"},
		// A rule type Pocket checks nothing of.
		{name: "deletion", method: "merge", rules: []any{rule("deletion", nil)}},
		{name: "non_fast_forward", method: "merge", rules: []any{rule("non_fast_forward", nil)}},
		{name: "creation", method: "merge", rules: []any{rule("creation", nil)}},
		{name: "merge queue", method: "queue", rules: []any{rule("merge_queue", map[string]any{"merge_method": "SQUASH"})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.base
			if f == nil {
				f = direct
			}
			fk := f(t)
			if tc.rules != nil {
				fk.rules = tc.rules
			}
			mode := tc.mode
			if mode == "" {
				mode = "always"
			}
			bypassAll(fk, mode)
			if tc.change != nil {
				tc.change(fk)
			}
			p, err := fk.client().Preview(context.Background(), fk.ref)
			want := tc.reason
			if want != "" {
				if err != nil {
					t.Fatal(err)
				}
				if p.Eligible || !strings.Contains(p.Reason, want) {
					t.Fatalf("eligible %v, reason %q; want %q", p.Eligible, p.Reason, want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !p.Eligible || p.Reason != "" {
				t.Fatalf("eligible %v, reason %q", p.Eligible, p.Reason)
			}
			if tc.method != "" && p.Method != tc.method {
				t.Fatalf("method %q, want %q", p.Method, tc.method)
			}
			if p.Note != PreviewNote {
				t.Fatalf("note %q", p.Note)
			}
			if len(fk.mutations) != 0 {
				t.Fatalf("a preview sent %v", fk.mutations)
			}
		})
	}
	// A queue base with a strict rule reads no compare: the queue brings the
	// branch up to date itself.
	f := queue(t)
	queueAlways(f)
	r, err := f.client().Rules(context.Background(), f.ref, "main")
	if err != nil || !r.Proof.Strict || r.Bypass != "always" {
		t.Fatalf("rules %+v, %v", r, err)
	}
	if _, err := f.client().Preview(context.Background(), f.ref); err != nil {
		t.Fatal(err)
	}
	for _, p := range f.gets {
		if strings.Contains(p, "/compare/") {
			t.Fatalf("a queue base read the compare: %s", p)
		}
	}
}

// TestBypassUnknownRule: a rule type or key Pocket does not check refuses and
// names itself; with no bypass the same rule is no proof any rule needs.
func TestBypassUnknownRule(t *testing.T) {
	for _, typ := range []string{"required_deployments", "some_future_rule", "update"} {
		t.Run(typ, func(t *testing.T) {
			f := direct(t)
			f.rules = []any{rule(typ, map[string]any{"anything": true})}
			bypassAll(f, "always")
			p, err := f.client().Preview(context.Background(), f.ref)
			if err != nil {
				t.Fatal(err)
			}
			if p.Eligible || !strings.Contains(p.Reason, "you can bypass rule "+typ+" here") {
				t.Fatalf("eligible %v, reason %q", p.Eligible, p.Reason)
			}
			// The same rule, with no bypass: ignored, as today.
			f = direct(t)
			f.rules = []any{rule(typ, map[string]any{"anything": true})}
			p, err = f.client().Preview(context.Background(), f.ref)
			if err != nil || !p.Eligible || p.Note != "" {
				t.Fatalf("no bypass: %+v, %v", p, err)
			}
			if f.bypassReads != 0 {
				t.Errorf("%d bypass reads with no bypass", f.bypassReads)
			}
			noProofReads(t, f)
		})
	}
}

// TestBypassAct: with every rule proven, the 3 mutations and the 2 reads go
// as they do on a base with no bypass: a merge, or a queue entry.
func TestBypassAct(t *testing.T) {
	for _, tc := range []struct {
		name string
		base func(*testing.T) *fake
		mode string
		kind string
		step string
		muts []string
	}{
		{name: "always, direct: merged", base: direct, mode: "always", kind: "merged", step: "merge", muts: []string{approve, merge}},
		{name: "always, queue: queued", base: queue, mode: "always", kind: "queued", step: "merge", muts: []string{approve, enqueue}},
		{name: "pull_requests_only, direct: merged", base: direct, mode: "pull_requests_only", kind: "merged", step: "merge", muts: []string{approve, merge}},
		{name: "pull_requests_only, queue: queued", base: queue, mode: "pull_requests_only", kind: "queued", step: "merge", muts: []string{approve, enqueue}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.base(t)
			bypassAll(f, tc.mode)
			c := f.client()
			p, err := c.Preview(context.Background(), f.ref)
			if err != nil || !p.Eligible || p.Note != PreviewNote {
				t.Fatalf("preview %+v, %v", p, err)
			}
			f.reset()
			res := c.Act(context.Background(), Intent{Preview: p, Mode: "single"}, "op-bypass", nil)
			if res.Kind != tc.kind || res.Step != tc.step {
				t.Fatalf("result %+v", res)
			}
			if strings.Join(f.mutations, ",") != strings.Join(tc.muts, ",") {
				t.Fatalf("mutations %v, want %v", f.mutations, tc.muts)
			}
			// 1 bypass read before the approval, 1 after it.
			if f.bypassReads != 2 {
				t.Errorf("%d bypass reads, want 2", f.bypassReads)
			}
		})
	}
}

// TestBypassActFailedGate: a gate that fails still refuses on a base the
// owner can bypass, before and after the approval.
func TestBypassActFailedGate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*fake)
		kind    string
		step    string
		reason  string
		preview bool // the preview itself refuses
	}{
		{name: "a failed check", change: func(f *fake) {
			f.setChecks(checkRun("Test", "COMPLETED", "SUCCESS", 15368), checkRun("e2e", "COMPLETED", "FAILURE", 15368))
		}, reason: "check e2e: failure", preview: true},
		{name: "conflicting", change: func(f *fake) { f.pr()["mergeable"] = "CONFLICTING" },
			reason: "the PR has conflicts", preview: true},
		{name: "an unresolved thread", change: func(f *fake) {
			f.rules = []any{rule("pull_request", prParams(map[string]any{"required_review_thread_resolution": true}))}
			obj(f.bypassPR, "data", "repository", "pullRequest", "reviewThreads")["nodes"] = []any{map[string]any{"isResolved": false}}
		}, reason: "1 review thread is not resolved", preview: true},
		// After the approval: the owner's approval is not in the bypass
		// answer, so Pocket counts no approval at the head.
		{name: "the owner's approval missing after the approval", change: func(f *fake) {
			f.onBypass = func(repo map[string]any) {
				obj(repo, "pullRequest", "latestOpinionatedReviews")["nodes"] = []any{}
			}
		}, kind: "approved", step: "read 2", reason: "the rules need 1 approvals at the head; Pocket counts 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := direct(t)
			bypassAll(f, "always")
			if tc.preview {
				tc.change(f)
				p, err := f.client().Preview(context.Background(), f.ref)
				if err != nil {
					t.Fatal(err)
				}
				if p.Eligible || !strings.Contains(p.Reason, tc.reason) {
					t.Fatalf("eligible %v, reason %q; want %q", p.Eligible, p.Reason, tc.reason)
				}
				return
			}
			c := f.client()
			p, err := c.Preview(context.Background(), f.ref)
			if err != nil || !p.Eligible {
				t.Fatalf("preview %+v, %v", p, err)
			}
			tc.change(f)
			res := c.Act(context.Background(), Intent{Preview: p, Mode: "single"}, "op-gate", nil)
			if res.Kind != tc.kind || res.Step != tc.step || !strings.Contains(res.Reason, tc.reason) {
				t.Fatalf("result %+v; want %s at %s with %q", res, tc.kind, tc.step, tc.reason)
			}
		})
	}
}

// TestNoBypassReadsNothing: a base with no bypass sends exactly the reads of
// today, and no proof read; a value Pocket does not handle refuses as today.
func TestNoBypassReadsNothing(t *testing.T) {
	for _, base := range []func(*testing.T) *fake{direct, queue} {
		f := base(t)
		f.reset()
		p, err := f.client().Preview(context.Background(), f.ref)
		if err != nil || !p.Eligible || p.Note != "" {
			t.Fatalf("preview %+v, %v", p, err)
		}
		if f.bypassReads != 0 {
			t.Errorf("%d bypass reads with no bypass", f.bypassReads)
		}
		// Today's reads, and no other: the rules, 1 page of them, the 2
		// rulesets and the branch.
		if f.reads != 1 || len(f.gets) != 4 {
			t.Errorf("%d PR reads, gets %v", f.reads, f.gets)
		}
		noProofReads(t, f)
	}
	for _, mode := range []string{"exempt", "unknown", "no_one", ""} {
		for _, base := range []func(*testing.T) *fake{direct, queue} {
			f := base(t)
			if mode == "" {
				for _, rs := range f.rulesets {
					delete(rs.(map[string]any), "current_user_can_bypass")
				}
			} else {
				bypassAll(f, mode)
			}
			want := mode
			if mode == "" {
				want = "unknown"
			}
			p, err := f.client().Preview(context.Background(), f.ref)
			if err != nil {
				t.Fatal(err)
			}
			if p.Eligible || p.Reason != "you can bypass a rule here ("+want+"): Pocket refuses to merge" {
				t.Fatalf("%s: eligible %v, reason %q", want, p.Eligible, p.Reason)
			}
			if f.bypassReads != 0 {
				t.Errorf("%s: %d bypass reads", want, f.bypassReads)
			}
			noProofReads(t, f)
		}
	}
}

// TestBypassMixedRulesets: only the rules of a ruleset the owner can bypass
// are proven; the rules of a ruleset he cannot act on are not Pocket's
// business, and 1 refusing value wins over the bypass.
func TestBypassMixedRulesets(t *testing.T) {
	codeOwners := func(id float64) map[string]any {
		return map[string]any{"type": "pull_request", "ruleset_id": id, "parameters": prParams(map[string]any{
			"require_code_owner_review": true,
		})}
	}
	// The rule sits in a ruleset the owner cannot bypass: never, else
	// nothing.
	deletion := map[string]any{"type": "deletion", "ruleset_id": 25664287.0}
	f := direct(t)
	f.rules = []any{codeOwners(26867289.0), deletion}
	f.rulesets["25664287"].(map[string]any)["current_user_can_bypass"] = "always"
	p, err := f.client().Preview(context.Background(), f.ref)
	if err != nil || !p.Eligible || p.Note != PreviewNote || f.bypassReads != 1 {
		t.Fatalf("%+v, %v, %d bypass reads", p, err, f.bypassReads)
	}
	// The same rule in the ruleset he can bypass: refused.
	f = direct(t)
	f.rules = []any{codeOwners(25664287.0)}
	f.rulesets["25664287"].(map[string]any)["current_user_can_bypass"] = "always"
	p, err = f.client().Preview(context.Background(), f.ref)
	if err != nil || p.Eligible || !strings.Contains(p.Reason, "pull_request.require_code_owner_review") {
		t.Fatalf("%+v, %v", p, err)
	}
	// 1 refusing value wins over a bypassable 1, whatever the order, and a
	// bypassable 1 that comes first is the answer when every ruleset is
	// bypassable.
	for _, tc := range []struct {
		name  string
		ids   []int64
		modes map[int64]string
		want  string
	}{
		{name: "never and always", ids: []int64{1, 2}, modes: map[int64]string{1: "never", 2: "always"}, want: "always"},
		{name: "always and never", ids: []int64{1, 2}, modes: map[int64]string{1: "always", 2: "never"}, want: "always"},
		{name: "pull_requests_only and always", ids: []int64{1, 2}, modes: map[int64]string{1: "pull_requests_only", 2: "always"}, want: "pull_requests_only"},
		{name: "always and exempt", ids: []int64{1, 2}, modes: map[int64]string{1: "always", 2: "exempt"}, want: "exempt"},
		{name: "exempt and always", ids: []int64{1, 2}, modes: map[int64]string{1: "exempt", 2: "always"}, want: "exempt"},
		{name: "unknown and always", ids: []int64{1, 2}, modes: map[int64]string{1: "unknown", 2: "always"}, want: "unknown"},
		{name: "never and never", ids: []int64{1, 2}, modes: map[int64]string{1: "never", 2: "never"}, want: "never"},
		{name: "no ruleset", ids: nil, modes: nil, want: "never"},
		{name: "1 unknown", ids: []int64{1}, modes: map[int64]string{1: "unknown"}, want: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := bypassOf(tc.ids, tc.modes); got != tc.want {
				t.Fatalf("bypass %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBypassFactsFailClosed: every answer that does not prove a fact refuses
// the read, and so the card.
func TestBypassFactsFailClosed(t *testing.T) {
	pr := func(f *fake) map[string]any { return obj(f.bypassPR, "data", "repository", "pullRequest") }
	strictless := func(f *fake) {
		f.rules = []any{rule("required_status_checks", map[string]any{
			"strict_required_status_checks_policy": true,
			"required_status_checks":               []any{},
		})}
	}
	for _, tc := range []struct {
		name   string
		change func(*fake)
		text   string
	}{
		{name: "no pullRequest", change: func(f *fake) { f.bypassPR = map[string]any{} }, text: "pullRequest is null"},
		{name: "null headRefOid", change: func(f *fake) {
			f.onBypass = func(repo map[string]any) { obj(repo, "pullRequest")["headRefOid"] = nil }
		}, text: "headRefOid is null"},
		{name: "null headRefName", change: func(f *fake) { pr(f)["headRefName"] = nil }, text: "headRefName is null"},
		{name: "null isCrossRepository", change: func(f *fake) { pr(f)["isCrossRepository"] = nil }, text: "isCrossRepository is null"},
		{name: "null author __typename", change: func(f *fake) { obj(pr(f), "author")["__typename"] = nil }, text: "author.__typename is null"},
		{name: "null reviewThreads", change: func(f *fake) { pr(f)["reviewThreads"] = nil }, text: "reviewThreads is null"},
		{name: "null reviewThreads nodes", change: func(f *fake) { obj(pr(f), "reviewThreads")["nodes"] = nil }, text: "reviewThreads is null"},
		{name: "null reviewThreads pageInfo", change: func(f *fake) { obj(pr(f), "reviewThreads")["pageInfo"] = nil }, text: "reviewThreads is null"},
		{name: "null reviewThreads hasNextPage", change: func(f *fake) { obj(pr(f), "reviewThreads", "pageInfo")["hasNextPage"] = nil }, text: "reviewThreads.pageInfo.hasNextPage is null"},
		{name: "too many review threads", change: func(f *fake) { obj(pr(f), "reviewThreads", "pageInfo")["hasNextPage"] = true }, text: "too many review threads"},
		{name: "a null review thread", change: func(f *fake) { obj(pr(f), "reviewThreads")["nodes"] = []any{nil} }, text: "a review thread is null"},
		{name: "a null isResolved", change: func(f *fake) {
			obj(pr(f), "reviewThreads")["nodes"] = []any{map[string]any{"isResolved": nil}}
		}, text: "a review thread is null"},
		{name: "null reviews", change: func(f *fake) { pr(f)["latestOpinionatedReviews"] = nil }, text: "latestOpinionatedReviews is null"},
		{name: "null reviews pageInfo", change: func(f *fake) { obj(pr(f), "latestOpinionatedReviews")["pageInfo"] = nil }, text: "latestOpinionatedReviews is null"},
		{name: "null reviews nodes", change: func(f *fake) { obj(pr(f), "latestOpinionatedReviews")["nodes"] = nil }, text: "latestOpinionatedReviews is null"},
		{name: "null reviews hasNextPage", change: func(f *fake) {
			obj(pr(f), "latestOpinionatedReviews", "pageInfo")["hasNextPage"] = nil
		}, text: "latestOpinionatedReviews.pageInfo.hasNextPage is null"},
		{name: "too many reviews", change: func(f *fake) { obj(pr(f), "latestOpinionatedReviews", "pageInfo")["hasNextPage"] = true }, text: "too many reviews"},
		{name: "a null review", change: func(f *fake) { obj(pr(f), "latestOpinionatedReviews")["nodes"] = []any{nil} }, text: "a review is null"},
		{name: "a review with a null state", change: func(f *fake) {
			obj(pr(f), "latestOpinionatedReviews")["nodes"] = []any{map[string]any{
				"state": nil, "authorCanPushToRepository": true,
				"author": map[string]any{"login": "someone"}, "commit": map[string]any{"oid": "3721d27e51c1d046da5d955c6354bb6f1ce0e5f5"},
			}}
		}, text: "a review is null"},
		{name: "a review with a null author", change: func(f *fake) {
			obj(pr(f), "latestOpinionatedReviews")["nodes"] = []any{map[string]any{
				"state": "APPROVED", "authorCanPushToRepository": true,
				"author": nil, "commit": map[string]any{"oid": "3721d27e51c1d046da5d955c6354bb6f1ce0e5f5"},
			}}
		}, text: "a review is null"},
		{name: "a review with a null login", change: func(f *fake) {
			obj(pr(f), "latestOpinionatedReviews")["nodes"] = []any{map[string]any{
				"state": "APPROVED", "authorCanPushToRepository": true,
				"author": map[string]any{"login": nil}, "commit": map[string]any{"oid": "3721d27e51c1d046da5d955c6354bb6f1ce0e5f5"},
			}}
		}, text: "a review is null"},
		{name: "a review with a null canPush", change: func(f *fake) {
			obj(pr(f), "latestOpinionatedReviews")["nodes"] = []any{map[string]any{
				"state": "APPROVED", "authorCanPushToRepository": nil,
				"author": map[string]any{"login": "someone"}, "commit": map[string]any{"oid": "3721d27e51c1d046da5d955c6354bb6f1ce0e5f5"},
			}}
		}, text: "a review is null"},
		{name: "a review with a null commit", change: func(f *fake) {
			obj(pr(f), "latestOpinionatedReviews")["nodes"] = []any{map[string]any{
				"state": "APPROVED", "authorCanPushToRepository": true,
				"author": map[string]any{"login": "someone"}, "commit": nil,
			}}
		}, text: "a review is null"},
		{name: "a review with a null commit oid", change: func(f *fake) {
			obj(pr(f), "latestOpinionatedReviews")["nodes"] = []any{map[string]any{
				"state": "APPROVED", "authorCanPushToRepository": true,
				"author": map[string]any{"login": "someone"}, "commit": map[string]any{"oid": nil},
			}}
		}, text: "a review is null"},
		{name: "the head changed", change: func(f *fake) {
			f.onBypass = func(repo map[string]any) { obj(repo, "pullRequest")["headRefOid"] = newHead }
		}, text: "the head changed while reading"},
		{name: "null pullRequests", change: func(f *fake) {
			f.onBypass = func(repo map[string]any) { delete(repo, "pullRequests") }
		}, text: "pullRequests is null"},
		{name: "null pullRequests pageInfo", change: func(f *fake) {
			f.onBypass = func(repo map[string]any) { obj(repo, "pullRequests")["pageInfo"] = nil }
		}, text: "pullRequests is null"},
		{name: "null pullRequests nodes", change: func(f *fake) {
			f.onBypass = func(repo map[string]any) { obj(repo, "pullRequests")["nodes"] = nil }
		}, text: "pullRequests is null"},
		{name: "null pullRequests hasNextPage", change: func(f *fake) {
			f.onBypass = func(repo map[string]any) { obj(repo, "pullRequests", "pageInfo")["hasNextPage"] = nil }
		}, text: "pullRequests.pageInfo.hasNextPage is null"},
		{name: "too many open pull requests", change: func(f *fake) {
			f.onBypass = func(repo map[string]any) { obj(repo, "pullRequests", "pageInfo")["hasNextPage"] = true }
		}, text: "too many open pull requests"},
		{name: "a null open pull request", change: func(f *fake) {
			f.onBypass = func(repo map[string]any) { obj(repo, "pullRequests")["nodes"] = []any{nil} }
		}, text: "an open pull request is null"},
		{name: "an open PR with a null number", change: func(f *fake) {
			f.onBypass = func(repo map[string]any) {
				obj(repo, "pullRequests")["nodes"] = []any{map[string]any{"number": nil, "headRefOid": f.head()}}
			}
		}, text: "an open pull request is null"},
		{name: "an open PR with a null head", change: func(f *fake) {
			f.onBypass = func(repo map[string]any) {
				obj(repo, "pullRequests")["nodes"] = []any{map[string]any{"number": 999, "headRefOid": nil}}
			}
		}, text: "an open pull request is null"},
		{name: "a null activity page", change: func(f *fake) { f.activityRaw = "null" }, text: "the branch activity is null"},
		{name: "no base tip", change: func(f *fake) {
			strictless(f)
			delete(f.branch, "commit")
		}, text: "the base branch's commit is missing"},
		{name: "the head is no sha", change: func(f *fake) {
			strictless(f)
			f.onBypass = func(repo map[string]any) { obj(repo, "pullRequest")["headRefOid"] = "short" }
			f.pr()["headRefOid"] = "short"
		}, text: "the head commit is missing"},
		{name: "a null behind_by", change: func(f *fake) {
			strictless(f)
			f.restFail = func(path string) (int, string, http.Header) {
				if strings.Contains(path, "/compare/") {
					return 200, `{"status":"ahead","ahead_by":1}`, nil
				}
				return 0, "", nil
			}
		}, text: "the compare names no behind_by"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := direct(t)
			bypassAll(f, "always")
			tc.change(f)
			p, err := f.client().Preview(context.Background(), f.ref)
			if !errors.Is(err, ErrIncomplete) || !strings.Contains(err.Error(), tc.text) {
				t.Fatalf("err %v, want %v with %q", err, ErrIncomplete, tc.text)
			}
			if p.Eligible {
				t.Fatal("eligible on a failed read")
			}
		})
	}
}

// TestRecordedProofReadsDecode: the recorded answers of a base the owner can
// bypass decode with the client's own types.
func TestRecordedProofReadsDecode(t *testing.T) {
	for _, tc := range []struct{ dir, file string }{
		{"bketelsen-pi-pocket-team", "238"},
		{"frostyard-core", "256"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			var body struct {
				Data rawBypassAnswer `json:"data"`
			}
			if err := json.Unmarshal(readRecorded(t, tc.dir, "bypass-"+tc.file+".json"), &body); err != nil {
				t.Fatal(err)
			}
			a := body.Data
			if a.Repository == nil || a.Repository.PullRequest == nil {
				t.Fatal("no pull request")
			}
			pr := a.Repository.PullRequest
			if !isOID(*pr.HeadRefOid) || *pr.HeadRefName == "" || pr.IsCrossRepository == nil {
				t.Fatalf("head %v/%v, cross %v", *pr.HeadRefOid, *pr.HeadRefName, pr.IsCrossRepository)
			}
			if pr.ReviewThreads == nil || pr.ReviewThreads.Nodes == nil || pr.ReviewThreads.PageInfo.HasNextPage == nil {
				t.Fatal("no review threads")
			}
			if pr.LatestOpinionatedReviews == nil || pr.LatestOpinionatedReviews.Nodes == nil {
				t.Fatal("no reviews")
			}
			open := body.Data.Repository.PullRequests
			if open == nil || open.PageInfo == nil || open.PageInfo.HasNextPage == nil || open.Nodes == nil {
				t.Fatal("no open pull requests")
			}
			for _, o := range *open.Nodes {
				if o == nil || o.Number == nil || o.HeadRefOid == nil {
					t.Fatalf("an open pull request is null: %+v", o)
				}
			}

			var page *[]activityEntry
			if err := json.Unmarshal(readRecorded(t, tc.dir, "activity-"+tc.file+".json"), &page); err != nil {
				t.Fatal(err)
			}
			if page == nil || len(*page) == 0 {
				t.Fatal("no activity")
			}
			e := (*page)[0]
			if e.Ref == nil || e.After == nil || e.ActivityType == nil || e.Actor == nil || e.Actor.Login == nil {
				t.Fatalf("activity entry %+v", e)
			}

			var cmp struct {
				BehindBy *int `json:"behind_by"`
			}
			if err := json.Unmarshal(readRecorded(t, tc.dir, "compare-"+tc.file+".json"), &cmp); err != nil {
				t.Fatal(err)
			}
			if cmp.BehindBy == nil {
				t.Fatal("no behind_by")
			}
		})
	}
}

// readRecorded is 1 recorded answer of testdata.
func readRecorded(t *testing.T, dir, file string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", dir, file))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestBypassSameHead: another open PR whose head is the same commit can block
// the merge on its reviews, which Pocket does not read. The fake always lists
// the PR itself among the repository's open PRs.
func TestBypassSameHead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		base   func(*testing.T) *fake
		rules  []any
		number int
		head   func(*fake) string
		reason string
	}{
		{name: "another open PR at the head", number: 999,
			reason: "open PR #999 has the same head, and Pocket does not read its reviews"},
		{name: "another open PR at another head", number: 999,
			head: func(*fake) string { return newHead }},
		{name: "this PR itself", number: 238},
		// A base whose bypassed rules include no pull_request rule reads no
		// reviews, so another PR at the head proves nothing about them.
		{name: "no pull_request rule", rules: []any{rule("deletion", nil)}, number: 999},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := tc.base
			if base == nil {
				base = direct
			}
			f := base(t)
			if tc.rules != nil {
				f.rules = tc.rules
			}
			bypassAll(f, "always")
			head := f.head()
			if tc.head != nil {
				head = tc.head(f)
			}
			addOpenPR(f, tc.number, head)
			p, err := f.client().Preview(context.Background(), f.ref)
			if err != nil {
				t.Fatal(err)
			}
			if tc.reason != "" {
				if p.Eligible || !strings.Contains(p.Reason, tc.reason) {
					t.Fatalf("eligible %v, reason %q; want %q", p.Eligible, p.Reason, tc.reason)
				}
				return
			}
			if !p.Eligible || p.Reason != "" {
				t.Fatalf("eligible %v, reason %q", p.Eligible, p.Reason)
			}
		})
	}
}

// TestBypassRuleParameters: a rule whose parameters Pocket ignores still
// refuses a parameter it does not know that is on.
func TestBypassRuleParameters(t *testing.T) {
	for _, typ := range []string{"deletion", "non_fast_forward", "creation", "required_linear_history"} {
		t.Run(typ, func(t *testing.T) {
			squash := func(f *fake) { f.repo()["viewerDefaultMergeMethod"] = "SQUASH" }
			for _, tc := range []struct {
				name   string
				rules  []any
				change func(*fake)
				reason string
			}{
				{name: "a parameter Pocket does not know, on",
					rules:  []any{rule(typ, map[string]any{"some_future_parameter": true})},
					reason: "you can bypass rule " + typ + ".some_future_parameter here"},
				{name: "a parameter Pocket does not know, off", change: squash,
					rules: []any{rule(typ, map[string]any{"some_future_parameter": false, "a_null": nil, "a_count": 0})}},
				{name: "no parameters", change: squash, rules: []any{rule(typ, nil)}},
				{name: "empty parameters", change: squash, rules: []any{rule(typ, map[string]any{})}},
				{name: "parameters that are no object",
					change: squash,
					rules:  []any{map[string]any{"type": typ, "ruleset_id": 25664287.0, "parameters": []any{}}},
					reason: "you can bypass rule " + typ + " here"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					f := direct(t)
					f.rules = tc.rules
					bypassAll(f, "always")
					if tc.change != nil {
						tc.change(f)
					}
					p, err := f.client().Preview(context.Background(), f.ref)
					if err != nil {
						t.Fatal(err)
					}
					if tc.reason != "" {
						if p.Eligible || !strings.Contains(p.Reason, tc.reason) {
							t.Fatalf("eligible %v, reason %q; want %q", p.Eligible, p.Reason, tc.reason)
						}
						return
					}
					if !p.Eligible {
						t.Fatalf("reason %q", p.Reason)
					}
				})
			}
		})
	}
	t.Run("merge_queue", func(t *testing.T) {
		queueRule := func(extra map[string]any) map[string]any {
			p := map[string]any{"merge_method": "SQUASH", "max_entries_to_build": 5, "grouping_strategy": "ALLGREEN"}
			for k, v := range extra {
				p[k] = v
			}
			return p
		}
		f := direct(t)
		f.rules = []any{rule("merge_queue", queueRule(map[string]any{"some_future_parameter": true}))}
		bypassAll(f, "always")
		p, err := f.client().Preview(context.Background(), f.ref)
		if err != nil {
			t.Fatal(err)
		}
		if p.Eligible || !strings.Contains(p.Reason, "you can bypass rule merge_queue.some_future_parameter here") {
			t.Fatalf("eligible %v, reason %q", p.Eligible, p.Reason)
		}
		f = direct(t)
		f.rules = []any{rule("merge_queue", queueRule(map[string]any{"some_future_parameter": false}))}
		bypassAll(f, "always")
		if p, err = f.client().Preview(context.Background(), f.ref); err != nil || !p.Eligible {
			t.Fatalf("%+v, %v", p, err)
		}
	})
}

// TestBypassMissingParameters: a rule that leaves out a parameter GitHub
// always sends proves nothing, and a shape Pocket does not know refuses.
func TestBypassMissingParameters(t *testing.T) {
	name := func(key string) string { return "you can bypass rule pull_request." + key + " here" }
	for _, tc := range []struct {
		name    string
		rules   []any
		change  func(*fake)
		reason  string
		errText string
	}{
		{name: "no parameters at all", rules: []any{rule("pull_request", nil)},
			reason: "you can bypass rule pull_request here"},
		{name: "empty parameters, the first missing key", rules: []any{rule("pull_request", map[string]any{})},
			reason: name("dismiss_stale_reviews_on_push")},
		{name: "a malformed dismiss_stale_reviews_on_push", rules: []any{rule("pull_request", prParams(map[string]any{
			"dismiss_stale_reviews_on_push": "yes",
		}))}, reason: name("dismiss_stale_reviews_on_push")},
		{name: "required_reviewers null", rules: []any{rule("pull_request", prParams(map[string]any{
			"required_reviewers": nil,
		}))}, reason: name("required_reviewers")},
		{name: "required_reviewers not a list", rules: []any{rule("pull_request", prParams(map[string]any{
			"required_reviewers": "everyone",
		}))}, reason: name("required_reviewers")},
		{name: "allowed_merge_methods null", rules: []any{rule("pull_request", prParams(map[string]any{
			"allowed_merge_methods": nil,
		}))}, reason: name("allowed_merge_methods")},
		{name: "allowed_merge_methods that are no strings", rules: []any{rule("pull_request", prParams(map[string]any{
			"allowed_merge_methods": []any{1, 2},
		}))}, reason: name("allowed_merge_methods")},
		{name: "extra approval that is no boolean", rules: []any{rule("pull_request", prParams(map[string]any{
			"require_extra_approval_for_unattributed_changes": "yes",
		}))}, reason: name("require_extra_approval_for_unattributed_changes")},
		// An absent extra approval is on, so a Bot author needs 1 more.
		{name: "extra approval absent, a Bot author", rules: []any{rule("pull_request", prParams(map[string]any{
			"required_approving_review_count": 1,
		}))}, change: func(f *fake) {
			params := obj(f.rules[0].(map[string]any), "parameters")
			delete(params, "require_extra_approval_for_unattributed_changes")
			obj(f.bypassPR, "data", "repository", "pullRequest", "author")["__typename"] = "Bot"
		}, reason: "the rules need 2 approvals at the head; Pocket counts 1"},
		// The same, a User: 1 approval is enough.
		{name: "extra approval absent, a user", rules: []any{rule("pull_request", prParams(map[string]any{
			"required_approving_review_count": 1,
		}))}, change: func(f *fake) {
			params := obj(f.rules[0].(map[string]any), "parameters")
			delete(params, "require_extra_approval_for_unattributed_changes")
		}},
		{name: "a required status checks rule without its strict policy", rules: []any{rule("required_status_checks", map[string]any{
			"required_status_checks": []any{},
		})}, reason: "you can bypass rule required_status_checks.strict_required_status_checks_policy here"},
		{name: "a required status checks rule with no parameters", rules: []any{rule("required_status_checks", nil)},
			errText: "the required checks rule has no parameters"},
		{name: "a strict policy that is no boolean", rules: []any{rule("required_status_checks", map[string]any{
			"required_status_checks":               []any{},
			"strict_required_status_checks_policy": "yes",
		})}, reason: "you can bypass rule required_status_checks.strict_required_status_checks_policy here"},
		{name: "do_not_enforce_on_create that is no boolean", rules: []any{rule("required_status_checks", map[string]any{
			"required_status_checks":               []any{},
			"strict_required_status_checks_policy": false,
			"do_not_enforce_on_create":             "yes",
		})}, reason: "you can bypass rule required_status_checks.do_not_enforce_on_create here"},
		{name: "no do_not_enforce_on_create is fine", change: func(f *fake) {
			params := obj(f.rules[0].(map[string]any), "parameters")
			delete(params, "do_not_enforce_on_create")
		}, rules: []any{rule("required_status_checks", map[string]any{
			"required_status_checks":               []any{},
			"strict_required_status_checks_policy": false,
			"do_not_enforce_on_create":             false,
		})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := direct(t)
			f.rules = tc.rules
			bypassAll(f, "always")
			if tc.change != nil {
				tc.change(f)
			}
			p, err := f.client().Preview(context.Background(), f.ref)
			if tc.errText != "" {
				if !errors.Is(err, ErrIncomplete) || !strings.Contains(err.Error(), tc.errText) {
					t.Fatalf("err %v, want %q", err, tc.errText)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.reason != "" {
				if p.Eligible || !strings.Contains(p.Reason, tc.reason) {
					t.Fatalf("eligible %v, reason %q; want %q", p.Eligible, p.Reason, tc.reason)
				}
				return
			}
			if !p.Eligible || p.Reason != "" {
				t.Fatalf("eligible %v, reason %q", p.Eligible, p.Reason)
			}
		})
	}
	// Every required key of a pull_request rule, 1 at a time.
	for _, key := range pullRequestKeys {
		t.Run("pull_request without "+key, func(t *testing.T) {
			params := prParams(nil)
			delete(params, key)
			f := direct(t)
			f.rules = []any{rule("pull_request", params)}
			bypassAll(f, "always")
			p, err := f.client().Preview(context.Background(), f.ref)
			if err != nil {
				t.Fatal(err)
			}
			if p.Eligible || !strings.Contains(p.Reason, "you can bypass rule pull_request."+key+" here") {
				t.Fatalf("eligible %v, reason %q", p.Eligible, p.Reason)
			}
		})
	}
}

// TestBypassIgnoredParameterShapes: a parameter that sets no merge
// requirement still has a shape Pocket reads, and a wrong 1 refuses by name.
func TestBypassIgnoredParameterShapes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		typ    string
		params string
		want   string // the unchecked name; "" for none
	}{
		// pull_request, through the gates.
		{name: "copilot review that is no boolean", typ: "pull_request",
			params: `{"automatic_copilot_code_review_enabled":"yes"}`,
			want:   "pull_request.automatic_copilot_code_review_enabled"},
		{name: "copilot review null", typ: "pull_request",
			params: `{"automatic_copilot_code_review_enabled":null}`,
			want:   "pull_request.automatic_copilot_code_review_enabled"},
		{name: "dismissal restriction that is no object", typ: "pull_request",
			params: `{"dismissal_restriction":"no"}`,
			want:   "pull_request.dismissal_restriction"},
		{name: "dismissal restriction null", typ: "pull_request",
			params: `{"dismissal_restriction":null}`,
			want:   "pull_request.dismissal_restriction"},
		{name: "dismissal restriction that is a list", typ: "pull_request",
			params: `{"dismissal_restriction":[]}`,
			want:   "pull_request.dismissal_restriction"},
		// merge_queue, 1 key at a time.
		{name: "merge method that is no string", typ: "merge_queue",
			params: `{"merge_method":null}`, want: "merge_queue.merge_method"},
		{name: "merge method empty", typ: "merge_queue",
			params: `{"merge_method":""}`, want: "merge_queue.merge_method"},
		{name: "grouping strategy null", typ: "merge_queue",
			params: `{"merge_method":"SQUASH","grouping_strategy":null}`,
			want:   "merge_queue.grouping_strategy"},
		{name: "entries to build not a number", typ: "merge_queue",
			params: `{"merge_method":"SQUASH","max_entries_to_build":"5"}`,
			want:   "merge_queue.max_entries_to_build"},
		{name: "entries to build negative", typ: "merge_queue",
			params: `{"merge_method":"SQUASH","max_entries_to_build":-1}`,
			want:   "merge_queue.max_entries_to_build"},
		{name: "entries to build fractional", typ: "merge_queue",
			params: `{"merge_method":"SQUASH","max_entries_to_build":1.5}`,
			want:   "merge_queue.max_entries_to_build"},
		{name: "min entries to merge null", typ: "merge_queue",
			params: `{"merge_method":"SQUASH","min_entries_to_merge":null}`,
			want:   "merge_queue.min_entries_to_merge"},
		{name: "max entries to merge not a number", typ: "merge_queue",
			params: `{"merge_method":"SQUASH","max_entries_to_merge":true}`,
			want:   "merge_queue.max_entries_to_merge"},
		{name: "min wait minutes null", typ: "merge_queue",
			params: `{"merge_method":"SQUASH","min_entries_to_merge_wait_minutes":null}`,
			want:   "merge_queue.min_entries_to_merge_wait_minutes"},
		{name: "check timeout negative", typ: "merge_queue",
			params: `{"merge_method":"SQUASH","check_response_timeout_minutes":-5}`,
			want:   "merge_queue.check_response_timeout_minutes"},
		{name: "an unknown key that is on", typ: "merge_queue",
			params: `{"merge_method":"SQUASH","some_future_parameter":true}`,
			want:   "merge_queue.some_future_parameter"},
		// The recorded parameters of frostyard/core's merge_queue rule.
		{name: "the recorded core parameters", typ: "merge_queue",
			params: `{"merge_method":"SQUASH","max_entries_to_build":5,"min_entries_to_merge":1,` +
				`"max_entries_to_merge":5,"min_entries_to_merge_wait_minutes":0,` +
				`"grouping_strategy":"ALLGREEN","check_response_timeout_minutes":60}`},
		{name: "no parameters at all", typ: "merge_queue", params: ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A pull_request rule carries every key GitHub sends, with this
			// 1's value changed.
			raw := json.RawMessage(tc.params)
			if tc.typ == "pull_request" {
				all := prParams(nil)
				var extra map[string]any
				if err := json.Unmarshal(raw, &extra); err != nil {
					t.Fatal(err)
				}
				for k, v := range extra {
					all[k] = v
				}
				b, merr := json.Marshal(all)
				if merr != nil {
					t.Fatal(merr)
				}
				raw = b
			}
			var p BypassRules
			p.rule(tc.typ, raw)
			if p.Unchecked != tc.want {
				t.Fatalf("unchecked %q, want %q", p.Unchecked, tc.want)
			}
		})
	}
	// The same through the card: the reason names the parameter.
	f := direct(t)
	f.rules = []any{rule("pull_request", prParams(map[string]any{
		"dismissal_restriction": "no",
	}))}
	bypassAll(f, "always")
	p, err := f.client().Preview(context.Background(), f.ref)
	if err != nil {
		t.Fatal(err)
	}
	if p.Eligible || !strings.Contains(p.Reason, "you can bypass rule pull_request.dismissal_restriction here") {
		t.Fatalf("eligible %v, reason %q", p.Eligible, p.Reason)
	}
	// Well-formed values of both keys pass.
	f = direct(t)
	f.rules = []any{rule("pull_request", prParams(map[string]any{
		"dismissal_restriction":                 map[string]any{"enabled": false, "allowed_actors": []any{}},
		"automatic_copilot_code_review_enabled": false,
	}))}
	bypassAll(f, "always")
	if p, err = f.client().Preview(context.Background(), f.ref); err != nil || !p.Eligible {
		t.Fatalf("%+v, %v", p, err)
	}
}
