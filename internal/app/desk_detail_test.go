package app

import (
	"net/http"
	"testing"

	"github.com/frostyard/frostui/frostuitest"
)

// detailApp serves the Desk at once and holds each read of thread T7
// until the test answers it, through the channel the read sends on reads.
func detailApp(t *testing.T, desk string) (a *App, loop *frostuitest.Loop, reads chan chan string, ran func() int) {
	t.Helper()
	reads = make(chan chan string, 4)
	a, loop = requestApp(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/x/desk/threads/T7":
			answer := make(chan string, 1)
			reads <- answer
			return reply(r, <-answer), nil
		case "/api/x/desk/threads":
			return reply(r, desk), nil
		}
		return reply(r, `{}`), nil
	})
	// Count the completions the UI thread ran, and turn a panic in one
	// into a test failure.
	done := 0
	a.post = func(fn func()) {
		loop.Post(func() {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("a completion panicked: %v", p)
				}
			}()
			fn()
			done++
		})
	}
	return a, loop, reads, func() int { return done }
}

// waitRead waits for the next read of T7 and returns its answer channel.
func waitRead(t *testing.T, loop *frostuitest.Loop, reads chan chan string) chan string {
	t.Helper()
	var answer chan string
	loop.RunUntil(t, func() bool {
		select {
		case answer = <-reads:
			return true
		default:
			return false
		}
	})
	return answer
}

// A Desk read that ends while a thread's detail is still being read must
// not crash the old answer, and the old answer must not replace the
// detail read for the newer Desk snapshot.
func TestDeskRefreshBeforeThreadDetailAnswers(t *testing.T) {
	a, loop, reads, ran := detailApp(t, `{"threads":[{"id":"T7","title":"Newer"}]}`)
	if a.threadDetail("T7") != nil {
		t.Fatal("the first call starts a read and has no detail yet")
	}
	old := waitRead(t, loop, reads) // the old read is held in the transport

	a.loadDesk()
	loop.RunUntil(t, func() bool { return !a.desk.loading })
	if a.desk.view == nil {
		t.Fatal("the Desk read must finish")
	}

	// The thread shows again for the newer snapshot: a fresh read starts,
	// though the old one has not answered.
	if a.threadDetail("T7") != nil {
		t.Fatal("the newer snapshot has no detail yet")
	}
	fresh := waitRead(t, loop, reads)

	// The fresh read answers first, then the stale one.
	fresh <- `{"thread":{"id":"T7","title":"Newer"}}`
	loop.RunUntil(t, func() bool { return a.threadDetail("T7") != nil })
	before := ran()
	old <- `{"thread":{"id":"T7","title":"Older"}}`
	loop.RunUntil(t, func() bool { return ran() > before })

	d := a.threadDetail("T7")
	if d == nil || d.Thread.Title != "Newer" {
		t.Fatalf("detail = %+v, want the newer snapshot's", d)
	}
}

// The stale answer alone (no fresh read yet) neither crashes nor fills
// the newer cache, and the thread is read again for the newer snapshot.
func TestStaleThreadDetailDoesNotFillNewCache(t *testing.T) {
	a, loop, reads, ran := detailApp(t, `{"threads":[]}`)
	a.threadDetail("T7")
	old := waitRead(t, loop, reads)
	a.loadDesk()
	loop.RunUntil(t, func() bool { return !a.desk.loading })
	before := ran()
	old <- `{"thread":{"id":"T7","title":"Older"}}`
	loop.RunUntil(t, func() bool { return ran() > before })
	if d := a.threadDetail("T7"); d != nil {
		t.Fatalf("an answer from before the Desk read filled the newer cache: %+v", d)
	}
	waitRead(t, loop, reads) <- `{"thread":{"id":"T7","title":"Fresh"}}`
	loop.RunUntil(t, func() bool { return a.threadDetail("T7") != nil })
	if got := a.threadDetail("T7").Thread.Title; got != "Fresh" {
		t.Fatalf("detail title = %q", got)
	}
}
