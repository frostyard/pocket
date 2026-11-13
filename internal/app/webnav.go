package app

import (
	"net/url"
	"strings"
)

// Navigation is what the artifact web view does with a URL its page goes to.
type Navigation int

const (
	// NavigationCancel drops the URL: the web view stays where it is.
	NavigationCancel Navigation = iota
	// NavigationAllow loads the URL in the web view.
	NavigationAllow
	// NavigationExternal cancels it in the web view and opens the URL in
	// the person's browser instead.
	NavigationExternal
)

// ArtifactNavigation decides what the artifact web view does with target: a
// URL its page navigates to, by a click when user is set, or by a script.
//
// The web view holds the owner's sign-in cookie, so it may load only the
// server's /a/ pages, the ones the server serves in a sandbox under its own
// CSP, and about:blank. Any other page of the server would load signed in
// as the owner: the whole app, with its API. A link to another site, or to
// another page of the server, opens in the browser when a person asked for
// it; a script cannot open one.
func ArtifactNavigation(server, target string, user bool) Navigation {
	if strings.EqualFold(target, "about:blank") {
		return NavigationAllow
	}
	u, err := url.Parse(target)
	if err != nil {
		return NavigationCancel
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return NavigationCancel
	}
	if isArtifactPage(server, u) {
		return NavigationAllow
	}
	if user {
		return NavigationExternal
	}
	return NavigationCancel
}

// ArtifactPopup reports whether a window the artifact page asks for, by
// window.open() or a target=_blank link, opens in the person's browser. It
// is true only for a web address a person's own gesture asked for: a script
// must not open windows on the person's desktop. A popup never opens
// another window in the app.
func ArtifactPopup(target string, user bool) bool {
	if !user {
		return false
	}
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return scheme == "http" || scheme == "https"
}

// isArtifactPage reports whether u is one of the server's /a/ pages: the
// same origin as server, a path under /a/, and no way around it.
func isArtifactPage(server string, u *url.URL) bool {
	if server == "" {
		return false
	}
	s, err := url.Parse(server)
	if err != nil || s.Host == "" {
		return false
	}
	if !strings.EqualFold(s.Scheme, u.Scheme) || !strings.EqualFold(s.Hostname(), u.Hostname()) {
		return false
	}
	if effectivePort(s) != effectivePort(u) {
		return false
	}
	// A URL that names a person is not one the app's own pages use.
	if u.User != nil {
		return false
	}
	// Both the decoded path and the escaped one must be under /a/: a
	// percent-encoded slash or a dot segment must not make another page of
	// the server pass for an artifact.
	if !strings.HasPrefix(u.Path, "/a/") || !strings.HasPrefix(u.EscapedPath(), "/a/") {
		return false
	}
	if strings.ContainsRune(u.Path, '\\') {
		return false
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// effectivePort is u's port, or the one its scheme means when it names none.
func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	}
	return ""
}
