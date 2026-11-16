package app

import (
	"net/http"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

// The session list's background count drives the composer's pill: only the
// open session's count shows, it follows each new list, and none hides it.
func TestBackgroundNowFollowsSessionList(t *testing.T) {
	a, _ := teamApp(t)
	var list []pocket.Session
	if err := pocket.Decode([]byte(`[{"id":7,"title":"Build","background":2},{"id":9,"title":"Other","background":5}]`), &list); err != nil {
		t.Fatal(err)
	}
	a.setSessions(list)
	if got := a.backgroundNow(); got != 0 {
		t.Fatalf("no open session counted %d", got)
	}
	a.current = 7
	if got := a.backgroundNow(); got != 2 {
		t.Fatalf("open session counted %d, want 2", got)
	}
	a.setSessions([]pocket.Session{{ID: 7, Title: "Build"}, {ID: 9, Background: 5}})
	if got := a.backgroundNow(); got != 0 {
		t.Fatalf("finished tasks still counted %d", got)
	}
	a.current = 8
	if got := a.backgroundNow(); got != 0 {
		t.Fatalf("unknown session counted %d", got)
	}
	if got := backgroundTitle(1); got != "1 command runs in the background: open Background tasks to watch or stop it" {
		t.Fatalf("title %q", got)
	}
	if got := backgroundTitle(3); got != "3 commands run in the background: open Background tasks to watch or stop them" {
		t.Fatalf("title %q", got)
	}
}

func TestComposerBackgroundPillOpensTasks(t *testing.T) {
	a, loop := teamApp(t)
	a.opts.After = func(time.Duration, func()) {}
	a.current = 7
	a.composer.reset(7)
	listed := make(chan struct{}, 1)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/c/7/background" {
			listed <- struct{}{}
			return reply(r, `{"tasks":[{"id":"bg-1","taskId":42,"command":"make test","status":"running"}]}`), nil
		}
		return snapshotHTTP{}.RoundTrip(r)
	})}
	a.setSessions([]pocket.Session{{ID: 7, Title: "Build the example app"}})
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.composerView(c) }, 850, 160, ui.TesterOptions{})
	tt.Frame()
	if tt.HasText("in background") {
		t.Fatal("pill shown with no background task")
	}
	a.setSessions([]pocket.Session{{ID: 7, Title: "Build the example app", Background: 1}})
	tt.Frame()
	if !tt.HasText("1 in background") {
		t.Fatal("pill absent while a task runs")
	}
	writePNG(t, tt, "composer-background")
	if err := tt.Click(backgroundTitle(1)); err != nil {
		t.Fatal(err)
	}
	loop.RunUntil(t, func() bool { return a.background != nil && !a.background.loading })
	if !a.background.open || a.background.session != 7 || len(listed) != 1 || len(a.background.tasks) != 1 {
		t.Fatal("click did not open the session's background tasks")
	}
	a.setSessions([]pocket.Session{{ID: 7, Title: "Build the example app"}})
	tt.Frame()
	if tt.HasText("in background") {
		t.Fatal("pill stayed after the tasks ended")
	}
}
