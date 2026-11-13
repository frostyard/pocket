package app

import (
	"slices"
	"sync"
	"testing"

	"github.com/frostyard/pocket/internal/pocket"
)

// fakeNative is a web view that records what the pane asks of it. While
// gate is set, SetCookie waits on it, as a native cookie store can.
type fakeNative struct {
	mu      sync.Mutex
	gate    chan struct{}
	cookies []WebCookie
	deleted []WebCookie
	loads   []string
}

func (f *fakeNative) SetCookie(c WebCookie) error {
	f.mu.Lock()
	gate := f.gate
	f.cookies = append(f.cookies, c)
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return nil
}

func (f *fakeNative) DeleteCookie(c WebCookie) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, c)
	return nil
}

func (f *fakeNative) Load(page string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loads = append(f.loads, page)
	return nil
}

func (f *fakeNative) lastLoad() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.loads) == 0 {
		return ""
	}
	return f.loads[len(f.loads)-1]
}

func newTestWeb(t *testing.T) (*ArtifactWeb, *fakeNative) {
	native := &fakeNative{}
	return NewArtifactWeb(native, func(what string, err error) { t.Errorf("%s: %v", what, err) }), native
}

const (
	testServer = "https://example.org"
	pageA      = testServer + "/a/1/aaa/1"
	pageB      = testServer + "/a/1/bbb/1"
)

// TestStaleCommitKeepsNewerShow is the guard of the cross-review of
// frostyard/pocket#34, re-review 1: a page of artifact A that commits
// outside the rule while the Show of artifact B waits for its cookie must
// not blank B once B loads.
func TestStaleCommitKeepsNewerShow(t *testing.T) {
	w, native := newTestWeb(t)
	if err := w.Show(pageA, testServer, "tok"); err != nil {
		t.Fatal(err)
	}
	w.Wait()
	gate := make(chan struct{})
	native.mu.Lock()
	native.gate = gate
	native.mu.Unlock()
	if err := w.Show(pageB, testServer, "tok"); err != nil {
		t.Fatal(err)
	}
	// A's page committed outside the rule (a reload past the hook) while
	// B's cookie is not set yet.
	w.Committed(testServer + "/")
	close(gate)
	w.Wait()
	if got := native.lastLoad(); got != pageB {
		t.Errorf("the view ends on %q, want B's page %q; loads: %q", got, pageB, native.loads)
	}
}

// TestCommitOutsideRuleBlanks checks that a page outside the rule, with no
// newer Show, is blanked, and that an artifact page is left alone.
func TestCommitOutsideRuleBlanks(t *testing.T) {
	w, native := newTestWeb(t)
	if err := w.Show(pageA, testServer, "tok"); err != nil {
		t.Fatal(err)
	}
	w.Wait()
	w.Committed(pageA)
	w.Wait()
	if got := native.lastLoad(); got != pageA {
		t.Errorf("an artifact page was replaced by %q", got)
	}
	w.Committed(testServer + "/")
	w.Wait()
	if got := native.lastLoad(); got != "about:blank" {
		t.Errorf("the server's / stays shown: the last load is %q, want about:blank", got)
	}
}

// TestArtifactWebSignOut checks that sign-out deletes the cookie Show set,
// at its path and at the path earlier builds used, blanks the view, and
// leaves the view signed in to no server.
func TestArtifactWebSignOut(t *testing.T) {
	w, native := newTestWeb(t)
	if err := w.Show(pageA, testServer, "tok"); err != nil {
		t.Fatal(err)
	}
	w.SignOut()
	w.Wait()
	if want := (WebCookie{Name: pocket.CookieName, Value: "tok", Domain: "example.org", Path: "/a/", Secure: true}); !slices.Equal(native.cookies, []WebCookie{want}) {
		t.Errorf("cookies set: %+v, want %+v", native.cookies, want)
	}
	want := []WebCookie{
		{Name: pocket.CookieName, Domain: "example.org", Path: "/a/"},
		{Name: pocket.CookieName, Domain: "example.org", Path: "/"},
	}
	if !slices.Equal(native.deleted, want) {
		t.Errorf("cookies deleted: %+v, want %+v", native.deleted, want)
	}
	if got := native.lastLoad(); got != "about:blank" {
		t.Errorf("the last load after sign-out is %q, want about:blank", got)
	}
	if got := w.Navigate(pageA, true); got == NavigationAllow {
		t.Error("a signed-out view still loads the server's artifact pages")
	}
}
