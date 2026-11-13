package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type actionHTTP func(*http.Request) (*http.Response, error)

func (f actionHTTP) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestThreadActionConflictRefreshesAndKeepsReason(t *testing.T) {
	a, loop := teamApp(t)
	var writes, reads atomic.Int32
	release := make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: actionHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			writes.Add(1)
			if r.URL.Path != "/api/x/desk/threads/T1/drop" {
				t.Errorf("path %s", r.URL.Path)
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["reason"] != "Replaced by another thread" {
				t.Errorf("body %v, %v", body, err)
			}
			<-release
			response := reply(r, `{"error":"An ask is still open"}`)
			response.StatusCode = 409
			return response, nil
		}
		reads.Add(1)
		return reply(r, `{"threads":[{"id":"T1","title":"Current thread","state":"open","asks":[{"id":"T1a","text":"Choose"}]}]}`), nil
	})}
	a.desk.editors = map[string]*threadEditor{"T1": {action: "drop", text: "Replaced by another thread"}}
	th := &pocket.DeskThread{ID: "T1", State: "open"}
	a.threadAct(th, "drop", map[string]any{"reason": "Replaced by another thread"})
	a.threadAct(th, "drop", map[string]any{"reason": "duplicate"})
	close(release)
	loop.RunUntil(t, func() bool {
		return a.desk.thread("T1") != nil && len(a.desk.thread("T1").Asks) == 1 && !a.desk.loading
	})
	if writes.Load() != 1 || reads.Load() != 1 || a.desk.busy["act:T1"] {
		t.Fatalf("writes=%d reads=%d busy=%v", writes.Load(), reads.Load(), a.desk.busy)
	}
	if a.desk.editors["T1"].text != "Replaced by another thread" {
		t.Fatal("lost reason after conflict")
	}
}

func TestDueWatchIsAnInboxDecisionAndActionsSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	a.sessions = nil
	a.lessons = lessons{}
	th := pocket.DeskThread{ID: "T1", Title: "Check the build after deployment", Origin: "delegate", State: "open", Watch: &pocket.DeskWatch{CheckOn: "2026-11-12", Check: "Check that all builds are green", Due: true}}
	a.desk = desk{view: &pocket.DeskView{Threads: []pocket.DeskThread{th}}}
	if a.inboxCount() != 1 || len(a.inboxItems()) != 1 || a.inboxItems()[0].kind != inboxWatch || motionGroup(&th) != "need" {
		t.Fatal("due check is not waiting on the owner")
	}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.inboxPane(c) }, 760, 620, ui.TesterOptions{})
	tt.Frame()
	for _, label := range []string{"Checks due", "Pass", "Fail…", "Watch…"} {
		if !tt.HasText(label) {
			t.Errorf("missing %s", label)
		}
	}
	tt.Click("Fail…")
	tt.Frame()
	if a.desk.editors["T1"].action != "fail" {
		t.Fatal("failure editor did not open")
	}
	writePNG(t, tt, "desk-due-watch")
	th.State = "closed"
	if dueWatch(&th) {
		t.Fatal("closed watch stays due")
	}
	th.State = "parked"
	if dueWatch(&th) {
		t.Fatal("parked watch stays due")
	}
}

func TestThreadActionsRespectStateAndRequireDropReason(t *testing.T) {
	a, _ := teamApp(t)
	th := &pocket.DeskThread{ID: "T2", State: "parked"}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Column(c).Children(func() { a.threadActions(c, th) }) }, 400, 500, ui.TesterOptions{})
	tt.Frame()
	if !tt.HasText("Wake") || tt.HasText("Park") || tt.HasText("Watch…") {
		t.Fatal("wrong parked actions")
	}
	tt.Click("Drop…")
	tt.Frame()
	tt.Click("Confirm drop")
	tt.Frame()
	if a.desk.busy["act:T2"] {
		t.Fatal("empty reason posted")
	}
	for _, value := range []string{"", strings.Repeat("x", 201), strings.Repeat("😀", 101), "two\nlines"} {
		if shortDeskLine(value) {
			t.Errorf("accepted invalid line %q", value)
		}
	}
	if !shortDeskLine(strings.Repeat("😀", 100)) {
		t.Fatal("valid 200-unit line refused")
	}
	th.State = "open"
	th.Origin = "plan"
	if canWatch(th, a.opts.Now()) {
		t.Fatal("plan can be watched")
	}
	th.Origin = "delegate"
	th.State = "closed"
	th.Closed = &struct {
		At      pocket.Millis `json:"at"`
		Why     string        `json:"why"`
		By      string        `json:"by"`
		Outcome string        `json:"outcome,omitempty"`
	}{At: pocket.Millis(a.opts.Now().Add(-8 * 24 * time.Hour).UnixMilli()), Why: "done"}
	if canWatch(th, a.opts.Now()) {
		t.Fatal("old closed thread can be watched")
	}
	th.Closed.At = pocket.Millis(a.opts.Now().Add(-time.Hour).UnixMilli())
	if !canWatch(th, a.opts.Now()) {
		t.Fatal("recent completion cannot be watched")
	}
	th.Closed.Why = "dropped"
	if canWatch(th, a.opts.Now()) {
		t.Fatal("dropped thread can be watched")
	}
}

func TestDeskSeenKeepsPreviousLookAndReadyLevelOrder(t *testing.T) {
	a, loop := teamApp(t)
	a.sessions = nil
	a.lessons = lessons{}
	var seen atomic.Int32
	a.link.client.HTTP = &http.Client{Transport: actionHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" && r.URL.Path == "/api/x/desk/seen" {
			seen.Add(1)
			return reply(r, `{}`), nil
		}
		if seen.Load() > 0 {
			return reply(r, `{"seen":100,"threads":[{"id":"T1","title":"New activity","state":"open","lastEventAt":20}]}`), nil
		}
		return reply(r, `{"seen":10,"threads":[{"id":"T1","title":"New activity","state":"open","lastEventAt":20}]}`), nil
	})}
	a.goPlace("x:motion")
	loop.RunUntil(t, func() bool { return a.desk.seenReady && seen.Load() == 1 && !a.desk.loading })
	if a.desk.seenBefore != 10 {
		t.Fatalf("previous look=%d", a.desk.seenBefore)
	}
	a.loadDesk()
	loop.RunUntil(t, func() bool { return !a.desk.loading })
	if seen.Load() != 1 {
		t.Fatal("ordinary refresh stamped another visit")
	}
	if a.desk.seenBefore != 10 {
		t.Fatal("fresh marks lost after stamp")
	}
	a.desk.view = &pocket.DeskView{Ready: []pocket.ReadyPR{{Ref: "none", At: 1}, {Ref: "strict", Level: "L1", At: 1}, {Ref: "easy-new", Level: "L4", At: 3}, {Ref: "easy-old", Level: "L4", At: 2}, {Ref: "medium", Level: "L2", At: 1}, {Ref: "risk", Level: "L1/L2", At: 1}, {Ref: "skim", Level: "L3", At: 1}}}
	var refs []string
	for _, it := range a.inboxItems() {
		refs = append(refs, it.pr.Ref)
	}
	if strings.Join(refs, ",") != "easy-old,easy-new,skim,medium,risk,strict,none" {
		t.Fatalf("order %v", refs)
	}
}

func TestDeskSupplementaryListsSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	a.desk.view = &pocket.DeskView{PlansDone: 1, Others: map[string]int{"example/tools": 1}, OtherPRs: map[string][]pocket.OtherPR{"example/tools": {{Ref: "example/tools#4", Title: "Update dependencies", Author: "dependabot", URL: "https://example.org/pr/4"}}}, Issues: &pocket.DeskIssues{OK: true, Items: []pocket.QueuedIssue{{Ref: "example/tools#8", Title: "Add a search field", Names: []string{"needs-plan"}, By: "owner", Urgent: true, URL: "https://example.org/issues/8"}}}}
	a.desk.completedLoaded = true
	a.desk.completed = []pocket.CompletedPlan{{ID: "T1", Title: "Improve the build", Lead: "duncan", Outcome: "verified", Rounds: 1, Session: 8}}
	a.tr.open = nil
	tt := ui.NewTesterWith(func(c *ui.Context) {
		frostui.Adapt(c)
		ui.Column(c).Gap(12).Padding(20).Children(func() { a.deskExtras(c, a.desk.view) })
	}, 760, 800, ui.TesterOptions{})
	tt.Frame()
	for _, label := range []string{"Completed plans", "Other PRs", "Queued issues"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	for _, label := range []string{"T1 · Improve the build", "example/tools#8 · Add a search field", "by dependabot"} {
		if !tt.HasText(label) {
			t.Errorf("missing %s", label)
		}
	}
	writePNG(t, tt, "desk-supplementary-lists")
}

func TestPlanAndSleepingThreadsDoNotExposeGenericDestructiveActions(t *testing.T) {
	a, _ := teamApp(t)
	th := &pocket.DeskThread{ID: "T1", State: "open", Origin: "plan"}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Column(c).Children(func() { a.threadActions(c, th) }) }, 400, 500, ui.TesterOptions{})
	tt.Frame()
	for _, label := range []string{"Drop…", "Park", "Watch…", "Keep"} {
		if tt.HasText(label) {
			t.Errorf("plan exposes %s", label)
		}
	}
	th.Origin = "delegate"
	th.Asleep = true
	tt.Frame()
	if tt.HasText("Park") {
		t.Fatal("sleeping thread can be parked")
	}
	th.Asleep = false
	th.State = "closed"
	th.Stale = true
	tt.Frame()
	if tt.HasText("Keep") {
		t.Fatal("closed thread can be kept")
	}
}

func TestThreadActionBodies(t *testing.T) {
	cases := []struct {
		action string
		body   map[string]any
	}{
		{"watch", map[string]any{"checkOn": "2026-11-20 09:00", "check": "Builds pass"}},
		{"attach", map[string]any{"into": "T2"}},
		{"result", map[string]any{"pass": true, "text": "Passed in Pocket."}},
		{"result", map[string]any{"pass": false, "text": "Build failed"}},
		{"unwatch", nil}, {"keep", nil}, {"park", nil}, {"wake", nil},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			a, loop := teamApp(t)
			posts := make(chan map[string]any, 1)
			a.link.client.HTTP = &http.Client{Transport: actionHTTP(func(r *http.Request) (*http.Response, error) {
				if r.Method == "POST" {
					if r.URL.Path != "/api/x/desk/threads/T1/"+tc.action {
						t.Errorf("path %s", r.URL)
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					posts <- body
				}
				return reply(r, `{"threads":[]}`), nil
			})}
			a.threadAct(&pocket.DeskThread{ID: "T1"}, tc.action, tc.body)
			loop.RunUntil(t, func() bool { return !a.desk.busy["act:T1"] && !a.desk.loading })
			got := <-posts
			want := tc.body
			if want == nil {
				want = map[string]any{}
			}
			gb, _ := json.Marshal(got)
			wb, _ := json.Marshal(want)
			if string(gb) != string(wb) {
				t.Fatalf("body %s, want %s", gb, wb)
			}
		})
	}
}
