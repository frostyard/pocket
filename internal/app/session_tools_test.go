package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type sessionToolsHTTP func(*http.Request) (*http.Response, error)

func (f sessionToolsHTTP) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSessionFindIncludesFoldedToolsAndEarlierRows(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.view = pocket.NewView()
	a.view.Order = []int{1, 2, 3, 4}
	a.view.Entries[2] = &pocket.Entry{ID: 2, Kind: "user", Text: "Find the NEEDLE"}
	a.view.Entries[3] = &pocket.Entry{ID: 3, Kind: "assistant", Blocks: []pocket.Block{{Type: "toolCall", ID: "c1", Name: "read", Args: map[string]any{"path": "needle.go"}}}}
	a.view.Entries[4] = &pocket.Entry{ID: 4, Kind: "toolResult", CallID: "c1", Text: "needle output"}
	a.tr.viewChanged(a)
	a.tr.find = sessionFind{open: true, query: "needle"}
	if n := len(a.findMatches()); n != 2 {
		t.Fatalf("matches %d", n)
	}
	a.chooseFind(a.findMatches(), 1)
	a.chooseFind(a.findMatches(), 1)
	if !a.tr.open["call:c1"] {
		t.Fatal("found tool stayed folded")
	}
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/c/7/context" || r.URL.Query().Get("before") != "2" {
			t.Errorf("earlier request %s", r.URL)
		}
		return reply(r, `[{"id":1,"kind":"user","text":"an earlier needle"}]`), nil
	})}
	a.loadEarlier()
	loop.RunUntil(t, func() bool { return !a.tr.earlier })
	if n := len(a.findMatches()); n != 3 {
		t.Fatalf("earlier matches %d", n)
	}
	a.open(0)
	if a.tr.find.open {
		t.Fatal("find leaked into another session")
	}
}

func TestSessionExportPreservesMarkdownAndCancel(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Path != "/api/c/7/export" {
			t.Errorf("export %s %s", r.Method, r.URL)
		}
		return reply(r, "# Session\n\nA reply.\n"), nil
	})}
	saved := make(chan bool, 1)
	a.opts.SaveFile = func(_ context.Context, name string, data []byte) (bool, error) {
		if name != "session-7.md" || string(data) != "# Session\n\nA reply.\n" {
			t.Errorf("export was altered: %s %s", name, data)
		}
		saved <- true
		return false, nil
	}
	a.sessionOperation("Export session")
	loop.RunUntil(t, func() bool { return !a.sessionOp.busy })
	<-saved
	if len(a.pendingToasts) != 0 {
		t.Fatal("cancel claimed export succeeded")
	}
}

func TestSessionToolsCaptureSourceAndIgnoreLateFork(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	entered, release := make(chan struct{}), make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.URL.Path != "/api/c/7/fork" {
			t.Errorf("fork %s %s", r.Method, r.URL)
		}
		close(entered)
		<-release
		return reply(r, `{"id":99}`), nil
	})}
	a.sessionOperation("Fork session")
	<-entered
	a.sessionOperation("Fork session") // duplicate click while pending does nothing
	a.open(0)
	before := loop.Ran()
	close(release)
	loop.RunUntil(t, func() bool { return loop.Ran() > before })
	if a.current != 0 {
		t.Fatal("late fork changed navigation")
	}
}

func TestSessionFindSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	a.current = 7
	a.view = pocket.NewView()
	a.view.Order = []int{1, 2}
	a.view.Entries[1] = &pocket.Entry{ID: 1, Kind: "user", Text: "Where is the configuration?"}
	a.view.Entries[2] = &pocket.Entry{ID: 2, Kind: "assistant", Blocks: []pocket.Block{{Type: "text", Text: "The configuration lives in settings.json."}}}
	a.tr.find = sessionFind{open: true, query: "configuration", picked: "t:2:0"}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.conversationPane(c) }, 900, 650, ui.TesterOptions{})
	tt.Frame()
	if !tt.Focused("Find in session") {
		t.Fatal("find did not take focus")
	}
	if !tt.HasText("2 matching rows") {
		t.Fatal("find bar absent")
	}
	writePNG(t, tt, "session-find")
}

func TestNewContextRequiresConfirmationForSameVisit(t *testing.T) {
	a, _ := teamApp(t)
	a.current = 7
	a.confirmNewContext()
	if a.sessionOp != nil || !a.dialogs.Showing() {
		t.Fatal("reset ran before confirmation")
	}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Box(c).Fill(); a.dialogs.View(c) }, 800, 500, ui.TesterOptions{})
	tt.Frame()
	a.open(0)
	tt.Click("New context")
	tt.Frame()
	if a.sessionOp != nil {
		t.Fatal("old confirmation reset a different visit")
	}
}
