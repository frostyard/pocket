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
// connection it named was replaced: it must not reconnect again.
func lateSwitchFailureIgnored(t *testing.T, a *App, loop *frostuitest.Loop, f *fakeStreams, answer chan error) {
	t.Helper()
	gen := a.link.gen
	answer <- errors.New("connection gone")
	loop.Settle(50 * time.Millisecond)
	if a.link.gen != gen {
		t.Fatal("a late switch failure from a replaced connection reconnected the link")
	}
	select {
	case <-f.streams:
		t.Fatal("a late switch failure opened another connection")
	default:
	}
}

func TestSwitchLostAcknowledgmentAutomaticReconnect(t *testing.T) {
	var timers []func()
	a, loop, f, first, answer := connectAndSwitch(t)
	a.opts.After = func(_ time.Duration, fn func()) { timers = append(timers, fn) }
	_ = first.w.Close() // the stream drops before "switched" arrives
	loop.RunUntil(t, func() bool { return len(timers) == 1 })
	timers[0]()
	acceptsReplacement(t, a, loop, f.next(t, loop), "c2")
	lateSwitchFailureIgnored(t, a, loop, f, answer)
}

func TestSwitchLostAcknowledgmentManualReconnect(t *testing.T) {
	a, loop, f, _, answer := connectAndSwitch(t)
	a.reconnectNow()
	acceptsReplacement(t, a, loop, f.next(t, loop), "c2")
	lateSwitchFailureIgnored(t, a, loop, f, answer)
}

func TestSwitchLostAcknowledgmentSignInAgain(t *testing.T) {
	a, loop, f, _, answer := connectAndSwitch(t)
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
	acceptsReplacement(t, a, loop, f.next(t, loop), "c3")
	lateSwitchFailureIgnored(t, a, loop, f, answer)
}

// Events of the old connection stay ignored after the replacement opens.
func TestSwitchOldConnectionEventsStayIgnored(t *testing.T) {
	a, loop, f, first, _ := connectAndSwitch(t)
	a.reconnectNow()
	second := f.next(t, loop)
	second.send(t, "hello", `{"connection":"c2"}`)
	loop.RunUntil(t, func() bool { return a.link.streamID == "c2" })
	// The old stream was cancelled: whatever it still carries is dropped.
	_, _ = fmt.Fprintf(first.w, "event: view\ndata: %s\n\n", strings.Replace(fullView7, `"text":"hi"`, `"text":"old"`, 1))
	second.send(t, "view", fullView7)
	loop.RunUntil(t, func() bool { return a.view.Conversation.ID == 7 })
	loop.Settle(20 * time.Millisecond)
	if rows := a.view.Rows(); len(rows) != 1 || rows[0].Text != "hi" {
		t.Fatalf("rows = %+v", rows)
	}
}
