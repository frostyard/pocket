package pocket

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPlanRoutesPreserveContracts(t *testing.T) {
	c, _ := NewClient("https://example.org", "test")
	called := []string{}
	c.HTTP = &http.Client{Transport: sessionToolTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer test" || r.Header.Get("X-Pocket") != "1" {
			t.Error("auth lost")
		}
		called = append(called, r.Method+" "+r.URL.Path)
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/answer") {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			marks, _ := body["marks"].(map[string]any)
			if body["gen"] != float64(4) || marks["1/r2"] != "partly" {
				t.Fatal(body)
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"T7","rev":"abc","canApprove":true,"graph":{"version":2,"tasks":[]},"sections":[{"id":"s1","asks":[{"id":"T7a","gen":4,"open":true,"criteria":[{"id":"r2","label":"Safe"}],"marks":{"1/r2":"partly"}}]}],"plan":"T9","leads":[{"id":"duncan"}],"startPlan":true}`)), Header: make(http.Header)}, nil
	})}
	ctx := context.Background()
	p, err := c.DeskPlan(ctx, "T7")
	if err != nil || p.Graph.Version != 2 || p.Sections[0].Asks[0].Marks["1/r2"] != "partly" {
		t.Fatal(p, err)
	}
	for _, action := range []string{"approve", "changes", "comment", "revise"} {
		if err = c.PlanAction(ctx, "T7", action, map[string]any{"rev": "abc", "graph": 2, "key": "test-key"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, action := range []string{"answer", "marks", "reopen"} {
		if err = c.PlanAskAction(ctx, "T7a", action, map[string]any{"gen": 4, "marks": map[string]string{"1/r2": "partly"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err = c.DismissPlanComment(ctx, "T7", "c1"); err != nil {
		t.Fatal(err)
	}
	if err = c.StopPlanConsult(ctx, "T7", "k1"); err != nil {
		t.Fatal(err)
	}
	roster, err := c.PlanRoster(ctx)
	if err != nil || !roster.StartPlan || roster.Leads[0].ID != "duncan" {
		t.Fatal(roster, err)
	}
	id, err := c.NewPlan(ctx, 7, map[string]any{"title": "A plan", "idea": "Build it", "lead": "duncan", "key": "test-key"})
	if err != nil || id != "T9" {
		t.Fatal(id, err)
	}
	want := []string{"GET /api/x/desk/plans/T7", "POST /api/x/desk/plans/T7/approve", "POST /api/x/desk/plans/T7/changes", "POST /api/x/desk/plans/T7/comment", "POST /api/x/desk/plans/T7/revise", "POST /api/x/desk/asks/T7a/answer", "POST /api/x/desk/asks/T7a/marks", "POST /api/x/desk/asks/T7a/reopen", "POST /api/x/desk/plans/T7/comments/c1/dismiss", "POST /api/x/desk/plans/T7/consults/k1/stop", "GET /api/x/desk/roster", "POST /api/x/desk/plans/new/7"}
	if strings.Join(called, "\n") != strings.Join(want, "\n") {
		t.Fatal(called)
	}
}
