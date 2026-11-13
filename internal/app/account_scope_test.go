package app

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/frostui"
	"github.com/frostyard/frostui/frostuitest"

	"github.com/frostyard/pocket/internal/config"
	"github.com/frostyard/pocket/internal/pocket"
)

// Two servers number their sessions 7, and the ID means nothing on the
// other one. So do two users on one server: a conversation is its server's.
const (
	serverOne = "https://example.org"
	serverTwo = "https://other.example.org"
)

// scopeNow is the clock the tests run on.
var scopeNow = time.Date(2026, 12, 3, 12, 0, 0, 0, time.UTC)

// scopeApp is an app that saves its settings in dir, with a clock of its
// own, answering every route with an empty object.
func scopeApp(t *testing.T, dir string) (*App, *frostuitest.Loop) {
	t.Helper()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	loop := frostuitest.NewLoop()
	a, err := New(Options{Post: loop.Post, Store: store, Creds: config.Credentials{Dir: store.Dir()}, Version: "test",
		Now: func() time.Time { return scopeNow }, TimeZone: func() string { return "UTC" }})
	if err != nil {
		t.Fatal(err)
	}
	return a, loop
}

// signInTo signs the window in as user on server, as the stream does: a new
// client for the server, then its hello, then its sessions, one of which is
// 7. Every route answers with an empty object.
func signInTo(t *testing.T, a *App, loop *frostuitest.Loop, server, user string) {
	t.Helper()
	signInWith(t, a, loop, server, user, func(r *http.Request) (*http.Response, error) {
		return reply(r, `{}`), nil
	})
}

// signInWith is signInTo with the routes rt answers.
func signInWith(t *testing.T, a *App, loop *frostuitest.Loop, server, user string, rt routeTransport) {
	t.Helper()
	client, err := pocket.NewClient(server, "test")
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP = &http.Client{Transport: rt}
	a.link.client = client
	a.settings.Server = server // as beginSignIn keeps it
	a.conn.State = frostui.Connecting
	helloAs(a, user)
	a.setSessions(scopeSessions(7))
	loop.Flush()
}

// helloAs delivers the stream's hello naming user.
func helloAs(a *App, user string) {
	a.handle(pocket.Event{Name: "hello", Data: []byte(`{"connection":"c1","server":{"pocketVersion":"0.28.1"},` +
		`"user":{"id":"` + user + `","name":"A","role":"owner"}}`)})
}

// scopeSessions is a session list with the IDs given, each ended a minute
// ago.
func scopeSessions(ids ...int) []pocket.Session {
	var out []pocket.Session
	for _, id := range ids {
		out = append(out, pocket.Session{ID: id, Title: "Session", Cwd: "/home/team",
			EndedAt: pocket.Millis(scopeNow.Add(-time.Minute).UnixMilli())})
	}
	return out
}

// leaveWorkInSeven signs in as user on server and leaves what belongs to its
// session 7: a draft, a history entry, a pin, a seen mark and the last
// session.
func leaveWorkInSeven(t *testing.T, a *App, loop *frostuitest.Loop, server, user string) {
	t.Helper()
	signInTo(t, a, loop, server, user)
	a.open(7)
	if a.current != 7 {
		t.Fatalf("session 7 is not open: %d", a.current)
	}
	a.composer.draft = "draft for seven"
	a.composer.history = append(a.composer.history, "earlier words")
	a.togglePin(7)
	a.markSeen(7)
}

// noWorkInSeven fails when session 7 shows the work of another account. It
// reports every leak, not only the first.
func noWorkInSeven(t *testing.T, a *App) {
	t.Helper()
	if a.current != 0 || a.loading != 0 {
		t.Errorf("another account restored session %d (current %d)", a.loading, a.current)
	}
	if a.pinned(7) {
		t.Error("another account shows the pin")
	}
	if st := a.stateOf(a.byID[7]); st != stateFresh {
		t.Errorf("session 7 is state %d, want fresh", st)
	}
	a.open(7) // from now on this account has seen it
	if a.composer.draft != "" {
		t.Errorf("another account shows the draft %q", a.composer.draft)
	}
	if len(a.composer.history) != 0 {
		t.Errorf("another account shows the history %v", a.composer.history)
	}
}

// TestAccountScopeKeepsDraftsAndPinsPerAccount signs in to one account,
// leaves work in its session 7, and checks that neither another server nor
// another user on the same server sees any of it, while the account itself
// gets it back when it signs in again.
func TestAccountScopeKeepsDraftsAndPinsPerAccount(t *testing.T) {
	a, loop := scopeApp(t, t.TempDir())
	leaveWorkInSeven(t, a, loop, serverOne, "u1")
	a.signedOut("the sign-in was rotated")

	signInTo(t, a, loop, serverTwo, "u1")
	noWorkInSeven(t, a)
	a.signedOut("the sign-in was rotated")

	signInTo(t, a, loop, serverOne, "u2")
	noWorkInSeven(t, a)
	a.signedOut("the sign-in was rotated")

	// The first account again, with a token that replaced the old one: a new
	// client for the same base.
	signInTo(t, a, loop, serverOne, "u1")
	if a.loading != 7 {
		t.Fatalf("last session %d was not restored, want 7", a.loading)
	}
	if a.current != 7 {
		t.Fatalf("session 7 was not opened: %d", a.current)
	}
	if !a.pinned(7) {
		t.Fatal("the pin did not come back")
	}
	if st := a.stateOf(a.byID[7]); st != stateSeen {
		t.Fatalf("session 7 is state %d, want seen", st)
	}
	if a.composer.draft != "draft for seven" {
		t.Fatalf("draft %q did not come back", a.composer.draft)
	}
	if len(a.composer.history) != 1 || a.composer.history[0] != "earlier words" {
		t.Fatalf("history %v did not come back", a.composer.history)
	}
}

// TestAccountScopeKeepsAnUnacknowledgedSendForItsAccount checks that a send
// the server never answered comes back as the draft of its own session, and
// of that account only.
func TestAccountScopeKeepsAnUnacknowledgedSendForItsAccount(t *testing.T) {
	a, loop := scopeApp(t, t.TempDir())
	leaveWorkInSeven(t, a, loop, serverOne, "u1")
	a.composer.draft = ""
	a.send("unsent words", "")
	if a.composer.pendingText != "unsent words" {
		t.Fatalf("the send did not wait for its answer: %q", a.composer.pendingText)
	}
	// Sign-out before the answer runs: the send was never acknowledged.
	a.signedOut("the sign-in was rotated")

	signInTo(t, a, loop, serverTwo, "u1")
	a.open(7)
	if a.composer.draft != "" {
		t.Fatalf("another server shows the unsent send %q", a.composer.draft)
	}
	a.signedOut("the sign-in was rotated")

	signInTo(t, a, loop, serverOne, "u1")
	if a.composer.draft != "unsent words" {
		t.Fatalf("draft %q did not come back", a.composer.draft)
	}
}

// TestAccountChangeOnTheSameSignIn checks a hello that names another user on
// the connection the window already has (no sign-out between them): the
// first user's selection, pin, draft, history and a send still in flight
// stay with that user, and the failed send's answer does not reach the new
// one.
func TestAccountChangeOnTheSameSignIn(t *testing.T) {
	a, loop := scopeApp(t, t.TempDir())
	release := make(chan struct{})
	signInWith(t, a, loop, serverOne, "u1", func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/submit") {
			<-release
			resp := reply(r, `{"error":"gone"}`)
			resp.StatusCode = http.StatusInternalServerError
			return resp, nil
		}
		return reply(r, `{}`), nil
	})
	a.open(7)
	a.composer.history = append(a.composer.history, "earlier words")
	a.togglePin(7)
	a.markSeen(7)
	a.send("private words", "")
	if a.composer.pendingText != "private words" {
		t.Fatalf("the send did not wait for its answer: %q", a.composer.pendingText)
	}

	a.conn.State = frostui.Connected // a reconnect, not a first connection
	helloAs(a, "u2")
	a.setSessions(scopeSessions(7))
	close(release) // the first user's send fails now
	// The answer runs either way: dropped, or logged as a failed send.
	loop.RunUntil(t, func() bool {
		return len(a.console.Lines(nil, "earlier sign-in: Sending")) > 0 || len(a.console.Lines(nil, "Sending: ")) > 0
	})

	noWorkInSeven(t, a)
	if a.composer.pendingText != "" || a.composer.sending {
		t.Errorf("the new user holds the first user's send: %q", a.composer.pendingText)
	}

	// The first user, signed in again, gets the send back as a draft.
	a.signedOut("the sign-in was rotated")
	signInTo(t, a, loop, serverOne, "u1")
	if a.current != 7 {
		t.Fatalf("session 7 was not restored for the first user: %d", a.current)
	}
	if a.composer.draft != "private words" {
		t.Fatalf("the first user's draft is %q", a.composer.draft)
	}
	if !a.pinned(7) {
		t.Fatal("the first user's pin did not come back")
	}
}

// TestAccountChangeSignsOutTheWebView checks that the artifact page the
// first user had open, with its sign-in cookie, does not outlive a hello
// that names another user, while a reconnect of the same user keeps it.
func TestAccountChangeSignsOutTheWebView(t *testing.T) {
	web := &fakeWeb{}
	a, loop := scopeApp(t, t.TempDir())
	a.opts.Web = web
	signInTo(t, a, loop, serverOne, "u1")
	a.arts.shown = "https://example.org/a/1/x/1"

	a.conn.State = frostui.Connected
	helloAs(a, "u1") // the same user reconnects
	if web.signs != 0 || a.arts.shown == "" {
		t.Fatalf("a reconnect of the same user signed out the web view (%d) or forgot its page", web.signs)
	}

	helloAs(a, "u2")
	if web.signs != 1 {
		t.Errorf("SignOut ran %d times, want once", web.signs)
	}
	if a.arts.shown != "" {
		t.Errorf("the first user's artifact page is still the shown one: %q", a.arts.shown)
	}
}
