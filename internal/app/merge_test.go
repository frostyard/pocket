package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostyard/frostui/frostuitest"

	"github.com/frostyard/pocket/internal/github"
	"github.com/frostyard/pocket/internal/pocket"
)

// testGitHubToken is an obviously fake token.
const testGitHubToken = "test-token-not-real"

// fakeGH is GitHub as the app sees it: deterministic answers, no waits.
type fakeGH struct {
	mu       sync.Mutex
	previews map[string]github.Preview
	errs     map[string]error
	// act answers Act; nil merges. It runs on the worker goroutine.
	act    func(in github.Intent, op string, step func(string) error) github.Result
	settle func(in github.Intent, wait bool) github.Result
	states map[string]github.PRState
	login  string
	rows   map[string]github.PreflightRow
	// app is what CheckApp answers; nil is both owners ready, as login.
	app      *github.AppCheck
	appErr   error
	appReads int
	// appGate holds CheckApp call appGateAt until it is closed, and
	// appEntered closes when that call enters.
	appGate    chan struct{}
	appGateAt  int
	appEntered chan struct{}
	leaseErr   error
	taken      int
	released   int

	acts, settles, stateReads, previewReads []string
}

func newFakeGH() *fakeGH {
	return &fakeGH{previews: map[string]github.Preview{}, errs: map[string]error{}, states: map[string]github.PRState{}, login: github.Login}
}

func (f *fakeGH) Preview(_ context.Context, r github.Ref) (github.Preview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.previewReads = append(f.previewReads, r.String())
	if err := f.errs[r.String()]; err != nil {
		return github.Preview{Ref: r}, err
	}
	p, ok := f.previews[r.String()]
	if !ok {
		return github.Preview{Ref: r}, errors.New("no such PR")
	}
	return p, nil
}

func (f *fakeGH) Act(_ context.Context, in github.Intent, op string, step func(string) error) github.Result {
	f.mu.Lock()
	f.acts = append(f.acts, in.Preview.Ref.String())
	act := f.act
	f.mu.Unlock()
	if act == nil {
		if err := step("about to approve"); err != nil {
			return github.Result{Op: op, Kind: "refused", Step: "approve", Reason: "journal write failed", Err: err}
		}
		if err := step("about to merge"); err != nil {
			return github.Result{Op: op, Kind: "approved", Step: "merge", Reason: "journal write failed", Err: err}
		}
		return github.Result{Op: op, Kind: "merged", Step: "merge", Method: in.Preview.Method, MergeCommit: strings.Repeat("c", 40)}
	}
	res := act(in, op, step)
	res.Op = op
	return res
}

func (f *fakeGH) Settle(_ context.Context, in github.Intent, op string, wait bool) github.Result {
	f.mu.Lock()
	f.settles = append(f.settles, in.Preview.Ref.String())
	settle := f.settle
	f.mu.Unlock()
	if settle == nil {
		return github.Result{Op: op, Kind: "unknown", Step: "settle", Reason: "no effect seen"}
	}
	res := settle(in, wait)
	res.Op = op
	return res
}

func (f *fakeGH) State(_ context.Context, r github.Ref) (github.PRState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stateReads = append(f.stateReads, r.String())
	return f.states[r.String()], nil
}

// CheckApp answers with app, or both owners ready, as login.
func (f *fakeGH) CheckApp(_ context.Context, owners []string) (github.AppCheck, error) {
	f.mu.Lock()
	f.appReads++
	n := f.appReads
	gate, entered := f.appGate, f.appEntered
	wait := gate != nil && f.appGateAt == n
	// The answer is the 1 there is when the call enters, not after a wait: a
	// new sign-in that changes it must not change this call's answer.
	check, err := f.appAnswerLocked(owners)
	f.mu.Unlock()
	if wait {
		if entered != nil {
			close(entered)
		}
		<-gate
	}
	return check, err
}

// appAnswerLocked is 1 call's answer, taken under the lock.
func (f *fakeGH) appAnswerLocked(owners []string) (github.AppCheck, error) {
	if f.appErr != nil {
		return github.AppCheck{}, f.appErr
	}
	if f.app != nil {
		return *f.app, nil
	}
	check := github.AppCheck{Login: f.login, Owners: map[string]string{}, Notes: map[string]string{}}
	for _, owner := range owners {
		check.Owners[owner] = ""
	}
	return check, nil
}

// holdCheck makes the next CheckApp call wait, and gives its release and the
// channel that closes when that call enters.
func (f *fakeGH) holdCheck(t *testing.T) (release func(), entered chan struct{}) {
	t.Helper()
	f.mu.Lock()
	n := f.appReads + 1
	f.mu.Unlock()
	return f.holdCheckAt(t, n)
}

// holdCheckAt makes CheckApp call n wait until the release is called, and
// gives the channel that closes when that call enters. The call's answer is
// the 1 there is when it enters, so a test may change the answer for the
// calls after it.
func (f *fakeGH) holdCheckAt(t *testing.T, n int) (release func(), entered chan struct{}) {
	t.Helper()
	gate, entered := make(chan struct{}), make(chan struct{})
	f.mu.Lock()
	f.appGate, f.appGateAt, f.appEntered = gate, n, entered
	f.mu.Unlock()
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return release, entered
}

// setApp is what CheckApp answers next.
func (f *fakeGH) setApp(check *github.AppCheck) {
	f.mu.Lock()
	f.app = check
	f.mu.Unlock()
}

// setAppErr is what CheckApp fails with next.
func (f *fakeGH) setAppErr(err error) {
	f.mu.Lock()
	f.appErr = err
	f.mu.Unlock()
}

// lease is the app's Options.GitHub: a client, a token generation and the
// release, counted.
func (f *fakeGH) lease(_ context.Context, _ string) (ghAPI, int, func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.leaseErr != nil {
		return nil, 0, nil, f.leaseErr
	}
	f.taken++
	released := false
	return f, f.taken, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if !released {
			released = true
			f.released++
		}
	}, nil
}

// leases are how many leases the jobs took and released.
func (f *fakeGH) leases() (taken, released int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.taken, f.released
}

func (f *fakeGH) Preflight(_ context.Context, r github.Ref) github.PreflightRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row, ok := f.rows[r.Owner+"/"+r.Name]; ok {
		return row
	}
	return github.PreflightRow{Repo: r.Owner + "/" + r.Name, Readable: true, Bypass: "never"}
}

func (f *fakeGH) actsSoFar() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.acts)
}

// fakeAuth is the App's sign-in in memory: the keyring entry, the device
// flow and the leases. It counts what the app asks it to do.
type fakeAuth struct {
	mu   sync.Mutex
	tok  github.AppToken
	have bool
	// openErr is what Open fails with; held gives ErrSignInHeld.
	openErr error
	// startErr, code and poll answer the device flow.
	startErr error
	code     github.DeviceCode
	poll     github.AppToken
	pollErr  error
	// pollFn, when set, answers Poll instead of poll/pollErr.
	pollFn func(ctx context.Context) (github.AppToken, error)
	// adoptErr, saveErr, deleteErr and forgetErr refuse those writes.
	adoptErr, saveErr, deleteErr, forgetErr error
	// reason is the BadSignIn reason Load would give.
	reason     string
	legacy     bool
	unsaved    bool
	deleteFail bool
	// adoptGate holds Adopt until it is closed; openGate holds Open; onClose
	// runs in Close.
	adoptGate chan struct{}
	openGate  chan struct{}
	onClose   func()

	gen       int
	starts    int
	begins    int
	cancelled []int
	adopted   []github.AppToken
	adoptGens []int
	invalid   []int
	opens     int
	closes    int
	saves     int
	deletes   int
	forgets   int
	legacyOff int
	statuses  int
}

func newFakeAuth() *fakeAuth {
	return &fakeAuth{
		tok:  github.AppToken{ClientID: github.AppClientID, Login: github.Login, Access: testGitHubToken, Refresh: "refresh-not-real", AccessExpires: time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC), RefreshExpires: time.Date(2027, 5, 13, 12, 0, 0, 0, time.UTC)},
		have: true,
		code: github.DeviceCode{DeviceCode: "device-code-not-real", UserCode: "ABCD-1234", Expires: time.Date(2026, 11, 14, 12, 15, 0, 0, time.UTC), Interval: 5 * time.Second},
		poll: github.AppToken{ClientID: github.AppClientID, Access: testGitHubToken, Refresh: "refresh-not-real",
			AccessExpires: time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC), RefreshExpires: time.Date(2027, 5, 13, 12, 0, 0, 0, time.UTC)},
	}
}

func (f *fakeAuth) Open() error {
	f.mu.Lock()
	f.opens++
	gate, err := f.openGate, f.openErr
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return err
}

func (f *fakeAuth) Start(context.Context) (github.DeviceCode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	if f.startErr != nil {
		return github.DeviceCode{}, f.startErr
	}
	return f.code, nil
}

func (f *fakeAuth) Poll(ctx context.Context, _ github.DeviceCode) (github.AppToken, error) {
	f.mu.Lock()
	fn, tok, err := f.pollFn, f.poll, f.pollErr
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return tok, err
}

func (f *fakeAuth) BeginSignIn() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gen++
	f.begins++
	return f.gen
}

func (f *fakeAuth) CancelSignIn(gen int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, gen)
}

func (f *fakeAuth) Adopt(tok github.AppToken, gen int) error {
	f.mu.Lock()
	gate := f.adoptGate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.adoptErr != nil {
		return f.adoptErr
	}
	if gen != f.gen {
		return github.ErrStale
	}
	f.adopted = append(f.adopted, tok)
	f.adoptGens = append(f.adoptGens, gen)
	f.tok, f.have, f.unsaved = tok, true, false
	return nil
}

func (f *fakeAuth) SaveAgain() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves++
	if f.saveErr != nil {
		return f.saveErr
	}
	f.unsaved = false
	return nil
}

func (f *fakeAuth) DeleteAgain() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes++
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleteFail = false
	return nil
}

func (f *fakeAuth) Invalidate(tokenGen int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invalid = append(f.invalid, tokenGen)
}

func (f *fakeAuth) Forget() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgets++
	f.gen++
	// The pair leaves memory at once, whatever the keyring does.
	f.tok, f.have, f.unsaved = github.AppToken{}, false, false
	if f.forgetErr != nil {
		f.deleteFail = true
		return f.forgetErr
	}
	f.deleteFail = false
	return nil
}

func (f *fakeAuth) Status() github.AppStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses++
	return github.AppStatus{SignedIn: f.have, Login: f.tok.Login, RefreshExpires: f.tok.RefreshExpires,
		Unsaved: f.unsaved, DeleteFailed: f.deleteFail, Reason: f.reason}
}

func (f *fakeAuth) Legacy() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.legacy, nil
}

func (f *fakeAuth) ForgetLegacy() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.legacyOff++
	f.legacy = false
	return nil
}

func (f *fakeAuth) Close() {
	f.mu.Lock()
	onClose := f.onClose
	f.closes++
	f.mu.Unlock()
	if onClose != nil {
		onClose()
	}
}

// holdOpen makes Open wait until the release is called.
func (f *fakeAuth) holdOpen(t *testing.T) (release func()) {
	t.Helper()
	gate := make(chan struct{})
	f.mu.Lock()
	f.openGate = gate
	f.mu.Unlock()
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return release
}

// signInCounts are BeginSignIn and Start calls, and the generation.
func (f *fakeAuth) signInCounts() (begins, starts, gen int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.begins, f.starts, f.gen
}

// holdAdopt makes Adopt wait until the release is called.
func (f *fakeAuth) holdAdopt(t *testing.T) (release func()) {
	t.Helper()
	gate := make(chan struct{})
	f.mu.Lock()
	f.adoptGate = gate
	f.mu.Unlock()
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return release
}

// setUnsaved makes the sign-in 1 the keyring did not keep.
func (f *fakeAuth) setUnsaved(on bool) {
	f.mu.Lock()
	f.unsaved = on
	f.mu.Unlock()
}

// setForgetErr is what Forget fails with.
func (f *fakeAuth) setForgetErr(err error) {
	f.mu.Lock()
	f.forgetErr = err
	f.mu.Unlock()
}

// setOpenErr is what Open fails with.
func (f *fakeAuth) setOpenErr(err error) {
	f.mu.Lock()
	f.openErr = err
	f.mu.Unlock()
}

// setReason is the BadSignIn reason Load gave.
func (f *fakeAuth) setReason(reason string) {
	f.mu.Lock()
	f.reason = reason
	f.mu.Unlock()
}

// setPollErr is what the poll fails with.
func (f *fakeAuth) setPollErr(err error) {
	f.mu.Lock()
	f.pollErr = err
	f.mu.Unlock()
}

// setPollFn answers Poll instead of the pair.
func (f *fakeAuth) setPollFn(fn func(ctx context.Context) (github.AppToken, error)) {
	f.mu.Lock()
	f.pollFn = fn
	f.mu.Unlock()
}

// holdPoll makes the device flow's poll wait until the test ends. A poll that
// answers at once can end the code's phase in the same flush, before
// RunUntil sees it (#108, #118).
func (f *fakeAuth) holdPoll(t *testing.T) {
	t.Helper()
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	f.setPollFn(func(context.Context) (github.AppToken, error) {
		<-hold
		return github.AppToken{}, context.Canceled
	})
}

// latePoll holds a sign-in's poll until the test gives it a pair, and tells
// when the poll's answer ran on the UI thread (#118). The fake code's expiry
// is past, so a poll that waits on its context answers at once.
type latePoll struct {
	pair   chan github.AppToken
	sent   bool
	posted chan struct{}
	once   sync.Once
	ran    atomic.Bool
}

// holdLatePoll makes the fake poll wait for the test's pair, and wraps a.post
// to mark the post that pollSignIn's goroutine makes, found by its stack:
// the countdown and other reads post through a.post too.
func holdLatePoll(t *testing.T, a *App, auth *fakeAuth) *latePoll {
	t.Helper()
	l := &latePoll{pair: make(chan github.AppToken, 1), posted: make(chan struct{})}
	auth.setPollFn(func(context.Context) (github.AppToken, error) {
		tok, ok := <-l.pair
		if !ok {
			return github.AppToken{}, context.Canceled
		}
		return tok, nil
	})
	base := a.post
	a.post = func(fn func()) {
		if strings.Contains(string(debug.Stack()), ").pollSignIn.func") {
			inner := fn
			fn = func() { inner(); l.ran.Store(true) }
			l.once.Do(func() { close(l.posted) })
		}
		base(fn)
	}
	// A test that ends before it gave the pair lets the poll return.
	t.Cleanup(func() {
		if !l.sent {
			close(l.pair)
		}
	})
	return l
}

// answer gives the poll its pair, and runs the loop until the poll's answer
// ran on the UI thread. The pair channel has 1 slot: a poll that never
// started fails at the timeout, not in a send that waits.
func (l *latePoll) answer(t *testing.T, loop *frostuitest.Loop, tok github.AppToken) {
	t.Helper()
	l.sent = true
	l.pair <- tok
	close(l.pair)
	select {
	case <-l.posted:
	case <-time.After(5 * time.Second):
		t.Fatal("the poll's answer was not posted")
	}
	loop.RunUntil(t, l.ran.Load)
}

// opens is how many times the app opened the sign-in.
func (f *fakeAuth) openCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens
}

// closeCount is how many times the app closed the sign-in.
func (f *fakeAuth) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes
}

// setOnClose is what Close runs, to see the order of the end.
func (f *fakeAuth) setOnClose(fn func()) {
	f.mu.Lock()
	f.onClose = fn
	f.mu.Unlock()
}

// forgetCount is how many Sign outs the app made.
func (f *fakeAuth) forgetCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.forgets
}

// appCount is how many times CheckApp answered.
func (f *fakeGH) appCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.appReads
}

// cancelCount is how many sign-ins the app cancelled.
func (f *fakeAuth) cancelCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cancelled)
}

// setLegacy says whether 1 of the 2 old token keys is kept.
func (f *fakeAuth) setLegacy(on bool) {
	f.mu.Lock()
	f.legacy = on
	f.mu.Unlock()
}

// setCodeExpiry is when the code the device flow gives ends.
func (f *fakeAuth) setCodeExpiry(at time.Time) {
	f.mu.Lock()
	f.code.Expires = at
	f.mu.Unlock()
}

// setPoll is the pair the device flow gives next.
func (f *fakeAuth) setPoll(tok github.AppToken) {
	f.mu.Lock()
	f.poll = tok
	f.mu.Unlock()
}

// set changes what the sign-in holds, as the keyring would.
func (f *fakeAuth) set(tok github.AppToken, have bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tok, f.have = tok, have
}

// get is the pair the keyring holds, and whether it holds 1.
func (f *fakeAuth) get() (github.AppToken, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tok, f.have
}

// adoptCount is how many pairs the app saved.
func (f *fakeAuth) adoptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.adopted)
}

// adoptedPairs are the pairs the app saved, in order.
func (f *fakeAuth) adoptedPairs() []github.AppToken {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.adopted)
}

// adoptGenerations are the generations the app adopted with, in order.
func (f *fakeAuth) adoptGenerations() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.adoptGens)
}

// legacyOffCount is how many times the app deleted the old keys.
func (f *fakeAuth) legacyOffCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.legacyOff
}

// invalidated is every generation the app invalidated.
func (f *fakeAuth) invalidated() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.invalid)
}

func oid(c byte) string { return strings.Repeat(string(c), 40) }

// readyPR is a Desk row for ref at head.
func readyPR(ref, head string) pocket.ReadyPR {
	return pocket.ReadyPR{Ref: ref, Title: "Change " + ref, URL: "https://example.org/not-used", Head: head[:7], Checks: "SUCCESS",
		Level: "L3", Thread: "T1", Agents: []string{"duncan"}, At: pocket.Millis(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC).UnixMilli())}
}

// preview is an eligible preview of ref at head.
func preview(ref, head string) github.Preview {
	r, _ := github.ParseRef(ref)
	return github.Preview{Ref: r, NodeID: "PR_" + ref, Title: "Change " + ref, BaseRef: "main", BaseOid: oid('b'), Head: head,
		Method: "merge", Eligible: true}
}

// mergeApp is teamApp with Approve + merge on, a fake GitHub, and refs
// ready on the Desk, in Inbox order (each a minute older than the next).
func mergeApp(t *testing.T, refs ...string) (*App, *frostuitest.Loop, *fakeGH, *fakeAuth) {
	t.Helper()
	a, loop := teamApp(t)
	gh := newFakeGH()
	auth := newFakeAuth()
	a.opts.GitHubAuth = auth
	a.gh.api = func(ctx context.Context, owner string) (ghAPI, int, func(), error) { return gh.lease(ctx, owner) }
	a.gh.newClient = func(string) ghAPI { return gh }
	a.desk.view.Ready = nil
	for i, ref := range refs {
		head := oid("0123456789abcdef"[i%16])
		p := readyPR(ref, head)
		p.At = pocket.Millis(time.Date(2026, 11, 12, 10, i, 0, 0, time.UTC).UnixMilli())
		a.desk.view.Ready = append(a.desk.view.Ready, p)
		gh.previews[ref] = preview(ref, head)
	}
	a.invalidateDerived()
	return a, loop, gh, auth
}

// startMerge starts the feature and reads the previews.
func startMerge(t *testing.T, a *App, loop *frostuitest.Loop) {
	t.Helper()
	a.startGitHub()
	loop.RunUntil(t, func() bool {
		return a.gh.phase == signInSignedIn && a.gh.owners[github.Login].known && a.gh.owners["frostyard"].known
	})
	a.githubDeskRead()
	loop.RunUntil(t, func() bool { return a.gh.inflight == 0 && len(a.gh.queue) == 0 })
}

// runDialog opens the dialog for refs, confirms it and waits for its end.
func runDialog(t *testing.T, a *App, loop *frostuitest.Loop, batch bool, refs ...string) *mergeDialog {
	t.Helper()
	var prs []pocket.ReadyPR
	for _, ref := range refs {
		for _, p := range a.desk.view.Ready {
			if p.Ref == ref {
				prs = append(prs, p)
			}
		}
	}
	a.openMergeDialog(prs, batch)
	d := a.gh.dialog
	loop.RunUntil(t, func() bool {
		for _, it := range d.items {
			if it.reading {
				return false
			}
		}
		return true
	})
	a.mergeConfirm(d)
	loop.RunUntil(t, func() bool { return d.done })
	return d
}

func dialogNotes(d *mergeDialog) []string {
	var out []string
	for _, it := range d.items {
		switch it.state {
		case "done":
			out = append(out, it.pr.Ref+" "+it.res.Kind)
		default:
			out = append(out, it.pr.Ref+" "+it.note)
		}
	}
	return out
}

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestMergeBatchGoesOnAfterARefusal(t *testing.T) {
	refs := []string{"frostyard/core#1", "frostyard/core#2", "frostyard/core#3", "frostyard/core#4"}
	a, loop, gh, _ := mergeApp(t, refs...)
	a.settings.GitHubBatches = true
	gh.act = func(in github.Intent, _ string, step func(string) error) github.Result {
		if in.Mode != "batch" {
			t.Errorf("mode %q", in.Mode)
		}
		switch in.Preview.Ref.Number {
		case 1:
			_ = step("about to approve")
			_ = step("about to merge")
			return github.Result{Kind: "merged", Step: "merge", Method: "merge"}
		case 2:
			return github.Result{Kind: "refused", Step: "read", Reason: "new commit since you confirmed (eeeeeee)"}
		case 3:
			return github.Result{Kind: "approved", Step: "read 2", Reason: "the PR is behind its base"}
		}
		return github.Result{Kind: "unknown", Step: "reconcile merge", Reason: "merge may be pending"}
	}
	startMerge(t, a, loop)
	if n := len(a.batchable()); n != 4 {
		t.Fatalf("batchable %d", n)
	}
	for _, ref := range refs {
		a.gh.sel[ref] = true
	}
	sel := a.selection()
	prs := []string{}
	for _, p := range sel {
		prs = append(prs, p.Ref)
	}
	d := runDialog(t, a, loop, true, prs...)
	if got := gh.actsSoFar(); !slices.Equal(got, refs) {
		t.Errorf("acts %v, want %v in Inbox order", got, refs)
	}
	want := []string{"frostyard/core#1 merged", "frostyard/core#2 refused", "frostyard/core#3 approved", "frostyard/core#4 unknown"}
	if got := dialogNotes(d); !slices.Equal(got, want) {
		t.Errorf("results %v", got)
	}
	if !slices.Contains(a.pendingToasts, "1 merged · 1 approved, not merged · 1 refused · 1 unknown") {
		t.Errorf("toasts %q", a.pendingToasts)
	}
	// The merged card left; the unknown one is locked.
	for _, it := range a.inboxItems() {
		if it.kind == inboxReady && it.pr.Ref == "frostyard/core#1" {
			t.Error("the merged card is still in the Inbox")
		}
	}
	if !a.mergeLocked("frostyard/core#4") || a.mergeLocked("frostyard/core#2") {
		t.Error("locks")
	}
	if len(a.gh.j.ops) != 4 {
		t.Fatalf("ops %d", len(a.gh.j.ops))
	}
	for _, o := range a.gh.j.ops {
		if !uuidV4.MatchString(o.ID) || o.Intent.Head == "" || o.Origin.Thread != "T1" || o.Origin.User != "u1" {
			t.Errorf("op %+v", o)
		}
	}
}

func TestMergeStopStartsNoOtherPR(t *testing.T) {
	refs := []string{"frostyard/core#1", "frostyard/core#2", "frostyard/core#3"}
	a, loop, gh, _ := mergeApp(t, refs...)
	release := make(chan struct{})
	started := make(chan struct{}, 3)
	gh.act = func(github.Intent, string, func(string) error) github.Result {
		started <- struct{}{}
		<-release
		return github.Result{Kind: "merged", Step: "merge", Method: "merge"}
	}
	startMerge(t, a, loop)
	var prs []pocket.ReadyPR
	prs = append(prs, a.desk.view.Ready...)
	a.openMergeDialog(prs, true)
	d := a.gh.dialog
	loop.RunUntil(t, func() bool { return !d.items[0].reading && !d.items[1].reading && !d.items[2].reading })
	a.mergeConfirm(d)
	<-started
	d.stopping = true // the Stop button
	close(release)
	loop.RunUntil(t, func() bool { return d.done })
	if got := gh.actsSoFar(); len(got) != 1 {
		t.Fatalf("acts %v", got)
	}
	want := []string{"frostyard/core#1 merged", "frostyard/core#2 not started: stopped", "frostyard/core#3 not started: stopped"}
	if got := dialogNotes(d); !slices.Equal(got, want) {
		t.Errorf("results %v", got)
	}
	if !slices.Contains(a.pendingToasts, "1 merged · 2 not started") {
		t.Errorf("toasts %q", a.pendingToasts)
	}
}

func TestMerge401StopsEveryUnstartedPR(t *testing.T) {
	refs := []string{"bketelsen/pi-pocket-team#1", "frostyard/core#2", "bketelsen/pi-pocket-team#3", "frostyard/core#4"}
	a, loop, gh, auth := mergeApp(t, refs...)
	gh.act = func(in github.Intent, _ string, _ func(string) error) github.Result {
		if in.Preview.Ref.Owner == "bketelsen" {
			err := &github.APIError{Status: 401, Message: "Bad credentials"}
			return github.Result{Kind: "failed", Step: "read", Reason: err.Error(), Err: err}
		}
		return github.Result{Kind: "merged", Step: "merge", Method: "merge"}
	}
	startMerge(t, a, loop)
	d := runDialog(t, a, loop, true, refs...)
	want := []string{"bketelsen/pi-pocket-team#1 failed", "frostyard/core#2 not started: GitHub refused the sign-in",
		"bketelsen/pi-pocket-team#3 not started: GitHub refused the sign-in", "frostyard/core#4 not started: GitHub refused the sign-in"}
	if got := dialogNotes(d); !slices.Equal(got, want) {
		t.Errorf("results %v", got)
	}
	// The 401 invalidated that job's generation, and nothing else.
	if got := auth.invalidated(); len(got) != 1 || got[0] == 0 {
		t.Errorf("invalidated %v", got)
	}
	if taken, released := gh.leases(); taken != released {
		t.Errorf("leases taken %d, released %d", taken, released)
	}
	// The sign-in itself stands: the next lease refreshes it.
	if st := auth.Status(); !st.SignedIn {
		t.Errorf("status %+v", st)
	}
}

func TestMergeRateLimitStopsTheBatch(t *testing.T) {
	refs := []string{"frostyard/core#1", "frostyard/core#2", "bketelsen/pi-pocket-team#3"}
	a, loop, gh, _ := mergeApp(t, refs...)
	until := time.Date(2026, 11, 12, 16, 45, 0, 0, time.Local)
	gh.act = func(github.Intent, string, func(string) error) github.Result {
		err := &github.APIError{Status: 403, Message: "API rate limit exceeded", RetryAfter: until}
		return github.Result{Kind: "failed", Step: "read", Reason: err.Error(), Err: err}
	}
	startMerge(t, a, loop)
	d := runDialog(t, a, loop, true, refs...)
	if got := gh.actsSoFar(); len(got) != 1 {
		t.Fatalf("acts %v", got)
	}
	want := []string{"frostyard/core#1 failed", "frostyard/core#2 not started: rate limit until 16:45", "bketelsen/pi-pocket-team#3 not started: rate limit until 16:45"}
	if got := dialogNotes(d); !slices.Equal(got, want) {
		t.Errorf("results %v", got)
	}
}

func TestRiskHighNeedsTheDiffAtTheHead(t *testing.T) {
	ref := "frostyard/core#7"
	a, loop, gh, _ := mergeApp(t, ref)
	a.settings.GitHubBatches = true
	p := gh.previews[ref]
	p.RiskHigh = true
	gh.previews[ref] = p
	var opened []string
	launchErr := errors.New("no browser")
	a.opts.OpenURL = func(u string) error { opened = append(opened, u); return launchErr }
	startMerge(t, a, loop)
	card := func() mergeCard { mc, _ := a.mergeCardOf(&a.desk.view.Ready[0]); return mc }
	if mc := card(); !mc.eligible || mc.batchable() || mc.diffOK {
		t.Fatalf("card %+v", mc)
	}
	if len(a.batchable()) != 0 {
		t.Error("a risk:high card can join a batch")
	}
	// A launch error records nothing.
	a.openDiff(ref, *card().prev)
	loop.RunUntil(t, func() bool { return len(a.pendingToasts) > 0 })
	if card().diffOK {
		t.Fatal("the button is on after a launch error")
	}
	want := "https://github.com/frostyard/core/compare/" + oid('b') + "..." + p.Head
	if len(opened) != 1 || opened[0] != want {
		t.Errorf("opened %v, want %s", opened, want)
	}
	// An opener with no error turns it on, for this head.
	launchErr = nil
	a.openDiff(ref, *card().prev)
	loop.RunUntil(t, func() bool { return card().diffOK })
	// The dialog asks for the diff's head; a batch refuses it.
	a.openMergeDialog([]pocket.ReadyPR{a.desk.view.Ready[0]}, false)
	d := a.gh.dialog
	loop.RunUntil(t, func() bool { return !d.items[0].reading })
	if label, on := a.confirmLabel(d); !on || label != "I read the diff at "+p.Head[:7]+": approve + merge" {
		t.Errorf("confirm %q %v", label, on)
	}
	a.closeMergeDialog()
	// A new head locks it again.
	p.Head = oid('e')
	gh.previews[ref] = p
	a.gh.previews[ref].at = time.Time{}
	a.ghPreviewReady()
	loop.RunUntil(t, func() bool { return card().prev.Head == p.Head })
	if mc := card(); mc.diffOK || !mc.newHead {
		t.Errorf("card after a new head %+v", mc)
	}
	if _, ok := a.gh.diff[ref]; ok {
		t.Error("the diff record stays after a new head")
	}
}

func TestMergedLeavesAndQueuedComesBack(t *testing.T) {
	refs := []string{"frostyard/core#1", "frostyard/core#2"}
	a, loop, gh, _ := mergeApp(t, refs...)
	gh.act = func(in github.Intent, _ string, _ func(string) error) github.Result {
		if in.Preview.Ref.Number == 1 {
			return github.Result{Kind: "merged", Step: "merge", Method: "squash"}
		}
		return github.Result{Kind: "queued", Step: "merge", Method: "queue"}
	}
	startMerge(t, a, loop)
	runDialog(t, a, loop, false, refs[0])
	a.closeMergeDialog()
	runDialog(t, a, loop, false, refs[1])
	a.closeMergeDialog()
	shown := func() []string {
		var out []string
		for _, it := range a.inboxItems() {
			if it.kind == inboxReady {
				out = append(out, it.pr.Ref)
			}
		}
		return out
	}
	if got := shown(); !slices.Equal(got, []string{refs[1]}) {
		t.Fatalf("shown %v", got)
	}
	if mc, _ := a.mergeCardOf(&a.desk.view.Ready[1]); !mc.queued || mc.eligible {
		t.Errorf("queued card %+v", mc)
	}
	// A Desk refresh: GitHub still says MERGED, and the queue holds #2.
	gh.states[refs[0]] = github.PRState{State: "MERGED"}
	gh.settle = func(github.Intent, bool) github.Result { return github.Result{Kind: "queued", Method: "queue"} }
	a.githubDeskRead()
	loop.RunUntil(t, func() bool { return len(a.gh.busy) == 0 })
	if got := shown(); !slices.Equal(got, []string{refs[1]}) {
		t.Fatalf("shown after a refresh %v", got)
	}
	// #2 left the queue: its card comes back with its gates.
	gh.settle = func(github.Intent, bool) github.Result {
		return github.Result{Kind: "approved", Reason: "approved, not merged"}
	}
	a.githubDeskRead()
	loop.RunUntil(t, func() bool { return len(a.gh.busy) == 0 && a.gh.inflight == 0 })
	o := a.gh.j.latest(refs[1])
	if o.Result != "approved" || o.Reason != "left the merge queue" {
		t.Errorf("op %+v", o)
	}
	if mc, _ := a.mergeCardOf(&a.desk.view.Ready[1]); mc.queued || !mc.eligible {
		t.Errorf("card back %+v", mc)
	}
	// GitHub says #1 is not merged: it shows again.
	gh.states[refs[0]] = github.PRState{State: "OPEN"}
	a.githubDeskRead()
	loop.RunUntil(t, func() bool { return len(a.gh.busy) == 0 })
	if got := shown(); !slices.Equal(got, refs) {
		t.Errorf("shown %v", got)
	}
	if !slices.Equal(gh.actsSoFar(), refs) {
		t.Errorf("acts %v", gh.actsSoFar())
	}
}

// TestQueuedLeavesWithTheExitReason: a queued PR that left the queue without a
// merge ends as approved with GitHub's own reason.
func TestQueuedLeavesWithTheExitReason(t *testing.T) {
	ref := "frostyard/core#3"
	a, loop, gh, _ := mergeApp(t, ref)
	gh.act = func(github.Intent, string, func(string) error) github.Result {
		return github.Result{Kind: "queued", Step: "merge", Method: "queue"}
	}
	startMerge(t, a, loop)
	runDialog(t, a, loop, false, ref)
	a.closeMergeDialog()
	gh.settle = func(github.Intent, bool) github.Result {
		return github.Result{Kind: "approved", Reason: "approved, not merged", QueueExit: "failed checks"}
	}
	a.githubDeskRead()
	loop.RunUntil(t, func() bool { return len(a.gh.busy) == 0 && a.gh.inflight == 0 })
	if o := a.gh.j.latest(ref); o.Result != "approved" || o.Reason != "left the merge queue: failed checks" {
		t.Errorf("op %+v", o)
	}
}

// TestQueuedStaysQueuedWhenTheExitReadFails: a failed read of why the PR left
// the queue proves nothing (the removal may say merged), so the attempt stays
// queued and a later read records the merge.
func TestQueuedStaysQueuedWhenTheExitReadFails(t *testing.T) {
	ref := "frostyard/core#3"
	a, loop, gh, _ := mergeApp(t, ref)
	gh.act = func(github.Intent, string, func(string) error) github.Result {
		return github.Result{Kind: "queued", Step: "merge", Method: "queue"}
	}
	startMerge(t, a, loop)
	runDialog(t, a, loop, false, ref)
	a.closeMergeDialog()
	gh.settle = func(github.Intent, bool) github.Result {
		return github.Result{Kind: "unknown", Step: "settle", Reason: "could not read why the PR left the merge queue", Err: errors.New("HTTP 502")}
	}
	a.githubDeskRead()
	loop.RunUntil(t, func() bool { return len(a.gh.busy) == 0 && a.gh.inflight == 0 })
	if o := a.gh.j.latest(ref); o.Result != "queued" {
		t.Fatalf("after a failed exit read: op %+v", o)
	}
	gh.settle = func(github.Intent, bool) github.Result {
		return github.Result{Kind: "merged", Step: "settle", Method: "squash"}
	}
	a.githubDeskRead()
	loop.RunUntil(t, func() bool { return len(a.gh.busy) == 0 && a.gh.inflight == 0 })
	if o := a.gh.j.latest(ref); o.Result != "merged" || o.Method != "squash" {
		t.Errorf("after the merge: op %+v", o)
	}
}

func writeJournal(t *testing.T, dir string, ops ...*mergeOp) {
	t.Helper()
	data, err := json.Marshal(journalFile{Version: journalVersion, Ops: ops})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, journalName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func journalOp(id, ref, step, result string) *mergeOp {
	return &mergeOp{ID: id, Origin: opOrigin{Server: "https://example.org", User: "u1", Thread: "T1"},
		Intent: opIntent{Ref: ref, NodeID: "PR_x", BaseRef: "main", Head: oid('a'), Method: "merge", Mode: "single"},
		Step:   step, Result: result, At: time.Date(2026, 11, 12, 15, 0, 0, 0, time.Local)}
}

func TestJournalReconcilesAtStartByReads(t *testing.T) {
	refs := []string{"frostyard/core#1", "frostyard/core#2", "frostyard/core#3", "frostyard/core#4"}
	a, loop, gh, _ := mergeApp(t, refs...)
	writeJournal(t, a.opts.Store.Dir(),
		journalOp("op-1", refs[0], "about to merge", ""),   // approved, the merge maybe sent
		journalOp("op-2", refs[1], "about to approve", ""), // approved, the merge never sent
		journalOp("op-3", refs[2], "about to merge", ""),   // merged
		journalOp("op-4", refs[3], "reconcile merge", "unknown"),
	)
	gh.settle = func(in github.Intent, wait bool) github.Result {
		if wait {
			t.Error("the start waits")
		}
		if in.Preview.Ref.Number == 3 {
			return github.Result{Kind: "merged", Method: "merge"}
		}
		return github.Result{Kind: "approved", Reason: "approved, not merged"}
	}
	startMerge(t, a, loop)
	loop.RunUntil(t, func() bool { return len(a.gh.busy) == 0 })
	got := map[string]string{}
	for _, o := range a.gh.j.ops {
		got[o.ID] = o.Result + ": " + o.Reason
	}
	want := map[string]string{
		"op-1": "unknown: Pocket stopped during the merge: merge may be pending",
		"op-2": "approved: Pocket stopped before the merge",
		"op-3": "merged: ",
		"op-4": "unknown: ",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %q, want %q", id, got[id], w)
		}
	}
	if len(gh.actsSoFar()) != 0 {
		t.Errorf("a mutation ran at start: %v", gh.actsSoFar())
	}
	if len(gh.settles) != 4 {
		t.Errorf("settles %v", gh.settles)
	}
	if !a.mergeLocked(refs[0]) || a.mergeLocked(refs[1]) || !a.mergeLocked(refs[3]) {
		t.Error("locks")
	}
}

func TestJournalWriteFailureBeforeAMutationSendsNothing(t *testing.T) {
	ref := "frostyard/core#1"
	a, loop, gh, _ := mergeApp(t, ref)
	var mutations []string
	gh.act = func(_ github.Intent, _ string, step func(string) error) github.Result {
		if err := step("about to approve"); err != nil {
			return github.Result{Kind: "refused", Step: "approve", Reason: "journal write failed", Err: err}
		}
		mutations = append(mutations, "approve")
		return github.Result{Kind: "merged", Method: "merge"}
	}
	startMerge(t, a, loop)
	writes := 0
	a.gh.j.rename = func(oldpath, newpath string) error {
		writes++
		if writes > 1 { // the start goes, the step before the approval fails
			return errors.New("disk full")
		}
		return os.Rename(oldpath, newpath)
	}
	d := runDialog(t, a, loop, false, ref)
	if len(mutations) != 0 {
		t.Fatalf("a mutation went out: %v", mutations)
	}
	if r := d.items[0].res; r.Kind != "refused" || r.Reason != "journal write failed" {
		t.Errorf("result %+v", r)
	}
	if !a.mergeLocked(ref) {
		t.Error("the PR whose result was not written is not locked")
	}
	if len(a.console.Lines(nil, "journal write failed")) == 0 {
		t.Error("Activity does not say the journal write failed")
	}
}

func TestJournalCorruptOrLockedTurnsTheButtonsOff(t *testing.T) {
	a, loop, _, _ := mergeApp(t, "frostyard/core#1")
	dir := a.opts.Store.Dir()
	if err := os.WriteFile(filepath.Join(dir, journalName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	startMerge(t, a, loop)
	if a.gh.j.state != journalCorrupt {
		t.Fatalf("state %d", a.gh.j.state)
	}
	want := "operations.json.corrupt-" + a.opts.Now().UTC().Format("20060102T150405Z")
	if a.gh.j.corrupt != want {
		t.Errorf("kept as %q, want %q", a.gh.j.corrupt, want)
	}
	if b, err := os.ReadFile(filepath.Join(dir, want)); err != nil || string(b) != "{not json" {
		t.Errorf("the corrupt file: %q %v", b, err)
	}
	if mc, _ := a.mergeCardOf(&a.desk.view.Ready[0]); mc.blocked == "" || mc.eligible {
		t.Errorf("card %+v", mc)
	}
	tt := settingsTester(t, a)
	if err := tt.Click("Start a new journal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	loop.RunUntil(t, func() bool { return a.gh.inflight == 0 && len(a.gh.queue) == 0 })
	if !a.gh.j.ok() {
		t.Fatal("still off")
	}
	if mc, _ := a.mergeCardOf(&a.desk.view.Ready[0]); !mc.eligible {
		t.Errorf("card after a new journal %+v", mc)
	}
	a.Close()

	// A lock that another Pocket (or a crash) left.
	b, loopB, _, _ := mergeApp(t, "frostyard/core#1")
	if err := os.WriteFile(filepath.Join(b.opts.Store.Dir(), lockName), []byte("pid 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	startMerge(t, b, loopB)
	if b.gh.j.state != journalLocked {
		t.Fatalf("state %d", b.gh.j.state)
	}
	if mc, _ := b.mergeCardOf(&b.desk.view.Ready[0]); mc.blocked == "" {
		t.Error("buttons on with a held lock")
	}
	tt = settingsTester(t, b)
	if err := tt.Click("Release the journal lock"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !b.gh.j.ok() || !b.gh.j.held {
		t.Fatal("still locked")
	}
	b.Close()
	if _, err := os.Stat(filepath.Join(b.opts.Store.Dir(), lockName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Close left the lock: %v", err)
	}
}

func TestJournalAtomicWriteKeepsTheOldFile(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 12, 15, 0, 0, 0, time.UTC)
	j := newJournal(dir, func() time.Time { return now })
	j.open()
	j.ops = append(j.ops, journalOp("op-1", "frostyard/core#1", "start", ""))
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, journalName))
	j.rename = func(string, string) error { return errors.New("rename failed") }
	j.ops[0].Step = "about to approve"
	if err := j.save(); err == nil {
		t.Fatal("no error")
	}
	after, _ := os.ReadFile(filepath.Join(dir, journalName))
	if string(before) != string(after) || !strings.Contains(string(after), `"step": "start"`) {
		t.Errorf("the old file changed:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(dir, journalName+".tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the copy stays: %v", err)
	}
	info, _ := os.Stat(filepath.Join(dir, journalName))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode())
	}
	j.close()
}

func TestJournalRetention(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	old := now.Add(-8 * 24 * time.Hour)
	recorded := journalOp("old-recorded", "frostyard/core#1", "merge", "merged")
	recorded.EndedAt, recorded.Receipts = old, []*opReceipt{{Result: "merged", State: receiptRecorded}}
	pending := journalOp("old-pending", "frostyard/core#2", "merge", "merged")
	pending.EndedAt, pending.Receipts = old, []*opReceipt{{Result: "merged", State: receiptPending, Since: old}}
	unknown := journalOp("old-unknown", "frostyard/core#3", "reconcile merge", "unknown")
	unknown.EndedAt, unknown.Receipts = old.Add(-30*24*time.Hour), []*opReceipt{{Result: "unknown", State: receiptRecorded}}
	fresh := journalOp("fresh", "frostyard/core#4", "merge", "refused")
	fresh.EndedAt, fresh.Receipts = now.Add(-time.Hour), []*opReceipt{{Result: "refused", State: receiptRecorded}}
	writeJournal(t, dir, recorded, pending, unknown, fresh)
	j := newJournal(dir, func() time.Time { return now })
	j.open()
	defer j.close()
	var ids []string
	for _, o := range j.ops {
		ids = append(ids, o.ID)
	}
	if !slices.Equal(ids, []string{"old-pending", "old-unknown", "fresh"}) {
		t.Errorf("kept %v", ids)
	}
	data, _ := os.ReadFile(filepath.Join(dir, journalName))
	if strings.Contains(string(data), "old-recorded") {
		t.Error("the pruned op stays in the file")
	}
}

// receiptHTTP records the receipts the Desk gets and answers each with the
// status of answer.
type receiptHTTP struct {
	mu     sync.Mutex
	bodies []map[string]any
	paths  []string
	status int
}

func (h *receiptHTTP) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/merges") {
		resp := reply(r, `{}`)
		resp.StatusCode = http.StatusNotFound
		return resp, nil
	}
	var body map[string]any
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, &body)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.bodies = append(h.bodies, body)
	h.paths = append(h.paths, r.URL.Path)
	resp := reply(r, `{"recorded":true}`)
	if h.status != 0 {
		resp = reply(r, `{"error":"no"}`)
		resp.StatusCode = h.status
	}
	return resp, nil
}

func (h *receiptHTTP) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.bodies)
}

func TestReceiptBodyRetryAndGiveUp(t *testing.T) {
	ref := "frostyard/core#1"
	a, loop, _, _ := mergeApp(t, ref)
	h := &receiptHTTP{status: 502}
	a.link.client.HTTP = &http.Client{Transport: h}
	startMerge(t, a, loop)
	runDialog(t, a, loop, false, ref)
	loop.RunUntil(t, func() bool { o := a.gh.j.latest(ref); return rcpt(o).Failed })
	o := a.gh.j.latest(ref)
	if h.count() != 1 || h.paths[0] != "/api/x/desk/threads/T1/merges" {
		t.Fatalf("posts %v", h.paths)
	}
	body := h.bodies[0]
	want := map[string]any{"op": o.ID, "ref": ref, "head": a.gh.previews[ref].p.Head, "result": "merged", "method": "merge", "via": "pocket"}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s = %v, want %v", k, body[k], v)
		}
	}
	if _, ok := body["reason"]; ok {
		t.Errorf("merged with a reason: %v", body)
	}
	if a.receiptNote(o) != "not recorded on the Desk" {
		t.Errorf("note %q", a.receiptNote(o))
	}
	// The next Desk refresh posts it again with the same op.
	h.mu.Lock()
	h.status = 0
	h.mu.Unlock()
	a.githubDeskRead()
	loop.RunUntil(t, func() bool { return rcpt(o).State == receiptRecorded })
	if h.count() != 2 || h.bodies[1]["op"] != o.ID {
		t.Errorf("retry %v", h.bodies)
	}
	// A receipt that waits more than 24 hours gives up.
	o2 := journalOp("op-2", ref, "read", "refused")
	o2.Reason = "the PR is a draft"
	o2.Receipts = []*opReceipt{{Result: "refused", Reason: o2.Reason, State: receiptPending, Since: a.opts.Now().Add(-25 * time.Hour), Failed: true}}
	a.gh.j.ops = append([]*mergeOp{o2}, a.gh.j.ops...)
	a.retryReceipts()
	if rcpt(o2).State != receiptGaveUp || a.receiptNote(o2) != "not recorded on the Desk (gave up)" || h.count() != 2 {
		t.Errorf("receipt %+v posts %d", rcpt(o2), h.count())
	}
	// 409: rejected, not retried.
	o3 := journalOp("op-3", ref, "read", "refused")
	o3.Reason = "gate"
	o3.Receipts = []*opReceipt{{Result: "refused", Reason: o3.Reason, State: receiptPending, Since: a.opts.Now()}}
	a.gh.j.ops = append(a.gh.j.ops, o3)
	h.mu.Lock()
	h.status = 409
	h.mu.Unlock()
	a.postReceipt(o3)
	loop.RunUntil(t, func() bool { return rcpt(o3).State == receiptRejected })
	if h.bodies[2]["reason"] != "gate" || h.bodies[2]["result"] != "refused" {
		t.Errorf("refused body %v", h.bodies[2])
	}
}

func TestReceiptWaitsForItsAccount(t *testing.T) {
	ref := "frostyard/core#1"
	a, loop, _, _ := mergeApp(t, ref)
	h := &receiptHTTP{}
	a.link.client.HTTP = &http.Client{Transport: h}
	startMerge(t, a, loop)
	o := journalOp("op-1", ref, "merge", "merged")
	o.Method = "squash"
	o.Receipts = []*opReceipt{{Result: "merged", Method: o.Method, State: receiptPending, Since: a.opts.Now()}}
	a.gh.j.ops = append(a.gh.j.ops, o)
	home := a.account
	for _, other := range []accountKey{{server: home.server, user: "u2"}, {server: "https://other.example.org", user: home.user}} {
		a.account = other
		a.retryReceipts()
		loop.Flush()
		if h.count() != 0 {
			t.Fatalf("posted while signed in as %v", other)
		}
	}
	a.account = home
	a.retryReceipts()
	loop.RunUntil(t, func() bool { return rcpt(o).State == receiptRecorded })
	if h.count() != 1 || h.bodies[0]["op"] != "op-1" || h.bodies[0]["method"] != "squash" {
		t.Errorf("bodies %v", h.bodies)
	}
}

func TestSignInChangeKeepsTheGitHubResult(t *testing.T) {
	ref := "frostyard/core#1"
	a, loop, gh, _ := mergeApp(t, ref)
	release := make(chan struct{})
	gh.act = func(github.Intent, string, func(string) error) github.Result {
		<-release
		return github.Result{Kind: "merged", Step: "merge", Method: "merge"}
	}
	startMerge(t, a, loop)
	a.openMergeDialog([]pocket.ReadyPR{a.desk.view.Ready[0]}, false)
	d := a.gh.dialog
	loop.RunUntil(t, func() bool { return !d.items[0].reading })
	a.mergeConfirm(d)
	// Another sign-in replaces the server client while GitHub works.
	other, _ := pocket.NewClient("https://example.org", "another")
	other.HTTP = &http.Client{Transport: snapshotHTTP{}}
	a.link.client = other
	close(release)
	loop.RunUntil(t, func() bool { return d.done })
	if o := a.gh.j.latest(ref); o.Result != "merged" {
		t.Errorf("op %+v", o)
	}
	if !a.mergeHidden(ref) {
		t.Error("the merged card did not leave")
	}
}

func TestBatchesOffHidesTheBatchControls(t *testing.T) {
	a, loop, _, _ := mergeApp(t, "frostyard/core#1", "frostyard/core#2")
	startMerge(t, a, loop)
	tt := inboxTester(t, a, loop)
	if tt.HasText("Select all eligible") || tt.HasText("Select") || !tt.HasText("Approve + merge") {
		t.Errorf("batches off: %q", tt.Texts())
	}
	a.settings.GitHubBatches = true
	frames(tt, loop)
	if !tt.HasText("Select all eligible") || !tt.HasText("Approve + merge 0") {
		t.Fatalf("batches on: %q", tt.Texts())
	}
	if err := tt.Click("Select all eligible"); err != nil {
		t.Fatal(err)
	}
	frames(tt, loop)
	if !tt.HasText("Approve + merge 2") {
		t.Errorf("after select all: %q", tt.Texts())
	}
}

// TestBypassNoteOnTheCard: an eligible card on a base where the owner can
// bypass a ruleset says Pocket checks every rule itself (ADR-0011); a card
// with no note, or a refused card, does not.
func TestBypassNoteOnTheCard(t *testing.T) {
	a, loop, gh, _ := mergeApp(t, "frostyard/core#1")
	p := gh.previews["frostyard/core#1"]
	p.Note = github.PreviewNote
	gh.previews["frostyard/core#1"] = p
	startMerge(t, a, loop)
	tt := inboxTester(t, a, loop)
	if !tt.HasText(github.PreviewNote) || !tt.HasText("Approve + merge") {
		t.Fatalf("eligible with the note: %q", tt.Texts())
	}

	a, loop, gh, _ = mergeApp(t, "frostyard/core#1")
	p = gh.previews["frostyard/core#1"]
	p.Note, p.Eligible, p.Reason = github.PreviewNote, false, "1 review thread is not resolved"
	gh.previews["frostyard/core#1"] = p
	startMerge(t, a, loop)
	tt = inboxTester(t, a, loop)
	if tt.HasText(github.PreviewNote) || !tt.HasText("1 review thread is not resolved") {
		t.Fatalf("refused: %q", tt.Texts())
	}
}

func TestNoKeyringMeansNoButton(t *testing.T) {
	a, loop, _, _ := mergeApp(t, "frostyard/core#1")
	a.opts.GitHubAuth = nil
	a.opts.GitHub = nil
	a.startGitHub()
	a.githubDeskRead()
	loop.Flush()
	if _, ok := a.mergeCardOf(&a.desk.view.Ready[0]); ok {
		t.Error("a card has Approve + merge without a keyring")
	}
	if a.gh.j != nil {
		t.Error("a journal without the feature")
	}
	tt := inboxTester(t, a, loop)
	if tt.HasText("Approve + merge") || !tt.HasText("Review on GitHub") {
		t.Errorf("inbox %q", tt.Texts())
	}
	st := settingsTester(t, a)
	if !st.HasText("Approve + merge needs the desktop keyring.") || st.HasText("Sign in with GitHub") {
		t.Errorf("settings %q", st.Texts())
	}
}

// TestSignInKeepsOnlyBketelsensSignIn: a pair that acts as another account,
// or a failed CheckApp, keeps nothing and keeps the stored sign-in.
func TestSignInKeepsOnlyBketelsensSignIn(t *testing.T) {
	a, loop, gh, auth := mergeApp(t, "frostyard/core#1")
	auth.setLegacy(true)
	startMerge(t, a, loop)
	old, _ := auth.get()
	// Another account: the pair is never adopted.
	gh.setApp(&github.AppCheck{Login: "frostyard-machines", Owners: map[string]string{}})
	a.githubSignIn()
	loop.RunUntil(t, func() bool { return !a.busySignIn() })
	if adopts := auth.adoptCount(); adopts != 0 {
		t.Fatalf("adopted %d pairs", adopts)
	}
	if !strings.Contains(a.gh.fail, "this sign-in is for frostyard-machines") {
		t.Errorf("fail %q", a.gh.fail)
	}
	if kept, ok := auth.get(); !ok || kept.Access != old.Access {
		t.Errorf("the stored sign-in changed: %+v", kept)
	}
	// A failed CheckApp: nothing kept, with its reason.
	gh.setApp(nil)
	gh.setAppErr(&github.APIError{Status: 500, Message: "Server Error"})
	a.githubSignIn()
	loop.RunUntil(t, func() bool { return !a.busySignIn() })
	if auth.adoptCount() != 0 || a.gh.fail == "" {
		t.Errorf("failed check: adopted %d, fail %q", auth.adoptCount(), a.gh.fail)
	}
	// The saved pair is the login CheckApp named, and the owners are read
	// from the same check.
	gh.setAppErr(nil)
	a.githubSignIn()
	loop.RunUntil(t, func() bool { return !a.busySignIn() })
	adopted := auth.adoptedPairs()
	if len(adopted) != 1 || adopted[0].Login != github.Login || adopted[0].ClientID != github.AppClientID {
		t.Fatalf("adopted %+v", adopted)
	}
	if a.gh.phase != signInSignedIn || !a.gh.owners["frostyard"].known {
		t.Errorf("phase %v owners %+v", a.gh.phase, a.gh.owners)
	}
	loop.RunUntil(t, func() bool { return auth.legacyOffCount() == 1 })
}

// TestPreflightTurnsAnOwnerReady: Run preflight reads the installations
// first, so an owner fixed on GitHub is ready after it.
func TestPreflightTurnsAnOwnerReady(t *testing.T) {
	a, loop, gh, _ := mergeApp(t, "frostyard/core#1", "bketelsen/pi-pocket-team#2")
	gh.setApp(&github.AppCheck{Login: github.Login, Owners: map[string]string{
		github.Login: "", "frostyard": "the App is not installed on frostyard"}})
	startMerge(t, a, loop)
	r, _ := github.ParseRef("frostyard/core#1")
	rb, _ := github.ParseRef("bketelsen/pi-pocket-team#2")
	if got := a.ghBlocked(r); got != "the App is not installed on frostyard" {
		t.Errorf("frostyard blocked %q", got)
	}
	if got := a.ghBlocked(rb); got != "" {
		t.Fatalf("bketelsen blocked %q", got)
	}
	// The owner fixes it on GitHub: 1 more preflight makes it ready.
	gh.setApp(nil)
	a.githubPreflight()
	loop.RunUntil(t, func() bool { return !a.preflightBusy() })
	if got := a.ghBlocked(r); got != "" {
		t.Errorf("after the fix: %q", got)
	}
	if pf := a.gh.preflight["frostyard"]; pf == nil || len(pf.rows) != 1 || pf.rows[0].Repo != "frostyard/core" {
		t.Errorf("frostyard's rows %+v", pf)
	}
	if pf := a.gh.preflight[github.Login]; pf == nil || len(pf.rows) != 1 {
		t.Errorf("bketelsen's rows %+v", pf)
	}
	// 1 job, 1 lease.
	if taken, released := gh.leases(); taken != released || taken == 0 {
		t.Errorf("leases taken %d, released %d", taken, released)
	}
}

func TestPreflightFailureBlocksTheRepository(t *testing.T) {
	a, loop, gh, _ := mergeApp(t, "frostyard/core#1", "frostyard/pocket#2")
	gh.rows = map[string]github.PreflightRow{"frostyard/core": {Repo: "frostyard/core", Readable: true, Bypass: "exempt", Reason: "you can bypass a rule here (exempt): Pocket refuses to merge"}}
	startMerge(t, a, loop)
	a.githubPreflight()
	loop.RunUntil(t, func() bool { return !a.preflightBusy() })
	if mc, _ := a.mergeCardOf(&a.desk.view.Ready[0]); !strings.Contains(mc.blocked, "bypass") || mc.eligible {
		t.Errorf("core %+v", mc)
	}
	if mc, _ := a.mergeCardOf(&a.desk.view.Ready[1]); mc.blocked != "" || !mc.eligible {
		t.Errorf("pocket %+v", mc)
	}
}

func TestTryAgainAndCheckAgain(t *testing.T) {
	ref := "frostyard/core#1"
	a, loop, gh, _ := mergeApp(t, ref)
	gh.act = func(github.Intent, string, func(string) error) github.Result {
		return github.Result{Kind: "unknown", Step: "reconcile merge", Reason: "merge may be pending"}
	}
	startMerge(t, a, loop)
	runDialog(t, a, loop, false, ref)
	a.closeMergeDialog()
	first := a.gh.j.latest(ref)
	if !a.mergeLocked(ref) {
		t.Fatal("unknown is not locked")
	}
	// Check again: still nothing seen; then a merge.
	a.mergeCheckAgain(ref)
	loop.RunUntil(t, func() bool { return !a.gh.busy[ref] })
	if first.Result != "unknown" {
		t.Errorf("op %+v", first)
	}
	gh.settle = func(_ github.Intent, wait bool) github.Result {
		if !wait {
			t.Error("Check again does not wait")
		}
		return github.Result{Kind: "approved", Reason: "approved, not merged"}
	}
	a.mergeCheckAgain(ref)
	loop.RunUntil(t, func() bool { return !a.gh.busy[ref] })
	if first.Result != "unknown" {
		t.Errorf("an approval after a merge that may be pending settled it: %+v", first)
	}
	// Try again: a new op, through the dialog.
	gh.act = nil
	d := runDialog(t, a, loop, false, ref)
	second := a.gh.j.latest(ref)
	if second == first || second.Result != "merged" || first.Superseded != second.ID || d.items[0].res.Kind != "merged" {
		t.Errorf("first %+v second %+v", first, second)
	}
	if len(gh.actsSoFar()) != 2 {
		t.Errorf("acts %v", gh.actsSoFar())
	}
}

// TestTheTokenNeverLeaksFromEchoedErrors gives GitHub errors whose text
// echoes the token (passed through github.Redact, as the real client does),
// and a sign-in whose CheckApp meets the same: the journal, Activity, the
// toasts and the views never hold the token.
func TestTheTokenNeverLeaksFromEchoedErrors(t *testing.T) {
	refs := []string{"frostyard/core#1", "frostyard/core#2"}
	a, loop, gh, auth := mergeApp(t, refs...)
	echo := func(status int) error {
		return github.Redact(&github.APIError{Status: status, Message: "Bad credentials for " + testGitHubToken}, testGitHubToken)
	}
	gh.errs[refs[1]] = echo(403)
	gh.act = func(github.Intent, string, func(string) error) github.Result {
		err := echo(422)
		return github.Result{Kind: "failed", Step: "approve", Reason: err.Error(), Err: err}
	}
	startMerge(t, a, loop)
	if pv := a.gh.previews[refs[1]]; pv == nil || pv.err == "" {
		t.Fatalf("the preview did not fail: %+v", pv)
	}
	d := runDialog(t, a, loop, false, refs[0])
	if r := d.items[0].res; r.Kind != "failed" {
		t.Fatalf("result %+v", r)
	}
	a.closeMergeDialog()
	// A sign-in whose CheckApp fails with the echoed token.
	gh.setAppErr(echo(401))
	a.githubSignIn()
	loop.RunUntil(t, func() bool { return !a.busySignIn() })
	if a.gh.fail == "" {
		t.Fatal("the sign-in did not fail")
	}
	if auth.adoptCount() != 0 {
		t.Errorf("a failed check adopted %d pairs", auth.adoptCount())
	}
	toasts := strings.Join(a.pendingToasts, "\n")
	inbox := strings.Join(inboxTester(t, a, loop).Texts(), "\n")
	settings := strings.Join(settingsTester(t, a).Texts(), "\n")
	var lines []string
	for _, l := range a.console.Lines(nil, "") {
		lines = append(lines, l.Text)
	}
	data, err := os.ReadFile(filepath.Join(a.opts.Store.Dir(), journalName))
	if err != nil {
		t.Fatal(err)
	}
	for name, held := range map[string]string{
		"operations.json": string(data), "Activity": strings.Join(lines, "\n"), "toasts": toasts,
		"inbox": inbox, "settings": settings, "sign-in": a.gh.fail, "preview": a.gh.previews[refs[1]].err,
	} {
		if strings.Contains(held, testGitHubToken) {
			t.Errorf("%s holds the token:\n%s", name, held)
		}
	}
	if !strings.Contains(strings.Join(lines, "\n"), "[token]") || !strings.Contains(a.gh.fail, "[token]") || !strings.Contains(string(data), "[token]") {
		t.Error("the redacted errors are not where they should show")
	}
}

// rcpt is the receipt of the attempt's current result.
func rcpt(o *mergeOp) *opReceipt {
	if r := o.receipt(o.Result); r != nil {
		return r
	}
	return &opReceipt{}
}
