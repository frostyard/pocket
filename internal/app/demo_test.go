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

// TestLiveDemo walks the demo server's scenarios (POCKET_DEMO_SERVER and
// POCKET_DEMO_TOKEN; the scripted model of a Pi Pocket checkout's
// test/pocket-demo.ts): a call waiting for approval, a run that thinks
// and uses tools, and a question card.
func TestLiveDemo(t *testing.T) {
	server, token := os.Getenv("POCKET_DEMO_SERVER"), os.Getenv("POCKET_DEMO_TOKEN")
	if server == "" || token == "" {
		t.Skip("POCKET_DEMO_SERVER and POCKET_DEMO_TOKEN are not set")
	}
	t.Setenv("FROSTUI_ICON_THEME", "Adwaita")
	frostui.SetIcons(frostui.NewIcons(icons.Embedded()))
	store, _ := config.Open(t.TempDir())
	loop := frostuitest.NewLoop()
	a, err := New(Options{Post: loop.Post, Store: store, Creds: config.Credentials{Dir: store.Dir()}, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTesterWith(a.View, 1440, 900, ui.TesterOptions{Dark: os.Getenv("POCKET_DARK") != ""})
	frames := func(n int) {
		for range n {
			tt.Advance(100 * time.Millisecond)
			loop.Flush()
			tt.Frame()
		}
	}
	until := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s; texts: %s", what, strings.Join(tt.Texts(), " | "))
			}
			time.Sleep(30 * time.Millisecond)
			frames(1)
		}
	}
	snap := func(name string) {
		frames(15)
		dir := os.Getenv("POCKET_SNAPSHOT_DIR")
		if dir == "" {
			return
		}
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		_ = png.Encode(f, tt.Image())
	}
	loop.Post(func() { a.connect(server, token) })
	until("the session list", func() bool { return len(a.sessions) >= 2 })
	until("a waiting session", func() bool {
		for i := range a.sessions {
			if a.sessions[i].Waiting {
				return true
			}
		}
		return false
	})
	snap("demo-overview")

	// The session that waits: its card.
	deploy := 0
	welcome := 0
	for i := range a.sessions {
		if a.sessions[i].Waiting {
			deploy = a.sessions[i].ID
		} else {
			welcome = a.sessions[i].ID
		}
	}
	loop.Post(func() { a.open(deploy) })
	until("the approval card", func() bool { return len(a.view.Approvals) > 0 })
	snap("demo-approval")
	if !tt.HasText("Allow bash?") {
		t.Errorf("no approval card: %s", strings.Join(tt.Texts(), " | "))
	}
	loop.Post(func() { a.answerFirst(true) })
	until("the approval to go", func() bool { return len(a.view.Approvals) == 0 })

	// A run that thinks and uses tools, caught while it streams.
	loop.Post(func() { a.open(welcome) })
	until("the welcome session", func() bool { return a.view.Conversation.ID == welcome && len(a.tr.items) > 0 })
	loop.Post(func() { a.send("Please fix the bug where sign-in loops", "") })
	until("tools at work", func() bool { return a.view.Live.Busy && len(a.view.Live.Tools) > 0 })
	snap("demo-working")
	until("the answer", func() bool {
		return !a.view.Live.Busy && strings.Contains(strings.Join(tt.Texts(), " "), "Want me to open a PR")
	})
	snap("demo-answered")

	// A question card.
	loop.Post(func() { a.send("Build the cache, but ask me which store first", "") })
	until("the question", func() bool {
		for _, ap := range a.view.Approvals {
			if ap.Form != nil {
				return true
			}
		}
		return false
	})
	snap("demo-ask")
	if err := tt.Click("Postgres (Recommended)"); err != nil {
		t.Fatal(err)
	}
	frames(2)
	if err := tt.Click("Submit"); err != nil {
		// The card's submit label comes from the server.
		t.Logf("no Submit button (%v); answering directly", err)
		loop.Post(func() {
			for i := range a.view.Approvals {
				ap := &a.view.Approvals[i]
				if ap.Form != nil {
					a.answer(ap, true, a.composer.answers[ap.ID])
				}
			}
		})
	}
	until("the reply to the answer", func() bool {
		return !a.view.Live.Busy && len(a.view.Approvals) == 0 && strings.Contains(strings.Join(tt.Texts(), " "), "going with that")
	})
	snap("demo-asked")
	loop.Post(a.disconnect)
	loop.Flush()
}
