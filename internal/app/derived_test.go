package app

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/pocket/internal/pocket"
)

func largeInbox(b testing.TB) *App {
	a, _ := teamApp(b)
	list := make([]pocket.Session, 500)
	for i := range list {
		list[i] = pocket.Session{ID: i + 1, Title: fmt.Sprintf("Agent: work item %04d", i), Cwd: "/home/team/agents/duncan", Busy: i%10 == 0}
	}
	a.setSessions(list)
	a.desk.view = &pocket.DeskView{Threads: make([]pocket.DeskThread, 100)}
	for i := range a.desk.view.Threads {
		a.desk.view.Threads[i] = pocket.DeskThread{ID: fmt.Sprintf("T%d", i), Title: fmt.Sprintf("work item %04d", i), State: "open", Agents: []string{"duncan"}, Asks: []pocket.DeskAsk{{ID: fmt.Sprintf("T%da", i), Text: "Review the result", Options: []string{"Done", "Later"}}}}
	}
	return a
}

func BenchmarkInboxFrame500Sessions100Threads(b *testing.B) {
	a := largeInbox(b)
	tt := ui.NewTesterWith(a.View, 1360, 860, ui.TesterOptions{})
	tt.Frame()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		tt.Frame()
	}
}

func BenchmarkInboxDerived500Sessions100Threads(b *testing.B) {
	a := largeInbox(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		a.inboxCount()
		a.inboxItems()
		a.inboxItems()
		a.status()
		for i := range a.desk.view.Threads {
			a.sessionForThread(&a.desk.view.Threads[i])
		}
	}
}

func TestDerivedReusesAndInvalidatesSources(t *testing.T) {
	a, loop := teamApp(t)
	first := a.derivedData()
	items := a.inboxItems()
	if a.derivedData() != first || &a.inboxItems()[0] != &items[0] {
		t.Fatal("unchanged sources rebuilt")
	}
	a.setSessions([]pocket.Session{{ID: 7, Title: "work item", Waiting: true}})
	if a.derivedData() == first || a.derivedData().waiting != 1 {
		t.Fatal("session update stayed cached")
	}
	first = a.derivedData()
	a.handle(pocket.Event{Name: "peek", Data: []byte(`{"conversationId":7}`)})
	if a.derivedData() == first {
		t.Fatal("peek did not invalidate")
	}
	a.desk.view = &pocket.DeskView{Threads: []pocket.DeskThread{{ID: "T7", Title: "work item", State: "open"}}}
	thread := &a.desk.view.Threads[0]
	if a.sessionForThread(thread) != a.byID[7] {
		t.Fatal("thread did not match")
	}
	a.setSessions([]pocket.Session{{ID: 8, Title: "different"}})
	if a.sessionForThread(thread) != nil {
		t.Fatal("thread held old session pointer")
	}
	// Network completions can replace Desk, roster, lessons or core memory.
	a.link.client.HTTP = &http.Client{Transport: snapshotHTTP{}}
	first = a.derivedData()
	done := false
	a.request("fixture source update", func(context.Context, *pocket.Client) error { return nil }, func(error) {
		a.desk.view = &pocket.DeskView{Threads: []pocket.DeskThread{{ID: "T8", Asks: []pocket.DeskAsk{{ID: "T8a", Text: "new ask"}}}}}
		done = true
	})
	loop.RunUntil(t, func() bool { return done })
	if a.derivedData() == first {
		t.Fatal("request completion stayed cached")
	}
	found := false
	for _, it := range a.inboxItems() {
		if it.key == "a:T8a" {
			found = true
		}
	}
	if !found {
		t.Fatal("new Desk ask missing")
	}
}

func TestDerivedUnreadExpiryAndSeen(t *testing.T) {
	a, _ := teamApp(t)
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	a.opts.Now = func() time.Time { return now }
	a.setSessions([]pocket.Session{{ID: 7, EndedAt: pocket.Millis(now.Add(-30*24*time.Hour + time.Second).UnixMilli())}})
	if a.stateOf(a.byID[7]) != stateFresh {
		t.Fatal("new unread not fresh")
	}
	first := a.derivedData()
	now = now.Add(time.Second)
	if a.stateOf(a.byID[7]) != stateSeen || a.derivedData() == first {
		t.Fatal("30-day freshness did not expire")
	}
	a.setSessions([]pocket.Session{{ID: 7, EndedAt: pocket.Millis(now.Add(-time.Minute).UnixMilli()), Failed: true}})
	if a.stateOf(a.byID[7]) != stateFailed {
		t.Fatal("failure missing")
	}
	a.markSeen(7)
	if a.stateOf(a.byID[7]) != stateSeen {
		t.Fatal("seen mark stayed cached")
	}
}

func TestDerivedCurrentViewWinsOverList(t *testing.T) {
	a, _ := teamApp(t)
	a.current = 7
	a.setSessions([]pocket.Session{{ID: 7, Waiting: true}})
	a.view.Conversation.ID = 7
	a.view.Live.Busy = true
	a.view.Rev++
	if a.stateOf(a.byID[7]) != stateWorking {
		t.Fatal("view did not override list")
	}
	a.view.Live.Busy = false
	a.view.LiveRev++
	if a.stateOf(a.byID[7]) != stateSeen {
		t.Fatal("live revision stayed cached")
	}
}
