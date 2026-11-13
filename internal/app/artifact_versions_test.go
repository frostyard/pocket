package app

import (
	"net/http"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type versionWeb struct{ urls []string }

func (w *versionWeb) View() ui.NativeWebView      { return nil }
func (w *versionWeb) Show(url, _, _ string) error { w.urls = append(w.urls, url); return nil }
func (w *versionWeb) SignOut()                    {}

func TestArtifactVersionFollowsLatestOrStaysPinnedSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	w := &versionWeb{}
	a.opts.Web = w
	a.arts = artifacts{sel: "7/page", list: []pocket.Artifact{{Session: 7, ID: "page", Title: "Build report", Type: "html", Current: 3, Versions: 3}}, versions: map[string]int{"7/page": 2}}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.artifactPreview(c) }, 1000, 400, ui.TesterOptions{})
	tt.Frame()
	if len(w.urls) != 1 || !strings.HasSuffix(w.urls[0], "/a/7/page/2") || !tt.HasText("Version 2") {
		t.Fatalf("wrong pinned preview: %v, %v", w.urls, tt.Texts())
	}
	writePNG(t, tt, "artifact-version")
	a.arts.list[0].Current = 4
	a.arts.list[0].Versions = 4
	tt.Frame()
	if len(w.urls) != 1 {
		t.Fatal("refresh moved a pinned version")
	}
	a.arts.versions["7/page"] = 0
	tt.Frame()
	if len(w.urls) != 2 || !strings.HasSuffix(w.urls[1], "/a/7/page/4") {
		t.Fatalf("latest did not follow refresh: %v", w.urls)
	}
	a.arts.sel = "8/page"
	a.arts.list = append(a.arts.list, pocket.Artifact{Session: 8, ID: "page", Title: "Other session", Type: "html", Current: 1, Versions: 1})
	tt.Frame()
	if !strings.HasSuffix(w.urls[len(w.urls)-1], "/a/8/page/1") {
		t.Fatal("version selection crossed sessions")
	}
}

type versionHTTP func(*http.Request) (*http.Response, error)

func (f versionHTTP) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSVGVersionReadsSelectedBodyAndCachesSeparately(t *testing.T) {
	a, loop := teamApp(t)
	paths := make(chan string, 2)
	a.link.client.HTTP = &http.Client{Transport: versionHTTP(func(r *http.Request) (*http.Response, error) {
		paths <- r.URL.Path
		return reply(r, `<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20"><rect width="20" height="20" fill="red"/></svg>`), nil
	})}
	a.arts = artifacts{sel: "7/pic", list: []pocket.Artifact{{Session: 7, ID: "pic", Title: "Picture", Type: "svg", Current: 2, Versions: 2}}, versions: map[string]int{"7/pic": 1}}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.artifactPreview(c) }, 1000, 400, ui.TesterOptions{})
	tt.Frame()
	loop.RunUntil(t, func() bool { return a.arts.svgs["7/pic@1"] != nil })
	a.arts.versions["7/pic"] = 2
	tt.Frame()
	loop.RunUntil(t, func() bool { return a.arts.svgs["7/pic@2"] != nil })
	if p := <-paths; p != "/a/7/pic/1" {
		t.Fatalf("first path %s", p)
	}
	if p := <-paths; p != "/a/7/pic/2" {
		t.Fatalf("second path %s", p)
	}
	if len(a.arts.svgs) != 2 {
		t.Fatal("versions share a cache entry")
	}
}
