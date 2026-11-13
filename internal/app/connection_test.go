package app

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/frostyard/frostui"
	"github.com/frostyard/frostui/frostuitest"
	"github.com/frostyard/pocket/internal/config"
	"github.com/frostyard/pocket/internal/pocket"
)

type routeTransport func(*http.Request) (*http.Response, error)

func (f routeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func requestApp(t *testing.T, transport routeTransport) (*App, *frostuitest.Loop) {
	t.Helper()
	loop := frostuitest.NewLoop()
	client, err := pocket.NewClient("https://example.org", "test")
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP = &http.Client{Transport: transport}
	a := &App{post: loop.Post, console: frostui.NewConsole(100), focused: true,
		opts: Options{Now: func() time.Time { return time.Unix(1000, 0) }, Creds: config.Credentials{Dir: t.TempDir()}},
		link: link{client: client}, view: pocket.NewView(), byID: map[int]*pocket.Session{}}
	return a, loop
}

func TestRoute401SignsOut(t *testing.T) {
	a, loop := requestApp(t, func(r *http.Request) (*http.Response, error) {
		resp := reply(r, `{}`)
		resp.StatusCode = http.StatusUnauthorized
		return resp, nil
	})
	a.desk.view = &pocket.DeskView{}
	called := false
	a.request("Read", func(ctx context.Context, c *pocket.Client) error { return c.Get(ctx, "/api/artifacts", nil) }, func(error) { called = true })
	loop.RunUntil(t, func() bool { return !a.signedIn() || called })
	if a.signedIn() || called || a.desk.view != nil || a.signIn.err == "" {
		t.Fatal("401 must clear the signed-in window without invoking its old callback")
	}
}

func TestRequestDropsAnswersFromOldSignIn(t *testing.T) {
	for _, fail := range []bool{false, true} {
		for _, replace := range []bool{false, true} {
			t.Run(map[bool]string{false: "success", true: "401"}[fail]+map[bool]string{false: "/signed-out", true: "/new-server"}[replace], func(t *testing.T) {
				a, loop := requestApp(t, nil)
				posted := make(chan struct{})
				a.post = func(fn func()) { loop.Post(fn); close(posted) }
				a.request("Old read", func(context.Context, *pocket.Client) error {
					if fail {
						return pocket.ErrSignedOut
					}
					return nil
				}, func(error) { a.desk.view = &pocket.DeskView{} })
				<-posted // answer is queued, not yet applied on the UI thread
				a.signedOut("signed out")
				if replace {
					a.link.client, _ = pocket.NewClient("https://other.example.org", "new-test")
				}
				loop.Flush()
				if a.desk.view != nil || a.signedIn() != replace || len(a.pendingToasts) != 0 {
					t.Fatal("old answer changed the current sign-in")
				}
			})
		}
	}
}

func TestHelloRefreshesTeamLessonsAndArtifacts(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	a, loop := requestApp(t, func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		return reply(r, `{}`), nil
	})
	a.conn.State = frostui.Connecting
	a.arts.loaded = true
	for range 2 {
		a.handle(pocket.Event{Name: "hello", Data: []byte(`{"connection":"test"}`)})
		loop.RunUntil(t, func() bool {
			return !a.team.loading && !a.lessons.loading && !a.arts.loading && !a.desk.loading && !a.wiki.loading && !a.terms.loading
		})
		a.conn.State = frostui.Reconnecting
	}
	mu.Lock()
	defer mu.Unlock()
	for _, path := range []string{"/api/x/team/agents", "/api/x/team/memory", "/api/artifacts"} {
		if counts[path] != 2 {
			t.Errorf("%s read %d times, want 2", path, counts[path])
		}
	}
}

func TestArtifactRefreshOnlyWhileVisible(t *testing.T) {
	a, loop := requestApp(t, func(r *http.Request) (*http.Response, error) { return reply(r, `{}`), nil })
	var timers []func()
	a.opts.After = func(d time.Duration, fn func()) {
		if d != time.Minute {
			t.Fatalf("refresh delay %s", d)
		}
		timers = append(timers, fn)
	}
	a.selected = "x:artifacts"
	a.loadArtifacts()
	loop.RunUntil(t, func() bool { return !a.arts.loading })
	if len(timers) != 1 {
		t.Fatalf("refresh timers: %d", len(timers))
	}
	timers[0]()
	if !a.arts.loading {
		t.Fatal("visible list did not refresh")
	}
	loop.RunUntil(t, func() bool { return !a.arts.loading })
	if len(timers) != 2 {
		t.Fatal("refresh was not scheduled again")
	}
	a.selected = "x:inbox"
	timers[1]()
	if a.arts.loading || a.arts.refreshPending {
		t.Fatal("hidden list keeps polling")
	}
}

func TestRefreshDuringReadIsNotLost(t *testing.T) {
	for _, kind := range []string{"team", "artifacts"} {
		t.Run(kind, func(t *testing.T) {
			a, loop := requestApp(t, func(r *http.Request) (*http.Response, error) { return reply(r, `{}`), nil })
			if kind == "team" {
				a.loadTeam()
				a.loadTeam()
			} else {
				a.loadArtifacts()
				a.loadArtifacts()
			}
			// Both calls precede applying the first answer, even if HTTP already finished.
			if kind == "team" && !a.team.dirty || kind == "artifacts" && !a.arts.dirty {
				t.Fatal("refresh was dropped")
			}
			loop.RunUntil(t, func() bool { return !a.team.loading && !a.lessons.loading && !a.arts.loading })
			if a.team.dirty || a.arts.dirty {
				t.Fatal("refresh remains pending")
			}
		})
	}
}

func TestRoute401ReleasesComposerAndRestoresDraft(t *testing.T) {
	a, loop := requestApp(t, func(r *http.Request) (*http.Response, error) {
		resp := reply(r, `{}`)
		resp.StatusCode = http.StatusUnauthorized
		return resp, nil
	})
	a.current = 7
	a.hello = &pocket.Hello{User: pocket.User{ID: "u1", Role: "owner"}}
	a.composer.session = 7
	a.composer.answered = map[string]bool{"approval": true}
	a.composer.promptsFor = 7
	a.tr.earlier = true
	a.send("unsent words", "")
	loop.RunUntil(t, func() bool { return !a.signedIn() })
	if a.composer.sending || len(a.composer.answered) != 0 || a.composer.promptsFor != 0 || a.tr.earlier {
		t.Fatal("sign-out retained in-flight UI state")
	}
	if a.composer.draft != "unsent words" || a.composer.drafts[7] != "unsent words" {
		t.Fatal("sign-out lost the unacknowledged send")
	}
	a.composer.reset(0)
	a.composer.reset(7)
	if a.composer.draft != "unsent words" {
		t.Fatal("opening the session lost the restored draft")
	}
}

func TestRetryInitialConnectionRestoresLastSession(t *testing.T) {
	ended := make(chan struct{})
	started := make(chan struct{})
	a, loop := requestApp(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/events" {
			close(started)
			<-r.Context().Done()
			close(ended)
			return nil, r.Context().Err()
		}
		return reply(r, `{}`), nil
	})
	a.conn.State = frostui.Connecting
	a.conn.Reason = "previous failure"
	a.settings.LastSession = "42"
	client := a.link.client
	a.reconnectNow()
	<-started
	t.Cleanup(a.disconnect)
	if a.conn.State != frostui.Connecting || a.conn.Reason != "" || a.link.client != client {
		t.Fatal("retry abandoned the initial connection or authenticated client")
	}
	a.handle(pocket.Event{Name: "hello", Data: []byte(`{"connection":"test"}`)})
	if a.loading != 42 {
		t.Fatalf("restored session %d, want 42", a.loading)
	}
	loop.RunUntil(t, func() bool {
		return !a.team.loading && !a.lessons.loading && !a.desk.loading && !a.wiki.loading && !a.terms.loading
	})
	a.disconnect()
	<-ended
}
