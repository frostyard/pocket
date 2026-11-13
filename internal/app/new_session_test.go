package app

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
)

func newSessionTester(a *App) *ui.Tester {
	return ui.NewTesterWith(func(c *ui.Context) {
		frostui.Adapt(c)
		ui.Box(c).Fill()
		a.newSessionDialog(c)
	}, 800, 500, ui.TesterOptions{})
}

func TestNewSessionPromptsBeforeSending(t *testing.T) {
	a, _ := teamApp(t)
	a.hello.Server.DefaultCwd = "/home/team/projects/example"
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		t.Errorf("request before creation: %s", r.URL)
		return reply(r, `{}`), nil
	})}
	a.newSession("")
	if a.newSessionDraft.cwd != a.hello.Server.DefaultCwd {
		t.Fatal("server default not offered")
	}
	tt := newSessionTester(a)
	tt.Frame()
	if !tt.Focused("Working directory") {
		t.Fatal("directory input did not take focus")
	}
	writePNG(t, tt, "new-session")
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.newSessionDraft.open {
		t.Fatal("cancel kept the dialog open")
	}
	a.newSession("/home/team/projects/another")
	if a.newSessionDraft.cwd != "/home/team/projects/another" {
		t.Fatal("source session directory not offered")
	}
}

func TestNewSessionEditsDirectoryAndPreservesServerError(t *testing.T) {
	a, loop := teamApp(t)
	bodies := make(chan map[string]any, 2)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/sessions" {
			return snapshotHTTP{}.RoundTrip(r)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies <- body
		if body["cwd"] == "/missing" {
			resp := reply(r, `{"error":"Directory does not exist"}`)
			resp.StatusCode = http.StatusBadRequest
			return resp, nil
		}
		return reply(r, `{"id":99}`), nil
	})}
	a.newSession("/home/team/projects/example")
	tt := newSessionTester(a)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("/missing")
	tt.Key(0, ui.KeyEnter)
	loop.RunUntil(t, func() bool { return a.newSessionDraft.err != "" })
	if got := (<-bodies)["cwd"]; got != "/missing" {
		t.Fatalf("sent cwd %v", got)
	}
	tt.Frame()
	if !a.newSessionDraft.open || a.newSessionDraft.cwd != "/missing" || !tt.HasText("Directory does not exist") {
		t.Fatal("failed creation did not retain the path and show the error")
	}
	writePNG(t, tt, "new-session-error")
	// Correct the rejected directory, then explicitly create the session again.
	if err := tt.Click("Working directory"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("/home/team/projects/example")
	if err := tt.Click("Create session"); err != nil {
		t.Fatal(err)
	}
	loop.RunUntil(t, func() bool { return a.current == 99 })
	if got := (<-bodies)["cwd"]; got != "/home/team/projects/example" {
		t.Fatalf("sent cwd %v", got)
	}
	if a.newSessionDraft.open {
		t.Fatal("successful creation left the form open")
	}
}

func TestNewSessionDefaultAndDismissedPendingCreation(t *testing.T) {
	a, loop := teamApp(t)
	entered, release := make(chan struct{}), make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if _, ok := body["cwd"]; ok {
			t.Error("empty directory did not defer to server default")
		}
		close(entered) // a duplicate POST fails this test
		<-release
		return reply(r, `{"id":99}`), nil
	})}
	a.newSession("")
	f := a.newSessionDraft
	a.createSession(f)
	<-entered
	a.createSession(f)
	f.open = false // Escape can dismiss a modal while its request finishes.
	a.newSession("/different")
	if a.newSessionDraft != f || !f.open {
		t.Fatal("a second dialog replaced a pending creation")
	}
	f.open = false
	close(release)
	loop.RunUntil(t, func() bool { return !f.busy })
	if a.current != 0 || !slices.Contains(a.pendingToasts, "Created session 99") {
		t.Fatal("dismissed creation moved navigation or failed to report success")
	}
}
