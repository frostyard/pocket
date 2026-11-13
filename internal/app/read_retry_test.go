package app

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// TestAgentLessonsKeepTheErrorAfterAFailedRefresh checks that a failed
// refresh of the lessons shows on an agent's page above the lessons read
// before it, and that Try again clears the error and reads again.
func TestAgentLessonsKeepTheErrorAfterAFailedRefresh(t *testing.T) {
	a, loop := teamApp(t)
	h := &retryHTTP{reply: func(string) (int, string) {
		return http.StatusOK, `{"agents":{"duncan":[{"id":"l1","text":"Run make ci first."}]},"limits":{"approved":40}}`
	}}
	a.link.client.HTTP = &http.Client{Transport: h}
	a.lessons = lessons{}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.agentLessons(c, "duncan") }, 1000, 800, ui.TesterOptions{})
	tt.Frame()
	loop.RunUntil(t, func() bool { return !a.lessons.loading })
	if a.lessons.memory == nil {
		t.Fatal("the lessons did not load")
	}
	h.answer(func(string) (int, string) { return http.StatusServiceUnavailable, `{}` })
	a.loadMemory()
	loop.RunUntil(t, func() bool { return !a.lessons.loading })
	tt.Frame()
	if !tt.HasText("Try again") || !tt.HasText("the server answered 503") {
		t.Fatalf("an agent's page does not show the failed refresh: %v", tt.Texts())
	}
	if !tt.HasText("Run make ci first.") {
		t.Fatalf("an agent's page hides the lessons read before the failed refresh: %v", tt.Texts())
	}
	h.answer(func(string) (int, string) {
		return http.StatusOK, `{"agents":{"duncan":[{"id":"l1","text":"Run make ci first."},{"id":"l2","text":"Use the arm64 runner."}]},"limits":{"approved":40}}`
	})
	if err := tt.Click("Try again"); err != nil {
		t.Fatal(err)
	}
	loop.RunUntil(t, func() bool { return !a.lessons.loading })
	tt.Frame()
	if tt.HasText("Try again") {
		t.Fatalf("the error stayed after the read recovered: %v", tt.Texts())
	}
	if !tt.HasText("Use the arm64 runner.") {
		t.Fatalf("Try again did not read the lessons: %v", tt.Texts())
	}
}

// TestOverlappingWikiPageReadsKeepTheNewestAnswer checks that an older read
// of a page that fails after a newer one started leaves the page shown
// whole, with no error over it.
func TestOverlappingWikiPageReadsKeepTheNewestAnswer(t *testing.T) {
	a, loop := teamApp(t)
	var calls atomic.Int64
	started := make(chan struct{})
	release := make(chan struct{})
	h := &retryHTTP{reply: func(path string) (int, string) {
		if !strings.HasPrefix(path, "/api/x/homewiki/page") {
			return http.StatusOK, "{}"
		}
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return http.StatusServiceUnavailable, `{}`
		}
		return http.StatusOK, `{"path":"docs/a.md","title":"A","body":"Newest body"}`
	}}
	a.link.client.HTTP = &http.Client{Transport: h}
	a.wiki = wiki{}
	a.readWikiPage("docs/a.md")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first read of the page never started")
	}
	a.readWikiPage("docs/a.md")
	loop.RunUntil(t, func() bool { return a.wiki.page != nil })
	before := loop.Ran()
	close(release)
	// Wait for the older read's answer to reach the UI thread.
	loop.RunUntil(t, func() bool { return loop.Ran() > before })
	if a.wiki.pageErr != "" {
		t.Fatalf("the older read's failure shows over the page: %q", a.wiki.pageErr)
	}
	if a.wiki.page == nil || a.wiki.page.Body != "Newest body" {
		t.Fatalf("the newest read did not show: %+v", a.wiki.page)
	}
	if a.wiki.reading {
		t.Fatal("a read that ended left the pane reading")
	}
	a.wiki.data = &pocket.Wiki{Pages: []pocket.WikiPage{{Path: "docs/a.md", Title: "A"}}}
	a.wiki.sel = "docs/a.md"
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.wikiPane(c) }, 1000, 400, ui.TesterOptions{})
	tt.Frame()
	if tt.HasText("Try again") || !tt.HasText("Newest body") {
		t.Fatalf("the page stays behind the older error: %v", tt.Texts())
	}
}

// TestArtifactRefreshClearsAFailedSVG checks that the person's own refresh
// of the artifacts lets the same version of a failed SVG read again and
// draw.
func TestArtifactRefreshClearsAFailedSVG(t *testing.T) {
	a, loop := teamApp(t)
	const list = `[{"session":7,"sessionTitle":"Task","id":"pic","title":"Picture","type":"svg","current":1,"versions":1,"size":20}]`
	h := &retryHTTP{reply: func(path string) (int, string) {
		if path == "/a/7/pic/1" {
			return http.StatusServiceUnavailable, `{}`
		}
		return http.StatusOK, list
	}}
	a.link.client.HTTP = &http.Client{Transport: h}
	a.arts = artifacts{loaded: true, sel: "7/pic", list: []pocket.Artifact{
		{Session: 7, SessionTitle: "Task", ID: "pic", Title: "Picture", Type: "svg", Current: 1, Versions: 1, Size: 20},
	}}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.artifactsPane(c) }, 1200, 500, ui.TesterOptions{})
	tt.Frame()
	loop.RunUntil(t, func() bool { return !a.arts.fetching["7/pic@1"] })
	tt.Frame()
	if !tt.HasText("Try again") {
		t.Fatalf("the failed SVG does not show the error: %v", tt.Texts())
	}
	if n := h.reads("/a/7/pic/1"); n != 1 {
		t.Fatalf("a redraw read the failed SVG again: %d reads", n)
	}
	h.answer(func(path string) (int, string) {
		if path == "/a/7/pic/1" {
			return http.StatusOK, goodSVG
		}
		return http.StatusOK, list
	})
	if err := tt.Click("Read the artifacts again"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	loop.RunUntil(t, func() bool { return !a.arts.loading })
	loop.RunUntil(t, func() bool { return !a.arts.fetching["7/pic@1"] })
	if n := h.reads("/a/7/pic/1"); n != 2 {
		t.Fatalf("the refresh read the failed SVG %d times, want 2", n)
	}
	if a.arts.svgs["7/pic@1"] == nil {
		t.Fatal("the refresh did not draw the SVG")
	}
}

// TestArtifactRefreshDuringAnSVGReadKeepsItsFailure checks that the person's
// refresh while an SVG read is under way leaves a map that read can write its
// failure into.
func TestArtifactRefreshDuringAnSVGReadKeepsItsFailure(t *testing.T) {
	a, loop := teamApp(t)
	const list = `[{"session":7,"sessionTitle":"Task","id":"pic","title":"Picture","type":"svg","current":1,"versions":1,"size":20}]`
	release := make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/a/7/pic/1" {
			<-release
			resp := reply(r, `{}`)
			resp.StatusCode, resp.Status = http.StatusServiceUnavailable, "503 Service Unavailable"
			return resp, nil
		}
		return reply(r, list), nil
	})}
	x := &pocket.Artifact{Session: 7, SessionTitle: "Task", ID: "pic", Title: "Picture", Type: "svg", Current: 1, Versions: 1, Size: 20}
	a.readSVG(x, svgKey(x))
	a.refreshArtifacts()
	close(release)
	loop.RunUntil(t, func() bool { return !a.arts.fetching[svgKey(x)] && !a.arts.loading })
	if a.arts.svgErr[svgKey(x)] == "" {
		t.Fatal("the read under way lost its failure")
	}
}
