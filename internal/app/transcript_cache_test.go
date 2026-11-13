package app

import (
	"fmt"
	"testing"

	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

// textView returns a view of rows stored assistant messages, each one text
// row, with the run idle.
func textView(rows int) *pocket.View {
	v := pocket.NewView()
	v.Conversation = pocket.Conversation{ID: 7}
	v.Order = make([]int, 0, rows)
	for i := 1; i <= rows; i++ {
		v.Order = append(v.Order, i)
		v.Entries[i] = &pocket.Entry{ID: i, Kind: "assistant", Blocks: []pocket.Block{{Type: "text", Text: fmt.Sprintf("stored row %d", i)}}}
	}
	v.Rev, v.LiveRev = 1, 1
	return v
}

// callView returns a view of one stored tool call, as it is shown while the
// call runs.
func callView() *pocket.View {
	v := pocket.NewView()
	v.Conversation = pocket.Conversation{ID: 7}
	v.Order = []int{1}
	v.Entries[1] = &pocket.Entry{ID: 1, Kind: "assistant", Blocks: []pocket.Block{
		{Type: "toolCall", ID: "c1", Name: "bash", Args: map[string]any{"command": "make test"}},
	}}
	v.Rev, v.LiveRev = 1, 1
	v.Live = pocket.Live{Busy: true}
	return v
}

// update applies one server update to the view and shows it, as the stream
// does.
func update(t *testing.T, a *App, u *pocket.ViewUpdate) {
	t.Helper()
	if u.Conversation.ID == 0 {
		u.Conversation = a.view.Conversation
	}
	if err := a.view.Apply(u); err != nil {
		t.Fatalf("apply: %v", err)
	}
	a.tr.viewChanged(a)
}

// storedCall returns the row of one folded call.
func storedCall(t *testing.T, a *App, callID string) *call {
	t.Helper()
	for i := range a.tr.items {
		for j := range a.tr.items[i].calls {
			if a.tr.items[i].calls[j].block.ID == callID {
				return &a.tr.items[i].calls[j]
			}
		}
	}
	t.Fatalf("no row holds call %s", callID)
	return nil
}

func TestTranscriptLiveUpdateRefreshesCallSlot(t *testing.T) {
	a, _ := teamApp(t)
	a.view = callView()
	a.tr.viewChanged(a)
	if cl := storedCall(t, a, "c1"); cl.slot != nil || cl.asked != nil {
		t.Fatalf("the call starts with slot %+v and approval %+v", cl.slot, cl.asked)
	}
	update(t, a, &pocket.ViewUpdate{Live: pocket.Live{Busy: true, Tools: []pocket.ToolSlot{
		{CallID: "c1", Name: "bash", Status: "running", Output: "one"},
	}}})
	cl := storedCall(t, a, "c1")
	if cl.slot == nil || cl.slot.Status != "running" || cl.slot.Output != "one" {
		t.Fatalf("the slot of a live-only update is %+v", cl.slot)
	}
	if got := callState(*cl); got != frostui.StepRunning {
		t.Fatalf("the call is %v, not running", got)
	}
	// The slot the view holds is a new one: the row must point at it.
	v := a.view
	v.LiveRev++
	v.Live.Tools[0].Output = "two"
	a.tr.viewChanged(a)
	if cl := storedCall(t, a, "c1"); cl.slot == nil || cl.slot.Output != "two" {
		t.Fatalf("the slot after it changed is %+v", cl.slot)
	}
	update(t, a, &pocket.ViewUpdate{Live: pocket.Live{Busy: true}})
	if cl := storedCall(t, a, "c1"); cl.slot != nil {
		t.Fatalf("the slot stayed after the call ended: %+v", cl.slot)
	}
}

func TestTranscriptLiveUpdateRefreshesApproval(t *testing.T) {
	a, _ := teamApp(t)
	a.view = callView()
	a.tr.viewChanged(a)
	update(t, a, &pocket.ViewUpdate{
		Live:      pocket.Live{Busy: true},
		Approvals: []pocket.Approval{{ID: "a1", CallID: "c1", Tool: "bash", Subject: "make test"}},
	})
	cl := storedCall(t, a, "c1")
	if cl.asked == nil || cl.asked.ID != "a1" {
		t.Fatalf("the approval of a live-only update is %+v", cl.asked)
	}
	if got := callState(*cl); got != frostui.StepHeld {
		t.Fatalf("the call is %v, not held", got)
	}
	update(t, a, &pocket.ViewUpdate{Live: pocket.Live{Busy: true}})
	if cl := storedCall(t, a, "c1"); cl.asked != nil {
		t.Fatalf("the approval stayed after it was answered: %+v", cl.asked)
	}
}

func TestTranscriptLiveUpdateAddsAndRemovesTheLiveRow(t *testing.T) {
	a, _ := teamApp(t)
	a.view = textView(2)
	a.tr.viewChanged(a)
	if n := len(a.tr.items); n != 2 {
		t.Fatalf("rows %d", n)
	}
	a.tr.find = sessionFind{open: true, query: "stored row"}
	if n := len(a.findMatches()); n != 2 {
		t.Fatalf("matches %d", n)
	}
	update(t, a, &pocket.ViewUpdate{Live: pocket.Live{Busy: true}})
	if n := len(a.tr.items); n != 3 || a.tr.items[2].kind != itemLive {
		t.Fatalf("rows %d after the run started", n)
	}
	if n := len(a.findMatches()); n != 2 {
		t.Fatalf("matches %d after the live row came", n)
	}
	update(t, a, &pocket.ViewUpdate{Live: pocket.Live{Limit: &pocket.Limit{Provider: "anthropic", Reason: "usage"}}})
	if n := len(a.tr.items); n != 3 || a.tr.items[2].kind != itemLive {
		t.Fatalf("rows %d while waiting for a limit", n)
	}
	update(t, a, &pocket.ViewUpdate{Live: pocket.Live{}})
	if n := len(a.tr.items); n != 2 {
		t.Fatalf("rows %d after the run ended", n)
	}
	if n := len(a.findMatches()); n != 2 {
		t.Fatalf("matches %d after the live row went", n)
	}
}

func TestTranscriptSearchSurvivesALiveUpdateAndFollowsEntries(t *testing.T) {
	a, _ := teamApp(t)
	a.view = textView(3)
	a.tr.viewChanged(a)
	update(t, a, &pocket.ViewUpdate{Live: pocket.Live{Busy: true}})
	a.tr.find = sessionFind{open: true, query: "stored row"}
	if n := len(a.findMatches()); n != 3 {
		t.Fatalf("matches %d", n)
	}
	cached := a.tr.search
	// A live update that keeps the run going adds and removes no row.
	update(t, a, &pocket.ViewUpdate{Live: pocket.Live{Busy: true, Tools: []pocket.ToolSlot{
		{CallID: "c9", Name: "bash", Status: "running"},
	}}})
	if len(a.tr.search) == 0 {
		t.Fatal("a live update dropped the search cache")
	}
	if &a.tr.search[0] != &cached[0] {
		t.Fatal("a live update rebuilt the search cache")
	}
	if n := len(a.findMatches()); n != 3 {
		t.Fatalf("matches %d after a live update", n)
	}
	// A stored entry is a new row: the cache must follow it.
	update(t, a, &pocket.ViewUpdate{
		Entries: []pocket.Entry{{ID: 4, Kind: "user", Text: "another stored row"}},
		Order:   []int{1, 2, 3, 4},
	})
	got := a.findMatches()
	if len(got) != 4 || a.tr.items[got[3]].key != "u:4" {
		t.Fatalf("matches %v after a stored row was added", got)
	}
}

func TestTranscriptRowsFollowStoredUpdates(t *testing.T) {
	a, _ := teamApp(t)
	a.view = callView()
	a.tr.viewChanged(a)

	// The call's result arrives: a stored update, so the row shows it.
	update(t, a, &pocket.ViewUpdate{
		Entries: []pocket.Entry{
			{ID: 1, Kind: "assistant", Blocks: []pocket.Block{{Type: "toolCall", ID: "c1", Name: "bash", Args: map[string]any{"command": "make test"}}}},
			{ID: 2, Kind: "toolResult", CallID: "c1", Text: "all tests passed"},
		},
		Order: []int{1, 2},
	})
	cl := storedCall(t, a, "c1")
	if cl.result == nil || cl.result.Text != "all tests passed" {
		t.Fatalf("the result of the call is %+v", cl.result)
	}
	if got := callState(*cl); got != frostui.StepDone {
		t.Fatalf("the call is %v, not done", got)
	}

	// A compaction adds to the order from a point on: the rows follow it.
	from := 2
	update(t, a, &pocket.ViewUpdate{
		Entries:   []pocket.Entry{{ID: 3, Kind: "compaction", Summary: "The context was compacted."}},
		Order:     []int{3},
		OrderFrom: &from,
	})
	if n := len(a.tr.items); n != 2 || a.tr.items[0].kind != itemTools || a.tr.items[1].kind != itemCompaction {
		t.Fatalf("rows %d after the order changed", n)
	}
	if n := len(a.tr.items[0].calls); n != 1 {
		t.Fatalf("the run of tools holds %d calls", n)
	}

	// A partial entry becomes whole: the row shows the whole text.
	update(t, a, &pocket.ViewUpdate{
		Entries: []pocket.Entry{{ID: 4, Kind: "user", Text: "half a mes", Partial: true}},
		Order:   []int{1, 2, 3, 4},
	})
	if n := len(a.tr.items); n != 3 || a.tr.items[2].text != "half a mes" {
		t.Fatalf("rows %d after a partial entry", n)
	}
	update(t, a, &pocket.ViewUpdate{
		Entries: []pocket.Entry{{ID: 4, Kind: "user", Text: "half a message"}},
	})
	if n := len(a.tr.items); n != 3 || a.tr.items[2].text != "half a message" {
		t.Fatalf("the row of a whole entry is %q", a.tr.items[2].text)
	}
}

// BenchmarkTranscriptLiveUpdate measures one streaming update over a
// transcript of stored rows.
func BenchmarkTranscriptLiveUpdate(b *testing.B) {
	for _, rows := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("rows=%d", rows), func(b *testing.B) {
			a, _ := teamApp(b)
			v := textView(rows)
			a.view = v
			a.tr = transcript{}
			a.tr.viewChanged(a)
			if n := len(a.tr.items); n != rows {
				b.Fatalf("rows %d", n)
			}
			tools := []pocket.ToolSlot{{CallID: "c1", Name: "bash", Status: "running"}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// A text delta carries no stored entry: only LiveRev moves.
				v.LiveRev++
				v.Live = pocket.Live{Busy: true, Tools: tools}
				a.tr.viewChanged(a)
			}
		})
	}
}

// TestTranscriptLiveUpdateAllocsStayFlat guards the cache: a streaming update
// changes the live state in place, so its allocations do not grow with the
// transcript.
func TestTranscriptLiveUpdateAllocsStayFlat(t *testing.T) {
	a, _ := teamApp(t)
	perUpdate := func(rows int) float64 {
		v := textView(rows)
		a.view = v
		a.tr = transcript{}
		a.tr.viewChanged(a)
		return testing.AllocsPerRun(50, func() {
			v.LiveRev++
			a.tr.viewChanged(a)
		})
	}
	small, large := perUpdate(100), perUpdate(10000)
	if large > small {
		t.Fatalf("a streaming update allocates %.0f times over 10000 rows and %.0f over 100: it must change the rows in place", large, small)
	}
	if large > 8 {
		t.Fatalf("a streaming update allocates %.0f times: it must change the rows in place", large)
	}
}
