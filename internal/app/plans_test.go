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

func TestReopeningPendingNewPlanKeepsOneCreation(t *testing.T) {
	a, loop := teamApp(t)
	entered, release := make(chan struct{}), make(chan struct{})
	created := make(chan string, 2)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			if strings.HasSuffix(r.URL.Path, "/roster") {
				return reply(r, `{"leads":[{"id":"duncan"}],"supervisors":[1],"plans":{"max":3},"startPlan":true}`), nil
			}
			return reply(r, strings.ReplaceAll(testPlanJSON, "T7", "T9")), nil
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		key, _ := body["key"].(string)
		created <- key
		select {
		case <-entered:
			t.Error("a second create was sent")
		default:
			close(entered)
		}
		<-release
		return reply(r, `{"plan":"T9"}`), nil
	})}
	a.openNewPlan()
	loop.RunUntil(t, func() bool { return !a.newPlan.loading })
	s := a.newPlan
	s.title = "One plan"
	s.idea = "Create it only once"
	a.startPlan()
	<-entered
	key := s.key.key
	s.open = false
	a.openNewPlan()
	if a.newPlan != s || !s.open || s.key.key != key || s.title != "One plan" {
		t.Fatal("pending creation was replaced")
	}
	a.startPlan()
	close(release)
	loop.RunUntil(t, func() bool { return !s.busy && a.plan != nil && !a.plan.loading })
	if a.selected != "p:T9" || a.plan.view.ID != "T9" {
		t.Fatal("created plan was lost")
	}
	if len(created) != 1 || <-created != key {
		t.Fatal("creation replay key changed")
	}
}

func TestPlanFormsAndUntouchedMarksFollowLatestView(t *testing.T) {
	a, loop := teamApp(t)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) { return reply(r, testPlanJSON), nil })}
	a.openPlan("T7")
	loop.RunUntil(t, func() bool { return !a.plan.loading })
	s := a.plan
	ask := s.view.Sections[0].Asks[0]
	d := s.draft(ask)
	d.words = "An unsent answer"
	d.marks["0/r2"] = "partly"
	a.beginPlanForm("comment", "s1", nil)
	s.text = "A comment draft"
	a.beginPlanForm("drop", "", nil)
	if s.text != "" {
		t.Fatal("comment became drop reason")
	}
	s.closeForm()
	a.beginPlanForm("comment", "s1", nil)
	if s.text != "A comment draft" {
		t.Fatal("comment draft lost")
	}
	a.openPlan("T7")
	loop.RunUntil(t, func() bool { return !s.loading })
	if a.plan != s || s.draft(ask).words != "An unsent answer" {
		t.Fatal("same plan discarded drafts")
	}
	ask.Marks["1/r1"] = "yes"
	marks := planMarks(ask, d)
	if marks["1/r1"] != "yes" || marks["0/r2"] != "partly" {
		t.Fatal("local edits overwrote untouched server marks")
	}
	d.marks["0/r1"] = ""
	if _, ok := planMarks(ask, d)["0/r1"]; ok {
		t.Fatal("explicitly cleared mark returned")
	}
}

func TestPlanButtonsSendDisplayedContracts(t *testing.T) {
	for _, action := range []string{"approve", "changes", "dismiss", "stop", "reopen", "drop"} {
		t.Run(action, func(t *testing.T) {
			a, loop := teamApp(t)
			type request struct {
				path string
				body map[string]any
			}
			sent := make(chan request, 1)
			data := testPlanJSON
			if action == "reopen" {
				data = strings.ReplaceAll(data, `"open":true`, `"open":false,"answer":{"words":"Small change","by":"Owner"}`)
			}
			if action == "stop" {
				data = strings.TrimSuffix(data, "}") + `,"consults":[{"id":"k1","agent":"duncan","state":"running","question":"Check it","canStop":true}]}`
			}
			a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
				if r.Method == "GET" {
					return reply(r, data), nil
				}
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				sent <- request{r.URL.Path, body}
				return reply(r, `{}`), nil
			})}
			a.openPlan("T7")
			loop.RunUntil(t, func() bool { return !a.plan.loading })
			if action == "stop" {
				a.plan.view.Sections = nil
				a.plan.view.Graph = nil
			}
			tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.planPane(c); a.planActionDialog(c); a.dialogs.View(c) }, 1000, 1000, ui.TesterOptions{})
			click := func(label string) {
				t.Helper()
				tt.Frame()
				if err := tt.Click(label); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
			}
			switch action {
			case "approve":
				click("Approve plan")
			case "changes":
				click("Request changes…")
				a.plan.text = "Explain the fallback"
				a.plan.section = "s1"
				click("Submit request changes")
			case "dismiss":
				click("Dismiss c1")
			case "stop":
				click("Stop consult k1")
			case "reopen":
				click("Reopen T7a")
				if _, ok := tt.Find("Plan action text"); ok {
					t.Fatal("plain reopen shows ignored text field")
				}
				click("Submit reopen decision")
			case "drop":
				click("Drop plan…")
				a.plan.text = "Replaced by a smaller plan"
				click("Submit drop plan")
				if !tt.HasText("End plan T7 · Ship a useful change and stop its tasks? This cannot be undone. Replaced by a smaller plan") {
					t.Fatal("drop confirmation is not specific")
				}
				writePNG(t, tt, "plan-drop-confirmation")
				click("Confirm drop plan")
			}
			loop.RunUntil(t, func() bool { return !a.plan.busy && !a.plan.loading })
			var got request
			select {
			case got = <-sent:
			default:
				t.Fatal("button did not send action")
			}
			want := map[string]string{"approve": "plans/T7/approve", "changes": "plans/T7/changes", "dismiss": "plans/T7/comments/c1/dismiss", "stop": "plans/T7/consults/k1/stop", "reopen": "asks/T7a/reopen", "drop": "threads/T7/drop"}[action]
			if got.path != "/api/x/desk/"+want {
				t.Fatal(got.path)
			}
			switch action {
			case "approve":
				if got.body["rev"] != "rev-one" || got.body["graph"] != float64(3) || got.body["key"] == "" {
					t.Fatal(got.body)
				}
			case "changes":
				if got.body["rev"] != "rev-one" || got.body["section"] != "s1" || got.body["text"] != "Explain the fallback" || got.body["key"] == "" {
					t.Fatal(got.body)
				}
			case "reopen":
				if got.body["gen"] != float64(2) || got.body["key"] == "" {
					t.Fatal(got.body)
				}
			case "drop":
				if got.body["reason"] != "Replaced by a smaller plan" {
					t.Fatal(got.body)
				}
			}
		})
	}
}

func TestPlanEventRefreshKeepsDraftAndUneditedMarks(t *testing.T) {
	a, loop := teamApp(t)
	reads := 0
	sent := make(chan map[string]any, 1)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			sent <- body
			return reply(r, `{}`), nil
		}
		if !strings.Contains(r.URL.Path, "/plans/") {
			return reply(r, `{}`), nil
		}
		reads++
		data := testPlanJSON
		if reads > 1 {
			data = strings.ReplaceAll(data, `"1/r1":"no"`, `"1/r1":"yes"`)
		}
		return reply(r, data), nil
	})}
	a.openPlan("T7")
	loop.RunUntil(t, func() bool { return !a.plan.loading })
	s := a.plan
	s.draft(s.view.Sections[0].Asks[0]).words = "Use the small change"
	a.deskChanged()
	loop.RunUntil(t, func() bool { return !s.loading && !a.desk.loading })
	ask := s.view.Sections[0].Asks[0]
	if planMarks(ask, s.draft(ask))["1/r1"] != "yes" || s.draft(ask).words != "Use the small change" {
		t.Fatal("event refresh lost draft or retained stale marks")
	}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.planPane(c) }, 1000, 900, ui.TesterOptions{})
	tt.Frame()
	if err := tt.Click("Send answer T7a"); err != nil {
		t.Fatal(err)
	}
	loop.RunUntil(t, func() bool { return !s.busy && !s.loading })
	select {
	case body := <-sent:
		if _, ok := body["marks"]; ok {
			t.Fatal("unedited marks were overwritten")
		}
	default:
		t.Fatal("answer not sent")
	}
}

func TestPlanReasonLimitsPreventInvalidRequests(t *testing.T) {
	for _, tc := range []struct{ kind, text string }{{"drop", strings.Repeat("x", 201)}, {"drop", "two\nlines"}, {"revise", strings.Repeat("x", 301)}, {"revise", "two\nlines"}} {
		t.Run(tc.kind+tc.text[:3], func(t *testing.T) {
			a, loop := teamApp(t)
			a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" {
					t.Error("invalid reason reached server")
				}
				return reply(r, testPlanJSON), nil
			})}
			a.openPlan("T7")
			loop.RunUntil(t, func() bool { return !a.plan.loading })
			a.beginPlanForm(tc.kind, "", nil)
			a.plan.text = tc.text
			tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Box(c).Fill(); a.planActionDialog(c); a.dialogs.View(c) }, 900, 600, ui.TesterOptions{})
			tt.Frame()
			label := map[string]string{"drop": "Submit drop plan", "revise": "Submit stop and revise"}[tc.kind]
			_ = tt.Click(label)
			tt.Frame()
			if a.plan.busy || a.dialogs.Showing() {
				t.Fatal("invalid reason reached confirmation")
			}
			if !tt.HasText("The text exceeds the limit or contains a line break.") {
				t.Fatal("invalid reason not explained")
			}
		})
	}
}

// A saved form draft belongs to its own form: an action that another part of
// the page submits never discards it, whether the form was cancelled or
// dismissed with Escape.
func TestPlanActionsKeepAnotherFormDraft(t *testing.T) {
	for _, leave := range []string{"cancel", "escape"} {
		for _, action := range []string{"marks", "answer", "dismiss", "stop"} {
			t.Run(leave+"/"+action, func(t *testing.T) {
				a, loop := teamApp(t)
				data := testPlanJSON
				if action == "stop" {
					data = strings.TrimSuffix(data, "}") + `,"consults":[{"id":"k1","agent":"duncan","state":"running","question":"Check it","canStop":true}]}`
				}
				a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
					if r.Method == "GET" {
						return reply(r, data), nil
					}
					return reply(r, `{}`), nil
				})}
				a.openPlan("T7")
				loop.RunUntil(t, func() bool { return !a.plan.loading })
				s := a.plan
				ask := s.view.Sections[0].Asks[0]
				a.beginPlanForm("comment", "s1", nil)
				s.text = "unsent comment"
				tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Box(c).Fill(); a.planActionDialog(c) }, 800, 500, ui.TesterOptions{})
				tt.Frame()
				switch leave {
				case "cancel":
					if err := tt.Click("Cancel action"); err != nil {
						t.Fatal(err)
					}
				case "escape":
					tt.Key(0, ui.KeyEscape)
				}
				tt.Frame()
				if s.form != "" || s.formTexts["comment:s1"] != "unsent comment" {
					t.Fatalf("%s did not keep the comment draft: %q %q", leave, s.form, s.formTexts["comment:s1"])
				}
				switch action {
				case "marks":
					a.planAct("marks:"+ask.ID, map[string]any{"gen": ask.Gen, "marks": map[string]string{"0/r1": "partly"}})
				case "answer":
					a.planAct("answer:"+ask.ID, map[string]any{"words": "Small change", "gen": ask.Gen})
				case "dismiss":
					a.planAct("dismiss:c1", map[string]any{})
				case "stop":
					a.planAct("stop:k1", map[string]any{})
				}
				loop.RunUntil(t, func() bool { return !s.busy && !s.loading })
				if s.err != "" {
					t.Fatalf("%s failed: %s", action, s.err)
				}
				if s.form != "" {
					t.Fatalf("%s opened a form", action)
				}
				a.beginPlanForm("comment", "s1", nil)
				if s.text != "unsent comment" {
					t.Fatalf("%s after %s erased the comment draft", action, leave)
				}
			})
		}
	}
}

// A successful submission clears the submitted form's draft and leaves
// another form's draft in place.
func TestPlanCommentSubmitClearsOnlyItsOwnDraft(t *testing.T) {
	a, loop := teamApp(t)
	sent := make(chan struct{}, 1)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return reply(r, testPlanJSON), nil
		}
		sent <- struct{}{}
		return reply(r, `{}`), nil
	})}
	a.openPlan("T7")
	loop.RunUntil(t, func() bool { return !a.plan.loading })
	s := a.plan
	a.beginPlanForm("comment", "s1", nil)
	s.text = "unsent comment"
	s.closeForm()
	a.beginPlanForm("drop", "", nil)
	s.text = "not yet a reason"
	s.closeForm()
	if s.formTexts["comment:s1"] == "" || s.formTexts["drop:"] == "" {
		t.Fatal("forms did not save their drafts")
	}
	a.beginPlanForm("comment", "s1", nil)
	if s.text != "unsent comment" {
		t.Fatalf("reopened comment text %q", s.text)
	}
	s.text = "Please explain the rollback."
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Box(c).Fill(); a.planActionDialog(c) }, 800, 500, ui.TesterOptions{})
	tt.Frame()
	if err := tt.Click("Submit add comment"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	loop.RunUntil(t, func() bool { return !s.busy && !s.loading })
	select {
	case <-sent:
	default:
		t.Fatal("comment action not sent")
	}
	if s.err != "" {
		t.Fatal(s.err)
	}
	if s.form != "" || s.text != "" {
		t.Fatalf("submitted form stayed open: %q %q", s.form, s.text)
	}
	if _, ok := s.formTexts["comment:s1"]; ok {
		t.Fatal("submitted comment kept its draft")
	}
	if s.formTexts["drop:"] != "not yet a reason" {
		t.Fatal("submitting a comment erased another form's draft")
	}
	a.beginPlanForm("comment", "s1", nil)
	if s.text != "" {
		t.Fatalf("submitted comment text returned: %q", s.text)
	}
	a.beginPlanForm("drop", "", nil)
	if s.text != "not yet a reason" {
		t.Fatalf("drop draft lost: %q", s.text)
	}
}
