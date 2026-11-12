package app

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/frostui/frostuitest"

	"github.com/frostyard/pocket/internal/config"
	"github.com/frostyard/pocket/internal/icons"
)

// TestLive drives the app against a real Pi Pocket server: set
// POCKET_TEST_SERVER and POCKET_TEST_TOKEN (node test/serve.ts in a Pi
// Pocket checkout prints both). With POCKET_SNAPSHOT_DIR it writes what
// the window shows as PNGs.
func TestLive(t *testing.T) {
	server, token := os.Getenv("POCKET_TEST_SERVER"), os.Getenv("POCKET_TEST_TOKEN")
	if server == "" || token == "" {
		t.Skip("POCKET_TEST_SERVER and POCKET_TEST_TOKEN are not set")
	}
	t.Setenv("FROSTUI_ICON_THEME", "Adwaita")
	frostui.SetIcons(frostui.NewIcons(icons.Embedded()))
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	loop := frostuitest.NewLoop()
	a, err := New(Options{Post: loop.Post, Store: store, Creds: config.Credentials{Dir: store.Dir()}, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	loop.Post(func() { a.connect(server, token) })
	loop.RunUntil(t, func() bool { return a.hello != nil && len(a.sessions) > 0 })
	t.Logf("connected: %d sessions, server %s", len(a.sessions), a.hello.Server.PocketVersion)
	tt := ui.NewTesterWith(a.View, 1440, 900, ui.TesterOptions{Dark: os.Getenv("POCKET_DARK") != ""})
	snap := func(name string) {
		for range 20 { // let lists glide and text fade in, on the tester's clock
			tt.Advance(100 * time.Millisecond)
			loop.Flush()
			tt.Frame()
		}
		dir := os.Getenv("POCKET_SNAPSHOT_DIR")
		if dir == "" {
			return
		}
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	snap("overview")

	id := a.sessions[0].ID
	loop.Post(func() { a.open(id) })
	loop.RunUntil(t, func() bool { tt.Frame(); return a.view.Conversation.ID == id && len(a.tr.items) > 0 })
	snap("session")

	before := len(a.view.Rows())
	loop.Post(func() { a.send("Write a short **markdown** list of three fruits", "") })
	loop.RunUntil(t, func() bool { tt.Frame(); return len(a.view.Rows()) >= before+2 && !a.view.Live.Busy })
	snap("replied")
	if !tt.HasText("Message") {
		t.Errorf("no composer in %q", strings.Join(tt.Texts(), " | "))
	}
	loop.Post(func() { a.disconnect() })
	loop.Flush()
}
