package app

import (
	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"testing"
)

func TestInspectorHeadingAtMinimumWidthSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	a.shell.Panes.Layout.Widths = map[string]float32{"inspector": 240, "sessions": 200}
	a.inboxSel = "a:T1458a"
	tt := ui.NewTesterWith(a.View, 1100, 800, ui.TesterOptions{})
	tt.Frame()
	tt.Frame()
	if !a.shell.Panes.Shown("inspector") {
		t.Fatal("inspector hidden")
	}
	r, ok := tt.Find("Details")
	if !ok || r.Y < frostui.TopBarHeight || r.X < 860 || r.X+r.W > 1100 {
		t.Fatalf("heading is not inside the 240-pixel content pane below the bar: %+v, %v", r, ok)
	}
	writePNG(t, tt, "inspector-minimum-width")
}
