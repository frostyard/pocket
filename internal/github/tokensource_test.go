package github

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The tests of the leases and the refresh: a fake store in memory, a fake
// github.com, an injected clock, and gates a test opens by hand. No sleep
// waits for a fact: a test waits on a state change, and a watchdog fails it.

const (
	oldAccess  = "ghu_OLD0000"
	newAccess  = "ghu_ACCESS0000"
	otherBuild = "Iv23liOTHERBUILD0"
)

// store is an AppStore in memory: 1 pair, the errors a test gives, and
// channels that hold a call until the test opens it.
type store struct {
	mu        sync.Mutex
	tok       AppToken
	have      bool
	loadErr   error
	saveErr   error
	forgetErr error
	saves     int
	deletes   int
	loads     int

	gate     chan struct{} // Save and Forget wait for it
	saving   chan struct{} // closed at the start of a Save
	erasing  chan struct{} // closed at the start of a Forget
	signalOn bool
}

func (s *store) Load() (AppToken, error) {
	s.mu.Lock()
	s.loads++
	err, tok, have := s.loadErr, s.tok, s.have
	s.mu.Unlock()
	if err != nil {
		return AppToken{}, err
	}
	if !have {
		return AppToken{}, ErrNoSignIn
	}
	return tok, nil
}

func (s *store) Save(tok AppToken) error {
	s.mu.Lock()
	s.saves++
	gate := s.gate
	if s.signalOn && s.saving != nil {
		close(s.saving)
		s.saving = nil
	}
	s.mu.Unlock()
	if gate != nil {
		<-gate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.tok, s.have = tok, true
	return nil
}

func (s *store) Forget() error {
	s.mu.Lock()
	s.deletes++
	gate := s.gate
	if s.signalOn && s.erasing != nil {
		close(s.erasing)
		s.erasing = nil
	}
	s.mu.Unlock()
	if gate != nil {
		<-gate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forgetErr != nil {
		return s.forgetErr
	}
	s.tok, s.have = AppToken{}, false
	return nil
}

func (s *store) set(tok AppToken) {
	s.mu.Lock()
	s.tok, s.have = tok, true
	s.mu.Unlock()
}

func (s *store) get() (AppToken, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tok, s.have
}

func (s *store) setErr(load, save, forget error) {
	s.mu.Lock()
	s.loadErr, s.saveErr, s.forgetErr = load, save, forget
	s.mu.Unlock()
}

func (s *store) counts() (saves, deletes, loads int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saves, s.deletes, s.loads
}

// hold makes the next Save or Forget wait, and gives the release of that
// wait and the channel that closes when the call starts. The test releases
// it, and so does the cleanup: a blocked call never outlives its test.
func (s *store) hold(t *testing.T, which string) (release func(), entered chan struct{}) {
	t.Helper()
	gate, entered := make(chan struct{}), make(chan struct{})
	s.mu.Lock()
	s.gate, s.signalOn = gate, true
	if which == "save" {
		s.saving = entered
	} else {
		s.erasing = entered
	}
	s.mu.Unlock()
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return release, entered
}

// source is a TokenSource on a fake store and a fake github.com.
type source struct {
	t     *testing.T
	ts    *TokenSource
	st    *store
	sign  *signIn
	oauth *OAuth
	clock *clock
}

func newSource(t *testing.T) *source {
	t.Helper()
	sign := newSignIn(t)
	c := newClock()
	st := &store{}
	o := sign.oauth(c)
	ts := NewTokenSource(o, st)
	// A blocked call in a failing test must not hold the suite.
	ts.closeWait = 50 * time.Millisecond
	t.Cleanup(ts.Close)
	return &source{t: t, ts: ts, st: st, sign: sign, oauth: o, clock: c}
}

// pair is a sign-in whose access token ends after valid.
func (s *source) pair(access string, valid time.Duration) AppToken {
	return AppToken{
		ClientID:       AppClientID,
		Login:          Login,
		Access:         access,
		Refresh:        testRefresh,
		AccessExpires:  s.clock.now().Add(valid),
		RefreshExpires: s.clock.now().Add(180 * 24 * time.Hour),
	}
}

// load keeps pair and loads it, as a start does.
func (s *source) load(pair AppToken) {
	s.t.Helper()
	s.st.set(pair)
	if err := s.ts.Load(); err != nil {
		s.t.Fatalf("load: %v", err)
	}
}

// lease takes 1 lease, and fails the test on an error.
func (s *source) lease(ctx context.Context) (string, int, func()) {
	s.t.Helper()
	tok, gen, release, err := s.ts.Lease(ctx)
	if err != nil {
		s.t.Fatalf("lease: %v", err)
	}
	return tok, gen, release
}

// waitFor waits until cond holds, waking at each state change of the
// source: no sleep and no timing. cond reads the state with ts.mu held.
func waitFor(t *testing.T, ts *TokenSource, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		ts.mu.Lock()
		ch := ts.changed
		ok := cond()
		ts.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-ch:
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// waitDone waits for a goroutine to finish.
func waitDone(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestTokenSourceLoad(t *testing.T) {
	t.Run("no entry", func(t *testing.T) {
		s := newSource(t)
		if err := s.ts.Load(); err != nil {
			t.Fatal(err)
		}
		if st := s.ts.Status(); st.SignedIn || st.Reason != "" {
			t.Errorf("status %+v", st)
		}
		if _, _, _, err := s.ts.Lease(context.Background()); !errors.Is(err, ErrNotSignedIn) {
			t.Errorf("lease: %v", err)
		}
	})
	t.Run("1 entry", func(t *testing.T) {
		s := newSource(t)
		pair := s.pair(oldAccess, 8*time.Hour)
		s.load(pair)
		st := s.ts.Status()
		if !st.SignedIn || st.Login != Login || !st.RefreshExpires.Equal(pair.RefreshExpires) || st.Reason != "" {
			t.Fatalf("status %+v", st)
		}
		tok, gen, release := s.lease(context.Background())
		defer release()
		if tok != oldAccess || gen != 1 {
			t.Errorf("lease %q gen %d", tok, gen)
		}
		if got := s.sign.tokenRequests(); got != 0 {
			t.Errorf("the load made %d refresh requests", got)
		}
	})
	t.Run("not a sign-in", func(t *testing.T) {
		s := newSource(t)
		s.st.setErr(&BadSignIn{Reason: "this sign-in is for another build of Pocket"}, nil, nil)
		if err := s.ts.Load(); err != nil {
			t.Fatal(err)
		}
		st := s.ts.Status()
		if st.SignedIn || !strings.Contains(st.Reason, "another build of Pocket") {
			t.Errorf("status %+v", st)
		}
	})
	t.Run("no keyring", func(t *testing.T) {
		s := newSource(t)
		locked := errors.New("approve + merge needs the desktop keyring")
		s.st.setErr(locked, nil, nil)
		if err := s.ts.Load(); !errors.Is(err, locked) {
			t.Fatalf("load: %v", err)
		}
	})
}

// TestLeaseValidity: 10 minutes of validity, and no refresh before it.
func TestLeaseValidity(t *testing.T) {
	s := newSource(t)
	s.load(s.pair(oldAccess, leaseValidity))
	if _, _, release := s.lease(context.Background()); release == nil {
		t.Fatal("no release")
	} else {
		release()
	}
	if got := s.sign.tokenRequests(); got != 0 {
		t.Fatalf("a refresh ran on a valid token: %d requests", got)
	}
	// Under the threshold: the lease refreshes first, and the store holds
	// the new pair before it gives it.
	s = newSource(t)
	s.load(s.pair(oldAccess, leaseValidity-time.Second))
	tok, gen, release := s.lease(context.Background())
	defer release()
	if tok != newAccess || gen != 2 {
		t.Errorf("lease %q gen %d", tok, gen)
	}
	if saved, ok := s.st.get(); !ok || saved.Access != newAccess || saved.Login != Login {
		t.Errorf("the store holds %+v", saved)
	}
	if got := s.sign.tokenRequests(); got != 1 {
		t.Errorf("refresh requests %d", got)
	}
}

// TestLeaseHeldAcrossTheThreshold: a live lease is never cut off, and no new
// lease starts while the refresh is due.
func TestLeaseHeldAcrossTheThreshold(t *testing.T) {
	s := newSource(t)
	s.load(s.pair(oldAccess, leaseValidity+time.Minute))
	first, _, release := s.lease(context.Background())

	s.clock.add(2 * time.Minute) // 9 minutes left: a refresh is due
	type got struct {
		tok string
		err error
	}
	second := make(chan got, 1)
	go func() {
		tok, _, release, err := s.ts.Lease(context.Background())
		if err == nil {
			release()
		}
		second <- got{tok, err}
	}()
	waitFor(t, s.ts, "the refresh to be due", func() bool { return s.ts.due })
	if got := s.sign.tokenRequests(); got != 0 {
		t.Fatalf("a refresh ran while a lease was held: %d requests", got)
	}
	release()
	select {
	case g := <-second:
		if g.err != nil || g.tok != newAccess {
			t.Fatalf("second lease %+v", g)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second lease did not start after the release")
	}
	if first == newAccess {
		t.Error("the first lease was replaced")
	}
}

// TestLeaseOneRefresh: 2 callers at once are 1 refresh.
func TestLeaseOneRefresh(t *testing.T) {
	s := newSource(t)
	s.load(s.pair(oldAccess, time.Minute))
	gate, entered := s.st.hold(t, "save")
	results := make(chan string, 2)
	for range 2 {
		go func() {
			tok, _, release, err := s.ts.Lease(context.Background())
			if err != nil {
				results <- "error: " + err.Error()
				return
			}
			release()
			results <- tok
		}()
	}
	<-entered // 1 save is in progress: the other caller waits
	gate()
	for range 2 {
		select {
		case tok := <-results:
			if tok != newAccess {
				t.Fatalf("a lease gave %q", tok)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a lease did not return")
		}
	}
	if got := s.sign.tokenRequests(); got != 1 {
		t.Errorf("refresh requests %d, want 1", got)
	}
}

// TestLeaseCancelledWaiter: a waiter that leaves returns ctx.Err(), holds no
// lease and starts no refresh.
func TestLeaseCancelledWaiter(t *testing.T) {
	s := newSource(t)
	s.load(s.pair(oldAccess, time.Minute))
	gate, _ := s.st.hold(t, "save")
	first := make(chan string, 1)
	go func() {
		tok, _, release, err := s.ts.Lease(context.Background())
		if err == nil {
			release()
		}
		first <- tok
	}()
	waitFor(t, s.ts, "the refresh to start", func() bool { return s.ts.refreshing })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, release, err := s.ts.Lease(ctx); !errors.Is(err, context.Canceled) || release != nil {
		t.Fatalf("err %v, release given: %v", err, release != nil)
	}
	s.ts.mu.Lock()
	leases := s.ts.leases
	s.ts.mu.Unlock()
	if leases != 0 {
		t.Errorf("a cancelled waiter holds %d leases", leases)
	}
	gate()
	select {
	case tok := <-first:
		if tok != newAccess {
			t.Errorf("the first lease gave %q", tok)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first lease did not return")
	}
	if got := s.sign.tokenRequests(); got != 1 {
		t.Errorf("refresh requests %d, want 1", got)
	}
}

// TestStatusWhileBlocked: Status answers while a refresh and a save block.
func TestStatusWhileBlocked(t *testing.T) {
	s := newSource(t)
	s.load(s.pair(oldAccess, time.Minute))
	gate, entered := s.st.hold(t, "save")
	go func() {
		if _, _, release, err := s.ts.Lease(context.Background()); err == nil {
			release()
		}
	}()
	<-entered // the refresh's save is in progress
	if st := statusNow(t, s.ts); !st.SignedIn || st.Login != Login {
		t.Errorf("status %+v during the save", st)
	}
	gate()

	// A blocked Adopt, as the sign-in has it.
	s2 := newSource(t)
	gate2, entered2 := s2.st.hold(t, "save")
	gen := s2.ts.BeginSignIn()
	go func() { _ = s2.ts.Adopt(s2.pair(oldAccess, 8*time.Hour), gen) }()
	<-entered2
	if st := statusNow(t, s2.ts); st.SignedIn || st.Unsaved {
		t.Errorf("status %+v during the adopt", st)
	}
	gate2()
}

// statusNow gives the status, and fails the test when it waits for a write.
func statusNow(t *testing.T, ts *TokenSource) AppStatus {
	t.Helper()
	done := make(chan AppStatus, 1)
	go func() { done <- ts.Status() }()
	select {
	case st := <-done:
		return st
	case <-time.After(5 * time.Second):
		t.Fatal("Status waited for a write")
		return AppStatus{}
	}
}

// TestRefresh: the pair keeps the login, the store has it before a lease,
// and a later load accepts it.
func TestTokenSourceRefresh(t *testing.T) {
	s := newSource(t)
	s.load(s.pair(oldAccess, time.Minute))
	tok, _, release := s.lease(context.Background())
	release()
	if tok != newAccess {
		t.Fatalf("lease %q", tok)
	}
	st := s.ts.Status()
	if st.Login != Login || !st.RefreshExpires.After(s.clock.now()) {
		t.Errorf("status %+v", st)
	}
	saved, ok := s.st.get()
	if !ok || saved.Access != newAccess || saved.Login != Login || saved.ClientID != AppClientID {
		t.Fatalf("the store holds %+v", saved)
	}
	again := NewTokenSource(s.oauth, s.st)
	if err := again.Load(); err != nil {
		t.Fatal(err)
	}
	if st := again.Status(); !st.SignedIn || st.Login != Login {
		t.Errorf("the new source loaded %+v", st)
	}
}

// TestRefreshAnotherClientID: a pair of another build ends the sign-in.
func TestTokenSourceRefreshAnotherBuild(t *testing.T) {
	s := newSource(t)
	pair := s.pair(oldAccess, time.Minute)
	pair.ClientID = otherBuild
	s.load(pair)
	_, _, _, err := s.ts.Lease(context.Background())
	if !errors.Is(err, ErrSignIn) {
		t.Fatalf("lease: %v", err)
	}
	if st := s.ts.Status(); st.SignedIn || st.Login != "" {
		t.Errorf("status %+v", st)
	}
	waitForDelete(t, s)
	if _, ok := s.st.get(); ok {
		t.Error("the entry was kept")
	}
}

// waitForDelete waits until the ended sign-in's delete has finished: the
// waiting Lease returns when memory is cleared, and the delete follows under
// the write mutex. The refresh worker clears refreshing (and wakes the
// waiters) only after that delete returned; the store counts a delete when
// it starts, so the count alone is not enough.
func waitForDelete(t *testing.T, s *source) {
	t.Helper()
	waitFor(t, s.ts, "the delete of the entry", func() bool { return deleteDone(s) })
}

// deleteDone says the ended sign-in's delete has returned. The caller holds
// s.ts.mu.
func deleteDone(s *source) bool {
	_, deletes, _ := s.st.counts()
	return deletes > 0 && !s.ts.refreshing
}

// TestWaitForDeleteWaitsForTheEnd: waitForDelete does not return while the
// delete of an ended sign-in is held after the store counted it.
func TestWaitForDeleteWaitsForTheEnd(t *testing.T) {
	s := newSource(t)
	s.load(s.pair(oldAccess, time.Minute))
	s.sign.answer(nil, func(w http.ResponseWriter, n int) {
		writeAnswer(w, 200, `{"error":"bad_refresh_token"}`)
	})
	release, entered := s.st.hold(t, "forget")
	if _, _, _, err := s.ts.Lease(context.Background()); !errors.Is(err, ErrSignIn) {
		t.Fatalf("lease: %v", err)
	}
	<-entered
	// The store counted the delete, and still holds the entry: the helper's
	// condition must not hold yet.
	if _, deletes, _ := s.st.counts(); deletes != 1 {
		t.Fatalf("deletes %d", deletes)
	}
	s.ts.mu.Lock()
	done := deleteDone(s)
	s.ts.mu.Unlock()
	if done {
		t.Fatal("waitForDelete's condition holds while the delete is held")
	}
	release()
	waitForDelete(t, s)
	if _, ok := s.st.get(); ok {
		t.Error("the entry was kept")
	}
}

// TestRefreshEndsTheSignIn: a spent, refused or malformed refresh clears
// memory and deletes the entry.
func TestTokenSourceRefreshEndsTheSignIn(t *testing.T) {
	for name, answer := range map[string]func(w http.ResponseWriter, n int){
		"bad_refresh_token": func(w http.ResponseWriter, n int) {
			writeAnswer(w, 200, `{"error":"bad_refresh_token"}`)
		},
		"another error": func(w http.ResponseWriter, n int) {
			writeAnswer(w, 200, `{"error":"unsupported_grant_type"}`)
		},
		"4xx": func(w http.ResponseWriter, n int) {
			writeAnswer(w, 401, `{"message":"Bad credentials"}`)
		},
		"malformed": func(w http.ResponseWriter, n int) {
			writeAnswer(w, 200, `{"access_token":"ghu_A"}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSource(t)
			s.load(s.pair(oldAccess, time.Minute))
			s.sign.answer(nil, answer)
			_, _, _, err := s.ts.Lease(context.Background())
			if !errors.Is(err, ErrSignIn) {
				t.Fatalf("lease: %v", err)
			}
			waitForDelete(t, s)
			if st := s.ts.Status(); st.SignedIn || st.DeleteFailed {
				t.Errorf("status %+v", st)
			}
			if _, ok := s.st.get(); ok {
				t.Error("the entry was kept")
			}
			if _, deletes, _ := s.st.counts(); deletes != 1 {
				t.Errorf("deletes %d", deletes)
			}
			if _, _, _, err := s.ts.Lease(context.Background()); !errors.Is(err, ErrNotSignedIn) {
				t.Errorf("a lease after the end: %v", err)
			}
		})
	}
}

// TestRefreshNoAnswer: the entry stays, the waiting lease fails, and the
// next lease tries again.
func TestTokenSourceRefreshNoAnswer(t *testing.T) {
	s := newSource(t)
	old := s.pair(oldAccess, time.Minute)
	s.load(old)
	s.sign.answer(nil, func(w http.ResponseWriter, n int) {
		if n == 1 {
			writeAnswer(w, 502, `{"message":"Server Error"}`)
			return
		}
		writeAnswer(w, 200, goodPairAnswer())
	})
	if _, _, _, err := s.ts.Lease(context.Background()); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("lease: %v", err)
	}
	if st := s.ts.Status(); !st.SignedIn || st.Login != Login {
		t.Errorf("status %+v", st)
	}
	if kept, ok := s.st.get(); !ok || kept.Access != oldAccess {
		t.Errorf("the store holds %+v", kept)
	}
	tok, _, release := s.lease(context.Background())
	release()
	if tok != newAccess {
		t.Errorf("the next lease gave %q", tok)
	}
}

// TestRefreshUnsaved: a failed save gives no lease, Unsaved, and Save again
// restores it.
func TestTokenSourceRefreshUnsaved(t *testing.T) {
	s := newSource(t)
	s.load(s.pair(oldAccess, time.Minute))
	s.st.setErr(nil, errors.New("the keyring is locked"), nil)
	if _, _, _, err := s.ts.Lease(context.Background()); !errors.Is(err, ErrUnsaved) {
		t.Fatalf("lease: %v", err)
	}
	if st := s.ts.Status(); !st.SignedIn || !st.Unsaved {
		t.Fatalf("status %+v", st)
	}
	if _, _, _, err := s.ts.Lease(context.Background()); !errors.Is(err, ErrUnsaved) {
		t.Errorf("a lease while unsaved: %v", err)
	}
	s.st.setErr(nil, nil, nil)
	if err := s.ts.SaveAgain(); err != nil {
		t.Fatal(err)
	}
	if st := s.ts.Status(); st.Unsaved {
		t.Errorf("status %+v after Save again", st)
	}
	if saved, ok := s.st.get(); !ok || saved.Access != newAccess {
		t.Errorf("the store holds %+v", saved)
	}
	tok, _, release := s.lease(context.Background())
	release()
	if tok != newAccess {
		t.Errorf("the lease after Save again gave %q", tok)
	}
}

// TestInvalidate: a 401 on the current generation refreshes; an older one
// changes nothing.
func TestInvalidate(t *testing.T) {
	s := newSource(t)
	s.load(s.pair(oldAccess, 8*time.Hour))
	_, gen, release := s.lease(context.Background())
	release()
	s.ts.Invalidate(gen - 1)
	if tok, _, release := s.lease(context.Background()); tok != oldAccess {
		t.Errorf("an old generation changed the token: %q", tok)
		release()
	} else {
		release()
	}
	s.ts.Invalidate(gen)
	if tok, _, release := s.lease(context.Background()); tok != newAccess {
		t.Errorf("the token after the 401 is %q", tok)
		release()
	} else {
		release()
	}
	if got := s.sign.tokenRequests(); got != 1 {
		t.Errorf("refresh requests %d", got)
	}
}

// TestAdoptStale: a cancelled generation saves nothing.
func TestAdoptStale(t *testing.T) {
	s := newSource(t)
	gen := s.ts.BeginSignIn()
	s.ts.CancelSignIn(gen)
	if err := s.ts.Adopt(s.pair(oldAccess, 8*time.Hour), gen); !errors.Is(err, ErrStale) {
		t.Fatalf("adopt: %v", err)
	}
	if saves, _, _ := s.st.counts(); saves != 0 {
		t.Errorf("the store saved %d pairs", saves)
	}
	if st := s.ts.Status(); st.SignedIn {
		t.Errorf("status %+v", st)
	}
	// A new sign-in carries the new generation.
	gen = s.ts.BeginSignIn()
	if err := s.ts.Adopt(s.pair(oldAccess, 8*time.Hour), gen); err != nil {
		t.Fatal(err)
	}
	if _, _, release := s.lease(context.Background()); release == nil {
		t.Error("no release after an adopt")
	} else {
		release()
	}
}

// TestWritesDuringBlockedAdopt: a Cancel, a Sign out or a new sign-in during
// a blocked Adopt applies after it.
func TestWritesDuringBlockedAdopt(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		s := newSource(t)
		gate, entered := s.st.hold(t, "save")
		gen := s.ts.BeginSignIn()
		adopted := make(chan error, 1)
		go func() { adopted <- s.ts.Adopt(s.pair(oldAccess, 8*time.Hour), gen) }()
		<-entered
		s.ts.CancelSignIn(gen)
		gate()
		if err := <-adopted; err != nil {
			t.Fatalf("adopt: %v", err)
		}
		// The sign-in committed: the pair is live, and the generation it
		// carried is no longer current, so it cannot be adopted again.
		if st := s.ts.Status(); !st.SignedIn {
			t.Errorf("status %+v", st)
		}
		if err := s.ts.Adopt(s.pair(oldAccess, 8*time.Hour), gen); !errors.Is(err, ErrStale) {
			t.Errorf("a second adopt with the old generation: %v", err)
		}
	})
	t.Run("sign out", func(t *testing.T) {
		s := newSource(t)
		gate, entered := s.st.hold(t, "save")
		gen := s.ts.BeginSignIn()
		adopted := make(chan error, 1)
		go func() { adopted <- s.ts.Adopt(s.pair(oldAccess, 8*time.Hour), gen) }()
		<-entered
		forgotten := make(chan error, 1)
		go func() { forgotten <- s.ts.Forget() }()
		gate()
		if err := <-adopted; err != nil {
			t.Fatalf("adopt: %v", err)
		}
		if err := <-forgotten; err != nil {
			t.Fatalf("sign out: %v", err)
		}
		if st := s.ts.Status(); st.SignedIn {
			t.Errorf("status %+v", st)
		}
		if _, ok := s.st.get(); ok {
			t.Error("the entry was kept after a Sign out")
		}
	})
	t.Run("new sign-in", func(t *testing.T) {
		s := newSource(t)
		gate, entered := s.st.hold(t, "save")
		gen := s.ts.BeginSignIn()
		adopted := make(chan error, 1)
		go func() { adopted <- s.ts.Adopt(s.pair(oldAccess, 8*time.Hour), gen) }()
		<-entered
		next := s.ts.BeginSignIn()
		gate()
		if err := <-adopted; err != nil {
			t.Fatalf("adopt: %v", err)
		}
		if next <= gen {
			t.Errorf("the sign-in generation went from %d to %d", gen, next)
		}
	})
}

// TestForgetDeleteFailed: a delete that fails keeps the run signed out, and
// Delete again deletes it.
func TestForgetDeleteFailed(t *testing.T) {
	s := newSource(t)
	s.load(s.pair(oldAccess, 8*time.Hour))
	s.st.setErr(nil, nil, errors.New("the keyring is locked"))
	if err := s.ts.Forget(); err == nil {
		t.Fatal("sign out: no error")
	}
	st := s.ts.Status()
	if st.SignedIn || !st.DeleteFailed {
		t.Fatalf("status %+v", st)
	}
	if _, _, _, err := s.ts.Lease(context.Background()); !errors.Is(err, ErrNotSignedIn) {
		t.Errorf("a lease after a failed sign out: %v", err)
	}
	s.st.setErr(nil, nil, nil)
	if err := s.ts.DeleteAgain(); err != nil {
		t.Fatal(err)
	}
	if st := s.ts.Status(); st.DeleteFailed {
		t.Errorf("status %+v after Delete again", st)
	}
	if _, ok := s.st.get(); ok {
		t.Error("the entry was kept")
	}
	if err := s.ts.DeleteAgain(); err != nil {
		t.Errorf("Delete again with nothing to delete: %v", err)
	}
}

// TestClose: Close stops new leases, and waits its limit at most.
func TestClose(t *testing.T) {
	if got := NewTokenSource(NewOAuth(), &store{}).closeWait; got != 35*time.Second {
		t.Errorf("the close wait is %v, want 35s", got)
	}
	s := newSource(t)
	s.load(s.pair(oldAccess, 8*time.Hour))
	if _, _, release := s.lease(context.Background()); release == nil {
		t.Fatal("no release")
	} else {
		release()
	}
	s.ts.closeWait = 10 * time.Millisecond
	s.ts.Close()
	if _, _, _, err := s.ts.Lease(context.Background()); !errors.Is(err, ErrNotSignedIn) {
		t.Errorf("a lease after Close: %v", err)
	}

	// A blocked refresh holds Close no longer than its wait.
	s = newSource(t)
	s.load(s.pair(oldAccess, time.Minute))
	gate, _ := s.st.hold(t, "save")
	go func() {
		if _, _, release, err := s.ts.Lease(context.Background()); err == nil {
			release()
		}
	}()
	waitFor(t, s.ts, "the refresh to start", func() bool { return s.ts.refreshing })
	s.ts.closeWait = 10 * time.Millisecond
	closed := make(chan struct{})
	go func() { s.ts.Close(); close(closed) }()
	waitDone(t, closed, "Close with a blocked refresh")
	gate()
}

// TestRefreshAfterSignOutWritesNothing: a refresh whose answer comes after a
// Sign out writes nothing, so the keyring stays signed out.
func TestRefreshAfterSignOutWritesNothing(t *testing.T) {
	s := newSource(t)
	s.load(s.pair(oldAccess, time.Minute))
	release, entered := s.sign.holdToken(t)
	leased := make(chan error, 1)
	go func() {
		_, _, rel, err := s.ts.Lease(context.Background())
		if err == nil {
			rel()
		}
		leased <- err
	}()
	<-entered // the refresh is in GitHub
	if err := s.ts.Forget(); err != nil {
		t.Fatal(err)
	}
	release()
	waitFor(t, s.ts, "the refresh to end", func() bool { return !s.ts.refreshing })
	select {
	case err := <-leased:
		if !errors.Is(err, ErrNotSignedIn) {
			t.Fatalf("lease: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the lease did not return")
	}
	if saves, _, _ := s.st.counts(); saves != 0 {
		t.Errorf("the refresh saved %d pairs after a Sign out", saves)
	}
	if saved, ok := s.st.get(); ok {
		t.Errorf("the keyring holds the refreshed pair after a Sign out: %s", saved.Access)
	}
	if st := s.ts.Status(); st.SignedIn {
		t.Errorf("status %+v", st)
	}
}

// TestRefreshAfterANewSignInKeepsTheNewPair: a refresh whose answer comes
// after another sign-in writes nothing, so the keyring keeps the new pair.
func TestRefreshAfterANewSignInKeepsTheNewPair(t *testing.T) {
	const adopted = "ghu_ADOPTED0000"
	s := newSource(t)
	s.load(s.pair(oldAccess, time.Minute))
	release, entered := s.sign.holdToken(t)
	leased := make(chan string, 1)
	go func() {
		tok, _, rel, err := s.ts.Lease(context.Background())
		if err == nil {
			rel()
		} else {
			tok = "error: " + err.Error()
		}
		leased <- tok
	}()
	<-entered
	gen := s.ts.BeginSignIn()
	if err := s.ts.Adopt(s.pair(adopted, 8*time.Hour), gen); err != nil {
		t.Fatal(err)
	}
	release()
	waitFor(t, s.ts, "the refresh to end", func() bool { return !s.ts.refreshing })
	select {
	case tok := <-leased:
		if tok != adopted {
			t.Fatalf("the lease gave %q", tok)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the lease did not return")
	}
	if saved, ok := s.st.get(); !ok || saved.Access != adopted {
		t.Errorf("the keyring holds %+v, want the adopted pair", saved)
	}
}

// TestNewPairClearsTheError: Adopt and Load clear the last refresh's error,
// so a later lease does not report it.
func TestNewPairClearsTheError(t *testing.T) {
	s := newSource(t)
	s.load(s.pair(oldAccess, time.Minute))
	s.sign.answer(nil, func(w http.ResponseWriter, n int) {
		writeAnswer(w, 502, `{"message":"Server Error"}`)
	})
	if _, _, _, err := s.ts.Lease(context.Background()); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("lease: %v", err)
	}
	lastErr := func() error {
		s.ts.mu.Lock()
		defer s.ts.mu.Unlock()
		return s.ts.lastErr
	}
	if lastErr() == nil {
		t.Fatal("no refresh error is recorded")
	}
	gen := s.ts.BeginSignIn()
	if err := s.ts.Adopt(s.pair(oldAccess, 8*time.Hour), gen); err != nil {
		t.Fatal(err)
	}
	if err := lastErr(); err != nil {
		t.Errorf("Adopt kept the last error: %v", err)
	}
	// Load clears it too.
	gen = s.ts.BeginSignIn()
	if err := s.ts.Adopt(s.pair(oldAccess, time.Minute), gen); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.ts.Lease(context.Background()); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("the second lease: %v", err)
	}
	if err := s.ts.Load(); err != nil {
		t.Fatal(err)
	}
	if err := lastErr(); err != nil {
		t.Errorf("Load kept the last error: %v", err)
	}
}

// TestStaleRefusedRefreshKeepsTheNewPair: a refresh that answers
// bad_refresh_token after a new pair was adopted ends nothing: the new pair
// and its entry stay.
func TestStaleRefusedRefreshKeepsTheNewPair(t *testing.T) {
	s := newSource(t)
	s.load(s.pair(oldAccess, time.Minute))
	release, entered := s.sign.blockToken(t, 200, `{"error":"bad_refresh_token"}`)
	leased := make(chan error, 1)
	go func() {
		_, _, rel, err := s.ts.Lease(context.Background())
		if err == nil {
			rel()
		}
		leased <- err
	}()
	<-entered // the refresh is in GitHub
	const adopted = "ghu_ADOPTED0000"
	gen := s.ts.BeginSignIn()
	if err := s.ts.Adopt(s.pair(adopted, 8*time.Hour), gen); err != nil {
		t.Fatal(err)
	}
	release()
	waitFor(t, s.ts, "the refresh to end", func() bool { return !s.ts.refreshing })
	select {
	case <-leased:
	case <-time.After(5 * time.Second):
		t.Fatal("the lease did not return")
	}
	if st := s.ts.Status(); !st.SignedIn || st.Login != Login {
		t.Errorf("the new pair is gone: %+v", st)
	}
	if saved, ok := s.st.get(); !ok || saved.Access != adopted {
		t.Errorf("the keyring holds %+v, want the adopted pair", saved)
	}
	if _, deletes, _ := s.st.counts(); deletes != 0 {
		t.Errorf("the ended refresh deleted the new entry: %d", deletes)
	}
}

// TestStaleNoAnswerRefreshKeepsTheNewPair: a refresh with no answer after a
// new pair was adopted changes nothing of the new pair.
func TestStaleNoAnswerRefreshKeepsTheNewPair(t *testing.T) {
	s := newSource(t)
	s.load(s.pair(oldAccess, time.Minute))
	release, entered := s.sign.blockToken(t, 502, `{"message":"Server Error"}`)
	go func() {
		if _, _, rel, err := s.ts.Lease(context.Background()); err == nil {
			rel()
		}
	}()
	<-entered
	const adopted = "ghu_ADOPTED0000"
	gen := s.ts.BeginSignIn()
	if err := s.ts.Adopt(s.pair(adopted, 8*time.Hour), gen); err != nil {
		t.Fatal(err)
	}
	release()
	waitFor(t, s.ts, "the refresh to end", func() bool { return !s.ts.refreshing })
	s.ts.mu.Lock()
	due, lastErr := s.ts.due, s.ts.lastErr
	s.ts.mu.Unlock()
	if due || lastErr != nil {
		t.Errorf("the stale refresh changed the new pair: due %v, err %v", due, lastErr)
	}
	// The new pair leases, with no second refresh.
	tok, _, releaseLease := s.lease(context.Background())
	releaseLease()
	if tok != adopted {
		t.Errorf("the lease gave %q", tok)
	}
	if got := s.sign.tokenRequests(); got != 1 {
		t.Errorf("refresh requests %d, want 1", got)
	}
}

// TestSaveAgainAfterANewPairSavesNothing: Save again writes only the pair the
// keyring did not keep: a pair adopted while it waited for the write mutex
// wins.
func TestSaveAgainAfterANewPairSavesNothing(t *testing.T) {
	const adopted = "ghu_ADOPTED0000"
	s := newSource(t)
	queued := make(chan string, 8)
	s.ts.queued = func(what string) { queued <- what }
	// A pair the keyring did not keep, as a failed save of a refresh leaves
	// it.
	s.load(s.pair(oldAccess, 8*time.Hour))
	s.ts.mu.Lock()
	s.ts.unsaved = true
	s.ts.mu.Unlock()
	// An Adopt holds the write mutex while Save again queues behind it.
	gate, entered := s.st.hold(t, "save")
	adoptedGen := s.ts.BeginSignIn()
	adoptedDone := make(chan error, 1)
	go func() { adoptedDone <- s.ts.Adopt(s.pair(adopted, 8*time.Hour), adoptedGen) }()
	if what := <-queued; what != "adopt" {
		t.Fatalf("first write: %s", what)
	}
	<-entered // the adopt holds the write mutex, in the store
	saveDone := make(chan error, 1)
	go func() { saveDone <- s.ts.SaveAgain() }()
	if what := <-queued; what != "save again" {
		t.Fatalf("second write: %s", what)
	}
	gate()
	if err := <-adoptedDone; err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if err := <-saveDone; err != nil {
		t.Fatalf("save again: %v", err)
	}
	if saved, ok := s.st.get(); !ok || saved.Access != adopted {
		t.Errorf("the keyring holds %+v, want the adopted pair", saved)
	}
	if st := s.ts.Status(); st.Unsaved {
		t.Errorf("status %+v", st)
	}
}

// TestForgetKeepsANewerPair: Sign out deletes only while its own clear is the
// last 1 and no pair is in memory: a sign-in that committed while Sign out
// waited keeps its entry.
func TestForgetKeepsANewerPair(t *testing.T) {
	const adopted = "ghu_ADOPTED0000"
	s := newSource(t)
	s.load(s.pair(oldAccess, 8*time.Hour))
	// Sign out's clear, then a new sign-in that commits before the delete
	// runs: exactly the state its delete checks.
	s.ts.mu.Lock()
	cleared := s.ts.clearLocked()
	s.ts.changeLocked()
	s.ts.mu.Unlock()
	gen := s.ts.BeginSignIn()
	if err := s.ts.Adopt(s.pair(adopted, 8*time.Hour), gen); err != nil {
		t.Fatal(err)
	}
	s.ts.startWork()
	s.ts.wmu.Lock()
	err := s.ts.deleteLocked(cleared)
	s.ts.wmu.Unlock()
	s.ts.endWork()
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if saved, ok := s.st.get(); !ok || saved.Access != adopted {
		t.Errorf("Sign out deleted the newer pair: %+v, %v", saved, ok)
	}
	if st := s.ts.Status(); !st.SignedIn {
		t.Errorf("status %+v", st)
	}
}

// TestDeleteAgainKeepsANewerPair: Delete again deletes only the entry of a
// sign-out: a pair adopted while it waited for the write mutex keeps its
// entry.
func TestDeleteAgainKeepsANewerPair(t *testing.T) {
	const adopted = "ghu_ADOPTED0000"
	s := newSource(t)
	queued := make(chan string, 8)
	s.load(s.pair(oldAccess, 8*time.Hour))
	s.st.setErr(nil, nil, errors.New("the keyring is locked"))
	if err := s.ts.Forget(); err == nil {
		t.Fatal("sign out: no error")
	}
	s.ts.queued = func(what string) { queued <- what }
	if st := s.ts.Status(); !st.DeleteFailed {
		t.Fatalf("status %+v", st)
	}
	s.st.setErr(nil, nil, nil)
	// A new sign-in holds the write mutex while Delete again queues behind
	// it, with the failed delete still in hand.
	gate, entered := s.st.hold(t, "save")
	gen := s.ts.BeginSignIn()
	adoptedDone := make(chan error, 1)
	go func() { adoptedDone <- s.ts.Adopt(s.pair(adopted, 8*time.Hour), gen) }()
	if what := <-queued; what != "adopt" {
		t.Fatalf("first write: %s", what)
	}
	<-entered
	deleted := make(chan error, 1)
	go func() { deleted <- s.ts.DeleteAgain() }()
	if what := <-queued; what != "delete again" {
		t.Fatalf("second write: %s", what)
	}
	gate()
	if err := <-adoptedDone; err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if err := <-deleted; err != nil {
		t.Fatalf("delete again: %v", err)
	}
	if saved, ok := s.st.get(); !ok || saved.Access != adopted {
		t.Errorf("Delete again deleted the new pair: %+v, %v", saved, ok)
	}
	if st := s.ts.Status(); !st.SignedIn {
		t.Errorf("status %+v", st)
	}
}

// TestCloseKeepsARunningRefresh: a refresh that was already running saves its
// pair when Pocket closes: GitHub has spent the old refresh token.
func TestCloseKeepsARunningRefresh(t *testing.T) {
	s := newSource(t)
	s.load(s.pair(oldAccess, time.Minute))
	release, entered := s.sign.blockToken(t, 200, goodPairAnswer())
	go func() {
		if _, _, rel, err := s.ts.Lease(context.Background()); err == nil {
			rel()
		}
	}()
	<-entered // the refresh is in GitHub
	s.ts.closeWait = 10 * time.Second
	closed := make(chan struct{})
	go func() { s.ts.Close(); close(closed) }()
	waitFor(t, s.ts, "Close to start", func() bool { return s.ts.closed })
	release()
	waitDone(t, closed, "Close with the running refresh")
	if saved, ok := s.st.get(); !ok || saved.Access != newAccess {
		t.Errorf("the keyring holds %+v, want the refreshed pair", saved)
	}
	if st := s.ts.Status(); !st.SignedIn {
		t.Errorf("status %+v", st)
	}
}

// TestLeaseWithACancelledContext: a cancelled waiter gets no lease, holds
// none, and starts no refresh.
func TestLeaseWithACancelledContext(t *testing.T) {
	t.Run("a valid pair", func(t *testing.T) {
		s := newSource(t)
		s.load(s.pair(oldAccess, 8*time.Hour))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, release, err := s.ts.Lease(ctx); !errors.Is(err, context.Canceled) || release != nil {
			t.Fatalf("err %v, release %v", err, release != nil)
		}
		s.ts.mu.Lock()
		leases := s.ts.leases
		s.ts.mu.Unlock()
		if leases != 0 {
			t.Errorf("a cancelled waiter holds %d leases", leases)
		}
		if got := s.sign.tokenRequests(); got != 0 {
			t.Errorf("refresh requests %d", got)
		}
	})
	t.Run("a refresh due", func(t *testing.T) {
		s := newSource(t)
		s.load(s.pair(oldAccess, time.Minute))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, _, err := s.ts.Lease(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("err %v", err)
		}
		s.ts.mu.Lock()
		refreshing := s.ts.refreshing
		s.ts.mu.Unlock()
		if refreshing {
			t.Error("a cancelled waiter started a refresh")
		}
		if got := s.sign.tokenRequests(); got != 0 {
			t.Errorf("a cancelled waiter started a refresh: %d requests", got)
		}
	})
}

// TestAStaleRefreshWorkerUsesItsOwnPair: the worker refreshes the pair the
// Lease decided for, never the pair that replaced it: the newer pair's
// refresh token is not spent while its lease runs.
func TestAStaleRefreshWorkerUsesItsOwnPair(t *testing.T) {
	const (
		refreshA = "REFRESH_A_0000"
		refreshB = "REFRESH_B_0000"
		accessB  = "ghu_NEWPAIR0000"
	)
	s := newSource(t)
	now := s.clock.now()
	a := AppToken{ClientID: AppClientID, Login: Login, Access: oldAccess, Refresh: refreshA,
		AccessExpires: now.Add(time.Minute), RefreshExpires: now.Add(180 * 24 * time.Hour)}
	b := AppToken{ClientID: AppClientID, Login: Login, Access: accessB, Refresh: refreshB,
		AccessExpires: now.Add(8 * time.Hour), RefreshExpires: now.Add(180 * 24 * time.Hour)}
	s.load(a)
	// The window: the worker has started and waits at its top; the test
	// replaces the pair, then lets it go on.
	entered, release := make(chan struct{}), make(chan struct{})
	s.ts.beforeRefresh = func() {
		close(entered)
		<-release
	}
	leased := make(chan string, 1)
	go func() {
		tok, _, rel, err := s.ts.Lease(context.Background())
		if err != nil {
			leased <- "error: " + err.Error()
			return
		}
		rel()
		leased <- tok
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh worker did not start")
	}
	gen := s.ts.BeginSignIn()
	if err := s.ts.Adopt(b, gen); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	// The waiting job now leases the new pair, while the old worker holds.
	select {
	case got := <-leased:
		if got != accessB {
			t.Fatalf("the lease gave %q, want the new pair", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the lease did not return")
	}
	close(release)
	waitFor(t, s.ts, "the refresh to end", func() bool { return !s.ts.refreshing })
	_, forms, _ := s.sign.seen()
	var sent []string
	for _, form := range forms {
		if f := form.Get("refresh_token"); f != "" {
			sent = append(sent, f)
		}
	}
	if !slices.Equal(sent, []string{refreshA}) {
		t.Errorf("the refresh sent %v, want only A's refresh token", sent)
	}
	if saved, ok := s.st.get(); !ok || saved.Access != accessB || saved.Refresh != refreshB {
		t.Errorf("the keyring holds %+v, want the new pair", saved)
	}
	if st := s.ts.Status(); !st.SignedIn || st.Login != Login {
		t.Errorf("status %+v", st)
	}
	// The new pair still leases, and its tokens are untouched.
	tok, _, done := s.lease(context.Background())
	done()
	if tok != accessB {
		t.Errorf("the lease gave %q", tok)
	}
}
