package app

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frostyard/pocket/internal/pocket"
)

// startTaskHTTP answers the task start route of session source with status and
// body, after release closes when release is not nil. Every other route is
// missing, as the snapshot transport has it.
func startTaskHTTP(t *testing.T, source int, entered, release chan struct{}, status int, body string) sessionToolsHTTP {
	t.Helper()
	path := "/api/x/team/session/" + strconv.Itoa(source) + "/start"
	return func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != path {
			return snapshotHTTP{}.RoundTrip(r)
		}
		if entered != nil {
			close(entered)
		}
		if release != nil {
			<-release
		}
		resp := reply(r, body)
		resp.StatusCode = status
		return resp, nil
	}
}

func TestFailedAgentTaskRestoresCommand(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	a.composer.draft = "@duncan fix the CI"
	a.link.client.HTTP = &http.Client{Transport: startTaskHTTP(t, 7, nil, nil, http.StatusServiceUnavailable, `{"error":"Try later"}`)}
	a.send(a.composer.draft, "")
	if a.composer.draft != "" || len(a.composer.pendingTasks) != 1 {
		t.Fatal("the command did not leave the composer as a pending task")
	}
	loop.RunUntil(t, func() bool { return len(a.composer.pendingTasks) == 0 })
	if a.composer.draft != "@duncan fix the CI" || a.composer.drafts[7] != "@duncan fix the CI" {
		t.Fatalf("a failed task lost its command: %q", a.composer.draft)
	}
	if len(a.pendingToasts) == 0 || !strings.Contains(a.pendingToasts[len(a.pendingToasts)-1], "Try later") {
		t.Fatalf("the failure was not reported: %v", a.pendingToasts)
	}
}

func TestAgentTaskFailureKeepsWhatWasTyped(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	entered, release := make(chan struct{}), make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: startTaskHTTP(t, 7, entered, release, http.StatusServiceUnavailable, `{"error":"Try later"}`)}
	a.send("@duncan fix the CI", "")
	<-entered
	a.composer.draft = "typed meanwhile"
	before := loop.Ran()
	close(release)
	loop.RunUntil(t, func() bool { return loop.Ran() > before })
	if a.composer.draft != "@duncan fix the CI\n\ntyped meanwhile" {
		t.Fatalf("the failure overwrote what was typed: %q", a.composer.draft)
	}
}

func TestAgentTaskSignOutRecoversCommandOnce(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	entered, release := make(chan struct{}), make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: startTaskHTTP(t, 7, entered, release, http.StatusOK, `{"session":99}`)}
	a.send("@duncan fix the CI", "")
	<-entered
	before := loop.Ran()
	a.link.client = nil
	a.reset()
	// Sign-out puts the command in the draft of its session, and the drafts go
	// aside for the account that wrote them.
	account := accountKey{server: "https://example.org", user: "u1"}
	if got := a.composer.kept[account].drafts[7]; got != "@duncan fix the CI" || len(a.composer.pendingTasks) != 0 {
		t.Fatalf("sign-out lost the task's command: %q", got)
	}
	if a.composer.drafts[7] != "" {
		t.Fatalf("the command stayed in the composer after sign-out: %q", a.composer.drafts[7])
	}
	close(release)
	loop.RunUntil(t, func() bool { return loop.Ran() > before })
	a.reset()
	if got := a.composer.kept[account].drafts[7]; got != "@duncan fix the CI" {
		t.Fatalf("the command was restored twice: %q", got)
	}
	// The same account, signed in again, gets the command back once.
	a.link.client, _ = pocket.NewClient("https://example.org", "test")
	a.adoptAccount("u1")
	if a.composer.drafts[7] != "@duncan fix the CI" {
		t.Fatalf("the account signed in again did not get the command back: %q", a.composer.drafts[7])
	}
}

func TestLateAgentTaskDoesNotHijackNavigation(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	entered, release := make(chan struct{}), make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: startTaskHTTP(t, 7, entered, release, http.StatusOK, `{"session":99}`)}
	a.send("@duncan fix the CI", "")
	<-entered
	a.open(0)
	before := loop.Ran()
	close(release)
	loop.RunUntil(t, func() bool { return loop.Ran() > before })
	if a.current != 0 {
		t.Fatalf("a late task answer navigated to session %d", a.current)
	}
	if !slices.Contains(a.pendingToasts, "Duncan started in session 99") {
		t.Fatalf("the created session was not named: %v", a.pendingToasts)
	}
	if len(a.composer.pendingTasks) != 0 {
		t.Fatal("the finished task stayed pending")
	}
}

func TestAgentTaskOpensTheNewSessionWhenStillThere(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	view := a.view
	a.link.client.HTTP = &http.Client{Transport: startTaskHTTP(t, 7, nil, nil, http.StatusOK, `{"session":99}`)}
	a.send("@duncan fix the CI", "")
	loop.RunUntil(t, func() bool { return a.current == 99 })
	if a.view == view || a.composer.draft != "" || len(a.composer.pendingTasks) != 0 {
		t.Fatal("the started task did not open its session cleanly")
	}
}

// TestAgentTaskRetryIsANewCommand checks one idempotency key per command: a
// retry by the person sends another, and the command comes back only once.
func TestAgentTaskRetryIsANewCommand(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	a.composer.draft = "@duncan fix the CI"
	keys := make(chan string, 2)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/x/team/session/7/start" {
			return snapshotHTTP{}.RoundTrip(r)
		}
		var body struct {
			Key string `json:"key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		keys <- body.Key
		resp := reply(r, `{"error":"Try later"}`)
		resp.StatusCode = http.StatusServiceUnavailable
		return resp, nil
	})}
	for range 2 {
		a.send("@duncan fix the CI", "")
		loop.RunUntil(t, func() bool { return len(a.composer.pendingTasks) == 0 })
	}
	first, second := <-keys, <-keys
	if first == "" || first == second {
		t.Fatalf("the retry reused the idempotency key: %q %q", first, second)
	}
	if a.composer.draft != "@duncan fix the CI" {
		t.Fatalf("the retry did not recover the command once: %q", a.composer.draft)
	}
}

// TestLateAgentTaskDoesNotLeaveAnotherPlace checks that a task answer that
// comes after the person went to another place, with the same session still
// current, names the new session instead of opening it.
func TestLateAgentTaskDoesNotLeaveAnotherPlace(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.selected = "s:7"
	a.composer.reset(7)
	entered, release := make(chan struct{}), make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: startTaskHTTP(t, 7, entered, release, http.StatusOK, `{"session":99}`)}
	a.send("@duncan fix the CI", "")
	<-entered
	a.selected = "x:inbox"
	before := loop.Ran()
	close(release)
	loop.RunUntil(t, func() bool { return loop.Ran() > before })
	if a.current != 7 || a.selected != "x:inbox" {
		t.Fatalf("a late task answer left the Inbox: current %d, selected %q", a.current, a.selected)
	}
	if !slices.Contains(a.pendingToasts, "Duncan started in session 99") {
		t.Fatalf("the created session was not named: %v", a.pendingToasts)
	}
}

// TestAgentPageTaskOpensTheNewSession checks the agent page's task form: it
// starts a task from another session while the person stays on the page, so
// the new session opens even though no session is current.
func TestAgentPageTaskOpensTheNewSession(t *testing.T) {
	a, loop := teamApp(t)
	a.openAgent("duncan")
	p := a.team.agent("duncan")
	source := a.taskSource()
	if p == nil || source == 0 || a.current != 0 {
		t.Fatalf("agent page fixture: agent %v source %d current %d", p != nil, source, a.current)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: startTaskHTTP(t, source, entered, release, http.StatusOK, `{"session":99}`)}
	a.startAgentTask(source, p, "fix the CI", "", "")
	<-entered
	before := loop.Ran()
	close(release)
	loop.RunUntil(t, func() bool { return loop.Ran() > before })
	if a.current != 99 {
		t.Fatalf("the agent page's task did not open its session: current %d, toasts %v", a.current, a.pendingToasts)
	}
	if slices.Contains(a.pendingToasts, "Duncan started in session 99") {
		t.Fatal("the task opened nowhere and was only announced")
	}
}

// TestAgentPageTaskLateAnswerOnlyToasts checks that leaving the agent page
// before the answer keeps the person where they went.
func TestAgentPageTaskLateAnswerOnlyToasts(t *testing.T) {
	a, loop := teamApp(t)
	a.openAgent("duncan")
	p := a.team.agent("duncan")
	source := a.taskSource()
	entered, release := make(chan struct{}), make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: startTaskHTTP(t, source, entered, release, http.StatusOK, `{"session":99}`)}
	a.startAgentTask(source, p, "fix the CI", "", "")
	<-entered
	a.goPlace("x:inbox")
	before := loop.Ran()
	close(release)
	loop.RunUntil(t, func() bool { return loop.Ran() > before })
	if a.current != 0 || a.selected != "x:inbox" {
		t.Fatalf("a late task answer left the Inbox: current %d, selected %q", a.current, a.selected)
	}
	if !slices.Contains(a.pendingToasts, "Duncan started in session 99") {
		t.Fatalf("the created session was not named: %v", a.pendingToasts)
	}
}
