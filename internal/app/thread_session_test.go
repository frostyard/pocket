package app

import (
	"github.com/frostyard/pocket/internal/pocket"
	"testing"
)

func TestThreadSessionBindingWinsOverTitleAndNeverGuessesMissingBinding(t *testing.T) {
	a, _ := teamApp(t)
	a.setSessions([]pocket.Session{{ID: 7, Title: "Renamed session", UpdatedAt: 1}, {ID: 8, Title: "CI fixes", UpdatedAt: 2}, {ID: 9, Title: "CI fixes", UpdatedAt: 3}})
	id := 7
	th := &pocket.DeskThread{ID: "T1", Title: "CI fixes", Session: &id}
	if got := a.sessionForThread(th); got == nil || got.ID != 7 {
		t.Fatalf("binding lost: %v", got)
	}
	id = 404
	if got := a.sessionForThread(th); got != nil {
		t.Fatalf("missing bound session guessed: %v", got)
	}
	th.Session = nil
	if got := a.sessionForThread(th); got == nil || got.ID != 9 {
		t.Fatalf("older module fallback lost: %v", got)
	}
}
