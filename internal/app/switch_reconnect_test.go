package app

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/frostui"
	"github.com/frostyard/frostui/frostuitest"
	"github.com/frostyard/pocket/internal/pocket"
)

// fakeStreams serves /api/events as one pipe per connection, and holds
// each /api/switch until the test answers it.
type fakeStreams struct {
	streams  chan *fakeStream
	switches chan chan error
}

type fakeStream struct {
	query string
	w     *io.PipeWriter
}

func (s *fakeStream) send(t *testing.T, name, data string) {
	t.Helper()
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, data); err != nil {
		t.Fatal(err)
	}
}

func newFakeStreams() *fakeStreams {
	return &fakeStreams{streams: make(chan *fakeStream, 4), switches: make(chan chan error, 4)}
}

func (f *fakeStreams) transport(r *http.Request) (*http.Response, error) {
	switch r.URL.Path {
	case "/api/events":
		pr, pw := io.Pipe()
		go func() {
			<-r.Context().Done()
			_ = pr.CloseWithError(r.Context().Err())
		}()
		f.streams <- &fakeStream{query: r.URL.RawQuery, w: pw}
		resp := reply(r, "")
		resp.Header.Set("Content-Type", "text/event-stream")
		resp.Body = pr
		return resp, nil
	case "/api/switch":
		answer := make(chan error, 1)
		f.switches <- answer
		select {
		case err := <-answer:
			if err != nil {
				return nil, err
			}
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
	return reply(r, `{}`), nil
}

func (f *fakeStreams) next(t *testing.T, loop *frostuitest.Loop) *fakeStream {
	t.Helper()
	var s *fakeStream
	loop.RunUntil(t, func() bool {
		select {
		case s = <-f.streams:
			return true
		default:
			return false
		}
	})
	return s
}

func (f *fakeStreams) nextSwitch(t *testing.T, loop *frostuitest.Loop) chan error {
	t.Helper()
	var answer chan error
	loop.RunUntil(t, func() bool {
		select {
		case answer = <-f.switches:
			return true
		default:
			return false
		}
	})
	return answer
}

const fullView7 = `{"full":true,"conversation":{"id":7},"entries":[{"id":1,"kind":"user","text":"hi"}],"order":[1],"live":{"busy":false},"approvals":[]}`

// connectAndSwitch opens a first connection, then starts a switch to
// session 7 that the server has taken but not yet confirmed on the stream.
func connectAndSwitch(t *testing.T) (*App, *frostuitest.Loop, *fakeStreams, *fakeStream, chan error) {
	t.Helper()
	f := newFakeStreams()
	a, loop := requestApp(t, f.transport)
	a.link.tab = "tab"
	a.conn.State = frostui.Connecting
	a.reconnectNow()
	t.Cleanup(func() { loop.Post(a.disconnect); loop.Flush() })
	first := f.next(t, loop)
	first.send(t, "hello", `{"connection":"c1"}`)
	loop.RunUntil(t, func() bool { return a.link.streamID == "c1" })
	a.loading = 7
	a.switchTo(7)
	if a.link.switching == 0 {
		t.Fatal("a switch on an open connection must gate the session left")
	}
	answer := f.nextSwitch(t, loop)
	return a, loop, f, first, answer
}

// acceptsReplacement checks that the replacement connection's full view
// of the current session shows, though the old switch never got its
// acknowledgment.
func acceptsReplacement(t *testing.T, a *App, loop *frostuitest.Loop, s *fakeStream, id string) {
	t.Helper()
	if !strings.Contains(s.query, "c=7") {
		t.Fatalf("the replacement connection must name session 7: %q", s.query)
	}
	s.send(t, "hello", `{"connection":"`+id+`"}`)
	s.send(t, "view", fullView7)
	loop.RunUntil(t, func() bool { return a.link.streamID == id && a.view.Conversation.ID == 7 })
	if a.link.switching != 0 || len(a.view.Rows()) != 1 || a.loading != 0 {
		t.Fatalf("the replacement view was gated: switching %d rows %d loading %d", a.link.switching, len(a.view.Rows()), a.loading)
	}
}

// lateSwitchFailureIgnored answers the old switch with an error after the
// connection it named was replaced: it must not reconnect again. The
// callback logs either way, so the test waits for it, not for a time.
func lateSwitchFailureIgnored(t *testing.T, a *App, loop *frostuitest.Loop, f *fakeStreams, answer chan error) {
	t.Helper()
	gen := a.link.gen
	answer <- errors.New("connection gone")
	loop.RunUntil(t, func() bool {
		return len(a.console.Lines(nil, "superseded session switch")) > 0 || len(a.console.Lines(nil, "Switching sessions failed")) > 0
	})
	if a.link.gen != gen || len(a.console.Lines(nil, "Switching sessions failed")) > 0 {
		t.Fatal("a late switch failure from a replaced connection reconnected the link")
	}
	select {
	case <-f.streams:
		t.Fatal("a late switch failure opened another connection")
	default:
	}
}

// reconnectModes lose the connection (or the sign-in) in each way the
// issue names, and return the replacement stream.
var reconnectModes = []struct {
	name   string
	reopen func(t *testing.T, a *App, loop *frostuitest.Loop, f *fakeStreams, first *fakeStream, timers *[]func()) *fakeStream
}{
	{"automatic", func(t *testing.T, a *App, loop *frostuitest.Loop, f *fakeStreams, first *fakeStream, timers *[]func()) *fakeStream {
		_ = first.w.Close() // the stream drops before "switched" arrives
		loop.RunUntil(t, func() bool { return len(*timers) == 1 })
		(*timers)[0]()
		return f.next(t, loop)
	}},
	{"manual", func(t *testing.T, a *App, loop *frostuitest.Loop, f *fakeStreams, _ *fakeStream, _ *[]func()) *fakeStream {
		a.reconnectNow()
		return f.next(t, loop)
	}},
	{"sign-in-again", func(t *testing.T, a *App, loop *frostuitest.Loop, f *fakeStreams, _ *fakeStream, _ *[]func()) *fakeStream {
		a.signedOut("signed out")
		if a.link.switching != 0 {
			t.Fatal("signing out must forget the pending switch")
		}
		client, err := pocket.NewClient("https://example.org", "new-test")
		if err != nil {
			t.Fatal(err)
		}
		client.HTTP = &http.Client{Transport: routeTransport(f.transport)}
		a.link.client = client
		a.current, a.loading = 7, 7
		a.reconnectNow()
		return f.next(t, loop)
	}},
}

// The switch POST succeeds, its "switched" event is lost with the
// connection, and the replacement connection's full view must show.
func TestSwitchSucceededAcknowledgmentLost(t *testing.T) {
	for i, mode := range reconnectModes {
		t.Run(mode.name, func(t *testing.T) {
			var timers []func()
			a, loop, f, first, answer := connectAndSwitch(t)
			a.opts.After = func(_ time.Duration, fn func()) { timers = append(timers, fn) }
			answer <- nil // the server took the switch
			acceptsReplacement(t, a, loop, mode.reopen(t, a, loop, f, first, &timers), fmt.Sprintf("c%d", i+2))
		})
	}
}

// The switch POST answers with an error only after the connection was
// replaced: the replacement's view shows, and the late failure is ignored.
func TestSwitchLateFailureAfterReconnect(t *testing.T) {
	for i, mode := range reconnectModes {
		t.Run(mode.name, func(t *testing.T) {
			var timers []func()
			a, loop, f, first, answer := connectAndSwitch(t)
			a.opts.After = func(_ time.Duration, fn func()) { timers = append(timers, fn) }
			acceptsReplacement(t, a, loop, mode.reopen(t, a, loop, f, first, &timers), fmt.Sprintf("c%d", i+2))
			lateSwitchFailureIgnored(t, a, loop, f, answer)
		})
	}
}

// An event the old connection queued before the reconnect, run after the
// replacement connection cleared the pending switch, stays ignored.
func TestSwitchOldConnectionEventsStayIgnored(t *testing.T) {
	a, loop, f, first, answer := connectAndSwitch(t)
	answer <- nil
	first.send(t, "view", strings.Replace(fullView7, `"text":"hi"`, `"text":"old"`, 1))
	// An io.Pipe write returns only once the reader took it, and the
	// stream reads again only after it posted the event before: so the
	// old view now waits in the UI queue.
	if _, err := fmt.Fprint(first.w, ": ping\n\n"); err != nil {
		t.Fatal(err)
	}
	a.reconnectNow()
	if a.link.switching != 0 {
		t.Fatal("the reconnect must clear the pending switch")
	}
	loop.Flush() // the old view runs now, with no switch to gate it
	if len(a.view.Rows()) != 0 {
		t.Fatalf("the old connection's view was applied: %+v", a.view.Rows())
	}
	acceptsReplacement(t, a, loop, f.next(t, loop), "c2")
	if rows := a.view.Rows(); len(rows) != 1 || rows[0].Text != "hi" {
		t.Fatalf("rows = %+v", rows)
	}
}
