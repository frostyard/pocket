package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui/frostuitest"

	"github.com/frostyard/pocket/internal/config"
	"github.com/frostyard/pocket/internal/pocket"
)

// TestArtifactNavigation is the guard of frostyard/pocket#11, gap 1: the
// artifact web view holds the owner's sign-in cookie, so it may load only
// the server's /a/ pages and about:blank. Anything else is dropped, or
// opened in the person's browser when a person asked for it.
func TestArtifactNavigation(t *testing.T) {
	const server = "https://example.org"
	allowed := []string{
		"https://example.org/a/1/abc/2",
		"https://EXAMPLE.org/a/1/x/1",
		"https://example.org:443/a/1/x/1",
		"about:blank",
	}
	for _, target := range allowed {
		if got := ArtifactNavigation(server, target, true); got != NavigationAllow {
			t.Errorf("ArtifactNavigation(%q, %q, true) = %v, want Allow", server, target, got)
		}
	}
	refused := []string{
		// The rest of the server loads signed in as the owner.
		"https://example.org/",
		"https://example.org/api/sessions",
		"https://example.org/a",
		// Ways around the /a/ prefix.
		"https://example.org/a/../api/x",
		"https://example.org/a/%2e%2e/api",
		"https://example.org/%61/1/x/1",
		"https://user@example.org/a/1/x/1",
		// Another origin, another scheme, another port.
		"http://example.org/a/1/x/1",
		"https://example.org:8443/a/1/x/1",
		"https://evil.example.net/a/1/x/1",
		// Other schemes never reach the page.
		"javascript:alert(1)",
		"file:///etc/passwd",
		"data:text/html,hi",
	}
	for _, target := range refused {
		if got := ArtifactNavigation(server, target, true); got == NavigationAllow {
			t.Errorf("ArtifactNavigation(%q, %q, true) = Allow, want it refused", server, target)
		}
	}
}

// TestArtifactNavigationExactResult pins what the web view does with a URL
// it refuses: a person's own gesture opens it in the browser, a script's
// does nothing.
func TestArtifactNavigationExactResult(t *testing.T) {
	for _, tc := range []struct {
		name, server, target string
		user                 bool
		want                 Navigation
	}{
		{"a server page a person clicked", "https://example.org", "https://example.org/", true, NavigationExternal},
		{"a server page a script went to", "https://example.org", "https://example.org/", false, NavigationCancel},
		{"another site a person clicked", "https://example.org", "https://evil.example.net/a/1/x/1", true, NavigationExternal},
		{"another site a script went to", "https://example.org", "https://evil.example.net/a/1/x/1", false, NavigationCancel},
		{"javascript from a click", "https://example.org", "javascript:alert(1)", true, NavigationCancel},
		{"a file from a click", "https://example.org", "file:///etc/passwd", true, NavigationCancel},
		{"data from a click", "https://example.org", "data:text/html,hi", true, NavigationCancel},
		{"an artifact page", "https://example.org", "https://example.org/a/1/x/1", false, NavigationAllow},
		{"about:blank when signed out", "", "about:blank", false, NavigationAllow},
		{"an artifact page when signed out", "", "https://example.org/a/1/x/1", true, NavigationExternal},
		{"an artifact page of an http server", "http://192.0.2.7:8080", "http://192.0.2.7:8080/a/1/x/1", true, NavigationAllow},
		{"the same server without its port", "http://192.0.2.7:8080", "http://192.0.2.7/a/1/x/1", true, NavigationExternal},
		{"a URL the view cannot read", "https://example.org", "https://example.org:port/a/1/x/1", true, NavigationCancel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ArtifactNavigation(tc.server, tc.target, tc.user); got != tc.want {
				t.Errorf("ArtifactNavigation(%q, %q, %v) = %v, want %v", tc.server, tc.target, tc.user, got, tc.want)
			}
		})
	}
}

// TestArtifactPopup checks that only a web address a person asked for goes
// to the browser, and that no popup ever opens in the app.
func TestArtifactPopup(t *testing.T) {
	for _, tc := range []struct {
		target string
		user   bool
		want   bool
	}{
		{"https://example.org/", true, true},
		{"http://example.org/x", true, true},
		{"https://example.org/", false, false},
		{"javascript:alert(1)", true, false},
		{"file:///etc/passwd", true, false},
		{"mailto:someone@example.org", true, false},
		{"about:blank", true, false},
		{"https://example.org:port/", true, false},
	} {
		if got := ArtifactPopup(tc.target, tc.user); got != tc.want {
			t.Errorf("ArtifactPopup(%q, %v) = %v, want %v", tc.target, tc.user, got, tc.want)
		}
	}
}

// fakeWeb is the artifact web view without one: it records what the app
// asks of it.
type fakeWeb struct {
	shows int
	signs int
}

func (f *fakeWeb) View() ui.NativeWebView { return nil }

func (f *fakeWeb) Show(_, _, _ string) error {
	f.shows++
	return nil
}

func (f *fakeWeb) SignOut() { f.signs++ }

// TestSignOutBlanksTheWebView is the guard of frostyard/pocket#11, gap 2: a
// sign-in cookie the web view set does not outlive the sign-out, and the
// next preview shows its page again.
func TestSignOutBlanksTheWebView(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	loop := frostuitest.NewLoop()
	web := &fakeWeb{}
	a, err := New(Options{Post: loop.Post, Store: store, Creds: config.Credentials{Dir: store.Dir()}, Version: "test", Web: web})
	if err != nil {
		t.Fatal(err)
	}
	a.arts.shown = "https://example.org/a/1/x/1"
	loop.Post(func() { a.signedOut("") })
	loop.Flush()
	if web.signs != 1 {
		t.Errorf("SignOut ran %d times, want once", web.signs)
	}
	if a.arts.shown != "" {
		t.Errorf("the artifact page is still the shown one: %q", a.arts.shown)
	}
}

// TestSameServer checks that a reconnect to the server already shown keeps
// the artifact page, and that a sign-in to another server does not.
func TestSameServer(t *testing.T) {
	at := func(server string) *pocket.Client {
		c, err := pocket.NewClient(server, "t")
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if sameServer(nil, at("https://example.org")) {
		t.Error("a first sign-in is not the same server")
	}
	if sameServer(at("https://example.org"), nil) {
		t.Error("no server after one is not the same server")
	}
	if !sameServer(at("https://example.org"), at("https://example.org")) {
		t.Error("a reconnect to the same server must keep the page")
	}
	if sameServer(at("https://example.org"), at("https://other.example.net")) {
		t.Error("another server must sign the web view out")
	}
}

// TestArtifactCookie is the guard of the cross-review of frostyard/pocket#34,
// finding 1: a navigation that the web view's hook does not see (a reload
// after history.replaceState, which Chromium allows a sandboxed page) must
// not reach the rest of the server signed in. The cookie goes to the
// server's host and its /a/ pages alone.
func TestArtifactCookie(t *testing.T) {
	for _, tc := range []struct {
		server string
		want   WebCookie
	}{
		{"https://example.org", WebCookie{Name: pocket.CookieName, Value: "tok", Domain: "example.org", Path: "/a/", Secure: true}},
		{"http://192.0.2.7:8080", WebCookie{Name: pocket.CookieName, Value: "tok", Domain: "192.0.2.7", Path: "/a/"}},
	} {
		got, err := ArtifactCookie(tc.server, "tok")
		if err != nil {
			t.Fatalf("ArtifactCookie(%q): %v", tc.server, err)
		}
		if got != tc.want {
			t.Errorf("ArtifactCookie(%q) = %+v, want %+v", tc.server, got, tc.want)
		}
		if !ArtifactPageAllowed(tc.server, tc.server+got.Path+"1/x/1") {
			t.Errorf("the cookie's path %q is not under the pages the view loads", got.Path)
		}
	}
	if _, err := ArtifactCookie("", "tok"); err == nil {
		t.Error("ArtifactCookie with no server must fail")
	}
}

// TestArtifactPageAllowed checks the rule the program applies once a
// navigation committed: what the hook did not see is blanked unless it is
// an artifact page or about:blank.
func TestArtifactPageAllowed(t *testing.T) {
	const server = "https://example.org"
	for _, tc := range []struct {
		target string
		want   bool
	}{
		{"https://example.org/a/1/x/1", true},
		{"about:blank", true},
		{"https://example.org/", false},
		{"https://example.org/api/sessions", false},
		{"https://evil.example.net/", false},
	} {
		if got := ArtifactPageAllowed(server, tc.target); got != tc.want {
			t.Errorf("ArtifactPageAllowed(%q, %q) = %v, want %v", server, tc.target, got, tc.want)
		}
	}
}
