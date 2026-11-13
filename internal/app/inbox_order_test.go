package app

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

func TestDecisionsStayInThreadFilingOrderSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	a.sessions = nil
	a.lessons = lessons{}
	a.desk = desk{view: &pocket.DeskView{Threads: []pocket.DeskThread{
		{ID: "T2", Title: "Second thread", Asks: []pocket.DeskAsk{{ID: "T2a", Text: "Second thread first filed", At: 20}, {ID: "T2b", Text: "Second thread second filed", At: 40}}},
		{ID: "T1", Title: "First thread", Asks: []pocket.DeskAsk{{ID: "T1a", Text: "First thread first filed", At: 30}, {ID: "T1b", Text: "First thread second filed", At: 10}}},
	}}}
	items := a.inboxItems()
	var ids []string
	for _, it := range items {
		ids = append(ids, it.ask.ID)
	}
	if !reflect.DeepEqual(ids, []string{"T1a", "T1b", "T2a", "T2b"}) {
		t.Fatalf("filing order: %v", ids)
	}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.inboxPane(c) }, 760, 1000, ui.TesterOptions{})
	tt.Frame()
	texts := tt.Texts()
	for _, title := range []string{"First thread", "Second thread"} {
		n := 0
		for _, text := range texts {
			if text == title {
				n++
			}
		}
		if n != 1 {
			t.Errorf("thread heading %q shown %d times", title, n)
		}
	}
	last := float32(-1)
	for _, id := range ids {
		r, ok := tt.Find(id)
		if !ok || r.Y <= last {
			t.Fatalf("ask %s missing or out of order: %v", id, r)
		}
		last = r.Y
	}
	writePNG(t, tt, "inbox-thread-order")
}

func TestReopenedAskGetsNewDraftAndNotification(t *testing.T) {
	a, rec, _, _ := notifyApp(t)
	a.settings.Notes().Batch = false
	a.desk.view = &pocket.DeskView{Threads: []pocket.DeskThread{{ID: "T1", Title: "Plan", Asks: []pocket.DeskAsk{{ID: "T1a", Text: "Choose"}}}}}
	ask := &a.desk.view.Threads[0].Asks[0]
	a.notifyNewAsks(a.desk.view)
	old := askKey(ask)
	a.desk.answers = map[string]string{old: "old answer"}
	gen := 1
	ask.Gen = &gen
	a.notifyNewAsks(a.desk.view)
	a.notifyNewAsks(a.desk.view)
	if len(rec.titles) != 1 {
		t.Fatalf("reopened ask notified %d times", len(rec.titles))
	}
	items := a.inboxItems()
	it := items[slices.IndexFunc(items, func(it inboxItem) bool { return it.ask != nil })]
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.askItem(c, &it) }, 700, 400, ui.TesterOptions{})
	tt.Frame()
	if a.desk.answers[askKey(ask)] != "" || tt.HasText("old answer") {
		t.Fatal("reopened ask inherited the old draft")
	}
	if a.desk.answers[old] != "old answer" {
		t.Fatal("old draft was overwritten")
	}
}

func TestThreadContextClampsLongTextSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	thread := &pocket.DeskThread{ID: "T1", Title: "A compact thread", Answered: []pocket.DeskAnswered{{ID: "T1a", Text: "Decision", Words: strings.Repeat("Long answer words. ", 40)}}}
	thread.Events = append(thread.Events, struct {
		At   pocket.Millis `json:"at"`
		Text string        `json:"text"`
	}{Text: strings.Repeat("Long event words. ", 40)})
	a.desk.details = map[string]*pocket.DeskThreadDetail{"T1": {}}
	tt := ui.NewTesterWith(func(c *ui.Context) {
		frostui.Adapt(c)
		ui.Column(c).Width(240).Gap(12).AlignItems(ui.Stretch).Children(func() { a.threadContext(c, thread) })
	}, 260, 600, ui.TesterOptions{})
	tt.Frame()
	for _, text := range []string{"→ " + thread.Answered[0].Words, thread.Events[0].Text} {
		r, ok := tt.Find(text)
		if !ok || r.H > 44 || r.H < 24 {
			t.Errorf("expected two lines, got %v (found %v)", r, ok)
		}
	}
	writePNG(t, tt, "thread-context-clamped")
}
