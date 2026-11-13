package pocket

import (
	"context"
	"net/http"
	"testing"
)

type shiftTransport func(*http.Request) (*http.Response, error)

func (f shiftTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestShiftGoalOptionalAndDrift(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		goal       bool
	}{
		{"missing module", `{}`, 404, false}, {"no goal", `{"goal":null}`, 200, false},
		{"changed field", `{"goal":{"objective":"Keep going","round":"unexpected","maxRounds":8}}`, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := NewClient("https://example.org", "test")
			c.HTTP = &http.Client{Transport: shiftTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/api/x/night-shift/goal/2" {
					t.Errorf("route %s", r.URL.Path)
				}
				return answer(r, tc.status, tc.body), nil
			})}
			goal, err := c.ShiftGoal(context.Background(), 2)
			if err != nil || (goal != nil) != tc.goal {
				t.Fatalf("goal=%v, err=%v", goal, err)
			}
			if goal != nil && (goal.Objective != "Keep going" || goal.MaxRounds != 8) {
				t.Fatal("drift dropped valid fields")
			}
		})
	}
}
