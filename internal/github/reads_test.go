package github

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
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
	if !row.Readable || row.Bypass != "always" || !strings.Contains(row.Reason, "bypass") {
		t.Errorf("bypass row %+v", row)
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
	pr["mergeQueueEntry"] = map[string]any{"enqueuer": map[string]any{"login": Login}, "headCommit": map[string]any{"oid": p.Head}}
	if r := c.Settle(context.Background(), in, "op", false); r.Kind != "queued" || r.Method != "queue" {
		t.Errorf("queued %+v", r)
	}
	pr["isInMergeQueue"], pr["mergeQueueEntry"] = false, nil
	pr["state"], pr["mergedBy"], pr["mergeCommit"] = "MERGED", map[string]any{"login": Login}, map[string]any{"oid": mergeOid}
	if r := c.Settle(context.Background(), in, "op", false); r.Kind != "merged" || r.Method != "squash" {
		t.Errorf("merged by the queue %+v", r)
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
