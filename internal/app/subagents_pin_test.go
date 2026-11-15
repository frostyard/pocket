package app

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// pinMillis is a server time d before the app's clock, as the runner writes
// it.
func pinMillis(a *App, d time.Duration) pocket.Millis {
	return pocket.Millis(a.opts.Now().Add(-d).UnixMilli())
}

// pinTester draws 1 part of the composer above the field.
func pinTester(a *App, draw func(*App, *ui.Context)) *ui.Tester {
	return ui.NewTesterWith(func(c *ui.Context) {
		frostui.Adapt(c)
		draw(a, c)
	}, 850, 300, ui.TesterOptions{})
}

// pinText returns the first text of the last frame that starts with prefix.
func pinText(t *testing.T, tt *ui.Tester, prefix string) string {
	t.Helper()
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, prefix) {
			return s
		}
	}
	t.Fatalf("nothing shows %q: %v", prefix, tt.Texts())
	return ""
}

func TestSubagentStateAndNewest(t *testing.T) {
	cases := []struct {
		agent pocket.Subagent
		want  string
	}{
		{pocket.Subagent{Busy: true, Failed: true}, "working"},
		{pocket.Subagent{Stopped: true, Failed: true}, "stopped"},
		{pocket.Subagent{Failed: true}, "failed"},
		{pocket.Subagent{}, "done"},
	}
	for _, c := range cases {
		if got := subagentState(c.agent); got != c.want {
			t.Errorf("state of %+v is %q, want %q", c.agent, got, c.want)
		}
	}
	if got := subagentNewest(pocket.Subagent{AskedAt: 5, AnsweredAt: 9}); got != 9 {
		t.Errorf("newest is %d, want 9", got)
	}
	if got := subagentNewest(pocket.Subagent{AskedAt: 5}); got != 5 {
		t.Errorf("newest of an unanswered one is %d, want 5", got)
	}
}

func TestOrderedSubagentsPutsTroubleFirst(t *testing.T) {
	agents := []pocket.Subagent{
		{Name: "done-old", ConversationID: 1, AnsweredAt: 100},
		{Name: "failed", ConversationID: 2, Failed: true, AnsweredAt: 50},
		{Name: "working-old", ConversationID: 3, Busy: true, AskedAt: 10},
		{Name: "done-new", ConversationID: 4, AnsweredAt: 300},
		{Name: "stopped", ConversationID: 5, Stopped: true, AnsweredAt: 40},
		{Name: "working-new", ConversationID: 6, Busy: true, AskedAt: 200},
	}
	got := orderedSubagents(agents)
	want := []string{"working-new", "working-old", "failed", "stopped", "done-new", "done-old"}
	var names []string
	for _, sa := range got {
		names = append(names, sa.Name)
	}
	if !slices.Equal(names, want) {
		t.Errorf("order is %v, want %v", names, want)
	}
	if agents[0].Name != "done-old" {
		t.Error("the copy sorted the caller's slice")
	}
}

func TestSubagentSummaryTexts(t *testing.T) {
	cases := []struct {
		agents []pocket.Subagent
		want   string
	}{
		{[]pocket.Subagent{{Busy: true}, {Busy: true}, {Busy: true}}, "3 subagents working"},
		{[]pocket.Subagent{{}}, "1 subagent done"},
		{[]pocket.Subagent{{Busy: true}, {Busy: true}, {Failed: true}, {}}, "2 working · 1 failed · 1 done"},
		{[]pocket.Subagent{{}, {}, {Failed: true}}, "1 failed · 2 done"},
	}
	for _, c := range cases {
		if got := subagentSummary(c.agents); got != c.want {
			t.Errorf("summary of %d agents is %q, want %q", len(c.agents), got, c.want)
		}
	}
}

func TestSubagentElapsedTexts(t *testing.T) {
	cases := map[time.Duration]string{
		45 * time.Second:               "45s",
		185 * time.Second:              "3m 05s",
		12*time.Minute + 5*time.Second: "12m 05s",
		72 * time.Minute:               "1h 12m",
		-5 * time.Second:               "0s",
	}
	for d, want := range cases {
		if got := subagentElapsed(d); got != want {
			t.Errorf("elapsed of %v is %q, want %q", d, got, want)
		}
	}
}

func TestPinnedSubagentsHonorsPutAway(t *testing.T) {
	a, _ := teamApp(t)
	a.current = 7
	a.view = pocket.NewView()
	a.view.Subagents = []pocket.Subagent{
		{Name: "old", ConversationID: 11, AnsweredAt: 100},
		{Name: "new", ConversationID: 12, AnsweredAt: 300},
		{Name: "working", ConversationID: 13, Busy: true, AskedAt: 50},
	}
	a.composer.pin.away = map[accountKey]map[int]pocket.Millis{a.account: {7: 200}}

	shown, active := a.pinnedSubagents()
	var names []string
	for _, sa := range shown {
		names = append(names, sa.Name)
	}
	if !slices.Equal(names, []string{"new", "working"}) {
		t.Errorf("shown is %v, want the one newer than the mark and the working one", names)
	}
	if !active {
		t.Error("a working subagent leaves the bar active")
	}

	// A newer answer shows an agent again; a mark past it hides it.
	a.composer.pin.away[a.account][7] = 400
	if shown, active := a.pinnedSubagents(); len(shown) != 1 || active != true || shown[0].Name != "working" {
		t.Errorf("after a later mark, shown is %v (active %v)", shown, active)
	}
	// A mark belongs to its account: another one sees what the server sent.
	a.account = accountKey{server: "https://other.example", user: "u2"}
	if shown, _ := a.pinnedSubagents(); len(shown) != 3 {
		t.Errorf("another account shows %d agents, want 3", len(shown))
	}
}

func TestSubagentDoingSaysWhatItDoes(t *testing.T) {
	a, _ := teamApp(t)
	a.current = 7
	sa := pocket.Subagent{Name: "Agrippa", ConversationID: 11, Busy: true}

	if got := a.subagentDoing(sa); got != "Working…" {
		t.Errorf("without a peek it does %q", got)
	}
	a.peeks[11] = &pocket.Peek{ConversationID: 11}
	if got := a.subagentDoing(sa); got != "Thinking…" {
		t.Errorf("with an empty peek it does %q", got)
	}
	a.peeks[11] = &pocket.Peek{ConversationID: 11, Lines: []pocket.PeekLine{
		{Kind: "tool", Name: "bash", Args: map[string]any{"command": "go test ./..."}, Status: "running"},
	}}
	if got := a.subagentDoing(sa); !strings.Contains(got, "go test") {
		t.Errorf("its last step is %q, want the running call", got)
	}
	a.peeks[11].Approvals = append(a.peeks[11].Approvals, struct {
		ID             string `json:"id"`
		ConversationID int    `json:"conversationId"`
		Tool           string `json:"tool"`
		Subject        string `json:"subject"`
		Reason         string `json:"reason"`
		Form           bool   `json:"form,omitempty"`
	}{ID: "ap1", ConversationID: 11, Tool: "bash", Subject: "make test"})
	if got := a.subagentDoing(sa); got != "Waiting for approval: bash make test" {
		t.Errorf("waiting for approval it says %q", got)
	}
	limited := sa
	limited.Limited = 100
	if got := a.subagentDoing(limited); got != "Waiting for a usage limit to clear" {
		t.Errorf("held by a limit it says %q", got)
	}
}

func TestSubagentsPinUnfoldsAndOpensAChild(t *testing.T) {
	a, _ := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	a.view = pocket.NewView()
	a.view.Conversation = pocket.Conversation{ID: 7, Title: "Main"}
	a.view.Subagents = []pocket.Subagent{
		{Name: "Agrippa", ConversationID: 11, Busy: true, AskedAt: pinMillis(a, 45*time.Second),
			Asked: "Find the network config of the lab"},
		{Name: "Duncan", ConversationID: 12, AnsweredAt: pinMillis(a, 2*time.Minute), Asked: "Run the CI matrix"},
		{Name: "Irulan", ConversationID: 13, AnsweredAt: pinMillis(a, 3*time.Minute)},
	}
	tt := pinTester(a, (*App).subagentsPin)
	tt.Frame()
	if label := pinText(t, tt, "Subagents: "); label != "Subagents: 1 working · 2 done. Agrippa: Working…" {
		t.Errorf("the folded label is %q", label)
	}
	if _, ok := tt.Find("Put the subagents bar away"); ok {
		t.Error("the bar can be put away while a subagent works")
	}
	writePNG(t, tt, "subagents-pin-folded")

	// A click unfolds the list, and a row opens its subagent.
	if err := tt.Click("Subagents: 1 working · 2 done. Agrippa: Working…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.composer.pin.open != 7 {
		t.Fatalf("the list did not unfold: open %d", a.composer.pin.open)
	}
	if !tt.HasText("Agrippa") || !tt.HasText("Waiting…") && !tt.HasText("Working…") {
		t.Errorf("the unfolded bar shows %v", tt.Texts())
	}
	writePNG(t, tt, "subagents-pin-open")
	if err := tt.Click("Open subagent Duncan"); err != nil {
		t.Fatal(err)
	}
	if a.current != 12 {
		t.Errorf("opening Duncan showed session %d, want 12", a.current)
	}
}

func TestSubagentsPinPutsItselfAway(t *testing.T) {
	a, _ := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	a.view = pocket.NewView()
	a.view.Conversation = pocket.Conversation{ID: 7, Title: "Main"}
	a.view.Subagents = []pocket.Subagent{
		{Name: "Duncan", ConversationID: 12, AnsweredAt: pinMillis(a, 2*time.Minute)},
		{Name: "Irulan", ConversationID: 13, AnsweredAt: pinMillis(a, 3*time.Minute)},
	}
	a.composer.pin.open = 7
	tt := pinTester(a, (*App).subagentsPin)
	tt.Frame()
	if !tt.HasText("Subagents: 2 subagents done") {
		t.Fatalf("no bar: %v", tt.Texts())
	}
	if err := tt.Click("Put the subagents bar away"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.composer.pin.open != 0 || a.composer.pin.away[a.account][7] == 0 {
		t.Fatalf("putting away kept open %d and mark %d", a.composer.pin.open, a.composer.pin.away[a.account][7])
	}
	if tt.HasText("Subagents: ") {
		t.Errorf("the bar stays after it was put away: %v", tt.Texts())
	}

	// Something newer brings it back, folded: the bar was put away unfolded.
	a.view.Subagents = append(a.view.Subagents, pocket.Subagent{Name: "Sheeana", ConversationID: 14,
		AnsweredAt: pinMillis(a, time.Minute)})
	tt.Frame()
	// Put away, the bar lists only what did something since.
	if !tt.HasText("Subagents: 1 subagent done") {
		t.Errorf("a newer subagent does not show the bar: %v", tt.Texts())
	}
	if a.composer.pin.open != 0 {
		t.Error("the bar opened again by itself")
	}
}

// TestSubagentsPinFoldsOnSignOut checks that a list unfolded in 1 sign-in
// does not show unfolded for the same session ID of the next one.
func TestSubagentsPinFoldsOnSignOut(t *testing.T) {
	a, _ := teamApp(t)
	a.composer.pin.open = 7
	a.composer.pin.peeked = []int{11}
	a.composer.resetRequests()
	if a.composer.pin.open != 0 || a.composer.pin.peeked != nil {
		t.Errorf("sign-out kept open %d and peeks %v", a.composer.pin.open, a.composer.pin.peeked)
	}
}

func TestSubagentsPinStopsAWorkingSubagent(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	a.view = pocket.NewView()
	a.view.Conversation = pocket.Conversation{ID: 7, Title: "Main"}
	a.view.Subagents = []pocket.Subagent{{Name: "Agrippa", ConversationID: 11, Busy: true,
		AskedAt: pinMillis(a, time.Minute)}}
	aborted := make(chan string, 1)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		aborted <- r.URL.Path
		return reply(r, `{}`), nil
	})}
	a.composer.pin.open = 7
	tt := pinTester(a, (*App).subagentsPin)
	tt.Frame()
	if err := tt.Click("Stop Agrippa"); err != nil {
		t.Fatal(err)
	}
	select {
	case path := <-aborted:
		if path != "/api/c/11/abort" {
			t.Errorf("stopped %q, want the subagent's abort", path)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no abort for the subagent")
	}
	loop.Flush()
}

func TestParentPinReturnsToTheMainSession(t *testing.T) {
	a, _ := teamApp(t)
	a.current = 12
	a.composer.reset(12)
	a.view = pocket.NewView()
	a.view.Conversation = pocket.Conversation{ID: 12, Kind: "subagent", Parent: &struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	}{ID: 3, Title: "Main"}}
	tt := pinTester(a, (*App).parentPin)
	tt.Frame()
	if !tt.HasText("Back to main session") {
		t.Fatalf("no way back: %v", tt.Texts())
	}
	writePNG(t, tt, "parent-pin")
	if err := tt.Click("Back to main session: Main"); err != nil {
		t.Fatal(err)
	}
	if a.current != 3 {
		t.Errorf("the way back led to session %d, want 3", a.current)
	}

	// A session that started no subagent has no way back to one.
	a.view = pocket.NewView()
	a.view.Conversation = pocket.Conversation{ID: 7, Title: "Main"}
	tt.Frame()
	if tt.HasText("Back to main session") {
		t.Errorf("a session of its own shows a way back: %v", tt.Texts())
	}
}

func TestPeekTargetsIncludeWorkingSubagents(t *testing.T) {
	a, _ := teamApp(t)
	a.current = 7
	a.view = pocket.NewView()
	a.view.Subagents = []pocket.Subagent{
		{Name: "Agrippa", ConversationID: 11, Busy: true},
		{Name: "Duncan", ConversationID: 12, AnsweredAt: 100},
		{Name: "Irulan", ConversationID: 13, Busy: true},
	}
	ids := a.peekTargets()
	for _, want := range []int{11, 13} {
		if !slices.Contains(ids, want) {
			t.Errorf("peeks leave out working subagent %d: %v", want, ids)
		}
	}
	if slices.Contains(ids, 12) {
		t.Errorf("peeks follow the done subagent 12: %v", ids)
	}

	// At most 20, all of them named once.
	a.view.Subagents = nil
	for i := range 25 {
		a.view.Subagents = append(a.view.Subagents, pocket.Subagent{Name: "many", ConversationID: 100 + i, Busy: true})
	}
	ids = a.peekTargets()
	if len(ids) != 20 {
		t.Errorf("peeks follow %d sessions, want 20", len(ids))
	}
	if len(slices.Compact(slices.Sorted(slices.Values(ids)))) != len(ids) {
		t.Errorf("peeks name a session twice: %v", ids)
	}

	// No session open, no subagents.
	a.current = 0
	if ids := a.peekTargets(); slices.ContainsFunc(ids, func(id int) bool { return id >= 100 }) {
		t.Errorf("peeks follow subagents with no session open: %v", ids)
	}
}
