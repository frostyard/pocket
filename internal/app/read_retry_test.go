package app

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// retryHTTP answers with what the test sets for a path, and counts the
// requests for each path, so a test can tell a redraw that reads again
// from one that keeps the failure it has.
type retryHTTP struct {
	mu    sync.Mutex
	paths map[string]int
	reply func(path string) (status int, body string)
}

func (h *retryHTTP) RoundTrip(r *http.Request) (*http.Response, error) {
	h.mu.Lock()
	if h.paths == nil {
		h.paths = map[string]int{}
	}
	h.paths[r.URL.Path]++
	answer := h.reply
	h.mu.Unlock()
	status, body := 200, "{}"
	if answer != nil {
		status, body = answer(r.URL.Path)
	}
	resp := reply(r, body)
	resp.StatusCode, resp.Status = status, fmt.Sprintf("%d %s", status, http.StatusText(status))
	return resp, nil
}

// reads counts the requests the test made for a path.
func (h *retryHTTP) reads(path string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.paths[path]
}

// answer makes the transport answer a path this way from now on.
func (h *retryHTTP) answer(fn func(path string) (int, string)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reply = fn
}

const goodSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20"><rect width="20" height="20" fill="red"/></svg>`

// TestSVGMalformedBodyReadsOnceUntilTryAgain checks that an SVG that does
// not parse is not read again on every redraw, that the preview says so,
// and that Try again reads it once more and draws it.
func TestSVGMalformedBodyReadsOnceUntilTryAgain(t *testing.T) {
	a, loop := teamApp(t)
	h := &retryHTTP{reply: func(string) (int, string) {
		return http.StatusOK, `<svg xmlns="http://www.w3.org/2000/svg"><rect`
	}}
	a.link.client.HTTP = &http.Client{Transport: h}
	a.arts = artifacts{sel: "7/pic", list: []pocket.Artifact{{Session: 7, ID: "pic", Title: "Picture", Type: "svg", Current: 1, Versions: 1}}}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.artifactPreview(c) }, 1000, 400, ui.TesterOptions{})
	// A frame waits for the read it started, so every cycle below is done
	// before the next one draws.
	frame := func() {
		tt.Frame()
		loop.RunUntil(t, func() bool { return !a.arts.fetching["7/pic@1"] })
	}
	frame()
	if n := h.reads("/a/7/pic/1"); n != 1 {
		t.Fatalf("the first preview read the body %d times", n)
	}
	for range 3 {
		frame()
	}
	if n := h.reads("/a/7/pic/1"); n != 1 {
		t.Fatalf("a redraw read the SVG that does not parse again: %d reads", n)
	}
	tt.Frame()
	if !tt.HasText("Try again") {
		t.Fatalf("the failed preview does not offer Try again: %v", tt.Texts())
	}
	h.answer(func(string) (int, string) { return http.StatusOK, goodSVG })
	if err := tt.Click("Try again"); err != nil {
		t.Fatal(err)
	}
	loop.RunUntil(t, func() bool { return !a.arts.fetching["7/pic@1"] })
	if n := h.reads("/a/7/pic/1"); n != 2 {
		t.Fatalf("Try again read the body %d times, want 2", n)
	}
	if a.arts.svgs["7/pic@1"] == nil {
		t.Fatal("Try again did not draw the SVG")
	}
	tt.Frame()
	if tt.HasText("Try again") {
		t.Fatal("the preview still shows the error after it recovered")
	}
}

// TestSVGServerErrorReadsOnceUntilTryAgain is TestSVGMalformedBodyReadsOnceUntilTryAgain
// for a body the server fails to send.
func TestSVGServerErrorReadsOnceUntilTryAgain(t *testing.T) {
	a, loop := teamApp(t)
	h := &retryHTTP{reply: func(string) (int, string) { return http.StatusServiceUnavailable, `{}` }}
	a.link.client.HTTP = &http.Client{Transport: h}
	a.arts = artifacts{sel: "7/pic", list: []pocket.Artifact{{Session: 7, ID: "pic", Title: "Picture", Type: "svg", Current: 1, Versions: 1}}}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.artifactPreview(c) }, 1000, 400, ui.TesterOptions{})
	frame := func() {
		tt.Frame()
		loop.RunUntil(t, func() bool { return !a.arts.fetching["7/pic@1"] })
	}
	frame()
	for range 3 {
		frame()
	}
	if n := h.reads("/a/7/pic/1"); n != 1 {
		t.Fatalf("a redraw read the SVG the server failed to send again: %d reads", n)
	}
	tt.Frame()
	if !tt.HasText("Try again") || !tt.HasText("the artifact answered 503") {
		t.Fatalf("the failed preview does not show the error: %v", tt.Texts())
	}
	h.answer(func(string) (int, string) { return http.StatusOK, goodSVG })
	if err := tt.Click("Try again"); err != nil {
		t.Fatal(err)
	}
	loop.RunUntil(t, func() bool { return !a.arts.fetching["7/pic@1"] })
	if n := h.reads("/a/7/pic/1"); n != 2 {
		t.Fatalf("Try again read the body %d times, want 2", n)
	}
	if a.arts.svgs["7/pic@1"] == nil {
		t.Fatal("Try again did not draw the SVG")
	}
}

// TestSVGNewVersionReadsAfterAFailure checks that a failure belongs to the
// key and version it was read for: another version reads at once.
func TestSVGNewVersionReadsAfterAFailure(t *testing.T) {
	a, loop := teamApp(t)
	h := &retryHTTP{reply: func(path string) (int, string) {
		if path == "/a/7/pic/2" {
			return http.StatusOK, goodSVG
		}
		return http.StatusServiceUnavailable, `{}`
	}}
	a.link.client.HTTP = &http.Client{Transport: h}
	a.arts = artifacts{sel: "7/pic", list: []pocket.Artifact{{Session: 7, ID: "pic", Title: "Picture", Type: "svg", Current: 2, Versions: 2}},
		versions: map[string]int{"7/pic": 1}}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.artifactPreview(c) }, 1000, 400, ui.TesterOptions{})
	tt.Frame()
	loop.RunUntil(t, func() bool { return !a.arts.fetching["7/pic@1"] })
	tt.Frame()
	if !tt.HasText("Try again") {
		t.Fatalf("the failed version does not show the error: %v", tt.Texts())
	}
	a.arts.versions["7/pic"] = 2
	tt.Frame()
	loop.RunUntil(t, func() bool { return !a.arts.fetching["7/pic@2"] })
	if n := h.reads("/a/7/pic/2"); n != 1 {
		t.Fatalf("another version read %d times, want 1", n)
	}
	if a.arts.svgs["7/pic@2"] == nil {
		t.Fatal("another version did not draw")
	}
	tt.Frame()
	if tt.HasText("Try again") {
		t.Fatal("another version kept the earlier version's error")
	}
}

// TestWikiFailureReadsOnceUntilTryAgain checks that a wiki the server
// fails to send is not read on every redraw, that the pane says so, and
// that Try again reads it again.
func TestWikiFailureReadsOnceUntilTryAgain(t *testing.T) {
	a, loop := teamApp(t)
	h := &retryHTTP{reply: func(string) (int, string) { return http.StatusServiceUnavailable, `{}` }}
	a.link.client.HTTP = &http.Client{Transport: h}
	a.wiki = wiki{}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.wikiPane(c) }, 1000, 400, ui.TesterOptions{})
	frame := func() {
		tt.Frame()
		loop.RunUntil(t, func() bool { return !a.wiki.loading })
	}
	frame()
	for range 3 {
		frame()
	}
	if n := h.reads("/api/x/homewiki/pages"); n != 1 {
		t.Fatalf("a redraw read the wiki again: %d reads", n)
	}
	tt.Frame()
	if !tt.HasText("Try again") || !tt.HasText("the server answered 503") {
		t.Fatalf("the failed wiki does not show the error: %v", tt.Texts())
	}
	h.answer(func(string) (int, string) {
		return http.StatusOK, `{"pages":[{"path":"README.md","title":"Homelab inventory"}]}`
	})
	if err := tt.Click("Try again"); err != nil {
		t.Fatal(err)
	}
	loop.RunUntil(t, func() bool { return !a.wiki.loading })
	if n := h.reads("/api/x/homewiki/pages"); n != 2 {
		t.Fatalf("Try again read the wiki %d times, want 2", n)
	}
	if a.wiki.data == nil {
		t.Fatal("Try again did not read the wiki")
	}
	tt.Frame()
	if !tt.HasText("Homelab inventory") || tt.HasText("Try again") {
		t.Fatalf("the wiki did not recover: %v", tt.Texts())
	}
}

// TestTeamFailureReadsOnceUntilTryAgain checks that the lessons the server
// fails to send are not read on every redraw, that the page says so, and
// that Try again reads them again.
func TestTeamFailureReadsOnceUntilTryAgain(t *testing.T) {
	a, loop := teamApp(t)
	h := &retryHTTP{reply: func(string) (int, string) { return http.StatusServiceUnavailable, `{}` }}
	a.link.client.HTTP = &http.Client{Transport: h}
	a.lessons = lessons{}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.teamPane(c) }, 1000, 800, ui.TesterOptions{})
	frame := func() {
		tt.Frame()
		loop.RunUntil(t, func() bool { return !a.lessons.loading })
	}
	frame()
	for range 3 {
		frame()
	}
	if n := h.reads("/api/x/team/memory"); n != 1 {
		t.Fatalf("a redraw read the lessons again: %d reads", n)
	}
	tt.Frame()
	if !tt.HasText("Try again") || !tt.HasText("the server answered 503") {
		t.Fatalf("the failed read does not show the error: %v", tt.Texts())
	}
	h.answer(func(string) (int, string) {
		return http.StatusOK, `{"agents":{"duncan":[{"id":"l1","text":"Run make ci first."}]},"limits":{"approved":40}}`
	})
	if err := tt.Click("Try again"); err != nil {
		t.Fatal(err)
	}
	loop.RunUntil(t, func() bool { return !a.lessons.loading })
	if n := h.reads("/api/x/team/memory"); n != 2 {
		t.Fatalf("Try again read the lessons %d times, want 2", n)
	}
	if a.lessons.memory == nil {
		t.Fatal("Try again did not read the lessons")
	}
	tt.Frame()
	if tt.HasText("Try again") {
		t.Fatal("the page still shows the error after it recovered")
	}
}
