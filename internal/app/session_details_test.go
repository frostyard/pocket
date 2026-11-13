package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type detailTransport func(*http.Request) (*http.Response, error)

func (f detailTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func detailReply(r *http.Request) *http.Response {
	if strings.Contains(r.URL.Path, "/night-shift/") {
		return reply(r, `{"goal":{"status":"running","round":2,"maxRounds":8,"objective":"Finish the build","stop":"All checks pass","deadlineLabel":"07:00 EST"}}`)
	}
	return reply(r, `{"identity":{"id":"duncan","name":"Duncan"},"implicit":true}`)
}

func TestSessionDetailsReadAndRefreshOnMatchingEvents(t *testing.T) {
	a, loop := teamApp(t)
	var mu sync.Mutex
	counts := map[string]int{}
	a.link.client.HTTP = &http.Client{Transport: detailTransport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		return detailReply(r), nil
	})}
	a.open(2)
	loop.RunUntil(t, func() bool { return !a.details.identityLoading && !a.details.goalLoading })
	if a.details.identity.Identity.ID != "duncan" || a.details.goal.Round != 2 {
		t.Fatal("missing session details")
	}
	for _, event := range []string{
		`{"module":"team","event":"identity","data":{"conversationId":3}}`,
		`{"module":"night-shift","event":"changed","data":{"conversation":3}}`,
	} {
		a.handle(pocket.Event{Name: "ext", Data: []byte(event)})
	}
	if a.details.identityLoading || a.details.goalLoading {
		t.Fatal("another session caused detail reads")
	}
	for _, event := range []string{
		`{"module":"team","event":"identity","data":{"conversationId":2}}`,
		`{"module":"night-shift","event":"changed","data":{"conversation":2}}`,
	} {
		a.handle(pocket.Event{Name: "ext", Data: []byte(event)})
	}
	loop.RunUntil(t, func() bool { return !a.details.identityLoading && !a.details.goalLoading })
	mu.Lock()
	defer mu.Unlock()
	if counts["/api/x/team/session/2"] != 2 || counts["/api/x/night-shift/goal/2"] != 2 {
		t.Fatalf("detail requests: %v", counts)
	}
}

func TestSessionDetailsDropLateAnswersAfterNavigation(t *testing.T) {
	a, loop := teamApp(t)
	release := make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: detailTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/1") {
			<-release
		}
		return detailReply(r), nil
	})}
	applied := 0
	a.post = func(fn func()) { loop.Post(func() { fn(); applied++ }) }
	a.open(1)
	old := a.details
	a.open(2)
	loop.RunUntil(t, func() bool { return !a.details.identityLoading && !a.details.goalLoading })
	current := a.details
	close(release)
	loop.RunUntil(t, func() bool { return applied == 4 })
	if a.details != current || current == old || current.id != 2 {
		t.Fatal("late answer replaced the current session")
	}
	if old.identity != nil || old.goal != nil {
		t.Fatal("late answer was applied to a departed session")
	}
}

func TestSessionIdentityActionsUseServerContracts(t *testing.T) {
	for _, tc := range []struct {
		name, agent, path string
		pin               bool
	}{
		{"choose", "duncan", "/identity", false}, {"clear", "", "/identity", false}, {"pin", "duncan", "/pin", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, loop := teamApp(t)
			posted := make(chan struct {
				path  string
				agent any
			}, 1)
			a.link.client.HTTP = &http.Client{Transport: detailTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodPost {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						return nil, err
					}
					posted <- struct {
						path  string
						agent any
					}{r.URL.Path, body["agent"]}
				}
				return detailReply(r), nil
			})}
			a.current = 2
			a.setSessionIdentity(tc.agent, tc.pin)
			loop.RunUntil(t, func() bool { return !a.details.identityBusy && !a.details.identityLoading })
			got := <-posted
			var want any
			if tc.agent != "" {
				want = tc.agent
			}
			if got.path != "/api/x/team/session/2"+tc.path || got.agent != want {
				t.Fatalf("posted %+v, want %s / %v", got, tc.path, want)
			}
		})
	}
}

func TestSessionDetailsSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	a.current = 2
	a.details = &sessionDetails{id: 2, identity: &pocket.TeamIdentity{Identity: &pocket.Persona{ID: "duncan", Name: "Duncan"}, Implicit: true}, goal: &pocket.ShiftGoal{Status: "running", Objective: "Finish the build", Stop: "All checks pass", Round: 2, MaxRounds: 8, DeadlineLabel: "07:00 EST"}}
	tt := ui.NewTesterWith(func(c *ui.Context) {
		frostui.Adapt(c)
		ui.Column(c).Width(280).Padding(16).Gap(12).AlignItems(ui.Stretch).Children(func() { a.sessionIdentity(c); a.shiftGoalSection(c) })
	}, 300, 600, ui.TesterOptions{})
	tt.Frame()
	for _, text := range []string{"Duncan", "Keep this identity", "Shift", "Finish the build", "Stop when: All checks pass", "2 of 8", "07:00 EST"} {
		if !tt.HasText(text) {
			t.Errorf("missing %q", text)
		}
	}
	writePNG(t, tt, "session-details")
}
