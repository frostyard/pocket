package app

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type memoryTransport func(*http.Request) (*http.Response, error)

func (f memoryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCoreMemoryUsesPrivatePipAndMemoryEvents(t *testing.T) {
	a, loop := teamApp(t)
	a.hello.Server.Extensions = []string{"pocket-memory"}
	var mu sync.Mutex
	paths := []string{}
	a.link.client.HTTP = &http.Client{Transport: memoryTransport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		paths = append(paths, r.URL.RequestURI())
		mu.Unlock()
		return reply(r, `{"pendingAll":true,"lessons":[{"id":"l1","text":"Run checks","pending":true}]}`), nil
	})}
	a.loadCoreMemory()
	if a.core != nil {
		t.Fatal("ordinary session chosen without private PiP")
	}
	a.handle(pocket.Event{Name: "pip", Data: []byte(`{"id":77}`)})
	loop.RunUntil(t, func() bool { return !a.core.loading })
	a.handle(pocket.Event{Name: "memory", Data: []byte(`{}`)})
	loop.RunUntil(t, func() bool { return !a.core.loading })
	if a.core.data == nil || len(a.core.data.Lessons) != 1 {
		t.Fatal("pending memory missing")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 {
		t.Fatalf("reads %v", paths)
	}
	for _, path := range paths {
		if path != "/api/c/77/memory?pending=all" {
			t.Errorf("wrong source/query: %s", path)
		}
	}
	a.handle(pocket.Event{Name: "pip", Data: []byte(`null`)})
	if a.core != nil {
		t.Fatal("removed PiP retained pending lessons")
	}
}

func TestCoreMemoryRejectsOlderScopedResponse(t *testing.T) {
	a, loop := teamApp(t)
	a.hello.Server.Extensions = []string{"pocket-memory"}
	a.link.client.HTTP = &http.Client{Transport: memoryTransport(func(r *http.Request) (*http.Response, error) {
		return reply(r, `{"lessons":[{"id":"l1","pending":true}]}`), nil
	})}
	a.handle(pocket.Event{Name: "pip", Data: []byte(`{"id":77}`)})
	loop.RunUntil(t, func() bool { return !a.core.loading })
	if a.core.data != nil || a.core.err == "" {
		t.Fatal("scoped response was presented as the global queue")
	}
}

func TestCoreMemoryApprovalSendsSeenProposalAndRefreshes(t *testing.T) {
	a, loop := teamApp(t)
	a.hello.Server.Extensions = []string{"pocket-memory"}
	a.core = &coreMemory{pip: 77}
	proposed := "Run the full gate"
	lesson := pocket.CoreLesson{ID: "lesson", Text: "Old text"}
	lesson.Proposal = &struct {
		Text   *string `json:"text"`
		Forget bool    `json:"forget"`
	}{Text: &proposed}
	requests := make(chan map[string]any, 1)
	a.link.client.HTTP = &http.Client{Transport: memoryTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			if r.URL.RequestURI() != "/api/c/77/memory/lesson/approve?pending=all" {
				t.Errorf("approval route: %s", r.URL)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				return nil, err
			}
			requests <- body
			resp := reply(r, `{"error":"changed"}`)
			resp.StatusCode = 409
			return resp, nil
		}
		return reply(r, `{"pendingAll":true,"lessons":[]}`), nil
	})}
	a.reviewCoreLesson(&lesson, "approve")
	loop.RunUntil(t, func() bool { return a.core.loaded && !a.core.loading })
	if body := <-requests; body["text"] != proposed {
		t.Fatalf("approval did not carry shown text: %v", body)
	}
	if len(a.pendingToasts) != 1 || a.core.busy[lesson.ID] {
		t.Fatal("stale approval did not release busy state and report failure")
	}
}

func TestCoreMemoryInboxSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	a.hello.Server.Extensions = []string{"pocket-memory"}
	a.sessions = nil
	a.lessons = lessons{}
	a.desk = desk{}
	a.core = &coreMemory{pip: 77, data: &pocket.CoreMemory{PendingAll: true, Lessons: []pocket.CoreLesson{{ID: "lesson", Text: "Run the full verification gate before opening a PR.", Scope: "project", ProjectName: "Example project", Pending: true}}}}
	if a.inboxCount() != 0 {
		t.Fatal("memory lessons should not count toward the urgent badge")
	}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.inboxPane(c) }, 800, 480, ui.TesterOptions{})
	tt.Frame()
	for _, text := range []string{"Lessons to review", "Pi Pocket memory", "Example project", "Approve", "Discard"} {
		if !tt.HasText(text) {
			t.Errorf("missing %q", text)
		}
	}
	writePNG(t, tt, "core-memory-inbox")
}
