package app

import (
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestNarrowWindowSnapshot draws the Inbox and a thread's context in a
// window narrower than a laptop's screen, where panes are tight.
func TestNarrowWindowSnapshot(t *testing.T) {
	a, loop := teamApp(t)
	tt := ui.NewTesterWith(a.View, 1100, 720, ui.TesterOptions{Dark: os.Getenv("POCKET_DARK") != ""})
	loop.Post(func() { a.inboxSel = "a:T1439c" })
	for range 5 {
		tt.Advance(100 * time.Millisecond)
		loop.Flush()
		tt.Frame()
	}
	writePNG(t, tt, "narrow-inbox")
}
