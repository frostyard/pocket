package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

const testPlanJSON = `{"id":"T7","title":"Ship a useful change","state":"review","rev":"rev-one","lead":"duncan","session":7,"preamble":"A **native** plan review.","canApprove":true,"graph":{"version":3,"tasks":[{"id":"t1","title":"Build it","agent":"duncan","state":"ready","acceptance":["The tests pass"],"deps":[]}]},"progress":{"steps":[{"id":"draft","label":"Draft","state":"done"},{"id":"review","label":"Review","state":"current"},{"id":"approved","label":"Approved","state":"todo"}]},"sections":[{"id":"s1","heading":"Approach","text":"Use the existing client protocol.","asks":[{"id":"T7a","text":"Which approach?","gen":2,"open":true,"options":["Small change","Rewrite"],"criteria":[{"id":"r1","label":"Easy to maintain"},{"id":"r2","label":"Easy to undo"}],"marks":{"0/r1":"yes","1/r1":"no"}}],"comments":[{"id":"c1","text":"Explain the fallback.","by":"Owner","state":"open"}]}]}`

func TestPlanAnswerIncludesGenerationAndMarks(t *testing.T) {
	a, loop := teamApp(t)
	sent := make(chan map[string]any, 1)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			if r.URL.Path != "/api/x/desk/asks/T7a/answer" {
				t.Error(r.URL)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			sent <- body
			return reply(r, `{}`), nil
		}
		return reply(r, testPlanJSON), nil
	})}
	a.openPlan("T7")
	loop.RunUntil(t, func() bool { return !a.plan.loading })
	s := a.plan
	ask := s.view.Sections[0].Asks[0]
	d := s.draft(ask)
	d.words = "Small change"
	d.why = "Keeps the existing behavior"
	d.marks["0/r2"] = "partly"
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.planPane(c) }, 1000, 900, ui.TesterOptions{})
	tt.Frame()
	writePNG(t, tt, "plan-review")
	if err := tt.Click("Send answer T7a"); err != nil {
		t.Fatal(err)
	}
	loop.RunUntil(t, func() bool { return !s.busy && !s.loading })
	body := <-sent
	marks, _ := body["marks"].(map[string]any)
	if body["gen"] != float64(2) || body["words"] != "Small change" || marks["0/r1"] != "yes" || marks["0/r2"] != "partly" || body["why"] != "Keeps the existing behavior" {
		t.Fatalf("answer %v", body)
	}
}
func TestPlanConflictReloadsWithoutReusingGenerationDraft(t *testing.T) {
	a, loop := teamApp(t)
	reads := 0
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			resp := reply(r, `{"error":"The decision reopened since you read it"}`)
			resp.StatusCode = 409
			return resp, nil
		}
		reads++
		data := testPlanJSON
		if reads > 1 {
			data = strings.ReplaceAll(strings.ReplaceAll(data, `"gen":2`, `"gen":3`), "rev-one", "rev-two")
		}
		return reply(r, data), nil
	})}
	a.openPlan("T7")
	loop.RunUntil(t, func() bool { return !a.plan.loading })
	s := a.plan
	ask := s.view.Sections[0].Asks[0]
	s.draft(ask).words = "old answer"
	s.form = "comment"
	s.text = "keep my comment"
	a.planAct("answer:T7a", map[string]any{"words": "old answer", "gen": 2, "marks": map[string]string{}})
	loop.RunUntil(t, func() bool { return !s.busy && !s.loading })
	if s.view.Rev != "rev-two" || s.view.Sections[0].Asks[0].Gen != 3 || s.draft(s.view.Sections[0].Asks[0]).words != "" || s.draft(ask).words != "old answer" || s.text != "keep my comment" || s.form != "" || !strings.Contains(s.err, "Review it") {
		t.Fatal("conflict did not separate new decision from old draft")
	}
}
func TestPlanApprovalReplayKeyTracksExactPayload(t *testing.T) {
	a, loop := teamApp(t)
	sent := make(chan map[string]any, 3)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return reply(r, testPlanJSON), nil
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		sent <- body
		resp := reply(r, `{"error":"temporary failure"}`)
		resp.StatusCode = 503
		return resp, nil
	})}
	a.openPlan("T7")
	loop.RunUntil(t, func() bool { return !a.plan.loading })
	send := func(rev string) map[string]any {
		a.planAct("approve", map[string]any{"rev": rev, "graph": 3})
		loop.RunUntil(t, func() bool { return !a.plan.busy })
		return <-sent
	}
	first, second, third := send("rev-one"), send("rev-one"), send("rev-two")
	if first["key"] == "" || first["key"] != second["key"] || first["key"] == third["key"] || first["graph"] != float64(3) {
		t.Fatal(first, second, third)
	}
}
func TestPlanReadCannotReplaceAnotherPlan(t *testing.T) {
	a, loop := teamApp(t)
	entered, release := make(chan struct{}), make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "T7") {
			close(entered)
			<-release
			return reply(r, testPlanJSON), nil
		}
		return reply(r, strings.ReplaceAll(testPlanJSON, "T7", "T8")), nil
	})}
	a.openPlan("T7")
	<-entered
	a.openPlan("T8")
	loop.RunUntil(t, func() bool { return !a.plan.loading })
	before := loop.Ran()
	close(release)
	loop.RunUntil(t, func() bool { return loop.Ran() > before })
	if a.plan.id != "T8" || a.plan.view.ID != "T8" {
		t.Fatal("late read replaced selected plan")
	}
}
func TestPlanCommentCapturesRevision(t *testing.T) {
	a, loop := teamApp(t)
	sent := make(chan map[string]any, 1)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return reply(r, testPlanJSON), nil
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		sent <- body
		return reply(r, `{}`), nil
	})}
	a.openPlan("T7")
	loop.RunUntil(t, func() bool { return !a.plan.loading })
	a.beginPlanForm("comment", "s1", nil)
	a.plan.text = "Please explain the rollback."
	// The form binds the revision it opened against, even if a refresh replaces the page.
	a.plan.view.Rev = "rev-two"
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Box(c).Fill(); a.planActionDialog(c) }, 800, 500, ui.TesterOptions{})
	tt.Frame()
	if err := tt.Click("Submit add comment"); err != nil {
		t.Fatal(err)
	}
	loop.RunUntil(t, func() bool { return !a.plan.busy && !a.plan.loading })
	var body map[string]any
	select {
	case body = <-sent:
	default:
		t.Fatal("comment action not sent")
	}
	if body["rev"] != "rev-one" || body["section"] != "s1" || body["key"] == "" {
		t.Fatal(body)
	}
}
func TestNewPlanKeepsKeyAfterFailureAndDoesNotNavigateAfterClose(t *testing.T) {
	a, loop := teamApp(t)
	calls := 0
	sent := make(chan map[string]any, 2)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return reply(r, `{"leads":[{"id":"duncan","name":"Duncan"}],"supervisors":[1],"plans":{"open":0,"max":3},"startPlan":true}`), nil
		}
		if r.URL.Path != "/api/x/desk/plans/new/1" {
			t.Error(r.URL)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		sent <- body
		calls++
		if calls == 1 {
			resp := reply(r, `{"error":"temporary"}`)
			resp.StatusCode = 503
			return resp, nil
		}
		return reply(r, `{"plan":"T9"}`), nil
	})}
	a.openNewPlan()
	loop.RunUntil(t, func() bool { return !a.newPlan.loading })
	s := a.newPlan
	s.title = "Make the tool useful"
	s.idea = "Add the missing review controls"
	a.startPlan()
	loop.RunUntil(t, func() bool { return !s.busy })
	first := <-sent
	a.startPlan()
	s.open = false
	selected := a.selected
	loop.RunUntil(t, func() bool { return !s.busy })
	second := <-sent
	if first["key"] == "" || first["key"] != second["key"] || first["lead"] != "duncan" || a.selected != selected || s.title == "" {
		t.Fatal("new plan replay or navigation changed")
	}
}
func TestPlanViewerCannotReadOrMutate(t *testing.T) {
	a, _ := teamApp(t)
	a.hello.User.Role = "viewer"
	a.openPlan("T7")
	a.openNewPlan()
	if a.plan != nil || a.newPlan != nil {
		t.Fatal("viewer offered owner plan routes")
	}
}
func TestPlanAskFromInboxOpensCriteriaView(t *testing.T) {
	a, loop := teamApp(t)
	gen := 2
	ask := pocket.DeskAsk{ID: "T7a", Text: "Choose", Gen: &gen}
	a.desk.view = &pocket.DeskView{Threads: []pocket.DeskThread{{ID: "T7", Origin: "plan", Asks: []pocket.DeskAsk{ask}}}}
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Error("Inbox bypassed criteria")
		}
		return reply(r, testPlanJSON), nil
	})}
	a.answerAsk(&ask, "Small change")
	loop.RunUntil(t, func() bool { return a.plan != nil && !a.plan.loading })
	if a.selected != "p:T7" {
		t.Fatal("plan decision not opened")
	}
}

func TestPlanRevisionConfirmsApprovedGraphAndTasks(t *testing.T) {
	a, loop := teamApp(t)
	sent := make(chan map[string]any, 1)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return reply(r, strings.ReplaceAll(testPlanJSON, `"graph":`, `"approvedGraph":`)), nil
		}
		if r.URL.Path != "/api/x/desk/plans/T7/revise" {
			t.Error(r.URL)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		sent <- body
		return reply(r, `{}`), nil
	})}
	a.openPlan("T7")
	loop.RunUntil(t, func() bool { return !a.plan.loading })
	a.plan.view.State = "building"
	a.plan.view.CanRevise = true
	a.beginPlanForm("revise", "", nil)
	a.plan.text = "The scope needs to include migration"
	a.plan.tasks["t1"] = true
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Box(c).Fill(); a.planActionDialog(c); a.dialogs.View(c) }, 900, 700, ui.TesterOptions{})
	tt.Frame()
	if err := tt.Click("Submit stop and revise"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	select {
	case <-sent:
		t.Fatal("scope change skipped confirmation")
	default:
	}
	if err := tt.Click("Confirm stop and revise"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	loop.RunUntil(t, func() bool { return !a.plan.busy && !a.plan.loading })
	var body map[string]any
	select {
	case body = <-sent:
	default:
		t.Fatal("scope change not sent")
	}
	tasks, _ := body["tasks"].([]any)
	if body["graph"] != float64(3) || body["key"] == "" || len(tasks) != 1 || tasks[0] != "t1" {
		t.Fatal(body)
	}
}

func TestSavingPlanMarksKeepsUnsentAnswer(t *testing.T) {
	a, loop := teamApp(t)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return reply(r, testPlanJSON), nil
		}
		return reply(r, `{}`), nil
	})}
	a.openPlan("T7")
	loop.RunUntil(t, func() bool { return !a.plan.loading })
	ask := a.plan.view.Sections[0].Asks[0]
	draft := a.plan.draft(ask)
	draft.words = "Keep this answer"
	draft.why = "Keep this reason"
	a.planAct("marks:T7a", map[string]any{"gen": ask.Gen, "marks": map[string]string{"0/r1": "yes"}})
	loop.RunUntil(t, func() bool { return !a.plan.busy && !a.plan.loading })
	if d := a.plan.draft(ask); d.words != "Keep this answer" || d.why != "Keep this reason" {
		t.Fatal("saving marks consumed unsent answer")
	}
}

func TestNewPlanFormSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	var roster pocket.PlanRoster
	if err := pocket.Decode([]byte(`{"leads":[{"id":"duncan","name":"Duncan"}],"supervisors":[1],"plans":{"open":1,"max":3},"startPlan":true}`), &roster); err != nil {
		t.Fatal(err)
	}
	a.newPlan = &newPlanDraft{open: true, roster: &roster, title: "Review the release workflow", idea: "Make approvals and verification clear before work begins.", lead: "duncan", supervisor: 1}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Box(c).Fill(); a.newPlanDialog(c) }, 900, 650, ui.TesterOptions{})
	tt.Frame()
	writePNG(t, tt, "new-plan")
	if !tt.HasText("Create plan") {
		t.Fatal("plan creation action absent")
	}
}
