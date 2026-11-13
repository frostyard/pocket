package app

import (
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

func TestZoomStepsThroughItsLevels(t *testing.T) {
	a, _ := teamApp(t)
	if z := a.zoom(); z != 1 {
		t.Fatalf("zoom with none saved = %v, want 1", z)
	}
	for _, step := range []struct {
		by   int
		want float32
	}{{1, 1.1}, {1, 1.25}, {-3, 0.9}, {-20, 0.5}, {-1, 0.5}, {100, 3}, {1, 3}} {
		a.zoomBy(step.by)
		if z := a.zoom(); z != step.want {
			t.Fatalf("zoomBy(%d) = %v, want %v", step.by, z, step.want)
		}
	}
	a.setZoom(1)
	if a.settings.Zoom != 0 {
		t.Errorf("100%% saves %v, want none", a.settings.Zoom)
	}
	// A zoom written into the file by hand shows as the nearest level.
	a.settings.Zoom = 1.3
	if z := a.zoom(); z != 1.25 {
		t.Errorf("a saved 1.3 zooms to %v, want 1.25", z)
	}
	if got := zoomLabel(0.67); got != "67%" {
		t.Errorf("zoomLabel(0.67) = %q", got)
	}
}

// TestZoomCommandsSaveAndZoomTheWindow: the zoom keys save the zoom, the
// window shows at it, and the commands stop at the ends.
func TestZoomCommandsSaveAndZoomTheWindow(t *testing.T) {
	a, loop := teamApp(t)
	var shown float32
	tt := ui.NewTesterWith(func(c *ui.Context) { a.View(c); shown = c.Zoom() }, 1440, 900,
		ui.TesterOptions{Dark: os.Getenv("POCKET_DARK") != ""})
	settle := func() {
		for range 5 {
			tt.Advance(100 * time.Millisecond)
			loop.Flush()
			tt.Frame()
		}
	}
	settle()
	if ok, _ := a.table.Enabled("zoom-reset"); ok {
		t.Error("Actual Size is offered at 100%")
	}
	loop.Post(func() { a.table.Run("zoom-in"); a.table.Run("zoom-in") })
	settle()
	if shown != 1.25 {
		t.Fatalf("the window shows at %v, want 1.25", shown)
	}
	saved, err := a.opts.Store.Load()
	if err != nil || saved.Zoom != 1.25 {
		t.Fatalf("saved zoom %v (%v), want 1.25", saved.Zoom, err)
	}
	if !tt.HasText("Inbox") {
		t.Errorf("the zoomed window lacks the Inbox: %v", tt.Texts())
	}
	writePNG(t, tt, "zoom-125-inbox")
	loop.Post(func() { a.openSettings("appearance") })
	settle()
	if !tt.HasText("125%") {
		t.Errorf("Settings does not show the zoom: %v", tt.Texts())
	}
	writePNG(t, tt, "zoom-125-settings")
	loop.Post(func() { a.prefs.layout.Open = false; a.table.Run("zoom-reset") })
	settle()
	if shown != 1 {
		t.Fatalf("Actual Size shows at %v", shown)
	}
	loop.Post(func() { a.setZoom(3) })
	settle()
	if ok, _ := a.table.Enabled("zoom-in"); ok {
		t.Error("Zoom In is offered at the largest zoom")
	}
	loop.Post(func() { a.setZoom(0.5) })
	settle()
	if ok, _ := a.table.Enabled("zoom-out"); ok {
		t.Error("Zoom Out is offered at the smallest zoom")
	}
}

// TestTerminalZoomZoomsTheWindow: a terminal's zoom (Ctrl and the wheel,
// its font size keys) zooms the window rather than the terminal's font.
func TestTerminalZoomZoomsTheWindow(t *testing.T) {
	a, _ := teamApp(t)
	opts := a.termOptions(nil)
	if opts.OnZoom == nil {
		t.Fatal("a terminal zooms its own font")
	}
	if !opts.OnZoom(terminal.Zoom{Steps: 2}) || a.zoom() != 1.25 {
		t.Fatalf("two steps in zoom the window to %v, want 1.25", a.zoom())
	}
	if !opts.OnZoom(terminal.Zoom{Reset: true}) || a.zoom() != 1 {
		t.Fatalf("a reset zooms the window to %v, want 1", a.zoom())
	}
}
