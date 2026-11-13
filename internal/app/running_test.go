package app

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

func TestRunningRefreshStopsWhenHiddenAndDropsOldSignIn(t *testing.T) {
	a, loop := teamApp(t)
	var timers []func()
	a.opts.After = func(_ time.Duration, fn func()) { timers = append(timers, fn) }
	a.selected = "x:motion"
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) { return reply(r, `{"sessions":[],"schedules":[]}`), nil })}
	a.loadRunning()
	loop.RunUntil(t, func() bool { return !a.running.loading })
	if len(timers) != 1 {
		t.Fatal("refresh was not scheduled")
	}
	a.selected = "x:inbox"
	timers[0]()
	if a.running.loading || len(timers) != 1 {
		t.Fatal("hidden board kept polling")
	}
	a.selected = "x:motion"
	a.loadRunning()
	old := a.running
	a.reset()
	loop.RunUntil(t, func() bool { return loop.Ran() >= 2 })
	if a.running != nil || old.data == nil {
		t.Fatal("old callback revived the board")
	}
}

func TestBackgroundUsesTaskIDAndIgnoresOldOutput(t *testing.T) {
	a, loop := teamApp(t)
	a.opts.After = func(time.Duration, func()) {}
	a.current = 7
	entered, release := make(chan struct{}), make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/c/7/background":
			return reply(r, `{"tasks":[{"id":"bg-1","taskId":42,"command":"make test","status":"running"}]}`), nil
		case "/api/c/7/background/42/stop":
			return reply(r, `{}`), nil
		case "/api/c/7/background/bg-1/output":
			close(entered)
			<-release
			return reply(r, `{"text":"old output"}`), nil
		default:
			t.Errorf("wrong task route %s", r.URL)
			return reply(r, `{}`), nil
		}
	})}
	a.openBackground()
	loop.RunUntil(t, func() bool { return !a.background.loading })
	a.stopBackground(a.background.tasks[0])
	loop.RunUntil(t, func() bool { return !a.background.busy && !a.background.loading })
	a.background.selected = "bg-1"
	a.loadBackground()
	<-entered
	before := loop.Ran()
	a.background = nil
	close(release)
	loop.RunUntil(t, func() bool { return loop.Ran() > before })
	if a.background != nil {
		t.Fatal("old output reopened the reader")
	}
}

func TestScheduleEditKeepsVersionRepeatAndDraftOnConflict(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	version := "4"
	a.editSchedule(pocket.Schedule{ID: "s1", Text: "old", Next: pocket.Millis(a.opts.Now().UnixMilli()), Zone: "UTC", Version: &version, Every: json.RawMessage(`{"minutes":120}`)})
	f := a.scheduleForm
	f.text = "new message"
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["version"] != "4" || body["text"] != "new message" || body["every"].(map[string]any)["minutes"] != float64(120) {
			t.Errorf("lost edit fields: %v", body)
		}
		resp := reply(r, `{"error":"Someone changed this schedule while you edited it."}`)
		resp.StatusCode = 409
		return resp, nil
	})}
	a.saveSchedule(f)
	a.saveSchedule(f)
	loop.RunUntil(t, func() bool { return !a.scheduleBusy })
	if !f.open || f.err == "" || f.text != "new message" || *f.schedule.Version != "4" {
		t.Fatal("conflict lost draft or changed its version")
	}
}

func TestRunningAndScheduleSnapshots(t *testing.T) {
	a, _ := teamApp(t)
	a.current = 7
	var board pocket.RunningBoard
	if err := pocket.Decode([]byte(`{"sessions":[{"id":7,"title":"Build the example app","tasks":[{"id":42,"kind":"pocket.background","conversationId":7,"label":"running make test","status":"running"},{"id":43,"kind":"pi.generation","conversationId":9,"subagent":"helper","label":"writing an answer","status":"running","stalled":1794497400000}]}],"schedules":[{"id":"s1","session":7,"sessionTitle":"Build the example app","text":"Check the nightly build","next":1794528000000,"zone":"UTC","repeat":"every 2 hours"}]}`), &board); err != nil {
		t.Fatal(err)
	}
	a.running = &runningState{data: &board}
	tt := ui.NewTesterWith(func(c *ui.Context) {
		frostui.Adapt(c)
		ui.Column(c).Fill().Padding(20).Gap(16).AlignItems(ui.Stretch).Children(func() { a.runningSection(c) })
	}, 900, 500, ui.TesterOptions{})
	tt.Frame()
	if !tt.HasText("Running now") || !tt.HasText("helper: writing an answer") {
		t.Fatal("running work absent")
	}
	writePNG(t, tt, "running-now")
	a.view.Schedules = board.Schedules
	version := "0"
	a.view.Schedules[0].Version = &version
	st := ui.NewTesterWith(func(c *ui.Context) {
		frostui.Adapt(c)
		ui.Column(c).Padding(16).Gap(12).AlignItems(ui.Stretch).Children(func() { a.schedulesSection(c) })
	}, 360, 400, ui.TesterOptions{})
	st.Frame()
	if !st.HasText("Edit") || !st.HasText("Pause") {
		t.Fatal("versioned schedule controls missing")
	}
	writePNG(t, st, "session-schedules")
	a.hello.User.Role = "viewer"
	st.Frame()
	if st.HasText("Cancel") || st.HasText("Pause") || st.HasText("Schedule a message") {
		t.Fatal("viewer offered mutation controls")
	}
	a.hello.User.Role = "owner"
	a.view.Schedules[0].Version = nil
	st.Frame()
	if st.HasText("Edit") || st.HasText("Pause") || !st.HasText("Cancel") {
		t.Fatal("old server offered unsupported controls")
	}
	a.background = &backgroundState{session: 7, open: true, tasks: []pocket.BackgroundTask{{ID: "bg-1", TaskID: 42, Command: "make test", Status: "running"}}, selected: "bg-1", output: &pocket.BackgroundOutput{Text: "Running package tests…\nok example.org/project\n", Skipped: 2048}}
	bt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Box(c).Fill(); a.backgroundDialog(c) }, 850, 650, ui.TesterOptions{})
	bt.Frame()
	writePNG(t, bt, "background-output")
	a.editSchedule(pocket.Schedule{})
	ft := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Box(c).Fill(); a.scheduleDialog(c) }, 800, 500, ui.TesterOptions{})
	ft.Frame()
	writePNG(t, ft, "schedule-create")
}
