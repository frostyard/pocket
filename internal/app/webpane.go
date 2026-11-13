package app

import (
	"net/url"
	"sync"

	"github.com/frostyard/pocket/internal/pocket"
)

// WebNative is the system's web view as the artifact pane uses it. Each
// call blocks until the native side is done, so ArtifactWeb makes them off
// the UI thread.
type WebNative interface {
	// SetCookie sets c, HttpOnly and SameSite=Lax.
	SetCookie(c WebCookie) error
	// DeleteCookie deletes the cookie of c's name, domain and path.
	DeleteCookie(c WebCookie) error
	// Load loads url in the web view.
	Load(url string) error
}

// ArtifactWeb is what the artifact web view does, apart from the view
// itself: it signs the view in and out, decides where its page may go, and
// makes the native calls in order, off the UI thread. A sign-out must not
// land after a later Show, and a blank for a page that committed outside
// the rule must not replace a newer Show.
type ArtifactWeb struct {
	native WebNative
	warn   func(what string, err error)

	mu sync.Mutex
	// server is the server the view is signed in to, or "" when signed
	// out.
	server string
	// hosts are the hostnames that got the sign-in cookie.
	hosts map[string]bool
	// last is the tail of the chain of native work: see do.
	last chan struct{}
	// loads counts the loads the chain made.
	loads uint64
}

// NewArtifactWeb is the pane's logic over native; warn logs a native call
// that failed, on any goroutine.
func NewArtifactWeb(native WebNative, warn func(what string, err error)) *ArtifactWeb {
	return &ArtifactWeb{native: native, warn: warn}
}

// Show sets the sign-in cookie for the server's host and its /a/ pages
// alone (ArtifactCookie), then loads page.
func (w *ArtifactWeb) Show(page, server, token string) error {
	u, err := url.Parse(server)
	if err != nil {
		return err
	}
	c, err := ArtifactCookie(server, token)
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.server = u.Scheme + "://" + u.Host
	if w.hosts == nil {
		w.hosts = map[string]bool{}
	}
	w.hosts[c.Domain] = true
	w.mu.Unlock()
	w.do(func() {
		if err := w.native.SetCookie(c); err != nil {
			w.warn("signing the web view in", err)
		}
		w.load(page)
	})
	return nil
}

// SignOut deletes every sign-in cookie Show set and loads about:blank, so
// that no page stays open, or stays signed in, after a sign-out or a change
// of server.
func (w *ArtifactWeb) SignOut() {
	w.mu.Lock()
	hosts := make([]string, 0, len(w.hosts))
	for h := range w.hosts {
		hosts = append(hosts, h)
	}
	w.hosts, w.server = nil, ""
	w.mu.Unlock()
	w.do(func() {
		for _, h := range hosts {
			// The cookie's path, and "/", the path earlier builds used.
			for _, path := range []string{ArtifactCookiePath, "/"} {
				if err := w.native.DeleteCookie(WebCookie{Name: pocket.CookieName, Domain: h, Path: path}); err != nil {
					w.warn("signing the web view out", err)
				}
			}
		}
		w.load("about:blank")
	})
}

// Navigate decides what the view does with a navigation its page starts
// (ArtifactNavigation, for the server it is signed in to).
func (w *ArtifactWeb) Navigate(target string, user bool) Navigation {
	return ArtifactNavigation(w.signedServer(), target, user)
}

// Committed is told of every page the view committed to. Reloads and moves
// in the history do not reach the navigation hook, so a page outside the
// rule is blanked here, unless a load the chain made after it replaced it
// already: a stale page must not blank a newer Show.
func (w *ArtifactWeb) Committed(target string) {
	if ArtifactPageAllowed(w.signedServer(), target) {
		return
	}
	w.mu.Lock()
	mark := w.loads
	w.mu.Unlock()
	w.do(func() {
		w.mu.Lock()
		newer := w.loads != mark
		w.mu.Unlock()
		if !newer {
			w.load("about:blank")
		}
	})
}

// Wait returns once the native work asked for so far is done.
func (w *ArtifactWeb) Wait() {
	done := make(chan struct{})
	w.do(func() { close(done) })
	<-done
}

// signedServer is the server the view is signed in to, or "".
func (w *ArtifactWeb) signedServer() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.server
}

// load counts the load, then makes it. It runs on the chain.
func (w *ArtifactWeb) load(page string) {
	w.mu.Lock()
	w.loads++
	w.mu.Unlock()
	if err := w.native.Load(page); err != nil {
		w.warn("loading the artifacts' web view", err)
	}
}

// do runs fn on a goroutine once the work before it ended. The native
// calls block the main thread while they wait, and they must keep their
// order.
func (w *ArtifactWeb) do(fn func()) {
	w.mu.Lock()
	prev := w.last
	done := make(chan struct{})
	w.last = done
	w.mu.Unlock()
	go func() {
		if prev != nil {
			<-prev
		}
		fn()
		close(done)
	}()
}
