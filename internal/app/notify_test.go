package app

import (
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui/frostuitest"

	"github.com/frostyard/pocket/internal/config"
	"github.com/frostyard/pocket/internal/pocket"
)

// shown records the notifications the app hands to the desktop.
type shown struct{ titles []string }

func (s *shown) Notify(title, body string, _ func()) { s.titles = append(s.titles, title) }

// notifyApp is an app with a team's sessions, a notifier that records,
// and a clock and timers the test moves.
func notifyApp(t *testing.T) (*App, *shown, *frostuitest.Loop, *[]func()) {
	t.Helper()
	a, loop := teamApp(t)
	rec := &shown{}
	timers := &[]func(){}
	a.opts.Notify = rec
	a.opts.After = func(_ time.Duration, fn func()) { *timers = append(*timers, fn) }
	a.focused = false
	return a, rec, loop, timers
}

func done(id int) pocket.Notification {
	return pocket.Notification{Title: "Pi finished", Body: "done", URL: "/s/" + strconv.Itoa(id), Tag: "done-" + strconv.Itoa(id)}
}

func TestFinishedRunsNotifyOnlyForYourOwnSessions(t *testing.T) {
	a, rec, _, _ := notifyApp(t)
	a.settings.Notes().Batch = false
	a.notify(done(2)) // delegated from session 1: not yours
	a.notify(done(1)) // started by you
	if len(rec.titles) != 1 {
		t.Fatalf("shown %v, want only session 1's", rec.titles)
	}
	a.togglePin(2)
	a.notify(done(2))
	if len(rec.titles) != 2 {
		t.Fatalf("a pinned delegation should notify: %v", rec.titles)
	}
	a.settings.Notes().Finished = config.FinishedNone
	a.notify(done(1))
	if len(rec.titles) != 2 {
		t.Fatalf("finished runs are off: %v", rec.titles)
	}
}

func TestBurstsShowAsOne(t *testing.T) {
	a, rec, _, timers := notifyApp(t)
	a.settings.Notes().Finished = config.FinishedAll
	for _, id := range []int{1, 2, 3, 4} {
		a.notify(done(id))
	}
	if len(rec.titles) != 1 {
		t.Fatalf("the first of a burst shows at once, alone: %v", rec.titles)
	}
	(*timers)[0]()
	if len(rec.titles) != 2 || rec.titles[1] != "3 runs finished" {
		t.Fatalf("the rest should show as one: %v", rec.titles)
	}
}

func TestQuietHoldsAllButApprovals(t *testing.T) {
	a, rec, _, _ := notifyApp(t)
	ns := a.settings.Notes()
	ns.Batch, ns.Finished = false, config.FinishedAll
	a.setQuiet(true, 0)
	a.notify(done(1))
	a.notify(pocket.Notification{Title: "Pi needs approval", Tag: "approval-x", URL: "/s/2"})
	if len(rec.titles) != 1 || rec.titles[0] != "Pi needs approval" {
		t.Fatalf("shown %v, want the approval alone", rec.titles)
	}
	if a.notes.quieted != 1 {
		t.Fatalf("held %d, want 1", a.notes.quieted)
	}
	ns.Urgent = false
	a.notify(pocket.Notification{Title: "Pi needs approval again", Tag: "approval-y", URL: "/s/2"})
	if len(rec.titles) != 1 {
		t.Fatalf("without urgent, approvals wait too: %v", rec.titles)
	}
	a.setQuiet(false, 0)
	a.notify(done(1))
	if len(rec.titles) != 2 {
		t.Fatalf("after do not disturb: %v", rec.titles)
	}
}

func TestQuietHoursCrossMidnight(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 12, h, m, 0, 0, time.Local) }
	for _, tc := range []struct {
		t    time.Time
		from string
		to   string
		want bool
	}{
		{at(23, 0), "22:00", "07:00", true},
		{at(6, 59), "22:00", "07:00", true},
		{at(7, 0), "22:00", "07:00", false},
		{at(12, 0), "22:00", "07:00", false},
		{at(13, 30), "13:00", "14:00", true},
		{at(13, 30), "nonsense", "14:00", false},
	} {
		if got := inQuietHours(tc.t, tc.from, tc.to); got != tc.want {
			t.Errorf("%s in %s–%s = %v", tc.t.Format("15:04"), tc.from, tc.to, got)
		}
	}
}

func TestSettingsDialogShows(t *testing.T) {
	a, loop := teamApp(t)
	tt := ui.NewTesterWith(a.View, 1440, 900, ui.TesterOptions{})
	loop.Post(func() { a.openSettings("notifications") })
	for range 3 {
		loop.Flush()
		tt.Advance(100 * time.Millisecond)
		tt.Frame()
	}
	for _, want := range []string{"Settings", "Desk decisions", "Finished runs", "Gather bursts", "Quiet hours"} {
		if !tt.HasText(want) {
			t.Errorf("settings lack %q", want)
		}
	}
	writePNG(t, tt, "settings")
}

func TestApprovalTagsExpireAfterOneDay(t *testing.T) {
	a, rec, _, _ := notifyApp(t)
	a.settings.Notes().Batch = false
	now := a.opts.Now()
	a.opts.Now = func() time.Time { return now }
	var timers []func()
	var delays []time.Duration
	a.opts.After = func(d time.Duration, fn func()) { timers = append(timers, fn); delays = append(delays, d) }
	first := pocket.Notification{Title: "First", Tag: "approval-first", URL: "/s/1"}
	second := pocket.Notification{Title: "Second", Tag: "approval-second", URL: "/s/1"}
	a.notify(first)
	a.notify(first)
	if len(rec.titles) != 1 || len(timers) != 1 || delays[0] != 24*time.Hour {
		t.Fatal("approval should notify once and schedule one expiry")
	}
	now = now.Add(12 * time.Hour)
	a.notify(second)
	if len(timers) != 1 {
		t.Fatal("each tag should not need its own timer")
	}
	now = now.Add(12 * time.Hour)
	timers[0]()
	if _, ok := a.notified[first.Tag]; ok {
		t.Fatal("day-old tag retained")
	}
	if _, ok := a.notified[second.Tag]; !ok {
		t.Fatal("newer tag expired early")
	}
	if len(timers) != 2 || delays[1] != 12*time.Hour {
		t.Fatal("newer tag does not expire at its own deadline")
	}
	now = now.Add(12 * time.Hour)
	timers[1]()
	if len(a.notified) != 0 || a.notifiedTimer {
		t.Fatal("expiry did not finish without another notification")
	}
	a.notify(first)
	if len(rec.titles) != 3 {
		t.Fatal("expired tag still suppresses a notification")
	}
	a.reset()
	if len(a.notified) != 0 {
		t.Fatal("sign-out retained another server's approval tags")
	}
}

func TestArtifactBrowserSignInSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	a.arts.sel = "4/notes"
	tt := ui.NewTesterWith(a.artifactPreview, 850, 400, ui.TesterOptions{})
	tt.Frame()
	if !tt.HasText("This system has no web view for Pocket to show the page in. Open in browser uses that browser's sign-in; you may need to sign in there.") {
		t.Fatal("browser fallback lacks accurate sign-in guidance")
	}
	writePNG(t, tt, "artifact-browser-signin")
}
