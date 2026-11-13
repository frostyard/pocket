package app

import (
	"net/http"
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
// 7.
func signInTo(t *testing.T, a *App, loop *frostuitest.Loop, server, user string) {
	t.Helper()
	client, err := pocket.NewClient(server, "test")
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP = &http.Client{Transport: routeTransport(func(r *http.Request) (*http.Response, error) {
		return reply(r, `{}`), nil
	})}
	a.link.client = client
	a.settings.Server = server // as beginSignIn keeps it
	a.conn.State = frostui.Connecting
	a.handle(pocket.Event{Name: "hello", Data: []byte(`{"connection":"c1","server":{"pocketVersion":"0.28.1"},` +
		`"user":{"id":"` + user + `","name":"A","role":"owner"}}`)})
	a.setSessions([]pocket.Session{{ID: 7, Title: "Seven", Cwd: "/home/team",
		EndedAt: pocket.Millis(scopeNow.Add(-time.Minute).UnixMilli())}})
	loop.Flush()
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

// noWorkInSeven fails when session 7 shows the work of another account.
func noWorkInSeven(t *testing.T, a *App) {
	t.Helper()
	if a.current != 0 || a.loading != 0 {
		t.Fatalf("another account restored session %d (current %d)", a.loading, a.current)
	}
	if a.pinned(7) {
		t.Fatal("another account shows the pin")
	}
	if st := a.stateOf(a.byID[7]); st != stateFresh {
		t.Fatalf("session 7 is state %d, want fresh", st)
	}
	a.open(7) // from now on this account has seen it
	if a.composer.draft != "" {
		t.Fatalf("another account shows the draft %q", a.composer.draft)
	}
	if len(a.composer.history) != 0 {
		t.Fatalf("another account shows the history %v", a.composer.history)
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
