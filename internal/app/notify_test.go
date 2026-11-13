package app

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui/frostuitest"

	"github.com/frostyard/pocket/internal/config"
	"github.com/frostyard/pocket/internal/pocket"
)

// shown records the notifications the app hands to the desktop, and their
// click actions.
type shown struct {
	titles []string
	clicks []func()
}

func (s *shown) Notify(title, body string, onClick func()) {
	s.titles = append(s.titles, title)
	s.clicks = append(s.clicks, onClick)
}

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

// switchServerTo puts the app on another server, as connect does after a sign-in.
func switchServerTo(t *testing.T, a *App, server string) {
	t.Helper()
	client, err := pocket.NewClient(server, "test")
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP = &http.Client{Transport: snapshotHTTP{}}
	a.link.client = client
}

// burst delivers 4 finished runs: the first shows at once, 3 wait.
func burst(a *App) {
	for _, id := range []int{1, 2, 3, 4} {
		a.notify(done(id))
	}
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

func TestSignOutDropsAHeldBurst(t *testing.T) {
	a, rec, _, timers := notifyApp(t)
	a.settings.Notes().Finished = config.FinishedAll
	burst(a)
	if len(rec.titles) != 1 || len(*timers) != 1 {
		t.Fatalf("shown %v, want the burst's first note and one window", rec.titles)
	}
	a.reset() // signed out: the sessions of that burst are gone
	(*timers)[0]()
	if len(rec.titles) != 1 {
		t.Fatalf("a burst of the sign-in Pocket left showed: %v", rec.titles)
	}
	if len(a.notes.held) != 0 || a.notes.open {
		t.Fatal("sign-out kept the queued notes")
	}
}

func TestSignOutDoesNotCloseTheNextBurstsWindow(t *testing.T) {
	a, rec, _, timers := notifyApp(t)
	a.settings.Notes().Finished = config.FinishedAll
	burst(a)
	a.reset()
	switchServerTo(t, a, "https://example.org")
	a.notify(done(1)) // this sign-in's first note shows at once
	a.notify(done(2)) // and these two wait in its own window
	a.notify(done(3))
	if len(rec.titles) != 2 || len(*timers) != 2 {
		t.Fatalf("shown %v, want the new window's first note", rec.titles)
	}
	(*timers)[0]() // the first sign-in's timer
	if len(rec.titles) != 2 {
		t.Fatalf("a timer of the sign-in Pocket left ended this window: %v", rec.titles)
	}
	(*timers)[1]()
	if len(rec.titles) != 3 || rec.titles[2] != "2 runs finished" {
		t.Fatalf("the new sign-in's burst should show as one: %v", rec.titles)
	}
}

func TestAnotherSignInsBurstNeverShows(t *testing.T) {
	a, rec, _, timers := notifyApp(t)
	a.settings.Notes().Finished = config.FinishedAll
	burst(a) // session 12 of the first server is not the next one's
	switchServerTo(t, a, "https://other.example")
	(*timers)[0]()
	if len(rec.titles) != 1 {
		t.Fatalf("the burst of the sign-in Pocket left showed: %v", rec.titles)
	}
	// A note of this sign-in starts a window of its own.
	a.notify(done(1))
	if len(rec.titles) != 2 {
		t.Fatalf("the new sign-in's first note should show at once: %v", rec.titles)
	}
}

func TestAnotherSignInsNoteStartsItsOwnWindow(t *testing.T) {
	a, rec, _, timers := notifyApp(t)
	a.settings.Notes().Finished = config.FinishedAll
	burst(a)
	switchServerTo(t, a, "https://other.example")
	a.notify(done(1)) // before the first window's timer runs
	if len(*timers) != 2 {
		t.Fatalf("want a window timer for each sign-in, got %d", len(*timers))
	}
	(*timers)[0]()
	if len(rec.titles) != 2 {
		t.Fatalf("shown %v, want the two first notes only", rec.titles)
	}
	(*timers)[1]() // this window held nothing
	if len(rec.titles) != 2 {
		t.Fatalf("a dropped burst showed later: %v", rec.titles)
	}
}

func TestNotificationClickFollowsItsSignIn(t *testing.T) {
	a, rec, loop, _ := notifyApp(t)
	ns := a.settings.Notes()
	ns.Batch, ns.Finished = false, config.FinishedAll
	a.notify(done(2))
	if len(rec.clicks) != 1 {
		t.Fatalf("shown %v, want one note", rec.titles)
	}
	rec.clicks[0]()
	loop.Flush()
	if a.current != 2 {
		t.Fatalf("a click on the sign-in that made the note opened %d, want 2", a.current)
	}
	a.open(0)
	switchServerTo(t, a, "https://other.example")
	// The note is still in the desktop's list, but its session 2 is the
	// first server's; the second server's session 2 is another session.
	rec.clicks[0]()
	loop.Flush()
	if a.current != 0 {
		t.Fatalf("a click of the sign-in Pocket left opened session %d", a.current)
	}
	if a.selected != "x:inbox" {
		t.Fatalf("selected %q, want the Inbox", a.selected)
	}
}

func TestFlushAppliesTheSettingsNow(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*App)
		quiet  int
	}{
		{"every notification off", func(a *App) { a.settings.Notes().Enabled = false }, 0},
		{"the kind off", func(a *App) { a.settings.Notes().Finished = config.FinishedNone }, 0},
		{"do not disturb", func(a *App) { a.settings.Notes().DND = true }, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, rec, _, timers := notifyApp(t)
			a.settings.Notes().Finished = config.FinishedAll
			burst(a)
			tc.change(a)
			(*timers)[0]()
			if len(rec.titles) != 1 {
				t.Fatalf("shown %v, want only the note of the burst's start", rec.titles)
			}
			if a.notes.quieted != tc.quiet {
				t.Fatalf("held %d, want %d", a.notes.quieted, tc.quiet)
			}
			if len(a.notes.held) != 0 {
				t.Fatal("the window ended but kept its notes")
			}
		})
	}
}

func TestFlushHoldsWhenQuietHoursBegin(t *testing.T) {
	a, rec, _, timers := notifyApp(t)
	now := a.opts.Now()
	a.opts.Now = func() time.Time { return now }
	ns := a.settings.Notes()
	ns.Finished, ns.Quiet = config.FinishedAll, true // 22:00 to 07:00
	burst(a)
	now = time.Date(2026, 11, 12, 23, 0, 0, 0, time.Local) // the window runs into the night
	(*timers)[0]()
	if len(rec.titles) != 1 {
		t.Fatalf("quiet hours showed %v", rec.titles)
	}
	if a.notes.quieted != 3 {
		t.Fatalf("held %d, want 3", a.notes.quieted)
	}
}

func TestFlushShowsUrgentApprovalsInQuietTime(t *testing.T) {
	for _, urgent := range []bool{true, false} {
		a, rec, _, timers := notifyApp(t)
		ns := a.settings.Notes()
		ns.Finished, ns.Urgent = config.FinishedAll, urgent
		burst(a)
		a.notify(pocket.Notification{Title: "Pi needs approval", Tag: "approval-a", URL: "/s/2"})
		a.notify(pocket.Notification{Title: "Pi needs approval again", Tag: "approval-b", URL: "/s/3"})
		ns.DND = true
		(*timers)[0]()
		// 3 finished runs wait, and 2 calls to allow.
		want, quieted := []string{"Pi finished"}, 5
		if urgent {
			want = append(want, "2 calls wait for you")
			quieted = 3 // the runs; the calls come through
		}
		if len(rec.titles) != len(want) || (urgent && rec.titles[1] != want[1]) {
			t.Fatalf("urgent %v: shown %v, want %v", urgent, rec.titles, want)
		}
		if a.notes.quieted != quieted {
			t.Fatalf("urgent %v: held %d, want %d", urgent, a.notes.quieted, quieted)
		}
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
