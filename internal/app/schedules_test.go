package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

func TestScheduleCreationUsesDeviceZoneOrOmitsUnknownZone(t *testing.T) {
	for _, tc := range []struct {
		name, local string
		clear       bool
	}{
		{name: "local", local: "America/New_York"},
		{name: "unknown"},
		{name: "cleared", local: "America/New_York", clear: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, loop := teamApp(t)
			a.current = 7
			a.opts.TimeZone = func() string { return tc.local }
			sent := make(chan map[string]any, 1)
			a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				sent <- body
				return reply(r, `{}`), nil
			})}
			a.editSchedule(pocket.Schedule{})
			f := a.scheduleForm
			if f.zone != tc.local {
				t.Fatalf("form zone %q, want %q", f.zone, tc.local)
			}
			f.when = "tomorrow 9am check the build"
			if tc.clear {
				f.zone = "  "
			}
			tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Box(c).Fill(); a.scheduleDialog(c) }, 800, 500, ui.TesterOptions{})
			tt.Frame()
			if err := tt.Click("Save schedule"); err != nil {
				t.Fatal(err)
			}
			loop.RunUntil(t, func() bool { return !a.scheduleBusy && !f.open })
			body := <-sent
			if body["when"] != f.when {
				t.Fatalf("message %v", body)
			}
			zone, present := body["zone"]
			if tc.local == "" || tc.clear {
				if present {
					t.Fatalf("empty zone sent: %v", body)
				}
			} else if zone != tc.local {
				t.Fatalf("sent zone %v, want %s", zone, tc.local)
			}
			writePNG(t, tt, "schedule-zone-"+tc.name)
		})
	}
}

func TestLocalTimeZoneFindsIANAName(t *testing.T) {
	t.Setenv("TZ", ":America/New_York")
	if got := localTimeZone(); got != "America/New_York" {
		t.Fatalf("local zone %q", got)
	}
	for name, want := range map[string]string{
		"/usr/share/zoneinfo/America/New_York": "America/New_York",
		"../usr/share/zoneinfo/Europe/Paris":   "Europe/Paris",
		"Local":                                "", "not-a-zone": "", "": "", "UTC": "UTC",
	} {
		if got := ianaZone(name); got != want {
			t.Errorf("ianaZone(%q)=%q, want %q", name, got, want)
		}
	}
}

func TestCalendarScheduleExplainsAutomaticNextTime(t *testing.T) {
	a, _ := teamApp(t)
	a.current = 7
	version := "1"
	a.editSchedule(pocket.Schedule{ID: "s1", Text: "Check build", Zone: "America/New_York", Version: &version, Next: pocket.Millis(a.opts.Now().UnixMilli()), Every: json.RawMessage(`{"at":"09:00","days":[1,2,3,4,5]}`), Repeat: "weekdays at 09:00"})
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Box(c).Fill(); a.scheduleDialog(c) }, 800, 600, ui.TesterOptions{})
	tt.Frame()
	if !tt.HasText("The repeat rule sets the next time automatically.") {
		t.Fatal("calendar next-time rule not explained")
	}
	before := a.scheduleForm.next
	if err := tt.Click("Next scheduled time"); err != nil {
		t.Fatal(err)
	}
	tt.Type("changed")
	if a.scheduleForm.next != before {
		t.Fatal("calendar-controlled next time was editable")
	}
	writePNG(t, tt, "schedule-calendar-edit")
}

func TestBackgroundOutputFailureKeepsFreshList(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	var refresh func()
	a.opts.After = func(_ time.Duration, fn func()) { refresh = fn }
	outputCalls := make(chan struct{}, 2)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/output") {
			outputCalls <- struct{}{}
			resp := reply(r, `{"error":"No such background task here."}`)
			resp.StatusCode = 404
			return resp, nil
		}
		return reply(r, `{"tasks":[{"id":"bg-new","taskId":43,"command":"make test","status":"running"}]}`), nil
	})}
	a.background = &backgroundState{session: 7, open: true, selected: "bg-old", tasks: []pocket.BackgroundTask{{ID: "bg-old"}}}
	a.loadBackground()
	loop.RunUntil(t, func() bool { return !a.background.loading })
	if len(a.background.tasks) != 1 || a.background.tasks[0].ID != "bg-new" || a.background.selected != "" || a.background.err == "" {
		t.Fatal("output failure hid fresh task list")
	}
	refresh()
	loop.RunUntil(t, func() bool { return !a.background.loading })
	if len(outputCalls) != 1 {
		t.Fatal("missing output kept polling")
	}
}

func TestBackgroundOutputControlsArePlainText(t *testing.T) {
	text := "before\x1b[31mred\x1b[0m\n\x1b]0;title\aafter\x1b]52;c;payload\x1b\\\tend\x00"
	if got := plainOutput(text); got != "beforered\nafter\tend" {
		t.Fatalf("output %q", got)
	}
}

func TestBoardThreadStartsRunningRefresh(t *testing.T) {
	a, loop := teamApp(t)
	a.opts.After = func(time.Duration, func()) {}
	a.desk.on = false
	a.selected = "x:board"
	a.desk.view = &pocket.DeskView{Threads: []pocket.DeskThread{{ID: "T1", Title: "Example task"}}}
	called := make(chan struct{}, 1)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/running/board" {
			called <- struct{}{}
			return reply(r, `{}`), nil
		}
		return snapshotHTTP{}.RoundTrip(r)
	})}
	tt := ui.NewTesterWith(func(c *ui.Context) {
		frostui.Adapt(c)
		a.boardRow(c, pocket.BoardRow{ID: 1, Thread: "T1", State: "launched", Agent: "duncan", Title: "Example task"})
	}, 900, 200, ui.TesterOptions{})
	tt.Frame()
	if err := tt.Click("Thread"); err != nil {
		t.Fatal(err)
	}
	loop.RunUntil(t, func() bool { return a.running != nil && !a.running.loading })
	if a.selected != "x:motion" || a.threadSel != "T1" || len(called) != 1 {
		t.Fatal("board link did not refresh Running now")
	}
}
