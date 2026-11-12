package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestAgentRowShowsRepositoriesOnHover hovers an agent in the sidebar.
func TestAgentRowShowsRepositoriesOnHover(t *testing.T) {
	a, loop := teamApp(t)
	tt := ui.NewTesterWith(a.View, 1440, 900, ui.TesterOptions{})
	loop.Flush()
	tt.Frame()
	at, ok := tt.Find("Duncan")
	if !ok {
		t.Fatalf("no Duncan in %q", tt.Texts())
	}
	tt.Move(at.X+at.W/2, at.Y+at.H/2)
	tt.Advance(2 * time.Second)
	tt.Frame()
	if !tt.HasText("  frostyard/nsl") && !tt.HasText("frostyard/nsl") {
		t.Errorf("hovering Duncan shows no repositories: %q", tt.Texts())
	}
	writePNG(t, tt, "agent-hover")
}
