package app

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

// askTitle names the card whose box the tests point at and scroll.
const askTitle = "Before the tests run"

// longQuestion is 1 question taller than the window: a long text and 2 fields,
// each with a long label and choices that describe themselves.
func longQuestion() pocket.Approval {
	sentence := "Write the reason in full sentences; the reviewer reads it without the rest of the thread. "
	choice := func(label string) pocket.AskChoice {
		return pocket.AskChoice{Label: label, Description: strings.Repeat(sentence, 3)}
	}
	return pocket.Approval{ID: "ask1", Tool: "ask", Form: &pocket.AskForm{
		Title:  askTitle,
		Text:   strings.Repeat("This question is taller than the window, so the dock must scroll to show all of it. ", 6),
		Submit: "Answer",
		Fields: []pocket.AskField{
			{Name: "scope", Header: "Scope", Label: strings.Repeat("Which scope does the change take? ", 2),
				Choices: []pocket.AskChoice{choice("The whole repository"), choice("The composer only"), choice("The docs only")},
				Value:   "The composer only"},
			{Name: "areas", Header: "Areas", Label: strings.Repeat("Which areas does it cover? ", 2), Multiple: true,
				Choices: []pocket.AskChoice{choice("The tests"), choice("The dock"), choice("The transcript")},
				Value:   "The tests"},
		},
	}}
}

// longAskTester draws the conversation pane with 1 long question waiting,
// and what setUp adds to the session's view.
func longAskTester(t *testing.T, setUp func(a *App)) *ui.Tester {
	t.Helper()
	a, _ := teamApp(t)
	a.current = 7
	a.view = pocket.NewView()
	a.view.Approvals = []pocket.Approval{longQuestion()}
	if setUp != nil {
		setUp(a)
	}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.conversationPane(c) }, 900, 420, ui.TesterOptions{})
	tt.Frame()
	return tt
}

// fullQueue fills the queue dock as a busy session with 8 waiting messages
// and 1 held one does, as TestQueueDockKeepsComposerVisible draws it.
func fullQueue(a *App) {
	a.view.Live.Busy = true
	long := strings.Repeat("A long waiting message. ", 13)
	for i := range 8 {
		a.view.Inbox = append(a.view.Inbox, pocket.QueueRow{ID: 40 + i, By: "u1", Mode: "followUp", Text: "[from: Brian] " + long})
	}
	a.view.Held = []pocket.HeldMessage{{Key: "h1", By: "u1", Text: strings.Repeat("held ", 400)}}
}

// whole reports whether r is drawn inside a window of height h.
func whole(r ui.Rect, h float32) bool { return r.H > 0 && r.Y >= 0 && r.Y+r.H <= h }

// askDockScrollsToTheAnswer checks, after the dock scrolled, that the Answer
// button is whole in the window and above the composer, which never covers it.
func askDockScrollsToTheAnswer(t *testing.T, tt *ui.Tester, field ui.Rect, height float32) {
	t.Helper()
	answer, ok := tt.Find("Answer")
	if !ok {
		t.Fatalf("the answer button is not drawn: %q", tt.Texts())
	}
	if !whole(answer, height) {
		t.Errorf("the answer button is out of the window: %+v", answer)
	}
	if answer.Y+answer.H > field.Y {
		t.Errorf("the composer covers the answer button: button %+v, field %+v", answer, field)
	}
}

// A question taller than the window scrolls in the dock: the person reaches
// its Answer button with the wheel, and the composer stays whole.
func TestAskDockScrollsToTheAnswer(t *testing.T) {
	tt := longAskTester(t, nil)
	writePNG(t, tt, "ask-dock-long")
	const height float32 = 420
	field, ok := tt.Find("Message")
	if !ok || !whole(field, height) {
		t.Fatalf("the composer field is out of the window: %+v (%v): %q", field, ok, tt.Texts())
	}
	title, ok := tt.Find(askTitle)
	if !ok {
		t.Fatalf("the card title is not drawn: %q", tt.Texts())
	}
	x, y := title.X+title.W/2, title.Y+title.H/2
	tt.Move(x, y)
	for range 20 {
		tt.Wheel(x, y, 0, 120)
	}
	tt.Advance(time.Second)
	tt.Frame()
	writePNG(t, tt, "ask-dock-long-scrolled")
	askDockScrollsToTheAnswer(t, tt, field, height)
}

// The keys reach the end of a long question too, with the pointer over the
// card: End scrolls the dock, and the Answer button comes into the window.
// 1 Tab first leaves the message field, which takes End as its own key; with
// the field focused, the dock keeps the wheel and Tab and not the arrows.
func TestAskDockKeysScrollToTheAnswer(t *testing.T) {
	tt := longAskTester(t, nil)
	const height float32 = 420
	field, ok := tt.Find("Message")
	if !ok || !whole(field, height) {
		t.Fatalf("the composer field is out of the window: %+v (%v): %q", field, ok, tt.Texts())
	}
	title, ok := tt.Find(askTitle)
	if !ok {
		t.Fatalf("the card title is not drawn: %q", tt.Texts())
	}
	tt.Move(title.X+title.W/2, title.Y+title.H/2)
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	tt.Key(0, ui.KeyEnd)
	tt.Frame()
	askDockScrollsToTheAnswer(t, tt, field, height)
}

// A long question beside a full queue dock never pushes the composer out:
// the approvals dock gives way to the queue and to the field, and the wheel
// still reaches its Answer button.
func TestAskDockShrinksBesideAFullQueue(t *testing.T) {
	tt := longAskTester(t, fullQueue)
	writePNG(t, tt, "ask-dock-with-queue")
	const height float32 = 420
	field, ok := tt.Find("Message")
	if !ok || !whole(field, height) {
		t.Fatalf("the composer field is out of the window: %+v (%v): %q", field, ok, tt.Texts())
	}
	send, ok := tt.Find("Send (Enter)")
	if !ok || !whole(send, height) {
		t.Fatalf("the send button is out of the window: %+v (%v)", send, ok)
	}
	title, ok := tt.Find(askTitle)
	if !ok {
		t.Fatalf("the card title is not drawn: %q", tt.Texts())
	}
	x, y := title.X+title.W/2, title.Y+title.H/2
	tt.Move(x, y)
	for range 20 {
		tt.Wheel(x, y, 0, 120)
	}
	tt.Advance(time.Second)
	tt.Frame()
	askDockScrollsToTheAnswer(t, tt, field, height)
	// The queue dock sits between the question and the field: the Answer
	// button shows above it, not under it.
	answer, _ := tt.Find("Answer")
	queue, ok := tt.Find("8 messages waiting for Pi")
	if !ok || answer.Y+answer.H > queue.Y {
		t.Errorf("the queue dock covers the answer button: button %+v, queue %+v (%v)", answer, queue, ok)
	}
}
