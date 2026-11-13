package app

import (
	"net/http"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
)

type deskFileTransport func(*http.Request) (*http.Response, error)

func (f deskFileTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDeskPathReferencesMatchWebRules(t *testing.T) {
	for _, path := range []string{"reports/2026-11-05-intake-go-live/502-check.md", "desk/T1.md", "plans/T560/plan.md", "decisions/next.md"} {
		if !deskPath(path) {
			t.Errorf("valid path rejected: %s", path)
		}
		for _, pair := range [][2]string{
			{"Read " + path + ".", "Read [" + path + "](deskfile:" + path + ")."},
			{"Read `" + path + "`.", "Read [`" + path + "`](deskfile:" + path + ")."},
			{"Read [the plan](" + path + ").", "Read [the plan](deskfile:" + path + ")."},
		} {
			if got := linkDeskPaths(pair[0]); got != pair[1] {
				t.Errorf("linkDeskPaths(%q)=%q, want %q", pair[0], got, pair[1])
			}
		}
	}
	for _, bad := range []string{"reports/../x.md", "/etc/passwd.md", "reports/.hidden.md", "reports/x.txt", "reports//x.md", "reports/x%2f..%2fx.md", "reports/" + strings.Repeat("a", 200) + ".md"} {
		if deskPath(bad) {
			t.Errorf("invalid path accepted: %s", bad)
		}
		for _, text := range []string{"See " + bad + " now", "See `" + bad + "` now", "See [it](" + bad + ") now"} {
			if linkDeskPaths(text) != text {
				t.Errorf("invalid reference rewritten: %q", text)
			}
		}
	}
	for _, text := range []string{"```\nreports/x.md\n```", "`cat reports/x.md`", "https://example.org/reports/x.md"} {
		if linkDeskPaths(text) != text {
			t.Errorf("code/URL rewritten: %q", text)
		}
	}
	linked := linkDeskPaths("reports/x.md")
	if linkDeskPaths(linked) != linked {
		t.Fatal("links are not idempotent")
	}
}

func TestDeskFileReadAndErrors(t *testing.T) {
	for _, status := range []int{200, 400, 404} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			a, loop := teamApp(t)
			a.link.client.HTTP = &http.Client{Transport: deskFileTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || r.URL.Path != "/api/x/desk/file" || r.URL.Query().Get("path") != "reports/x.md" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				resp := reply(r, `{"text":"# A report","error":"No file"}`)
				resp.StatusCode = status
				return resp, nil
			})}
			a.followLink("deskfile:reports/x.md")
			loop.RunUntil(t, func() bool { return !a.deskFile.loading })
			if status == 200 {
				if a.deskFile.text != "# A report" || !a.deskFile.open || a.deskFile.md.LoadImage != nil {
					t.Fatal("report not shown safely")
				}
			} else if a.deskFile.open || len(a.pendingToasts) == 0 {
				t.Fatal("file error did not close the reader and show a toast")
			}
		})
	}
}

func TestDeskFileRejectsInvalidPathsBeforeRequest(t *testing.T) {
	a, _ := teamApp(t)
	a.link.client.HTTP = &http.Client{Transport: deskFileTransport(func(r *http.Request) (*http.Response, error) {
		t.Errorf("invalid path sent to %s", r.URL)
		return reply(r, `{}`), nil
	})}
	a.followLink("deskfile:reports/../secret.md")
	if a.deskFile != nil || len(a.pendingToasts) != 1 {
		t.Fatal("invalid path reached the file reader")
	}
}

func TestDeskFileSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	a.deskFile = &deskFile{open: true, path: "reports/build/README.md", text: "# Build report\n\nAll checks passed.\n\n- Tests passed\n- Packages built\n\nRead plans/build.md for the plan."}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.deskFileDialog(c) }, 900, 650, ui.TesterOptions{})
	tt.Frame()
	for _, text := range []string{"Build report", "All checks passed.", "Close"} {
		if !tt.HasText(text) {
			t.Errorf("missing %q", text)
		}
	}
	writePNG(t, tt, "desk-file")
}
