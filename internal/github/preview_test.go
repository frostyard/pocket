package github

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestQueryTextIsTheRecordedQuery(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "pr-query.graphql"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimRight(prQuery, "\n") != strings.TrimRight(string(b), "\n") {
		t.Fatal("query.go's PR query differs from testdata/pr-query.graphql")
	}
}

func TestParseRef(t *testing.T) {
	for s, want := range map[string]*Ref{
		"frostyard/core#256":                  {"frostyard", "core", 256},
		"bketelsen/pi-pocket-team#1":          {"bketelsen", "pi-pocket-team", 1},
		"a-b/c.d_e-f#12":                      {"a-b", "c.d_e-f", 12},
		"frostyard/core#0":                    nil,
		"frostyard/core#":                     nil,
		"frostyard/core#99999999999":          nil,
		"frostyard/core#-1":                   nil,
		"frostyard_x/core#1":                  nil,
		"frostyard/co/re#1":                   nil,
		"https://github.com/a/b/pull/1":       nil,
		" frostyard/core#1":                   nil,
		"frostyard/core#1 ":                   nil,
		"frostyard/core#1\n":                  nil,
		"frostyard/../core#1":                 nil,
		"frostyard/core#1#2":                  nil,
		"frostyard/c%2Fore#1":                 nil,
		"frostyard/core#99999999999999999999": nil,
	} {
		got, err := ParseRef(s)
		if want == nil {
			if err == nil {
				t.Errorf("ParseRef(%q) = %v, want an error", s, got)
			}
			continue
		}
		if err != nil || got != *want || got.String() != s {
			t.Errorf("ParseRef(%q) = %v, %v", s, got, err)
		}
	}
}

// previewCase changes the fake, then previews.
type previewCase struct {
	name   string
	base   func(*testing.T) *fake
	change func(*fake)
	reason string // "" is eligible
	method string
}

func TestPreviewGates(t *testing.T) {
	requiredStatus := func(f *fake) {
		f.rules = append(f.rules, map[string]any{"type": "required_status_checks", "ruleset_id": 26867289.0,
			"parameters": map[string]any{"required_status_checks": []any{map[string]any{"context": "ci/legacy"}}}})
	}
	for _, tc := range []previewCase{
		{name: "direct base passes", base: direct, method: "merge"},
		{name: "queue base passes, BLOCKED before approval", base: queue, method: "queue"},
		// 1
		{name: "1 login is the machine account", base: direct, change: func(f *fake) { obj(f.data, "viewer")["login"] = "frostyard-machines" }, reason: "this token is for frostyard-machines"},
		// 2
		{name: "2 bypass always", base: direct, change: func(f *fake) { f.rulesets["25664287"].(map[string]any)["current_user_can_bypass"] = "always" }, reason: "you can bypass a rule here (always)"},
		{name: "2 bypass pull_requests_only", base: queue, change: func(f *fake) {
			f.rulesets["24434288"].(map[string]any)["current_user_can_bypass"] = "pull_requests_only"
		}, reason: "you can bypass a rule here (pull_requests_only)"},
		{name: "2 bypass missing", base: direct, change: func(f *fake) { delete(f.rulesets["26867289"].(map[string]any), "current_user_can_bypass") }, reason: "you can bypass a rule here (unknown)"},
		{name: "2 classic protection on", base: direct, change: func(f *fake) { obj(f.branch, "protection")["enabled"] = true }, reason: "classic branch protection"},
		{name: "2 protected but no classic protection", base: direct, method: "merge", change: func(f *fake) { f.branch["protected"] = true }},
		// 3
		{name: "3 closed", base: direct, change: func(f *fake) { f.pr()["state"] = "CLOSED" }, reason: "the PR is closed"},
		{name: "3 merged", base: direct, change: func(f *fake) { f.pr()["state"] = "MERGED" }, reason: "the PR is merged"},
		{name: "3 draft", base: direct, change: func(f *fake) { f.pr()["isDraft"] = true }, reason: "the PR is a draft"},
		{name: "3 viewer authored", base: direct, change: func(f *fake) { f.pr()["viewerDidAuthor"] = true }, reason: "you authored this PR"},
		{name: "3 last commit is not the head", base: direct, change: func(f *fake) { f.commit()["oid"] = strings.Repeat("b", 40) }, reason: "the last commit is not the head"},
		// 4
		{name: "4 no ready-for-human", base: direct, change: func(f *fake) { f.pr()["labels"] = labels("level:L1") }, reason: "no ready-for-human label"},
		// 5
		{name: "5 conflicting", base: direct, change: func(f *fake) { f.pr()["mergeable"] = "CONFLICTING" }, reason: "the PR has conflicts"},
		{name: "5 unknown twice", base: direct, change: func(f *fake) { f.pr()["mergeable"] = "UNKNOWN" }, reason: "GitHub has not computed mergeability"},
		{name: "5 unknown, then mergeable", base: direct, method: "merge", change: func(f *fake) {
			f.onRead = func(n int, pr map[string]any) {
				if n == 1 {
					pr["mergeable"] = "UNKNOWN"
				}
			}
		}},
		// 6
		{name: "6 required check missing", base: direct, change: func(f *fake) { f.setChecks(checkRun("build", "COMPLETED", "SUCCESS", 15368)) }, reason: "required check Test has not run"},
		{name: "6 required check of another app", base: direct, change: func(f *fake) { f.setChecks(checkRun("Test", "COMPLETED", "SUCCESS", 99)) }, reason: "required check Test has not run"},
		{name: "6 required check with no app", base: direct, change: func(f *fake) { f.setChecks(checkRun("Test", "COMPLETED", "SUCCESS", nil)) }, reason: "required check Test has not run"},
		{name: "6 status for an integration check", base: direct, change: func(f *fake) { f.setChecks(statusCtx("Test", "SUCCESS")) }, reason: "required check Test has not run"},
		{name: "6 status meets a check with no app", base: direct, method: "merge", change: func(f *fake) {
			requiredStatus(f)
			f.setChecks(checkRun("Test", "COMPLETED", "SUCCESS", 15368), statusCtx("ci/legacy", "SUCCESS"))
		}},
		{name: "6 check run meets a check with no app", base: direct, method: "merge", change: func(f *fake) {
			requiredStatus(f)
			f.setChecks(checkRun("Test", "COMPLETED", "SUCCESS", 15368), checkRun("ci/legacy", "COMPLETED", "SUCCESS", nil))
		}},
		{name: "6 no checks, some required", base: direct, change: func(f *fake) { f.commit()["statusCheckRollup"] = nil }, reason: "required check Test has not run"},
		{name: "6 no checks, none required", base: direct, method: "merge", change: func(f *fake) {
			f.commit()["statusCheckRollup"] = nil
			f.rules = []any{}
		}},
		// 7
		{name: "7 check in progress", base: direct, change: func(f *fake) {
			f.setChecks(checkRun("Test", "COMPLETED", "SUCCESS", 15368), checkRun("e2e", "IN_PROGRESS", "", 15368))
		}, reason: "check e2e has not finished"},
		{name: "7 check failed", base: direct, change: func(f *fake) {
			f.setChecks(checkRun("Test", "COMPLETED", "SUCCESS", 15368), checkRun("e2e", "COMPLETED", "FAILURE", 15368))
		}, reason: "check e2e: failure"},
		{name: "7 status pending", base: direct, change: func(f *fake) {
			f.setChecks(checkRun("Test", "COMPLETED", "SUCCESS", 15368), statusCtx("ci/x", "PENDING"))
		}, reason: "status ci/x has not finished"},
		{name: "7 status error", base: direct, change: func(f *fake) {
			f.setChecks(checkRun("Test", "COMPLETED", "SUCCESS", 15368), statusCtx("ci/x", "ERROR"))
		}, reason: "status ci/x: error"},
		{name: "7 neutral and skipped pass", base: direct, method: "merge", change: func(f *fake) {
			f.setChecks(checkRun("Test", "COMPLETED", "NEUTRAL", 15368), checkRun("x", "COMPLETED", "SKIPPED", nil), statusCtx("ci/x", "SUCCESS"))
		}},
		// 8
		{name: "8 changes requested by a review", base: direct, change: func(f *fake) {
			obj(f.pr(), "latestOpinionatedReviews")["nodes"] = []any{map[string]any{"state": "CHANGES_REQUESTED", "author": map[string]any{"login": "someone"}, "commit": map[string]any{"oid": f.head()}}}
		}, reason: "someone requested changes"},
		{name: "8 review decision changes requested", base: direct, change: func(f *fake) { f.pr()["reviewDecision"] = "CHANGES_REQUESTED" }, reason: "changes are requested"},
		// 9
		{name: "9 behind, direct", base: direct, change: func(f *fake) { f.pr()["mergeStateStatus"] = "BEHIND" }, reason: "the PR is behind its base"},
		{name: "9 behind, queue", base: queue, method: "queue", change: func(f *fake) { f.pr()["mergeStateStatus"] = "BEHIND" }},
		{name: "9 dirty", base: direct, change: func(f *fake) { f.pr()["mergeStateStatus"] = "DIRTY" }, reason: "the PR has conflicts"},
		{name: "9 dirty, queue", base: queue, change: func(f *fake) { f.pr()["mergeStateStatus"] = "DIRTY" }, reason: "the PR has conflicts"},
		{name: "9 unstable", base: direct, change: func(f *fake) { f.pr()["mergeStateStatus"] = "UNSTABLE" }, reason: "a check is failing"},
		{name: "9 draft state", base: direct, change: func(f *fake) { f.pr()["mergeStateStatus"] = "DRAFT" }, reason: "the PR is a draft"},
		{name: "9 unknown state", base: direct, change: func(f *fake) { f.pr()["mergeStateStatus"] = "UNKNOWN" }, reason: "GitHub has not computed the merge state"},
		{name: "9 clean", base: direct, method: "merge", change: func(f *fake) { f.pr()["mergeStateStatus"] = "CLEAN" }},
		{name: "9 has hooks", base: direct, method: "merge", change: func(f *fake) { f.pr()["mergeStateStatus"] = "HAS_HOOKS" }},
		// 10
		{name: "10 squash not allowed", base: direct, change: func(f *fake) {
			f.repo()["viewerDefaultMergeMethod"] = "SQUASH"
			f.repo()["squashMergeAllowed"] = false
		}, reason: "your default merge method (squash) is not allowed here"},
		{name: "10 merge not allowed", base: direct, change: func(f *fake) { f.repo()["mergeCommitAllowed"] = false }, reason: "your default merge method (merge) is not allowed here"},
		{name: "10 rebase", base: direct, method: "rebase", change: func(f *fake) { f.repo()["viewerDefaultMergeMethod"] = "REBASE" }},
		{name: "10 squash", base: direct, method: "squash", change: func(f *fake) { f.repo()["viewerDefaultMergeMethod"] = "SQUASH" }},
		{name: "10 queue takes no repository method", base: queue, method: "queue", change: func(f *fake) { f.repo()["mergeCommitAllowed"] = false }},
		// Nulls that may be null.
		{name: "null viewerLatestReview", base: direct, method: "merge", change: func(f *fake) { f.pr()["viewerLatestReview"] = nil }},
		{name: "null reviewDecision", base: direct, method: "merge", change: func(f *fake) { f.pr()["reviewDecision"] = nil }},
		{name: "null mergedBy, mergeCommit, mergedAt, mergeQueueEntry", base: direct, method: "merge"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.base(t)
			if tc.change != nil {
				tc.change(f)
			}
			p, err := f.client().Preview(context.Background(), f.ref)
			if err != nil {
				t.Fatal(err)
			}
			if p.Eligible != (tc.reason == "") || !strings.Contains(p.Reason, tc.reason) {
				t.Fatalf("eligible %v, reason %q; want %q", p.Eligible, p.Reason, tc.reason)
			}
			if tc.reason == "" && (p.Method != tc.method || p.Reason != "") {
				t.Fatalf("method %q, reason %q", p.Method, p.Reason)
			}
			if len(f.mutations) != 0 {
				t.Fatalf("a preview sent %v", f.mutations)
			}
		})
	}
}

func TestPreviewFields(t *testing.T) {
	f := queue(t)
	f.pr()["labels"] = labels(labelReady, labelRisk)
	p, err := f.client().Preview(context.Background(), f.ref)
	if err != nil {
		t.Fatal(err)
	}
	want := Preview{
		Ref:      f.ref,
		NodeID:   "PR_kwDOT1orks8AAAABPJeaYg",
		Title:    "feat(risk-paths): add supply chain to the categories of rule B",
		BaseRef:  "main",
		BaseOid:  "17ea42dde5615dce7fb8d1846fb024ef96fbdd07",
		Head:     "35be957c8a8c373b84e0ef1c035698a7cedb6179",
		RiskHigh: true,
		Queue:    true,
		Method:   "queue",
		Eligible: true,
		ReadAt:   time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC),
	}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("got  %+v\nwant %+v", p, want)
	}
	if len(f.waits) != 0 {
		t.Fatalf("waits %v", f.waits)
	}
}

func TestPreviewUnknownReadsAgainAfter2Seconds(t *testing.T) {
	f := direct(t)
	f.pr()["mergeable"] = "UNKNOWN"
	p, err := f.client().Preview(context.Background(), f.ref)
	if err != nil || p.Eligible {
		t.Fatal(p, err)
	}
	if !reflect.DeepEqual(f.waits, []time.Duration{2 * time.Second}) || f.reads != 2 {
		t.Fatalf("waits %v, reads %d", f.waits, f.reads)
	}
}

// TestPreviewRefuses: a read Pocket cannot act on is an error, never a
// button.
func TestPreviewRefuses(t *testing.T) {
	page := func(n int, name string) []any {
		var nodes []any
		for i := 0; i < n; i++ {
			nodes = append(nodes, checkRun(name, "COMPLETED", "SUCCESS", 15368))
		}
		return nodes
	}
	manyRules := func(f *fake, n int) {
		for i := 0; i < n; i++ {
			f.rules = append(f.rules, map[string]any{"type": "deletion", "ruleset_id": 25664287.0})
		}
	}
	for _, tc := range []struct {
		name   string
		change func(*fake)
		want   error
		text   string
	}{
		{"200 with errors (not found)", func(f *fake) {
			b, _ := os.ReadFile(filepath.Join("testdata", "bketelsen-pi-pocket-team", "pr-999999-not-found.json"))
			f.raw = string(b)
		}, ErrIncomplete, "Could not resolve to a PullRequest with the number of 999999."},
		{"null repository", func(f *fake) { f.data["repository"] = nil }, ErrIncomplete, "repository is null"},
		{"null pullRequest", func(f *fake) { f.repo()["pullRequest"] = nil }, ErrIncomplete, "pullRequest is null"},
		{"null viewer", func(f *fake) { f.data["viewer"] = nil }, ErrIncomplete, "viewer is null"},
		{"null viewer login", func(f *fake) { obj(f.data, "viewer")["login"] = nil }, ErrIncomplete, "viewer.login is null"},
		{"null head", func(f *fake) { f.pr()["headRefOid"] = nil }, ErrIncomplete, "headRefOid is null"},
		{"missing title", func(f *fake) { delete(f.pr(), "title") }, ErrIncomplete, "title is null"},
		{"null mergeable", func(f *fake) { f.pr()["mergeable"] = nil }, ErrIncomplete, "mergeable is null"},
		{"null merge method", func(f *fake) { f.repo()["viewerDefaultMergeMethod"] = nil }, ErrIncomplete, "viewerDefaultMergeMethod is null"},
		{"null label node", func(f *fake) { obj(f.pr(), "labels")["nodes"] = []any{nil} }, ErrIncomplete, "a label is null"},
		{"null labels", func(f *fake) { f.pr()["labels"] = nil }, ErrIncomplete, "labels is null"},
		{"null review commit", func(f *fake) {
			obj(f.pr(), "latestOpinionatedReviews")["nodes"] = []any{map[string]any{"state": "APPROVED", "author": map[string]any{"login": "x"}, "commit": nil}}
		}, ErrIncomplete, "a review is null"},
		{"null commits node", func(f *fake) { obj(f.pr(), "commits")["nodes"] = []any{nil} }, ErrIncomplete, "the last commit is null"},
		{"no commits node", func(f *fake) { obj(f.pr(), "commits")["nodes"] = []any{} }, ErrIncomplete, "commits is null"},
		{"null commit", func(f *fake) {
			obj(f.pr(), "commits")["nodes"] = []any{map[string]any{"commit": nil}}
		}, ErrIncomplete, "the last commit is null"},
		{"null check suite", func(f *fake) {
			c := checkRun("Test", "COMPLETED", "SUCCESS", 15368)
			c["checkSuite"] = nil
			f.setChecks(c)
		}, ErrIncomplete, "a check run is null"},
		{"null conclusion of a completed check", func(f *fake) { f.setChecks(checkRun("Test", "COMPLETED", "", 15368)) }, ErrIncomplete, "conclusion of check Test is null"},
		{"null check node", func(f *fake) { obj(f.commit(), "statusCheckRollup", "contexts")["nodes"] = []any{nil} }, ErrIncomplete, "a check is null"},
		{"too many labels", func(f *fake) { obj(f.pr(), "labels", "pageInfo")["hasNextPage"] = true }, ErrIncomplete, "too many labels"},
		{"too many reviews", func(f *fake) { obj(f.pr(), "latestOpinionatedReviews", "pageInfo")["hasNextPage"] = true }, ErrIncomplete, "too many reviews"},
		{"more than 5 pages of checks", func(f *fake) {
			f.pages = [][]any{page(100, "Test"), page(100, "a"), page(100, "b"), page(100, "c"), page(100, "d"), page(1, "e")}
		}, ErrIncomplete, "too many checks"},
		{"a later page at another head", func(f *fake) {
			f.pages = [][]any{page(100, "Test"), page(3, "a")}
			f.pageHead = map[int]string{1: strings.Repeat("d", 40)}
		}, ErrIncomplete, "the head changed while reading the checks"},
		{"more than 10 pages of rules", func(f *fake) { manyRules(f, 1000) }, ErrIncomplete, "too many rules"},
		{"no protection object", func(f *fake) { delete(f.branch, "protection") }, ErrIncomplete, "the branch protection is missing"},
		{"a rule with no ruleset", func(f *fake) { f.rules = append(f.rules, map[string]any{"type": "deletion"}) }, ErrIncomplete, "a rule names no ruleset"},
		{"5xx", func(f *fake) {
			f.prFail = func(int) (int, string, http.Header) { return 503, `{"message":"unavailable"}`, nil }
		}, ErrNoAnswer, "503"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := direct(t)
			tc.change(f)
			p, err := f.client().Preview(context.Background(), f.ref)
			if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.text) {
				t.Fatalf("err %v, want %v with %q", err, tc.want, tc.text)
			}
			if p.Eligible {
				t.Fatal("eligible on a failed read")
			}
		})
	}
}

func TestPreviewReadsEveryPage(t *testing.T) {
	f := direct(t)
	var p1, p2, p3 []any
	for i := 0; i < 100; i++ {
		p1 = append(p1, checkRun("Test", "COMPLETED", "SUCCESS", 15368))
		p2 = append(p2, checkRun("b", "COMPLETED", "SUCCESS", 15368))
	}
	p3 = []any{checkRun("late", "COMPLETED", "FAILURE", 15368)}
	f.pages = [][]any{p1, p2, p3}
	for i := 0; i < 150; i++ {
		f.rules = append(f.rules, map[string]any{"type": "deletion", "ruleset_id": 25664287.0})
	}
	f.rules = append(f.rules, queueRule("SQUASH"))
	f.pr()["isMergeQueueEnabled"] = true
	p, err := f.client().Preview(context.Background(), f.ref)
	if err != nil {
		t.Fatal(err)
	}
	if p.Eligible || p.Reason != "check late: failure" || !p.Queue || p.Method != "queue" || f.reads != 3 {
		t.Fatalf("%+v, reads %d", p, f.reads)
	}
}

// queueRule is a merge_queue rule of a pi-pocket-team ruleset with method.
func queueRule(method any) map[string]any {
	return map[string]any{"type": "merge_queue", "ruleset_id": 26867289.0, "parameters": map[string]any{"merge_method": method}}
}

// TestRulesQueueMethod: a merge queue whose method Pocket cannot read
// refuses the read, so a merge that the queue makes always has a method.
func TestRulesQueueMethod(t *testing.T) {
	for _, method := range []any{nil, "FAST_FORWARD"} {
		f := direct(t)
		f.rules = append(f.rules, queueRule(method))
		if _, err := f.client().Rules(context.Background(), f.ref, "main"); !errors.Is(err, ErrIncomplete) {
			t.Errorf("merge_method %v: err %v, want ErrIncomplete", method, err)
		}
	}
	f := direct(t)
	f.rules = append(f.rules, queueRule("REBASE"))
	r, err := f.client().Rules(context.Background(), f.ref, "main")
	if err != nil || !r.Queue || r.queueMethod != "rebase" {
		t.Fatalf("%+v, %v", r, err)
	}
}

func TestRules(t *testing.T) {
	f := queue(t)
	r, err := f.client().Rules(context.Background(), f.ref, "main")
	if err != nil {
		t.Fatal(err)
	}
	want := RepoRules{Required: []RequiredCheck{{"docs-gate", 15368}, {"fleet-conventions", 15368}, {"scaffold-e2e", 15368}}, Queue: true, Bypass: "never", queueMethod: "squash"}
	if !reflect.DeepEqual(r, want) {
		t.Fatalf("%+v", r)
	}
	// pi-pocket-team lists Test twice: once is enough.
	f = direct(t)
	if r, err = f.client().Rules(context.Background(), f.ref, "main"); err != nil || !reflect.DeepEqual(r, RepoRules{Required: []RequiredCheck{{"Test", 15368}}, Bypass: "never"}) {
		t.Fatalf("%+v %v", r, err)
	}
	// No ruleset at all (pi-pocket-config): never, no checks.
	f = newFake(t, Ref{"bketelsen", "pi-pocket-config", 55}, "bketelsen-pi-pocket-config", "pr-55-merged.json")
	if r, err = f.client().Rules(context.Background(), f.ref, "main"); err != nil || !reflect.DeepEqual(r, RepoRules{Bypass: "never"}) {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestRulesEscapesTheBranch(t *testing.T) {
	var paths []string
	f := direct(t)
	f.restFail = func(string) (int, string, http.Header) { return 404, `{"message":"Not Found"}`, nil }
	c := f.client()
	c.HTTP.Transport = escapedPaths{&paths, http.DefaultTransport}
	_, err := c.Rules(context.Background(), f.ref, "release/1.0 x")
	var api *APIError
	if !errors.As(err, &api) || api.Status != 404 {
		t.Fatal(err)
	}
	if len(paths) == 0 || paths[0] != "/repos/bketelsen/pi-pocket-team/rules/branches/release%2F1.0%20x" {
		t.Fatalf("paths %v", paths)
	}
}

// escapedPaths records each request's escaped path before it goes.
type escapedPaths struct {
	paths *[]string
	next  http.RoundTripper
}

func (e escapedPaths) RoundTrip(r *http.Request) (*http.Response, error) {
	*e.paths = append(*e.paths, r.URL.EscapedPath())
	return e.next.RoundTrip(r)
}
