package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostyard/frostui/frostuitest"

	"github.com/frostyard/pocket/internal/config"
	"github.com/frostyard/pocket/internal/github"
	"github.com/frostyard/pocket/internal/pocket"
)

// The tests of the App's sign-in: each state of Settings > GitHub, and what
// each click asks of the sign-in.

// signedInApp is mergeApp with the sign-in opened, both owners ready and
// the previews read.
func signedInApp(t *testing.T, refs ...string) (*App, *frostuitest.Loop, *fakeGH, *fakeAuth) {
	t.Helper()
	a, loop, gh, auth := mergeApp(t, refs...)
	startMerge(t, a, loop)
	return a, loop, gh, auth
}

// TestSettingsSignInStates draws Settings > GitHub in each state of the
// sign-in, and reads its words.
func TestSettingsSignInStates(t *testing.T) {
	t.Run("no keyring", func(t *testing.T) {
		a, loop, _, _ := mergeApp(t, "frostyard/core#1")
		a.opts.GitHubAuth = nil
		a.opts.GitHub = nil
		loop.Flush()
		st := settingsTester(t, a)
		if !st.HasText("Approve + merge needs the desktop keyring.") || st.HasText("Sign in with GitHub") {
			t.Errorf("settings %q", st.Texts())
		}
	})
	t.Run("held by another Pocket", func(t *testing.T) {
		a, loop, gh, auth := mergeApp(t, "frostyard/core#1")
		auth.setOpenErr(config.ErrSignInHeld)
		a.startGitHub()
		loop.RunUntil(t, func() bool { return a.gh.held })
		st := settingsTester(t, a)
		if !st.HasText("another Pocket holds the GitHub sign-in") || st.HasText("Sign in with GitHub") {
			t.Errorf("settings %q", st.Texts())
		}
		if gh.appCount() != 0 {
			t.Errorf("the installations were read without the lock: %d", gh.appReads)
		}
		r, _ := github.ParseRef("frostyard/core#1")
		if got := a.ghBlocked(r); got != "another Pocket holds the GitHub sign-in" {
			t.Errorf("blocked %q", got)
		}
	})
	t.Run("not signed in", func(t *testing.T) {
		a, loop, _, auth := mergeApp(t, "frostyard/core#1")
		auth.set(github.AppToken{}, false)
		a.startGitHub()
		loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
		st := settingsTester(t, a)
		if !st.HasText("Sign in with GitHub") {
			t.Errorf("settings %q", st.Texts())
		}
		r, _ := github.ParseRef("frostyard/core#1")
		if got := a.ghBlocked(r); got != "sign in to GitHub (Settings > GitHub)" {
			t.Errorf("blocked %q", got)
		}
	})
	t.Run("a stored sign-in that is not usable", func(t *testing.T) {
		a, loop, _, auth := mergeApp(t, "frostyard/core#1")
		auth.set(github.AppToken{}, false)
		auth.setReason("the stored GitHub sign-in is not usable: the version is 2, not 1")
		a.startGitHub()
		loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
		st := settingsTester(t, a)
		if !st.HasText("the version is 2, not 1") || !st.HasText("Sign in with GitHub") {
			t.Errorf("settings %q", st.Texts())
		}
	})
	t.Run("the old tokens", func(t *testing.T) {
		a, loop, _, auth := mergeApp(t, "frostyard/core#1")
		auth.set(github.AppToken{}, false)
		auth.setLegacy(true)
		a.startGitHub()
		loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
		st := settingsTester(t, a)
		if !st.HasText("Pocket now signs in through its GitHub App. Sign in, then delete your 2 old fine-grained tokens on GitHub.") {
			t.Errorf("settings %q", st.Texts())
		}
	})
	t.Run("the code", func(t *testing.T) {
		a, loop, _, auth := mergeApp(t, "frostyard/core#1")
		auth.set(github.AppToken{}, false)
		auth.holdPoll(t)
		a.startGitHub()
		loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
		a.githubSignIn()
		loop.RunUntil(t, func() bool { return a.gh.phase == signInCode })
		st := settingsTester(t, a)
		for _, want := range []string{"ABCD-1234", "Copy code", "Open github.com/login/device", "Time left:",
			"Cancel", "Enter this code only here, now. Check that GitHub names " + github.AppName + "."} {
			if !st.HasText(want) {
				t.Errorf("settings lack %q: %q", want, st.Texts())
			}
		}
	})
	t.Run("checking", func(t *testing.T) {
		a, loop, gh, auth := mergeApp(t, "frostyard/core#1")
		auth.set(github.AppToken{}, false)
		a.startGitHub()
		loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
		release, _ := gh.holdCheck(t)
		a.githubSignIn()
		loop.RunUntil(t, func() bool { return a.gh.phase == signInChecking })
		st := settingsTester(t, a)
		// Cancel is there while checking: the answer then saves nothing.
		if !st.HasText("checking the sign-in…") || !st.HasText("Cancel") {
			t.Errorf("settings %q", st.Texts())
		}
		release()
		loop.RunUntil(t, func() bool { return a.gh.phase == signInSignedIn })
	})
	t.Run("saving", func(t *testing.T) {
		a, loop, _, auth := mergeApp(t, "frostyard/core#1")
		auth.set(github.AppToken{}, false)
		a.startGitHub()
		loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
		release := auth.holdAdopt(t)
		a.githubSignIn()
		loop.RunUntil(t, func() bool { return a.gh.phase == signInSaving })
		st := settingsTester(t, a)
		if !st.HasText("Saving…") || st.HasText("Cancel") {
			t.Errorf("settings %q", st.Texts())
		}
		release()
		loop.RunUntil(t, func() bool { return a.gh.phase == signInSignedIn })
	})
	t.Run("signed in", func(t *testing.T) {
		a, loop, _, _ := mergeApp(t, "frostyard/core#1")
		gh := newFakeGH()
		gh.setApp(&github.AppCheck{Login: github.Login,
			Owners: map[string]string{github.Login: "", "frostyard": ""},
			Notes:  map[string]string{"frostyard": "an enqueue may fail: the App lacks merge_queues: write on frostyard"}})
		a.gh.api = func(ctx context.Context, owner string) (ghAPI, int, func(), error) { return gh.lease(ctx, owner) }
		startMerge(t, a, loop)
		st := settingsTester(t, a)
		for _, want := range []string{
			"Signed in as bketelsen through " + github.AppName,
			"bketelsen: ready", "frostyard: ready", "sign in again by",
			"an enqueue may fail", "Run preflight", "Sign out",
			"Settings > Applications > Authorized GitHub Apps > " + github.AppName + " > Revoke.",
		} {
			if !st.HasText(want) {
				t.Errorf("settings lack %q: %q", want, st.Texts())
			}
		}
	})
	t.Run("an owner not ready", func(t *testing.T) {
		a, loop, gh, _ := mergeApp(t, "frostyard/core#1")
		gh.setApp(&github.AppCheck{Login: github.Login, Owners: map[string]string{
			github.Login: "", "frostyard": "the App is not installed on frostyard"}})
		startMerge(t, a, loop)
		st := settingsTester(t, a)
		if !st.HasText("the App is not installed on frostyard") || !st.HasText("frostyard: the App is not installed on frostyard") {
			t.Errorf("settings %q", st.Texts())
		}
	})
	t.Run("unsaved", func(t *testing.T) {
		a, loop, _, auth := signedInApp(t, "frostyard/core#1")
		auth.setUnsaved(true)
		frames(settingsTester(t, a), loop)
		loop.Flush()
		st := settingsTester(t, a)
		if !st.HasText("the keyring did not keep the new GitHub sign-in") || !st.HasText("Save again") || !st.HasText("Sign out") {
			t.Errorf("settings %q", st.Texts())
		}
		r, _ := github.ParseRef("frostyard/core#1")
		if got := a.ghBlocked(r); !strings.Contains(got, "did not keep") {
			t.Errorf("blocked %q", got)
		}
		// Save again restores the button.
		auth.setUnsaved(true)
		st.Click("Save again")
		loop.RunUntil(t, func() bool { return !a.gh.status.Unsaved })
		if got := a.ghBlocked(r); got != "" {
			t.Errorf("after Save again: %q", got)
		}
	})
	t.Run("the delete failed", func(t *testing.T) {
		a, loop, _, auth := signedInApp(t, "frostyard/core#1")
		auth.setForgetErr(errors.New("the keyring is locked"))
		a.githubSignOut()
		loop.RunUntil(t, func() bool { return a.gh.status.DeleteFailed })
		st := settingsTester(t, a)
		if !st.HasText("Signed out for this run, but the keyring still holds the sign-in") || !st.HasText("Delete again") {
			t.Errorf("settings %q", st.Texts())
		}
		// Delete again deletes it, and the sign-in is offered again.
		auth.setForgetErr(nil)
		st.Click("Delete again")
		loop.RunUntil(t, func() bool { return !a.gh.status.DeleteFailed })
		if !settingsTester(t, a).HasText("Sign in with GitHub") {
			t.Error("the sign-in is not offered after Delete again")
		}
	})
	t.Run("a failed sign-in", func(t *testing.T) {
		a, loop, _, auth := mergeApp(t, "frostyard/core#1")
		auth.set(github.AppToken{}, false)
		auth.setPollErr(&github.APIError{Status: 403, Message: "the code expired"})
		a.startGitHub()
		loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
		a.githubSignIn()
		loop.RunUntil(t, func() bool { return a.gh.fail != "" })
		st := settingsTester(t, a)
		if !st.HasText("the code expired") || !st.HasText("Sign in with GitHub") {
			t.Errorf("settings %q", st.Texts())
		}
	})
}

// TestTheCodeOpensPocketsURL: Open github.com/login/device is Pocket's own
// constant, never the answer's verification_uri.
func TestTheCodeOpensPocketsURL(t *testing.T) {
	a, loop, _, auth := mergeApp(t, "frostyard/core#1")
	auth.set(github.AppToken{}, false)
	auth.code.DeviceCode = "device-code-not-real"
	// The poll waits until the test ends: an answer at once can end the
	// code's phase in the same flush, before RunUntil sees it (#108).
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	auth.setPollFn(func(context.Context) (github.AppToken, error) {
		<-hold
		return github.AppToken{}, context.Canceled
	})
	var mu sync.Mutex
	var opened []string
	a.opts.OpenURL = func(u string) error {
		mu.Lock()
		opened = append(opened, u)
		mu.Unlock()
		return nil
	}
	seen := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), opened...)
	}
	a.startGitHub()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
	a.githubSignIn()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInCode })
	st := settingsTester(t, a)
	if err := st.Click("Open github.com/login/device"); err != nil {
		t.Fatal(err)
	}
	loop.RunUntil(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(opened) > 0
	})
	if got := seen(); !slices.Equal(got, []string{"https://github.com/login/device"}) {
		t.Errorf("opened %v", got)
	}
}

// TestCancelSavesNothing: a pair that comes after Cancel is not saved.
func TestCancelSavesNothing(t *testing.T) {
	a, loop, _, auth := mergeApp(t, "frostyard/core#1")
	auth.set(github.AppToken{}, false)
	poll, releasePoll := make(chan github.AppToken, 1), make(chan struct{})
	auth.setPollFn(func(ctx context.Context) (github.AppToken, error) {
		select {
		case tok := <-poll:
			return tok, nil
		case <-ctx.Done():
			return github.AppToken{}, ctx.Err()
		}
	})
	a.startGitHub()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
	a.githubSignIn()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInCode })
	a.cancelSignIn()
	close(releasePoll)
	poll <- github.AppToken{ClientID: github.AppClientID, Access: testGitHubToken, Refresh: "r",
		AccessExpires: a.opts.Now().Add(time.Hour), RefreshExpires: a.opts.Now().Add(time.Hour)}
	loop.RunUntil(t, func() bool { return auth.cancelCount() == 1 })
	frames(inboxTester(t, a, loop), loop)
	if auth.adoptCount() != 0 {
		t.Errorf("a cancelled sign-in saved %d pairs", auth.adoptCount())
	}
	if a.gh.phase != signInNone {
		t.Errorf("phase %v", a.gh.phase)
	}
}

// TestSignOutDuringThePollSavesNothing: Sign out while the poll runs keeps
// nothing.
func TestSignOutDuringThePollSavesNothing(t *testing.T) {
	a, loop, _, auth := mergeApp(t, "frostyard/core#1")
	auth.set(github.AppToken{}, false)
	poll := make(chan github.AppToken, 1)
	auth.setPollFn(func(ctx context.Context) (github.AppToken, error) {
		select {
		case tok := <-poll:
			return tok, nil
		case <-ctx.Done():
			return github.AppToken{}, ctx.Err()
		}
	})
	a.startGitHub()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
	a.githubSignIn()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInCode })
	a.githubSignOut()
	loop.RunUntil(t, func() bool { return auth.forgetCount() == 1 })
	poll <- github.AppToken{ClientID: github.AppClientID, Access: testGitHubToken, Refresh: "r",
		AccessExpires: a.opts.Now().Add(time.Hour), RefreshExpires: a.opts.Now().Add(time.Hour)}
	loop.RunUntil(t, func() bool { return polled(a) })
	if auth.adoptCount() != 0 {
		t.Errorf("a signed-out sign-in saved %d pairs", auth.adoptCount())
	}
	if a.gh.phase != signInNone {
		t.Errorf("phase %v", a.gh.phase)
	}
}

// polled reports that the poll's result reached the app: the phase is not
// the code's any more.
func polled(a *App) bool { return a.gh.phase != signInCode }

// TestANewerSignInWins: a pair of an older sign-in saves nothing, and the
// newer sign-in is the 1 saved.
func TestANewerSignInWins(t *testing.T) {
	a, loop, _, auth := mergeApp(t, "frostyard/core#1")
	auth.set(github.AppToken{}, false)
	pair := func() github.AppToken {
		return github.AppToken{ClientID: github.AppClientID, Access: testGitHubToken, Refresh: "r",
			AccessExpires: a.opts.Now().Add(time.Hour), RefreshExpires: a.opts.Now().Add(time.Hour)}
	}
	// Both polls answer at the same moment: only the newer sign-in's pair
	// may be saved.
	release := make(chan struct{})
	auth.setPollFn(func(context.Context) (github.AppToken, error) {
		<-release
		return pair(), nil
	})
	a.startGitHub()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
	a.githubSignIn()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInCode })
	a.cancelSignIn()
	a.githubSignIn()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInCode })
	second := a.gh.signInGen
	close(release)
	loop.RunUntil(t, func() bool { return auth.adoptCount() > 0 })
	loop.Flush()
	if gens := auth.adoptGenerations(); len(gens) != 1 || gens[0] != second {
		t.Errorf("adopted generations %v, want only %d", gens, second)
	}
}

// TestSignOutClearsTheOwnersAndThePreviews: Sign out forgets every preview,
// every owner and the preflight rows.
func TestSignOutClearsTheOwnersAndThePreviews(t *testing.T) {
	a, loop, _, auth := signedInApp(t, "frostyard/core#1", "bketelsen/pi-pocket-team#2")
	if len(a.gh.previews) == 0 || len(a.gh.owners) != 2 {
		t.Fatalf("before: previews %d owners %v", len(a.gh.previews), a.gh.owners)
	}
	a.gh.preflight = map[string]*preflightResult{"frostyard": {rows: []github.PreflightRow{{Repo: "frostyard/core"}}}}
	a.githubSignOut()
	// Forget runs on the read's goroutine; the status comes back in its
	// completion, on the UI thread, after it (#118).
	loop.RunUntil(t, func() bool { return auth.forgetCount() == 1 && !a.gh.status.SignedIn })
	if len(a.gh.previews) != 0 || len(a.gh.owners) != 0 || len(a.gh.preflight) != 0 {
		t.Errorf("after: previews %d owners %v preflight %d", len(a.gh.previews), len(a.gh.owners), len(a.gh.preflight))
	}
	if a.gh.phase != signInNone || a.gh.status.SignedIn {
		t.Errorf("phase %v status %+v", a.gh.phase, a.gh.status)
	}
	r, _ := github.ParseRef("frostyard/core#1")
	if got := a.ghBlocked(r); !strings.Contains(got, "sign in") {
		t.Errorf("blocked %q", got)
	}
	if !settingsTester(t, a).HasText("Sign in with GitHub") {
		t.Error("Settings does not offer the sign-in")
	}
}

// TestOwnersUnknownAtStart: before CheckApp answers, no card has a button.
func TestOwnersUnknownAtStart(t *testing.T) {
	a, loop, gh, _ := mergeApp(t, "frostyard/core#1")
	release, _ := gh.holdCheck(t)
	a.startGitHub()
	loop.RunUntil(t, func() bool { return a.gh.checkBusy })
	r, _ := github.ParseRef("frostyard/core#1")
	if got := a.ghBlocked(r); got != "checking the GitHub App's installations" {
		t.Errorf("blocked %q", got)
	}
	tt := inboxTester(t, a, loop)
	if tt.HasText("Approve + merge " + r.String()) {
		t.Errorf("a button before the owners are known: %q", tt.Texts())
	}
	release()
	loop.RunUntil(t, func() bool { return a.gh.owners[r.Owner].known })
	if got := a.ghBlocked(r); got != "" {
		t.Errorf("after CheckApp: %q", got)
	}
}

// TestOneOwnerNotReadyBlocksOnlyItsPRs: frostyard's PRs wait for the App,
// and bketelsen's work.
func TestOneOwnerNotReadyBlocksOnlyItsPRs(t *testing.T) {
	refs := []string{"frostyard/core#1", "bketelsen/pi-pocket-team#2"}
	a, loop, gh, _ := mergeApp(t, refs...)
	gh.setApp(&github.AppCheck{Login: github.Login, Owners: map[string]string{
		github.Login: "", "frostyard": "the App lacks checks: read on frostyard"}})
	startMerge(t, a, loop)
	tt := inboxTester(t, a, loop)
	if tt.HasText("Approve + merge frostyard/core#1") {
		t.Errorf("frostyard's card has a button: %q", tt.Texts())
	}
	if !tt.HasText("Approve + merge bketelsen/pi-pocket-team#2") {
		t.Errorf("bketelsen's card has none: %q", tt.Texts())
	}
	r, _ := github.ParseRef("frostyard/core#1")
	if got := a.ghBlocked(r); got != "the App lacks checks: read on frostyard" {
		t.Errorf("blocked %q", got)
	}
}

// TestJournalLockReleaseDoesNotOpen: Release the journal lock never opens
// the sign-in again, and never takes its lock.
func TestJournalLockReleaseDoesNotOpen(t *testing.T) {
	a, loop, _, auth := mergeApp(t, "frostyard/core#1")
	// Another Pocket holds the journal's lock.
	other := newJournal(a.opts.Store.Dir(), a.opts.Now)
	other.open()
	a.startGitHub()
	loop.RunUntil(t, func() bool { return a.gh.open })
	if a.gh.j.ok() {
		t.Fatal("the journal's lock is not held by another Pocket")
	}
	opens := auth.openCount()
	st := settingsTester(t, a)
	if !st.HasText("Release the journal lock") {
		t.Fatalf("settings %q", st.Texts())
	}
	if err := st.Click("Release the journal lock"); err != nil {
		t.Fatal(err)
	}
	loop.RunUntil(t, func() bool { return a.gh.j.ok() })
	if got := auth.openCount(); got != opens {
		t.Errorf("Release the journal lock opened the sign-in: %d, was %d", got, opens)
	}
	// The sign-in lock is not touched either: the app still holds it.
	if got := auth.closeCount(); got != 0 {
		t.Errorf("the sign-in was closed: %d", got)
	}
}

// TestCloseCallsCloseFirst: closeGitHub ends the sign-in before the
// journal's lock goes.
func TestCloseCallsCloseFirst(t *testing.T) {
	a, loop, _, auth := signedInApp(t, "frostyard/core#1")
	lock := filepath.Join(a.opts.Store.Dir(), lockName)
	auth.setOnClose(func() {
		if _, err := os.Stat(lock); err != nil {
			t.Errorf("the journal's lock went before the sign-in closed: %v", err)
		}
	})
	a.closeGitHub()
	if auth.closeCount() != 1 {
		t.Errorf("Close called %d times", auth.closeCount())
	}
	if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the journal's lock stays: %v", err)
	}
	loop.Flush()
}

// TestOneLeasePerBatchPR: each PR of a batch takes 1 lease, and every job
// releases it, also when it fails.
func TestOneLeasePerBatchPR(t *testing.T) {
	refs := []string{"frostyard/core#1", "frostyard/core#2", "bketelsen/pi-pocket-team#3"}
	a, loop, gh, _ := mergeApp(t, refs...)
	gh.act = func(in github.Intent, _ string, _ func(string) error) github.Result {
		if in.Preview.Ref.Number == 2 {
			err := &github.APIError{Status: 422, Message: "Validation Failed"}
			return github.Result{Kind: "refused", Step: "read", Reason: err.Error(), Err: err}
		}
		return github.Result{Kind: "merged", Step: "merge", Method: "merge"}
	}
	startMerge(t, a, loop)
	var prs []pocket.ReadyPR
	prs = append(prs, a.desk.view.Ready...)
	a.openMergeDialog(prs, true)
	d := a.gh.dialog
	loop.RunUntil(t, func() bool {
		for _, it := range d.items {
			if it.reading {
				return false
			}
		}
		return true
	})
	before, _ := gh.leases()
	a.mergeConfirm(d)
	loop.RunUntil(t, func() bool { return d.done })
	taken, released := gh.leases()
	if taken-before != len(refs) {
		t.Errorf("%d leases for %d PRs", taken-before, len(refs))
	}
	loop.RunUntil(t, func() bool { return released == taken })
	if taken != released {
		t.Errorf("leases taken %d, released %d", taken, released)
	}
}

// TestALeaseThatFailsIsNoButton: a lease that fails gives a named reason,
// and starts no mutation.
func TestALeaseThatFailsIsNoButton(t *testing.T) {
	ref := "frostyard/core#1"
	a, loop, gh, _ := signedInApp(t, ref)
	gh.mu.Lock()
	gh.leaseErr = github.ErrNotSignedIn
	gh.mu.Unlock()
	a.gh.previews[ref].at = time.Time{}
	a.ghPreviewReady()
	loop.RunUntil(t, func() bool { return a.gh.inflight == 0 })
	if pv := a.gh.previews[ref]; pv == nil || pv.err == "" {
		t.Fatalf("the preview did not fail: %+v", pv)
	}
	if len(gh.actsSoFar()) != 0 {
		t.Errorf("acts %v", gh.actsSoFar())
	}
}

// TestSignInSnapshots draws Settings > GitHub in each state of the App's
// sign-in, so the states can be looked at.
func TestSignInSnapshots(t *testing.T) {
	// No keyring.
	a, loop, _, _ := mergeApp(t, "frostyard/core#1")
	a.opts.GitHubAuth = nil
	a.opts.GitHub = nil
	loop.Flush()
	writePNG(t, settingsTester(t, a), "settings-github-no-keyring")

	// Another Pocket holds the sign-in.
	a, loop, _, auth := mergeApp(t, "frostyard/core#1")
	auth.setOpenErr(config.ErrSignInHeld)
	a.startGitHub()
	loop.RunUntil(t, func() bool { return a.gh.held })
	writePNG(t, settingsTester(t, a), "settings-github-held")

	// Not signed in, with the 2 old tokens still kept.
	a, loop, _, auth = mergeApp(t, "frostyard/core#1")
	auth.set(github.AppToken{}, false)
	auth.setReason("the stored GitHub sign-in is not usable: the version is 2, not 1")
	auth.setLegacy(true)
	a.startGitHub()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
	writePNG(t, settingsTester(t, a), "settings-github-signed-out")

	// The code.
	a, loop, _, auth = mergeApp(t, "frostyard/core#1")
	auth.set(github.AppToken{}, false)
	auth.setCodeExpiry(a.opts.Now().Add(14 * time.Minute))
	auth.holdPoll(t)
	a.startGitHub()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
	a.githubSignIn()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInCode })
	writePNG(t, settingsTester(t, a), "settings-github-code")

	// Checking and saving.
	a, loop, gh, auth := mergeApp(t, "frostyard/core#1")
	auth.set(github.AppToken{}, false)
	a.startGitHub()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
	releaseCheck, _ := gh.holdCheck(t)
	a.githubSignIn()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInChecking })
	writePNG(t, settingsTester(t, a), "settings-github-checking")
	releaseCheck()
	releaseAdopt := auth.holdAdopt(t)
	loop.RunUntil(t, func() bool { return a.gh.phase == signInSaving })
	writePNG(t, settingsTester(t, a), "settings-github-saving")
	releaseAdopt()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInSignedIn })

	// Signed in, with 1 owner not ready and a note.
	a, loop, gh, _ = mergeApp(t, "frostyard/core#1", "bketelsen/pi-pocket-team#2")
	gh.setApp(&github.AppCheck{Login: github.Login,
		Owners: map[string]string{github.Login: "", "frostyard": "the App is not installed on frostyard"},
		Notes:  map[string]string{github.Login: "an enqueue may fail: the App lacks merge_queues: write on bketelsen"}})
	gh.rows = map[string]github.PreflightRow{
		"bketelsen/pi-pocket-team": {Repo: "bketelsen/pi-pocket-team", Readable: true, Bypass: "never"},
		"frostyard/core":           {Repo: "frostyard/core", Readable: false, Missing: "checks=read", Reason: "the App lacks checks: read on frostyard"},
	}
	startMerge(t, a, loop)
	a.githubPreflight()
	loop.RunUntil(t, func() bool { return !a.preflightBusy() })
	writePNG(t, settingsTester(t, a), "settings-github-signed-in")

	// The keyring did not keep the new sign-in.
	a, loop, _, auth = mergeApp(t, "frostyard/core#1")
	startMerge(t, a, loop)
	auth.setUnsaved(true)
	loop.Flush()
	writePNG(t, settingsTester(t, a), "settings-github-unsaved")

	// Signed out, but the keyring still holds it.
	a, loop, _, auth = mergeApp(t, "frostyard/core#1")
	startMerge(t, a, loop)
	auth.setForgetErr(errors.New("the keyring is locked"))
	a.githubSignOut()
	loop.RunUntil(t, func() bool { return a.gh.status.DeleteFailed })
	writePNG(t, settingsTester(t, a), "settings-github-delete-failed")

	// A sign-in that failed.
	a, loop, _, auth = mergeApp(t, "frostyard/core#1")
	auth.set(github.AppToken{}, false)
	auth.setPollErr(errors.New("the sign-in was denied on GitHub: you cancelled on GitHub"))
	a.startGitHub()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
	a.githubSignIn()
	loop.RunUntil(t, func() bool { return a.gh.fail != "" })
	writePNG(t, settingsTester(t, a), "settings-github-failed")
}

// TestSignInWaitsForOpen: before Open answers, Sign in asks the sign-in for
// nothing: no generation, no code. Settings says it is opening.
func TestSignInWaitsForOpen(t *testing.T) {
	a, loop, _, auth := mergeApp(t, "frostyard/core#1")
	release := auth.holdOpen(t)
	a.startGitHub()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInOpening })
	st := settingsTester(t, a)
	if !st.HasText("Opening the GitHub sign-in…") {
		t.Fatalf("settings %q", st.Texts())
	}
	if slices.Contains(st.Texts(), "Sign in with GitHub") {
		t.Errorf("a Sign in button before Open answered: %q", st.Texts())
	}
	// The click never reaches the sign-in, so it cannot write the keyring.
	a.githubSignIn()
	loop.Flush()
	if begins, starts, _ := auth.signInCounts(); begins != 0 || starts != 0 {
		t.Errorf("a sign-in before Open: BeginSignIn %d, Start %d", begins, starts)
	}
	release()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInSignedIn })
	if !settingsTester(t, a).HasText("Signed in as " + github.Login) {
		t.Error("the sign-in did not open")
	}
}

// TestCancelWhileCheckingSavesNothing: Cancel is there while CheckApp runs,
// and its answer saves nothing.
func TestCancelWhileCheckingSavesNothing(t *testing.T) {
	a, loop, gh, auth := mergeApp(t, "frostyard/core#1")
	auth.set(github.AppToken{}, false)
	a.startGitHub()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
	release, _ := gh.holdCheck(t)
	a.githubSignIn()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInChecking })
	st := settingsTester(t, a)
	if !st.HasText("Cancel") {
		t.Fatalf("no Cancel while checking: %q", st.Texts())
	}
	if err := st.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
	release()
	loop.Flush()
	loop.Settle(time.Millisecond)
	if auth.adoptCount() != 0 {
		t.Errorf("a cancelled sign-in saved %d pairs", auth.adoptCount())
	}
}

// TestPreflightTakesItsErrorOnTheUIThread: the preflight's 401 is taken on
// the UI thread, which invalidates the lease's generation.
func TestPreflightTakesItsErrorOnTheUIThread(t *testing.T) {
	a, loop, gh, auth := signedInApp(t, "frostyard/core#1")
	gh.setAppErr(&github.APIError{Status: 401, Message: "Bad credentials"})
	release, _ := gh.holdCheck(t)
	a.githubPreflight()
	loop.RunUntil(t, func() bool { return a.preflightBusy() })
	// The UI thread reads the state while the job runs.
	frames(settingsTester(t, a), loop)
	release()
	loop.RunUntil(t, func() bool { return !a.preflightBusy() })
	if got := auth.invalidated(); len(got) != 1 {
		t.Errorf("the preflight's 401 did not invalidate its generation: %v", got)
	}
	if pf := a.gh.preflight["frostyard"]; pf == nil || pf.err == "" {
		t.Errorf("the preflight's error is not shown: %+v", pf)
	}
}

// TestCheckOwnersTakesItsErrorOnTheUIThread: a refused CheckApp of the start
// invalidates the lease's generation.
func TestCheckOwnersTakesItsErrorOnTheUIThread(t *testing.T) {
	a, loop, gh, auth := mergeApp(t, "frostyard/core#1")
	gh.setAppErr(&github.APIError{Status: 401, Message: "Bad credentials"})
	release, _ := gh.holdCheck(t)
	a.startGitHub()
	loop.RunUntil(t, func() bool { return a.gh.checkBusy })
	frames(settingsTester(t, a), loop)
	release()
	loop.RunUntil(t, func() bool { return !a.gh.checkBusy })
	if got := auth.invalidated(); len(got) != 1 {
		t.Errorf("the refused CheckApp did not invalidate its generation: %v", got)
	}
}

// TestTheCodeButtonsShowTheirWords: the buttons say what they do.
func TestTheCodeButtonsShowTheirWords(t *testing.T) {
	a, loop, _, auth := mergeApp(t, "frostyard/core#1")
	auth.set(github.AppToken{}, false)
	auth.holdPoll(t)
	a.startGitHub()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })
	a.githubSignIn()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInCode })
	texts := settingsTester(t, a).Texts()
	for _, want := range []string{"Copy code", "Open github.com/login/device"} {
		if !slices.Contains(texts, want) {
			t.Errorf("no drawn %q: %q", want, texts)
		}
	}
	// The whole words are the buttons' own text, not only their labels: the
	// short words are drawn nowhere.
	for _, short := range []string{"Copy", "Open"} {
		if slices.Contains(texts, short) {
			t.Errorf("the button says %q, not the whole words: %q", short, texts)
		}
	}
}

// TestTheSummarySaysNotReady: an owner with a reason is "not ready" in the
// summary, and its reason shows once, below.
func TestTheSummarySaysNotReady(t *testing.T) {
	a, loop, gh, _ := mergeApp(t, "frostyard/core#1")
	gh.setApp(&github.AppCheck{Login: github.Login, Owners: map[string]string{
		github.Login: "", "frostyard": "the App is not installed on frostyard"}})
	startMerge(t, a, loop)
	st := settingsTester(t, a)
	line := "Signed in as " + github.Login + " through " + github.AppName +
		" · " + github.Login + ": ready · frostyard: not ready · sign in again by"
	if !st.HasText(line) {
		t.Errorf("the summary lacks %q: %q", line, st.Texts())
	}
	n := 0
	for _, text := range st.Texts() {
		if strings.Contains(text, "the App is not installed on frostyard") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the reason shows %d times: %q", n, st.Texts())
	}
}

// TestUncheckedOwnersSaySo: after a failed CheckApp no check runs, and the
// cards say the installations are not checked.
func TestUncheckedOwnersSaySo(t *testing.T) {
	a, loop, gh, _ := mergeApp(t, "frostyard/core#1")
	gh.setAppErr(errors.New("GitHub gave no answer"))
	a.startGitHub()
	loop.RunUntil(t, func() bool { return a.gh.open && !a.gh.checkBusy })
	loop.Flush()
	r, _ := github.ParseRef("frostyard/core#1")
	want := "the GitHub App's installations are not checked: Run preflight (Settings > GitHub)"
	if got := a.ghBlocked(r); got != want {
		t.Errorf("blocked %q, want %q", got, want)
	}
	// While a check runs, the text is the other 1.
	release, _ := gh.holdCheck(t)
	a.githubPreflight()
	loop.RunUntil(t, func() bool { return a.preflightBusy() })
	if got := a.ghBlocked(r); got != "checking the GitHub App's installations" {
		t.Errorf("while checking: %q", got)
	}
	release()
	loop.RunUntil(t, func() bool { return !a.preflightBusy() })
}

// isClosed says ch is closed, without waiting.
func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// TestAStaleCheckKeepsNothing: a CheckApp that started before Sign out, or
// before a new sign-in, applies nothing: no owners from the old sign-in.
func TestAStaleCheckKeepsNothing(t *testing.T) {
	for name, signIn := range map[string]bool{"sign out": false, "sign out and a new sign-in": true} {
		t.Run(name, func(t *testing.T) {
			a, loop, gh, auth := signedInApp(t, "frostyard/core#1")
			// The stale check will answer "frostyard is not installed"; the
			// sign-in after the Sign out reads the owners as ready.
			gh.setApp(&github.AppCheck{Login: github.Login, Owners: map[string]string{
				github.Login: "", "frostyard": "the App is not installed on frostyard"}})
			release, entered := gh.holdCheck(t)
			a.checkOwners(nil)
			// The stale check is in GitHub, with its answer.
			loop.RunUntil(t, func() bool { return isClosed(entered) })
			a.githubSignOut()
			loop.RunUntil(t, func() bool { return auth.forgetCount() == 1 })
			if signIn {
				gh.setApp(nil)
				a.githubSignIn()
				loop.RunUntil(t, func() bool { return a.gh.phase == signInSignedIn })
			}
			release()
			loop.RunUntil(t, func() bool { return !a.gh.checkBusy })
			if signIn {
				// The stale answer must not undo the new sign-in's owners.
				if got := a.gh.owners["frostyard"].reason; got != "" {
					t.Errorf("the old check set frostyard's state: %q", got)
				}
				return
			}
			if len(a.gh.owners) != 0 {
				t.Errorf("the old check set the owners: %+v", a.gh.owners)
			}
		})
	}
}

// TestAStalePreflightKeepsNothing: a preflight that started before Sign out
// applies nothing: no rows, no owners, no blocked repositories.
func TestAStalePreflightKeepsNothing(t *testing.T) {
	for name, signIn := range map[string]bool{"sign out": false, "sign out and a new sign-in": true} {
		t.Run(name, func(t *testing.T) {
			a, loop, gh, auth := signedInApp(t, "frostyard/core#1", "bketelsen/pi-pocket-team#2")
			gh.rows = map[string]github.PreflightRow{
				"frostyard/core": {Repo: "frostyard/core", Readable: true, Bypass: "never"},
			}
			release, entered := gh.holdCheck(t)
			a.githubPreflight()
			// The stale preflight is in GitHub, with its answer.
			loop.RunUntil(t, func() bool { return isClosed(entered) })
			a.githubSignOut()
			loop.RunUntil(t, func() bool { return auth.forgetCount() == 1 })
			if signIn {
				a.githubSignIn()
				loop.RunUntil(t, func() bool { return a.gh.phase == signInSignedIn })
			}
			release()
			loop.RunUntil(t, func() bool { return !a.preflightBusy() })
			if signIn {
				// The stale preflight's rows must not come back.
				for _, owner := range githubOwners {
					if pf := a.gh.preflight[owner]; pf != nil && len(pf.rows) != 0 {
						t.Errorf("the old preflight left %s's rows: %+v", owner, pf.rows)
					}
				}
				return
			}
			if len(a.gh.owners) != 0 {
				t.Errorf("the old preflight set the owners: %+v", a.gh.owners)
			}
			for _, owner := range githubOwners {
				if pf := a.gh.preflight[owner]; pf != nil && len(pf.rows) != 0 {
					t.Errorf("the old preflight left %s's rows: %+v", owner, pf.rows)
				}
			}
			if len(a.gh.blocked) != 0 {
				t.Errorf("the old preflight left blocked: %v", a.gh.blocked)
			}
		})
	}
}

// TestAPollClearsOnlyItsOwnHandle: poll A's late answer keeps poll B's
// cancel handle, so Cancel stops B's poll.
func TestAPollClearsOnlyItsOwnHandle(t *testing.T) {
	a, loop, _, auth := mergeApp(t, "frostyard/core#1")
	auth.set(github.AppToken{}, false)
	// A code that has not expired, on the real clock the deadline uses.
	auth.setCodeExpiry(time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
	// No timer posts UI work in this test: the countdown's frames would
	// look like the completion the test waits for.
	a.opts.After = func(time.Duration, func()) {}
	a.startGitHub()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInNone })

	// The poster, before any poll starts: each poll captures it. A posted
	// function sent right after poll A returned is A's completion, and the
	// test waits until that 1 has run.
	var mu sync.Mutex
	aReturned, aTagged, aRan := false, false, false
	inner := a.post
	a.post = func(fn func()) {
		mu.Lock()
		isA := aReturned && !aTagged
		if isA {
			aTagged = true
		}
		mu.Unlock()
		if !isA {
			inner(fn)
			return
		}
		inner(func() {
			fn()
			mu.Lock()
			aRan = true
			mu.Unlock()
		})
	}
	aDone := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return aRan
	}

	firstAnswer := make(chan github.AppToken, 1)
	started := make(chan int, 4)
	secondDone := make(chan bool, 1)
	calls := 0
	auth.setPollFn(func(ctx context.Context) (github.AppToken, error) {
		auth.mu.Lock()
		calls++
		n := calls
		auth.mu.Unlock()
		started <- n
		if n == 1 {
			tok := <-firstAnswer
			mu.Lock()
			aReturned = true
			mu.Unlock()
			return tok, nil
		}
		<-ctx.Done()
		secondDone <- true
		return github.AppToken{}, ctx.Err()
	})
	pair := github.AppToken{ClientID: github.AppClientID, Access: testGitHubToken, Refresh: "r",
		AccessExpires: a.opts.Now().Add(time.Hour), RefreshExpires: a.opts.Now().Add(time.Hour)}
	a.githubSignIn()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInCode })
	if n := <-started; n != 1 {
		t.Fatalf("the first poll is call %d", n)
	}
	// Poll A is in flight; Cancel it and start a new sign-in.
	a.cancelSignIn()
	a.githubSignIn()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInCode })
	if n := <-started; n != 2 {
		t.Fatalf("the second poll is call %d", n)
	}
	// Poll A answers last, after poll B started, and its completion runs
	// before Cancel.
	firstAnswer <- pair
	loop.RunUntil(t, aDone)
	if a.gh.cancelPoll == nil {
		t.Fatal("poll A's answer dropped poll B's cancel handle")
	}
	// Cancel stops poll B.
	a.cancelSignIn()
	select {
	case <-secondDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Cancel did not stop poll B")
	}
	if auth.adoptCount() != 0 {
		t.Errorf("a cancelled poll saved %d pairs: %v", auth.adoptCount(), auth.adoptGenerations())
	}
}

// TestAPreflights401Invalidates: a 401 of 1 repository's own read is taken
// on the UI thread, and invalidates the lease's generation.
func TestAPreflights401Invalidates(t *testing.T) {
	a, loop, gh, auth := signedInApp(t, "frostyard/core#1", "frostyard/pocket#2")
	gh.rows = map[string]github.PreflightRow{
		"frostyard/core": {Repo: "frostyard/core", Reason: "GitHub answered 401: Bad credentials",
			Err: &github.APIError{Status: 401, Message: "Bad credentials"}},
	}
	a.githubPreflight()
	loop.RunUntil(t, func() bool { return !a.preflightBusy() })
	if got := auth.invalidated(); len(got) != 1 {
		t.Errorf("the preflight's 401 did not invalidate its generation: %v", got)
	}
	if pf := a.gh.preflight["frostyard"]; pf == nil || len(pf.rows) != 2 {
		t.Errorf("rows %+v", pf)
	}
}

// TestAStaleCheckStillReconcilesTheJournal: a Sign out while the first
// CheckApp runs still settles the attempts a crash left, by reads; the
// owners stay unknown.
func TestAStaleCheckStillReconcilesTheJournal(t *testing.T) {
	ref := "frostyard/core#1"
	a, loop, gh, auth := mergeApp(t, ref)
	writeJournal(t, a.opts.Store.Dir(), journalOp("op-1", ref, "about to merge", ""))
	gh.mu.Lock()
	gh.settle = func(github.Intent, bool) github.Result {
		return github.Result{Kind: "approved", Reason: "approved, not merged"}
	}
	gh.mu.Unlock()
	release, entered := gh.holdCheck(t)
	a.startGitHub()
	// The startup check is in GitHub, with its answer.
	loop.RunUntil(t, func() bool { return isClosed(entered) })
	a.githubSignOut()
	loop.RunUntil(t, func() bool { return auth.forgetCount() == 1 })
	release()
	loop.RunUntil(t, func() bool {
		o := a.gh.j.find("op-1")
		return o != nil && o.Result != ""
	})
	// The merge may have been sent, so the read settles it as unknown, not
	// as approved.
	if o := a.gh.j.find("op-1"); o.Result != "unknown" || o.Reason != "Pocket stopped during the merge: merge may be pending" {
		t.Errorf("the attempt was not settled by reads: %+v", o)
	}
	gh.mu.Lock()
	settles := slices.Clone(gh.settles)
	acts := slices.Clone(gh.acts)
	gh.mu.Unlock()
	if !slices.Equal(settles, []string{ref}) {
		t.Errorf("settles %v", settles)
	}
	if len(acts) != 0 {
		t.Errorf("a mutation ran at start: %v", acts)
	}
	if len(a.gh.owners) != 0 {
		t.Errorf("the stale check set the owners: %+v", a.gh.owners)
	}
}

// TestAnOldCheckKeepsTheInstallationsBusy: a check that started before Sign
// out clears only its own busy flag, so a preflight that runs keeps the cards
// saying the installations are being checked.
func TestAnOldCheckKeepsTheInstallationsBusy(t *testing.T) {
	a, loop, gh, auth := signedInApp(t, "frostyard/core#1", "bketelsen/pi-pocket-team#2")
	// The check that a new sign-in will make stale.
	releaseCheck, checkEntered := gh.holdCheck(t)
	a.checkOwners(nil)
	loop.RunUntil(t, func() bool { return isClosed(checkEntered) })
	a.githubSignOut()
	loop.RunUntil(t, func() bool { return auth.forgetCount() == 1 })
	a.githubSignIn()
	loop.RunUntil(t, func() bool { return a.gh.phase == signInSignedIn })
	// A preflight, still in GitHub.
	releasePreflight, preflightEntered := gh.holdCheck(t)
	a.githubPreflight()
	loop.RunUntil(t, func() bool { return isClosed(preflightEntered) })
	// The old check answers: only its own flag goes.
	releaseCheck()
	loop.RunUntil(t, func() bool { return !a.gh.checkBusy })
	if !a.checkingOwners() {
		t.Fatalf("the old check cleared the preflight's flag: preflight %v", a.gh.preflightBusy)
	}
	r, _ := github.ParseRef("frostyard/core#1")
	if got := a.ghBlocked(r); got != "checking the GitHub App's installations" {
		t.Errorf("blocked %q", got)
	}
	releasePreflight()
	loop.RunUntil(t, func() bool { return !a.checkingOwners() })
	if got := a.ghBlocked(r); got != "" {
		t.Errorf("after the preflight: %q", got)
	}
}
