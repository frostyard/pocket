package github

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// TokenSource gives the GitHub jobs a leased access token, and refreshes
// the pair when it nears its end
// (docs/adr/0010-act-as-the-owner-through-a-github-app-user-token.md,
// section "Leases and refresh"):
//
//   - a lease needs 10 minutes of validity and a token not marked spent;
//   - when a refresh is due no new lease starts, and the refresh waits for
//     every lease to end, so it never cuts off a job;
//   - 1 refresh at a time, on its own goroutine and context, so a waiter
//     that leaves does not stop it;
//   - 1 small mutex for the state, never held across a network or keyring
//     call, and 1 write mutex for every credential write. Status never
//     waits.

const (
	// leaseValidity is the validity a lease needs: a job ends within 5
	// minutes, so a leased token never expires inside it.
	leaseValidity = 10 * time.Minute
	// refreshTimeout is how long 1 refresh may take.
	refreshTimeout = 30 * time.Second
	// closeWaitDefault is how long Close waits for a refresh or a write in
	// progress.
	closeWaitDefault = 35 * time.Second
)

// AppStore keeps the pair: the desktop keyring in the app, a map in tests.
// config.GitHubApp meets it.
type AppStore interface {
	Load() (AppToken, error)
	Save(AppToken) error
	Forget() error
}

// AppStatus is what Settings shows: no token, so it never leaks 1.
type AppStatus struct {
	SignedIn       bool
	Login          string
	RefreshExpires time.Time
	Unsaved        bool
	DeleteFailed   bool
	Reason         string
}

// TokenSource leases the access token of 1 GitHub App sign-in.
type TokenSource struct {
	oauth *OAuth
	store AppStore

	// mu guards the state below, and is never held across a network or
	// keyring call.
	mu      sync.Mutex
	changed chan struct{} // closed and replaced at each state change

	tok        AppToken // the pair in memory
	gen        int      // the token generation of tok
	have       bool     // tok is a pair Pocket may lease
	signInGen  int      // the sign-in generation
	clearSeq   int      // how many times the pair was forgotten
	leases     int      // leases handed out and not released
	due        bool     // a refresh is due: no new lease starts
	refreshing bool     // 1 refresh runs
	refreshSeq int      // how many refreshes ended
	lastErr    error    // what the last refresh gave
	spent      bool     // GitHub refused the token: the next lease refreshes
	unsaved    bool     // the keyring did not keep tok
	deleteFail bool     // the keyring did not delete the entry
	reason     string   // why the stored sign-in is not usable
	closed     bool

	// wmu serializes every credential write: Adopt, SaveAgain, DeleteAgain,
	// Forget and the save after a refresh.
	wmu sync.Mutex

	// bmu guards busy, the number of a refresh or a credential write in
	// progress; idle closes when it reaches 0.
	bmu  sync.Mutex
	busy int
	idle chan struct{}

	// now is the clock; nil is the OAuth's clock, else time.Now. Tests
	// inject it.
	now func() time.Time
	// closeWait is how long Close waits; 35 seconds unless a test sets it.
	closeWait time.Duration
}

// NewTokenSource is a source of leases over o's sign-in, kept in s.
func NewTokenSource(o *OAuth, s AppStore) *TokenSource {
	return &TokenSource{oauth: o, store: s, changed: make(chan struct{}), closeWait: closeWaitDefault}
}

func (t *TokenSource) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	if t.oauth != nil && t.oauth.now != nil {
		return t.oauth.now()
	}
	return time.Now()
}

// changeLocked wakes every waiter: the state changed.
func (t *TokenSource) changeLocked() {
	close(t.changed)
	t.changed = make(chan struct{})
}

// Load reads the store once: no entry is no sign-in and no error, a value
// that is not a sign-in sets the reason, and any other error comes back (an
// unsupported or locked keyring turns Approve + merge off).
func (t *TokenSource) Load() error {
	tok, err := t.store.Load()
	if err != nil {
		var bad *BadSignIn
		if errors.As(err, &bad) {
			t.mu.Lock()
			t.reason = bad.Error()
			t.changeLocked()
			t.mu.Unlock()
			return nil
		}
		if errors.Is(err, ErrNoSignIn) {
			return nil
		}
		return err
	}
	if !tok.valid() {
		t.mu.Lock()
		t.reason = (&BadSignIn{Reason: "a field is empty"}).Error()
		t.changeLocked()
		t.mu.Unlock()
		return nil
	}
	t.mu.Lock()
	t.tok, t.have = tok, true
	t.gen++
	t.due, t.spent, t.unsaved, t.deleteFail, t.reason = false, false, false, false, ""
	t.lastErr = nil
	t.changeLocked()
	t.mu.Unlock()
	return nil
}

// Lease gives an access token, its token generation and a release, which
// the caller must call when its job ends. It waits while a refresh is due
// and a lease or another refresh holds the pair; ctx ends that wait.
func (t *TokenSource) Lease(ctx context.Context) (token string, tokenGen int, release func(), err error) {
	// seq is the count of the refreshes that ended when this waiter last
	// slept: a waiter reports the failure of the refresh it waited for, and
	// a lease that comes later tries again.
	seq := -1
	for {
		t.mu.Lock()
		if t.closed {
			t.mu.Unlock()
			return "", 0, nil, fmt.Errorf("%w: Pocket is closing", ErrNotSignedIn)
		}
		if seq >= 0 && t.lastErr != nil && (!t.have || t.refreshSeq != seq) {
			// The refresh this waiter waited for failed: it reports it, and
			// a later lease tries again.
			err := t.lastErr
			t.mu.Unlock()
			return "", 0, nil, err
		}
		if !t.have {
			t.mu.Unlock()
			return "", 0, nil, ErrNotSignedIn
		}
		if t.unsaved {
			t.mu.Unlock()
			return "", 0, nil, ErrUnsaved
		}
		if !t.spent && !t.due && t.tok.AccessExpires.Sub(t.clock()) >= leaseValidity {
			t.leases++
			gen, tok := t.gen, t.tok.Access
			t.mu.Unlock()
			return tok, gen, t.release, nil
		}
		// A refresh is due: no new lease starts.
		if !t.due {
			t.due = true
			t.changeLocked()
		}
		if !t.refreshing && t.leases == 0 {
			if !t.tok.RefreshExpires.After(t.clock()) {
				// The refresh token expired: the sign-in ended.
				t.clearLocked()
				t.changeLocked()
				t.mu.Unlock()
				t.deleteEntry()
				return "", 0, nil, ErrSignIn
			}
			t.refreshing = true
			t.startWork()
			go t.refresh()
		}
		seq = t.refreshSeq
		ch := t.changed
		t.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return "", 0, nil, ctx.Err()
		}
	}
}

// release ends 1 lease.
func (t *TokenSource) release() {
	t.mu.Lock()
	if t.leases > 0 {
		t.leases--
	}
	t.changeLocked()
	t.mu.Unlock()
}

// refresh trades the refresh token for a new pair, saves it, and lets the
// leases go on: 1 at a time, on its own context and goroutine.
func (t *TokenSource) refresh() {
	defer t.endWork()
	defer t.endRefresh()
	t.mu.Lock()
	tok, gen := t.tok, t.gen
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()
	next, err := t.oauth.Refresh(ctx, tok.Refresh)
	switch {
	case err == nil:
	case errors.Is(err, ErrSignIn):
		// The refresh token is spent, refused or expired: the sign-in ended.
		t.mu.Lock()
		t.clearLocked()
		t.lastErr = err
		t.changeLocked()
		t.mu.Unlock()
		t.deleteEntry()
		return
	default:
		// No answer: the entry stays, because the refresh may have landed;
		// the waiters fail and the next lease tries again.
		t.mu.Lock()
		t.due = true
		t.lastErr = fmt.Errorf("%w: could not refresh the GitHub sign-in", ErrNoAnswer)
		t.mu.Unlock()
		return
	}
	if next.ClientID != tok.ClientID {
		t.mu.Lock()
		t.clearLocked()
		t.lastErr = fmt.Errorf("%w: the new pair is for another build of Pocket", ErrSignIn)
		t.changeLocked()
		t.mu.Unlock()
		t.deleteEntry()
		return
	}
	// A refresh names no login: the pair keeps the current one.
	next.Login = tok.Login
	// The store holds the new pair before a lease may use it, and only
	// while the pair it refreshes is still the current one: a Sign out or a
	// new sign-in that ran while the refresh was in flight wins, and the
	// keyring keeps what that left.
	saved, serr := t.saveRefresh(next, gen)
	t.mu.Lock()
	if saved && t.have && t.gen == gen && !t.closed {
		t.tok, t.gen = next, t.gen+1
		t.due, t.spent, t.unsaved = false, false, serr != nil
		if serr == nil {
			t.reason = ""
			t.lastErr = nil
		}
	}
	t.mu.Unlock()
}

// endRefresh says the 1 refresh is over, and wakes the waiters: the next
// count tells them whether the refresh they waited for failed.
func (t *TokenSource) endRefresh() {
	t.mu.Lock()
	t.refreshing = false
	t.refreshSeq++
	t.changeLocked()
	t.mu.Unlock()
}

// clearLocked forgets the pair and ends the sign-in generation: no lease
// starts after it, and the jobs that hold 1 finish. The caller wakes the
// waiters.
func (t *TokenSource) clearLocked() {
	t.tok, t.have = AppToken{}, false
	t.signInGen++
	t.clearSeq++
	t.due, t.spent, t.unsaved, t.lastErr = false, false, false, nil
}

// deleteEntry deletes the entry, under the write mutex.
func (t *TokenSource) deleteEntry() {
	t.startWork()
	defer t.endWork()
	t.wmu.Lock()
	err := t.store.Forget()
	t.wmu.Unlock()
	t.mu.Lock()
	t.deleteFail = err != nil
	t.changeLocked()
	t.mu.Unlock()
}

// saveRefresh writes the refreshed pair under the write mutex, as a
// credential write Close waits for, but only while gen is still the current
// pair: it gives false, and writes nothing, when a Sign out or another
// sign-in ran during the refresh.
func (t *TokenSource) saveRefresh(next AppToken, gen int) (bool, error) {
	t.startWork()
	defer t.endWork()
	t.wmu.Lock()
	defer t.wmu.Unlock()
	t.mu.Lock()
	stale := !t.have || t.gen != gen || t.closed
	t.mu.Unlock()
	if stale {
		return false, nil
	}
	if err := t.store.Save(next); err != nil {
		return true, fmt.Errorf("%w: %s", ErrUnsaved, Redact(err, next.secrets()...))
	}
	return true, nil
}

// startWork counts a refresh or a credential write in progress.
func (t *TokenSource) startWork() {
	t.bmu.Lock()
	t.busy++
	t.bmu.Unlock()
}

// endWork ends 1.
func (t *TokenSource) endWork() {
	t.bmu.Lock()
	t.busy--
	if t.busy == 0 && t.idle != nil {
		close(t.idle)
		t.idle = nil
	}
	t.bmu.Unlock()
}

// BeginSignIn starts a sign-in: it adds 1 to the sign-in generation, and
// Adopt must carry it.
func (t *TokenSource) BeginSignIn() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.signInGen++
	t.changeLocked()
	return t.signInGen
}

// CancelSignIn cancels sign-in gen: a sign-in that has not committed saves
// nothing. A cancel of an older generation changes nothing.
func (t *TokenSource) CancelSignIn(gen int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if gen != t.signInGen {
		return
	}
	t.signInGen++
	t.changeLocked()
}

// Adopt saves tok as the sign-in of generation gen: ErrStale when that
// generation is no longer current, ErrUnsaved when the keyring keeps
// nothing.
func (t *TokenSource) Adopt(tok AppToken, gen int) error {
	if !tok.valid() {
		return fmt.Errorf("%w: the sign-in is incomplete", ErrUnsaved)
	}
	t.startWork()
	defer t.endWork()
	t.wmu.Lock()
	defer t.wmu.Unlock()
	t.mu.Lock()
	stale := gen != t.signInGen
	cleared := t.clearSeq
	t.mu.Unlock()
	if stale {
		return ErrStale
	}
	if err := t.store.Save(tok); err != nil {
		return fmt.Errorf("%w: %s", ErrUnsaved, Redact(err, tok.secrets()...))
	}
	t.mu.Lock()
	// A Sign out while the save ran applies after it: the pair does not
	// come back.
	if t.clearSeq == cleared {
		t.tok, t.have = tok, true
		t.gen++
		t.due, t.spent, t.unsaved, t.deleteFail, t.reason = false, false, false, false, ""
		t.lastErr = nil
	}
	t.changeLocked()
	t.mu.Unlock()
	return nil
}

// SaveAgain saves the pair the keyring did not keep.
func (t *TokenSource) SaveAgain() error {
	t.mu.Lock()
	tok, unsaved := t.tok, t.unsaved
	t.mu.Unlock()
	if !unsaved {
		return nil
	}
	t.startWork()
	defer t.endWork()
	t.wmu.Lock()
	defer t.wmu.Unlock()
	if err := t.store.Save(tok); err != nil {
		return fmt.Errorf("%w: %s", ErrUnsaved, Redact(err, tok.secrets()...))
	}
	t.mu.Lock()
	t.unsaved = false
	t.changeLocked()
	t.mu.Unlock()
	return nil
}

// DeleteAgain deletes the entry a Sign out could not delete.
func (t *TokenSource) DeleteAgain() error {
	t.mu.Lock()
	failed := t.deleteFail
	t.mu.Unlock()
	if !failed {
		return nil
	}
	t.startWork()
	defer t.endWork()
	t.wmu.Lock()
	err := t.store.Forget()
	t.wmu.Unlock()
	t.mu.Lock()
	t.deleteFail = err != nil
	t.changeLocked()
	t.mu.Unlock()
	if err != nil {
		return fmt.Errorf("the keyring did not delete the GitHub sign-in: %s", err)
	}
	return nil
}

// Forget is Sign out: no new lease starts, the jobs that hold 1 finish, and
// the entry goes. A delete that fails keeps DeleteFailed for Delete again.
func (t *TokenSource) Forget() error {
	t.mu.Lock()
	t.clearLocked()
	t.changeLocked()
	t.mu.Unlock()
	t.startWork()
	defer t.endWork()
	t.wmu.Lock()
	err := t.store.Forget()
	t.wmu.Unlock()
	t.mu.Lock()
	t.deleteFail = err != nil
	t.changeLocked()
	t.mu.Unlock()
	if err != nil {
		return fmt.Errorf("the keyring still holds the GitHub sign-in: %s", err)
	}
	return nil
}

// Invalidate marks the pair of tokenGen spent, after a 401: an older
// generation changes nothing, and the current one refreshes at the next
// lease.
func (t *TokenSource) Invalidate(tokenGen int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.have || tokenGen != t.gen || t.spent {
		return
	}
	t.spent = true
	t.changeLocked()
}

// Status copies the state: it never waits, not on a refresh and not on a
// write.
func (t *TokenSource) Status() AppStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := AppStatus{
		SignedIn:     t.have,
		Unsaved:      t.unsaved,
		DeleteFailed: t.deleteFail,
		Reason:       t.reason,
	}
	if t.have {
		st.Login = t.tok.Login
		st.RefreshExpires = t.tok.RefreshExpires
	}
	return st
}

// Close stops new leases and waits closeWait (35 seconds) at most for a
// refresh or a credential write in progress. The sign-in lock goes only
// with the process, never here.
func (t *TokenSource) Close() {
	t.mu.Lock()
	t.closed = true
	t.changeLocked()
	t.mu.Unlock()
	deadline := time.Now().Add(t.closeWait)
	for {
		t.bmu.Lock()
		if t.busy == 0 {
			t.bmu.Unlock()
			return
		}
		if t.idle == nil {
			t.idle = make(chan struct{})
		}
		ch := t.idle
		t.bmu.Unlock()
		left := time.Until(deadline)
		if left <= 0 {
			return
		}
		timer := time.NewTimer(left)
		select {
		case <-ch:
			timer.Stop()
		case <-timer.C:
			return
		}
	}
}
