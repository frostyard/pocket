package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

func TestChangesDiffRevertConfirmationAndDownload(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	reverted := make(chan string, 1)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/c/7/prompts":
			return reply(r, `[]`), nil
		case "/api/c/7/changes":
			return reply(r, `{"repo":{"root":"/workspace","branch":"main"},"files":[{"path":"a & b.go","kind":"modified","added":1,"removed":1,"byPi":true}]}`), nil
		case "/api/c/7/changes/diff":
			return reply(r, "--- a/example.go\n+++ b/example.go\n@@ -1 +1 @@\n-old\n+new\n"), nil
		case "/api/c/7/view":
			if r.URL.Query().Get("path") != "/workspace/a & b.go" {
				t.Error(r.URL)
			}
			return reply(r, `{"path":"/workspace/a & b.go","kind":"text","text":"new\n","size":4}`), nil
		case "/api/c/7/download":
			return reply(r, "new\n"), nil
		case "/api/c/7/changes/revert":
			var body struct {
				Path string `json:"path"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			reverted <- body.Path
			return reply(r, `{}`), nil
		default:
			t.Errorf("unexpected %s", r.URL)
			return reply(r, `{}`), nil
		}
	})}
	a.opts.SaveFile = func(_ context.Context, name string, data []byte) (bool, error) {
		if name != "a & b.go" || string(data) != "new\n" {
			t.Errorf("download %q %q", name, data)
		}
		return true, nil
	}
	a.openSessionFiles()
	loop.RunUntil(t, func() bool { return !a.files.loading })
	tt := ui.NewTesterWith(a.View, 1100, 800, ui.TesterOptions{})
	tt.Frame()
	if err := tt.Click("Review a & b.go"); err != nil {
		t.Fatal(err)
	}
	loop.RunUntil(t, func() bool { return !a.files.previewing })
	tt.Frame()
	if !tt.HasText("+new") {
		t.Fatal("native diff missing")
	}
	writePNG(t, tt, "session-changes")
	if err := tt.Click("Revert…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	select {
	case <-reverted:
		t.Fatal("reverted without confirmation")
	default:
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("View file"); err != nil {
		t.Fatal(err)
	}
	loop.RunUntil(t, func() bool { return !a.files.previewing })
	tt.Frame()
	if err := tt.Click("Save a copy…"); err != nil {
		t.Fatal(err)
	}
	loop.RunUntil(t, func() bool { return !a.files.busy })
	tt.Frame()
	if err := tt.Click("Revert…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Revert file"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	loop.RunUntil(t, func() bool { return !a.files.busy && !a.files.loading })
	select {
	case path := <-reverted:
		if path != "a & b.go" {
			t.Fatal(path)
		}
	default:
		t.Fatal("confirmed revert not sent")
	}
	if a.files.selected != "" {
		t.Fatal("reverted preview remained")
	}
}
func TestFilePreviewIgnoresOldSelection(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.files = &sessionFiles{session: 7, open: true}
	entered, release := make(chan struct{}), make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("path") == "old" {
			close(entered)
			<-release
			return reply(r, `{"kind":"text","text":"old"}`), nil
		}
		return reply(r, `{"kind":"text","text":"new"}`), nil
	})}
	a.previewSessionFile("old", "old", false)
	<-entered
	a.previewSessionFile("new", "new", false)
	loop.RunUntil(t, func() bool { return !a.files.previewing })
	before := loop.Ran()
	close(release)
	loop.RunUntil(t, func() bool { return loop.Ran() > before })
	if a.files.preview.Text != "new" {
		t.Fatal("stale response replaced selection")
	}
	a.files = nil
	a.hello.User.Role = "viewer"
	a.openSessionFiles()
	if a.files != nil {
		t.Fatal("viewer opened files")
	}
}
func TestFileListSamePreservesPathsAndFailedRevert(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.files = &sessionFiles{session: 7, open: true, tab: "Files", files: &pocket.FileList{Version: "v1", Files: []string{"keep.txt"}}, selected: "keep.txt"}
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/files") {
			if r.URL.Query().Get("since") != "v1" {
				t.Error(r.URL)
			}
			return reply(r, `{"version":"v1","same":true}`), nil
		}
		resp := reply(r, `{"error":"Pi is working here"}`)
		resp.StatusCode = 409
		return resp, nil
	})}
	a.loadSessionFiles()
	loop.RunUntil(t, func() bool { return !a.files.loading })
	if len(a.files.files.Files) != 1 {
		t.Fatal("same response lost list")
	}
	a.confirmRevert(pocket.ChangedFile{Path: "keep.txt", Kind: "modified"})
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Box(c).Fill(); a.dialogs.View(c) }, 800, 500, ui.TesterOptions{})
	tt.Frame()
	if err := tt.Click("Revert file"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	loop.RunUntil(t, func() bool { return !a.files.busy })
	if a.files.selected != "keep.txt" || !strings.Contains(a.files.err, "Pi is working") {
		t.Fatal("revert failure lost preview or reason")
	}
}
func TestFileConfirmationCannotCrossSession(t *testing.T) {
	a, _ := teamApp(t)
	a.current = 7
	a.files = &sessionFiles{session: 7, open: true}
	a.confirmRevert(pocket.ChangedFile{Path: "new.txt", Kind: "new"})
	a.current = 8
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Box(c).Fill(); a.dialogs.View(c) }, 800, 500, ui.TesterOptions{})
	tt.Frame()
	if err := tt.Click("Revert file"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.files.busy {
		t.Fatal("confirmation acted after navigation")
	}
}
