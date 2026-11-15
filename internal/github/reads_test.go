package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestWhoami(t *testing.T) {
	for _, tc := range []struct {
		expiry string
		want   time.Time
	}{
		{"2026-12-14 10:00:00 UTC", time.Date(2026, 12, 14, 10, 0, 0, 0, time.UTC)},
		{"2026-12-14 10:00:00 -0500", time.Date(2026, 12, 14, 15, 0, 0, 0, time.UTC)},
		{"", time.Time{}},
		{"next week", time.Time{}},
	} {
		f := direct(t)
		f.user, f.expiry = "frostyard-machines", tc.expiry
		id, err := f.client().Whoami(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if id.Login != "frostyard-machines" || !id.Expires.Equal(tc.want) {
			t.Errorf("expiry %q: %+v, want %v", tc.expiry, id, tc.want)
		}
	}
}

func TestWhoamiRedactsAndNamesTheMissingPermission(t *testing.T) {
	f := direct(t)
	f.restFail = func(string) (int, string, http.Header) {
		return 403, `{"message":"no (` + testToken + `)"}`, http.Header{"X-Accepted-Github-Permissions": {"metadata=read"}}
	}
	_, err := f.client().Whoami(context.Background())
	var api *APIError
	if !errors.As(err, &api) || api.Status != 403 || api.Accepted != "metadata=read" {
		t.Fatalf("err %v", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("the error holds the token: %v", err)
	}
	row := f.client().Preflight(context.Background(), f.ref)
	if row.Readable || row.Missing != "metadata=read" || row.Reason == "" || strings.Contains(row.Reason, testToken) {
		t.Errorf("preflight %+v", row)
	}
}

// TestWhoamiNamesTheCall: an error of GET /user names the call.
func TestWhoamiNamesTheCall(t *testing.T) {
	f := direct(t)
	f.restFail = func(string) (int, string, http.Header) { return 401, `{"message":"Bad credentials"}`, nil }
	_, err := f.client().Whoami(context.Background())
	if err == nil || !strings.HasPrefix(err.Error(), "GET /user: ") {
		t.Fatalf("err %v", err)
	}
}

func TestCheckRepo(t *testing.T) {
	f := direct(t)
	if err := f.client().CheckRepo(context.Background(), f.ref.Owner, f.ref.Name); err != nil {
		t.Fatal(err)
	}
	f.restFail = func(string) (int, string, http.Header) { return 404, `{"message":"Not Found"}`, nil }
	var api *APIError
	if err := f.client().CheckRepo(context.Background(), f.ref.Owner, f.ref.Name); !errors.As(err, &api) || api.Status != 404 {
		t.Errorf("err %v", err)
	}
}

func TestPreflight(t *testing.T) {
	f := direct(t)
	row := f.client().Preflight(context.Background(), f.ref)
	if !row.Readable || row.Bypass != "never" || row.Classic || row.Reason != "" || row.Repo != "bketelsen/pi-pocket-team" {
		t.Errorf("row %+v", row)
	}
	f.rulesets["25664287"].(map[string]any)["current_user_can_bypass"] = "always"
	row = f.client().Preflight(context.Background(), f.ref)
	if !row.Readable || row.Bypass != "always" || row.Reason != "" {
		t.Errorf("bypass row %+v", row)
	}
	f.rulesets["25664287"].(map[string]any)["current_user_can_bypass"] = "exempt"
	row = f.client().Preflight(context.Background(), f.ref)
	if !row.Readable || row.Bypass != "exempt" || !strings.Contains(row.Reason, "bypass") {
		t.Errorf("exempt row %+v", row)
	}
	obj(f.data, "viewer")["login"] = "frostyard-machines"
	if row := f.client().Preflight(context.Background(), f.ref); !strings.Contains(row.Reason, "frostyard-machines") {
		t.Errorf("login row %+v", row)
	}
}

func TestStateAndSettle(t *testing.T) {
	f := direct(t)
	c := f.client()
	p, err := c.Preview(context.Background(), f.ref)
	if err != nil {
		t.Fatal(err)
	}
	in := Intent{Preview: p, Mode: "single"}
	st, err := c.State(context.Background(), f.ref)
	if err != nil || st.State != "OPEN" || st.Head != p.Head {
		t.Fatalf("state %+v %v", st, err)
	}
	// Nothing done: no effect seen.
	if r := c.Settle(context.Background(), in, "op1", false); r.Kind != "unknown" || r.Reason != "no effect seen" || r.Op != "op1" {
		t.Errorf("settle %+v", r)
	}
	// With wait: 3 more reads.
	f.reset()
	c.Settle(context.Background(), in, "op1", true)
	if f.reads != 4 || len(f.waits) != 3 {
		t.Errorf("reads %d waits %v", f.reads, f.waits)
	}
	// Approved at the shown head, not merged.
	approvedBefore(f)
	if r := c.Settle(context.Background(), in, "op1", false); r.Kind != "approved" || r.Reason != "approved, not merged" {
		t.Errorf("approved %+v", r)
	}
	// Merged by the owner at the shown head.
	pr := f.pr()
	pr["state"], pr["mergedBy"], pr["mergeCommit"] = "MERGED", map[string]any{"login": Login}, map[string]any{"oid": mergeOid}
	if r := c.Settle(context.Background(), in, "op1", false); r.Kind != "merged" || r.MergeCommit != mergeOid {
		t.Errorf("merged %+v", r)
	}
	if st, _ := c.State(context.Background(), f.ref); st.State != "MERGED" || st.MergedBy != Login {
		t.Errorf("state %+v", st)
	}
	// Merged by another actor: refused, not claimed.
	pr["mergedBy"] = map[string]any{"login": "someone"}
	if r := c.Settle(context.Background(), in, "op1", false); r.Kind != "refused" || !strings.Contains(r.Reason, "merged by someone") {
		t.Errorf("other %+v", r)
	}
}

func TestSettleQueue(t *testing.T) {
	f := queue(t)
	c := f.client()
	p, err := c.Preview(context.Background(), f.ref)
	if err != nil {
		t.Fatal(err)
	}
	in := Intent{Preview: p, Mode: "batch"}
	pr := f.pr()
	pr["isInMergeQueue"] = true
	pr["mergeQueueEntry"] = map[string]any{"enqueuer": map[string]any{"login": Login}}
	if r := c.Settle(context.Background(), in, "op", false); r.Kind != "queued" || r.Method != "queue" {
		t.Errorf("queued %+v", r)
	}
	pr["isInMergeQueue"], pr["mergeQueueEntry"] = false, nil
	pr["state"], pr["mergedBy"], pr["mergeCommit"] = "MERGED", map[string]any{"login": Login}, map[string]any{"oid": mergeOid}
	if r := c.Settle(context.Background(), in, "op", false); r.Kind != "merged" || r.Method != "squash" {
		t.Errorf("merged by the queue %+v", r)
	}
}

// TestSettleQueueClaimsThePRsHead: the queue entry's headCommit is the merge
// group's commit once GitHub builds the group, never the PR's head, so the
// queued claim reads the PR's own head. The fixture still serves headCommit:
// the client ignores it.
func TestSettleQueueClaimsThePRsHead(t *testing.T) {
	f := queue(t)
	c := f.client()
	p, err := c.Preview(context.Background(), f.ref)
	if err != nil {
		t.Fatal(err)
	}
	group := strings.Repeat("b", 40) // the merge group's commit, not the head
	if group == p.Head {
		t.Fatal("the merge group's commit is the PR's head")
	}
	pr := f.pr()
	pr["isInMergeQueue"] = true
	pr["mergeQueueEntry"] = map[string]any{
		"enqueuer":   map[string]any{"login": Login},
		"headCommit": map[string]any{"oid": group},
	}
	r := c.Settle(context.Background(), Intent{Preview: p, Mode: "single"}, "op", false)
	if r.Kind != "queued" || r.Method != "queue" {
		t.Fatalf("settle %+v, want queued at the PR's head %s (the merge group is %s)", r, p.Head, group)
	}
	if f.exitReads != 0 {
		t.Errorf("a PR still in the queue read the exit %d times", f.exitReads)
	}
}

// queueExitEvents is an answer of PocketQueueExit with 2 events: an enqueue by
// enqueuer, then a removal with reason by actor.
func queueExitEvents(enqueuer, reason, actor string) map[string]any {
	return map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
		"timelineItems": map[string]any{"nodes": []any{
			map[string]any{"__typename": "AddedToMergeQueueEvent", "enqueuer": map[string]any{"login": enqueuer}},
			map[string]any{"__typename": "RemovedFromMergeQueueEvent", "reason": reason, "actor": map[string]any{"login": actor}},
		}},
	}}}}
}

// TestSettleQueueExit: a queued PR that is open and out of the queue says why it
// left, in words, and a removal the queue made because it merged keeps the
// attempt queued until GitHub shows MERGED.
func TestSettleQueueExit(t *testing.T) {
	// base is a queue base whose PR is open, out of the queue, and approved by
	// the owner at the shown head.
	base := func(t *testing.T) (*fake, *Client, Intent) {
		t.Helper()
		f := queue(t)
		c := f.client()
		p, err := c.Preview(context.Background(), f.ref)
		if err != nil {
			t.Fatal(err)
		}
		approvedBefore(f)
		pr := f.pr()
		pr["isInMergeQueue"], pr["mergeQueueEntry"] = false, nil
		return f, c, Intent{Preview: p, Mode: "single"}
	}
	for _, tc := range []struct {
		name                         string
		set                          func(*fake)
		kind, method, exit, contains string
		// wantErr: the result carries the read's error; noExit: the
		// queue-exit query is never sent.
		wantErr, noExit bool
	}{
		{name: "a merge conflict", set: func(f *fake) { f.exitAnswer = load(t, "frostyard-core/queue-exit-209.json") },
			kind: "approved", exit: "merge conflict"},
		{name: "the queue merged it", set: func(f *fake) { f.exitAnswer = load(t, "frostyard-core/queue-exit-260.json") },
			kind: "queued", method: "queue", contains: "GitHub does not say MERGED yet"},
		{name: "a person removed it for failed checks", set: func(f *fake) {
			f.exitAnswer = queueExitEvents(Login, "failed_checks", "bketelsen")
		}, kind: "approved", exit: "failed checks; removed by bketelsen"},
		{name: "another login enqueued it", set: func(f *fake) {
			f.exitAnswer = queueExitEvents("someone-else", "failed_checks", "bketelsen")
		}, kind: "approved"},
		// A failed exit read proves nothing (the removal may say merged): the
		// result is unknown with the error, so a queued attempt stays queued.
		{name: "the exit read fails", set: func(f *fake) { f.exitFail = 502 },
			kind: "unknown", contains: "could not read why the PR left the merge queue", wantErr: true},
		// The PR moved to another head: the last exit is another enqueue's,
		// never this attempt's, so it is not read and never claims queued.
		{name: "the head moved", set: func(f *fake) {
			f.setHead(newHead)
			f.exitAnswer = load(t, "frostyard-core/queue-exit-260.json")
		}, kind: "approved", contains: "approved, not merged", noExit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c, in := base(t)
			tc.set(f)
			r := c.Settle(context.Background(), in, "op", false)
			if r.Kind != tc.kind || r.Method != tc.method || r.QueueExit != tc.exit {
				t.Errorf("settle %+v, want kind %q, method %q, exit %q", r, tc.kind, tc.method, tc.exit)
			}
			if tc.contains != "" && !strings.Contains(r.Reason, tc.contains) {
				t.Errorf("reason %q, want %q", r.Reason, tc.contains)
			}
			if (r.Err != nil) != tc.wantErr {
				t.Errorf("err %v, want an error %v", r.Err, tc.wantErr)
			}
			want := 1
			if tc.noExit {
				want = 0
			}
			if f.exitReads != want {
				t.Errorf("exit reads %d, want %d", f.exitReads, want)
			}
			if len(f.mutations) != 0 {
				t.Errorf("mutations %v", f.mutations)
			}
		})
	}
}

// TestQueueExitWords: a removal's reason in words, with who removed it when a
// person did, 100 runes at most.
func TestQueueExitWords(t *testing.T) {
	for _, tc := range []struct{ reason, actor, want string }{
		{"merged", mergeQueueActor, "merged"},
		{"failed_checks", mergeQueueActor, "failed checks"},
		{"merge_conflict", mergeQueueActor, "merge conflict"},
		{"merge_conflict", "bketelsen", "merge conflict; removed by bketelsen"},
		{"failed_checks", "dependabot[bot]", "failed checks"},
		{"failed_checks", "", "failed checks"},
		{"CHECKS_TIMED_OUT", "someone", "checks timed out; removed by someone"},
	} {
		if got := queueExitWords(tc.reason, tc.actor); got != tc.want {
			t.Errorf("queueExitWords(%q, %q) = %q, want %q", tc.reason, tc.actor, got, tc.want)
		}
	}
	long := queueExitWords(strings.Repeat("a_b", 40), "someone")
	if utf8.RuneCountInString(long) != maxQueueExit || !utf8.ValidString(long) {
		t.Errorf("%d runes: %q", utf8.RuneCountInString(long), long)
	}
}

func TestSettleReadError(t *testing.T) {
	f := direct(t)
	c := f.client()
	p, _ := c.Preview(context.Background(), f.ref)
	f.prFail = func(int) (int, string, http.Header) {
		return 401, `{"message":"Bad credentials ` + testToken + `"}`, nil
	}
	r := c.Settle(context.Background(), Intent{Preview: p, Mode: "single"}, "op", false)
	var api *APIError
	if r.Kind != "unknown" || !errors.As(r.Err, &api) || api.Status != 401 || strings.Contains(r.Reason, testToken) {
		t.Errorf("settle %+v", r)
	}
}

// TestReadsSendNoMutation holds the reads of Settings and of a settled
// attempt to reads: every GraphQL body is the PR query (the fake fails
// any other text) and every REST call is a GET.
func TestReadsSendNoMutation(t *testing.T) {
	for _, base := range []func(*testing.T) *fake{direct, queue} {
		f := base(t)
		c := f.client()
		ctx := context.Background()
		p, err := c.Preview(ctx, f.ref)
		if err != nil {
			t.Fatal(err)
		}
		in := Intent{Preview: p, Mode: "single"}
		if _, err := c.Whoami(ctx); err != nil {
			t.Fatal(err)
		}
		if err := c.CheckRepo(ctx, f.ref.Owner, f.ref.Name); err != nil {
			t.Fatal(err)
		}
		c.Preflight(ctx, f.ref)
		if _, err := c.State(ctx, f.ref); err != nil {
			t.Fatal(err)
		}
		c.Settle(ctx, in, "op", false)
		c.Settle(ctx, in, "op", true)
		if len(f.mutations) != 0 {
			t.Errorf("mutations %v", f.mutations)
		}
		if f.reads == 0 || len(f.gets) == 0 {
			t.Errorf("reads %d gets %v", f.reads, f.gets)
		}
	}
}

// TestSettleQueueClaimsNothingWithoutTheQueueMethod: on a queue base, a
// failed rules read, or rules with no merge queue method, give unknown with
// the error and no method: a merge's method is never made up.
func TestSettleQueueClaimsNothingWithoutTheQueueMethod(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*fake)
	}{
		{"rules read fails", func(f *fake) {
			f.restFail = func(path string) (int, string, http.Header) {
				if strings.Contains(path, "/rules/branches/") {
					return 403, `{"message":"Resource not accessible"}`, nil
				}
				return 0, "", nil
			}
		}},
		{"rules name no queue", func(f *fake) { f.rulesRaw = "[]" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := queue(t)
			c := f.client()
			p, err := c.Preview(context.Background(), f.ref)
			if err != nil {
				t.Fatal(err)
			}
			pr := f.pr()
			pr["state"], pr["mergedBy"], pr["mergeCommit"] = "MERGED", map[string]any{"login": Login}, map[string]any{"oid": mergeOid}
			tc.set(f)
			r := c.Settle(context.Background(), Intent{Preview: p, Mode: "single"}, "op", false)
			if r.Kind != "unknown" || r.Method != "" || r.Err == nil {
				t.Errorf("settle %+v", r)
			}
			if len(f.mutations) != 0 {
				t.Errorf("mutations %v", f.mutations)
			}
		})
	}
}

// TestCheckApp: the sign-in's login, and whether the App is ready on each
// owner.
func TestCheckApp(t *testing.T) {
	owners := []string{"bketelsen", "frostyard"}
	ready := func(t *testing.T) *fake {
		f := direct(t)
		f.installs = []map[string]any{
			install(AppSlug, "bketelsen", readyPerms(), false),
			install(AppSlug, "frostyard", readyPerms(), false),
		}
		return f
	}
	t.Run("ready", func(t *testing.T) {
		f := ready(t)
		check, err := f.client().CheckApp(context.Background(), owners)
		if err != nil {
			t.Fatal(err)
		}
		if check.Login != Login {
			t.Errorf("login %q", check.Login)
		}
		for _, owner := range owners {
			if reason, ok := check.Owners[owner]; !ok || reason != "" {
				t.Errorf("%s: %q", owner, reason)
			}
		}
		if len(check.Notes) != 0 {
			t.Errorf("notes %v", check.Notes)
		}
	})
	t.Run("another login", func(t *testing.T) {
		f := ready(t)
		f.user = "frostyard-machines"
		check, err := f.client().CheckApp(context.Background(), owners)
		if err != nil {
			t.Fatal(err)
		}
		if check.Login != "frostyard-machines" {
			t.Errorf("login %q", check.Login)
		}
	})
	t.Run("not installed", func(t *testing.T) {
		f := ready(t)
		f.installs = f.installs[:1]
		check, err := f.client().CheckApp(context.Background(), owners)
		if err != nil {
			t.Fatal(err)
		}
		if got := check.Owners["frostyard"]; got != "the App is not installed on frostyard" {
			t.Errorf("reason %q", got)
		}
		if got := check.Owners["bketelsen"]; got != "" {
			t.Errorf("bketelsen: %q", got)
		}
	})
	t.Run("another app", func(t *testing.T) {
		f := ready(t)
		f.installs = []map[string]any{install("another-app", "frostyard", readyPerms(), false)}
		check, err := f.client().CheckApp(context.Background(), owners)
		if err != nil {
			t.Fatal(err)
		}
		if got := check.Owners["frostyard"]; got != "the App is not installed on frostyard" {
			t.Errorf("reason %q", got)
		}
	})
	t.Run("no checks", func(t *testing.T) {
		f := ready(t)
		perms := readyPerms()
		delete(perms, "checks")
		f.installs[1] = install(AppSlug, "frostyard", perms, false)
		check, err := f.client().CheckApp(context.Background(), owners)
		if err != nil {
			t.Fatal(err)
		}
		if got := check.Owners["frostyard"]; got != "the App lacks checks: read on frostyard" {
			t.Errorf("reason %q", got)
		}
	})
	t.Run("write meets read", func(t *testing.T) {
		f := ready(t)
		perms := readyPerms()
		perms["checks"] = "write"
		f.installs[1] = install(AppSlug, "frostyard", perms, false)
		check, err := f.client().CheckApp(context.Background(), owners)
		if err != nil {
			t.Fatal(err)
		}
		if got := check.Owners["frostyard"]; got != "" {
			t.Errorf("reason %q", got)
		}
	})
	t.Run("contents read", func(t *testing.T) {
		f := ready(t)
		perms := readyPerms()
		perms["contents"] = "read"
		f.installs[1] = install(AppSlug, "frostyard", perms, false)
		check, err := f.client().CheckApp(context.Background(), owners)
		if err != nil {
			t.Fatal(err)
		}
		if got := check.Owners["frostyard"]; got != "the App lacks contents: write on frostyard" {
			t.Errorf("reason %q", got)
		}
	})
	t.Run("suspended", func(t *testing.T) {
		f := ready(t)
		f.installs[1] = install(AppSlug, "frostyard", readyPerms(), true)
		check, err := f.client().CheckApp(context.Background(), owners)
		if err != nil {
			t.Fatal(err)
		}
		if got := check.Owners["frostyard"]; got != "the App is suspended on frostyard" {
			t.Errorf("reason %q", got)
		}
	})
	t.Run("no merge queues", func(t *testing.T) {
		f := ready(t)
		perms := readyPerms()
		delete(perms, "merge_queues")
		f.installs[1] = install(AppSlug, "frostyard", perms, false)
		check, err := f.client().CheckApp(context.Background(), owners)
		if err != nil {
			t.Fatal(err)
		}
		if got := check.Owners["frostyard"]; got != "" {
			t.Errorf("reason %q", got)
		}
		if note := check.Notes["frostyard"]; !strings.Contains(note, "an enqueue may fail") {
			t.Errorf("note %q", note)
		}
	})
	t.Run("2 pages", func(t *testing.T) {
		f := ready(t)
		f.installs = nil
		for range installationsPerPage {
			f.installs = append(f.installs, install("another-app", "someone", readyPerms(), false))
		}
		f.installs = append(f.installs, install(AppSlug, "frostyard", readyPerms(), false))
		check, err := f.client().CheckApp(context.Background(), []string{"frostyard"})
		if err != nil {
			t.Fatal(err)
		}
		if got := check.Owners["frostyard"]; got != "" {
			t.Errorf("reason %q", got)
		}
		if len(f.gets) != 3 { // GET /user and 2 pages
			t.Errorf("reads %v", f.gets)
		}
	})
	t.Run("a failed page", func(t *testing.T) {
		f := ready(t)
		f.restFail = func(path string) (int, string, http.Header) {
			if path == "/user/installations" {
				return 502, `{"message":"Server Error"}`, nil
			}
			return 0, "", nil
		}
		if _, err := f.client().CheckApp(context.Background(), owners); !errors.Is(err, ErrNoAnswer) {
			t.Errorf("err %v", err)
		}
	})
	t.Run("a refused read", func(t *testing.T) {
		f := ready(t)
		f.restFail = func(path string) (int, string, http.Header) {
			if path == "/user" {
				return 401, `{"message":"Bad credentials"}`, nil
			}
			return 0, "", nil
		}
		_, err := f.client().CheckApp(context.Background(), owners)
		var api *APIError
		if !errors.As(err, &api) || api.Status != 401 {
			t.Errorf("err %v", err)
		}
		if !strings.HasPrefix(err.Error(), "GET /user: ") {
			t.Errorf("the error names no call: %v", err)
		}
	})
}

// TestInstallationsHoldTheTotalCount: a page that is shorter than the
// total_count, or longer than it, proves nothing.
func TestInstallationsHoldTheTotalCount(t *testing.T) {
	installs := func(n int) []map[string]any {
		out := make([]map[string]any, 0, n)
		for i := range n {
			out = append(out, install("another-app", fmt.Sprintf("owner%d", i), readyPerms(), false))
		}
		return out
	}
	t.Run("a short page", func(t *testing.T) {
		f := direct(t)
		f.installs = installs(2)
		total := 101
		f.installTotal = &total
		_, err := f.client().CheckApp(context.Background(), []string{Login})
		if !errors.Is(err, ErrIncomplete) {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("no total_count", func(t *testing.T) {
		f := direct(t)
		f.installs = nil
		var more = -1
		f.installTotal = &more
		if _, err := f.client().CheckApp(context.Background(), []string{Login}); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("page 2 fails", func(t *testing.T) {
		f := direct(t)
		f.installs = installs(installationsPerPage + 1)
		f.installPage = func(page int) (int, string) {
			if page == 2 {
				return 502, `{"message":"Server Error"}`
			}
			return 0, ""
		}
		check, err := f.client().CheckApp(context.Background(), []string{Login})
		if !errors.Is(err, ErrNoAnswer) {
			t.Fatalf("err %v", err)
		}
		if len(check.Owners) != 0 || check.Login != "" {
			t.Errorf("a failed read kept %+v", check)
		}
	})
	t.Run("exactly 100", func(t *testing.T) {
		f := direct(t)
		f.installs = append(installs(installationsPerPage), install(AppSlug, Login, readyPerms(), false))
		f.installs = f.installs[:installationsPerPage]
		check, err := f.client().CheckApp(context.Background(), []string{Login})
		if err != nil {
			t.Fatal(err)
		}
		if got := check.Owners[Login]; got != "the App is not installed on bketelsen" {
			t.Errorf("reason %q", got)
		}
		if len(f.gets) != 2 { // GET /user and 1 page
			t.Errorf("reads %v", f.gets)
		}
	})
}

// TestPreflightCarriesTheError: a read that fails gives the row its redacted
// error, so a caller tells a refused sign-in from a repository that has no
// button.
func TestPreflightCarriesTheError(t *testing.T) {
	f := direct(t)
	row := f.client().Preflight(context.Background(), f.ref)
	if row.Err != nil || row.Reason != "" {
		t.Fatalf("row %+v", row)
	}
	f.restFail = func(string) (int, string, http.Header) {
		return 401, `{"message":"Bad credentials for ` + testToken + `"}`, nil
	}
	row = f.client().Preflight(context.Background(), f.ref)
	var api *APIError
	if !errors.As(row.Err, &api) || api.Status != 401 {
		t.Fatalf("row %+v", row)
	}
	if row.Readable || row.Reason == "" || strings.Contains(row.Err.Error(), testToken) {
		t.Errorf("row %+v", row)
	}
}
