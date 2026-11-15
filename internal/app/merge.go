package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/config"
	"github.com/frostyard/pocket/internal/github"
	"github.com/frostyard/pocket/internal/pocket"
)

// Approve + merge on the Inbox's ready cards (ADR-0008): the owner approves
// and merges a ready PR as bketelsen, with his own token, from a click in
// this window. No server message, Desk field or stream event starts a
// mutation: only the confirm button of the dialog does (mergeConfirm).
// GitHub calls are built from the Desk row's ref, never from its url.

// GitHubAuth is the GitHub App sign-in: the keyring entry, the device flow
// and the leased token behind 1 seam (cmd/pocket/github.go joins them).
// Tests give a fake.
type GitHubAuth interface {
	Open() error
	Start(ctx context.Context) (github.DeviceCode, error)
	Poll(ctx context.Context, dc github.DeviceCode) (github.AppToken, error)
	BeginSignIn() int
	CancelSignIn(gen int)
	Adopt(tok github.AppToken, gen int) error
	SaveAgain() error
	DeleteAgain() error
	Invalidate(tokenGen int)
	Forget() error
	Status() github.AppStatus
	Legacy() (bool, error)
	ForgetLegacy() error
	Close()
}

// ghAPI is what the app uses of a *github.Client; tests give a fake.
type ghAPI interface {
	Preview(ctx context.Context, ref github.Ref) (github.Preview, error)
	Act(ctx context.Context, in github.Intent, op string, step func(name string) error) github.Result
	Settle(ctx context.Context, in github.Intent, op string, wait bool) github.Result
	State(ctx context.Context, ref github.Ref) (github.PRState, error)
	Preflight(ctx context.Context, ref github.Ref) github.PreflightRow
	CheckApp(ctx context.Context, owners []string) (github.AppCheck, error)
}

const (
	// githubLimit is the time limit of 1 GitHub job (an attempt with its
	// reconciliation, or a read).
	githubLimit = 5 * time.Minute
	// previewTTL is how long a card's preview stays fresh.
	previewTTL = 60 * time.Second
	// previewReads is how many previews read at a time.
	previewReads = 4
	// batchMax is the most PRs in 1 batch.
	batchMax = 25
	// receiptRetry is how long Pocket posts a receipt again.
	receiptRetry = 24 * time.Hour
)

// ghPreview is the last preview of 1 ready PR.
type ghPreview struct {
	p       *github.Preview
	err     string
	at      time.Time
	reading int
}

// ghReadReq is a preview read that waits for a free slot.
type ghReadReq struct {
	key    string
	ref    github.Ref
	dialog *mergeDialog
}

// githubOwners are the owners the Pocket App must be installed on, in the
// order Settings shows them.
var githubOwners = []string{github.Login, "frostyard"}

// deviceURL is where a person enters the code. Pocket's own constant: it
// never opens the answer's verification_uri.
const deviceURL = "https://github.com/login/device"

// signInPhase is where the sign-in of the App is.
type signInPhase int

const (
	// signInOpening is before Open answers: the keyring and the sign-in
	// lock may take a moment.
	signInOpening signInPhase = iota
	// signInOff is the feature off: no keyring, or another Pocket holds the
	// sign-in lock.
	signInOff
	// signInStarting asks GitHub for a code.
	signInStarting
	// signInNone is signed out.
	signInNone
	// signInCode shows the code, and polls for the pair.
	signInCode
	// signInChecking runs CheckApp on the new pair.
	signInChecking
	// signInSaving runs Adopt, with Cancel off.
	signInSaving
	// signInSignedIn holds a sign-in Pocket may lease.
	signInSignedIn
)

// ownerState is what the App's installations say of 1 owner: unknown until
// CheckApp answers, ready (no reason), or not ready with its reason. Note
// is what does not refuse, such as a missing Merge queues permission.
type ownerState struct {
	known  bool
	reason string
	note   string
}

// preflightResult is the preflight table of 1 owner.
type preflightResult struct {
	busy bool
	rows []github.PreflightRow
	err  string
}

// ghState is Approve + merge's state, on the UI thread. It survives a
// sign-out: a GitHub result is never dropped.
type ghState struct {
	// api gives a client on a leased token, the token's generation and the
	// release a job calls with defer.
	api       func(ctx context.Context, owner string) (ghAPI, int, func(), error)
	newClient func(token string) ghAPI
	j         *journal

	// The App's sign-in (ADR-0010).
	phase         signInPhase
	open          bool   // Open answered
	keyErr        string // Open failed: the keyring's reason
	held          bool   // another Pocket holds the sign-in lock
	status        github.AppStatus
	legacy        bool // 1 of the 2 old token keys is still kept
	signInGen     int
	code          github.DeviceCode
	codeAt        time.Time
	pollID        int
	cancelPoll    func()
	checkBusy     bool   // checkOwners runs
	checkJob      int    // the newest CheckApp job of the start
	preflightJob  int    // the newest preflight job
	preflightBusy bool   // the 1 preflight job runs
	fail          string // why the last sign-in failed
	deleteWhy     string // why the keyring did not delete the sign-in
	owners        map[string]ownerState

	epoch map[string]int

	previews map[string]*ghPreview
	inflight int
	queue    []ghReadReq

	sel  map[string]bool
	diff map[string]string

	dialog  *mergeDialog
	running map[string]bool
	// notMerged are refs whose attempt merged, which GitHub reads as not
	// merged now: their cards show again.
	notMerged map[string]bool
	posting   map[string]*pocket.Client
	busy      map[string]bool

	preflight map[string]*preflightResult
	blocked   map[string]string
}

func newGHState() ghState {
	return ghState{
		newClient: func(token string) ghAPI { return github.New(token) },
		epoch:     map[string]int{}, owners: map[string]ownerState{},
		previews: map[string]*ghPreview{}, sel: map[string]bool{}, diff: map[string]string{},
		running: map[string]bool{}, notMerged: map[string]bool{}, posting: map[string]*pocket.Client{},
		busy: map[string]bool{}, preflight: map[string]*preflightResult{},
		blocked: map[string]string{},
	}
}

// initGitHub wires Options.GitHub into the seam the app uses.
func (a *App) initGitHub() {
	a.gh = newGHState()
	if gh := a.opts.GitHub; gh != nil {
		a.gh.api = func(ctx context.Context, owner string) (ghAPI, int, func(), error) {
			c, gen, release, err := gh(ctx, owner)
			if err != nil {
				return nil, 0, nil, err
			}
			if c == nil {
				return nil, 0, nil, errors.New("no GitHub client for " + owner)
			}
			return c, gen, release, nil
		}
	}
}

// ghOn says the feature is on: a leased token and the App's sign-in.
func (a *App) ghOn() bool { return a.gh.api != nil && a.opts.GitHubAuth != nil }

// startGitHub opens the journal, then the GitHub sign-in (the sign-in lock,
// the keyring entry and the App's installations), and reconciles the
// attempts a crash left once the sign-in is there: before that every lease
// fails. Reads only.
func (a *App) startGitHub() {
	if !a.ghOn() || a.gh.j != nil {
		return
	}
	a.gh.j = newJournal(a.opts.Store.Dir(), a.opts.Now)
	a.gh.j.open()
	if !a.gh.j.ok() {
		a.console.Add(frostui.LevelWarn, "github", "Approve + merge is off: "+a.gh.j.why)
	}
	a.openSignIn()
}

// openSignIn takes the sign-in lock and reads the stored sign-in, off the
// UI thread: the keyring may wait. The owners are checked, and only then
// are the attempts a crash left reconciled.
func (a *App) openSignIn() {
	auth := a.opts.GitHubAuth
	ghRead(a, "Opening the GitHub sign-in", func(context.Context) (bool, error) {
		if err := auth.Open(); err != nil {
			return false, err
		}
		// A failing keyring on the old keys is no reason to fail the open.
		legacy, err := auth.Legacy()
		return legacy && err == nil, nil
	}, func(legacy bool, err error) {
		if err != nil {
			a.gh.phase = signInOff
			if errors.Is(err, config.ErrSignInHeld) {
				a.gh.held = true
				return
			}
			a.gh.keyErr = err.Error()
			return
		}
		a.gh.open, a.gh.legacy, a.gh.keyErr = true, legacy, ""
		a.gh.status = auth.Status()
		if !a.gh.status.SignedIn {
			a.gh.phase = signInNone
			return
		}
		a.gh.phase = signInSignedIn
		a.checkOwners(func() { a.reconcileJournal() })
	})
}

// checkOwners reads GET /user and the App's installations with 1 lease, and
// sets each owner's state. done runs on the UI thread after it.
func (a *App) checkOwners(done func()) {
	if !a.ghOn() || a.gh.checkBusy {
		return
	}
	apiFn := a.gh.api
	a.gh.checkBusy = true
	a.gh.checkJob++
	job, signInGen := a.gh.checkJob, a.gh.signInGen
	a.ghReview()
	gen := 0
	ghRead(a, "Checking the GitHub App's installations", func(ctx context.Context) (github.AppCheck, error) {
		api, tokenGen, release, err := apiFn(ctx, githubOwners[0])
		gen = tokenGen
		if err != nil {
			return github.AppCheck{}, err
		}
		if release != nil {
			defer release()
		}
		return api.CheckApp(ctx, githubOwners)
	}, func(check github.AppCheck, err error) {
		// A check that started before a Sign out, or a new sign-in, says
		// nothing of the sign-in there is now: only its own busy flag goes.
		if signInGen != a.gh.signInGen {
			if a.gh.checkJob == job {
				a.gh.checkBusy = false
			}
			a.ghReview()
			// done still runs: the attempts a crash left are settled by
			// reads, whatever the owners are.
			if done != nil {
				done()
			}
			return
		}
		a.jobErr(err, gen)
		if a.gh.checkJob == job {
			a.gh.checkBusy = false
		}
		if err != nil {
			a.console.Add(frostui.LevelWarn, "github", "Checking the GitHub App's installations: "+err.Error())
			if done != nil {
				done()
			}
			return
		}
		a.setOwners(check)
		if done != nil {
			done()
		}
	})
}

// setOwners takes a CheckApp's answer: 1 owner ready, or the reason it is
// not, and the notes that do not refuse.
func (a *App) setOwners(check github.AppCheck) {
	for _, owner := range githubOwners {
		st := ownerState{known: true, reason: check.Owners[owner], note: check.Notes[owner]}
		a.gh.owners[owner] = st
	}
	a.ghReview()
}

// unknownOwners forgets what the installations said: a new sign-in checks
// again.
func (a *App) unknownOwners() {
	a.gh.owners = map[string]ownerState{}
	a.ghReview()
}

// ghReview says the GitHub views ask for another frame: the sign-in's state
// or an owner's changed.
func (a *App) ghReview() { a.invalidateDerived() }

// closeGitHub stops the poll, ends the sign-in and removes the journal's
// lock. The sign-in lock goes only with the process.
func (a *App) closeGitHub() {
	if a.gh.cancelPoll != nil {
		a.gh.cancelPoll()
		a.gh.cancelPoll = nil
	}
	if a.opts.GitHubAuth != nil {
		a.opts.GitHubAuth.Close()
	}
	if a.gh.j != nil {
		a.gh.j.close()
	}
}

// githubDo runs fn on a goroutine with its own context and hands its result
// to done on the UI thread, whatever the server sign-in is then: unlike
// request, a GitHub result is never dropped, and a sign-out cancels nothing.
func (a *App) githubDo(what string, fn func(ctx context.Context) github.Result, done func(github.Result)) {
	ghRead(a, what, func(ctx context.Context) (github.Result, error) {
		res := fn(ctx)
		return res, nil
	}, func(res github.Result, _ error) {
		if res.Err != nil {
			a.console.Add(frostui.LevelWarn, "github", what+": "+res.Err.Error())
		}
		done(res)
	})
}

// ghRead is githubDo for a read that gives a value or an error. fn must
// give errors that hold no token (the GitHub client redacts its own).
func ghRead[T any](a *App, what string, fn func(ctx context.Context) (T, error), done func(T, error)) {
	post := a.post
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), githubLimit)
		defer cancel()
		v, err := fn(ctx)
		post(func() {
			if err != nil {
				a.console.Add(frostui.LevelWarn, "github", what+": "+err.Error())
			}
			a.invalidateDerived()
			defer a.invalidateDerived()
			done(v, err)
		})
	}()
}

// ghRef parses a Desk ref into a PR of an owner Pocket keeps a token for.
func ghRef(s string) (github.Ref, bool) {
	r, err := github.ParseRef(s)
	if err != nil || !slices.Contains(githubOwners, r.Owner) {
		return github.Ref{}, false
	}
	return r, true
}

// ghBlocked says why a PR gets no button now, or "": the sign-in first, in
// the order of ADR-0010, then the owner's state, then the attempt's.
func (a *App) ghBlocked(r github.Ref) string {
	a.syncStatus()
	switch {
	case !a.ghOn():
		return "Approve + merge needs the desktop keyring (Settings > GitHub)"
	case a.gh.keyErr != "":
		return "Approve + merge needs the desktop keyring: " + a.gh.keyErr + " (Settings > GitHub)"
	case a.gh.held:
		return "another Pocket holds the GitHub sign-in"
	case a.gh.phase == signInOpening:
		return "opening the GitHub sign-in"
	case a.gh.status.Unsaved:
		return "the keyring did not keep the new GitHub sign-in (Settings > GitHub)"
	case !a.gh.status.SignedIn && a.gh.status.DeleteFailed:
		return "the keyring still holds a sign-in you signed out of (Settings > GitHub)"
	case a.gh.phase != signInSignedIn || !a.gh.status.SignedIn:
		return "sign in to GitHub (Settings > GitHub)"
	case a.checkingOwners():
		return "checking the GitHub App's installations"
	case !a.gh.owners[r.Owner].known:
		return "the GitHub App's installations are not checked: Run preflight (Settings > GitHub)"
	case a.gh.owners[r.Owner].reason != "":
		return a.gh.owners[r.Owner].reason
	case a.gh.j == nil:
		return "Approve + merge starts with the journal"
	case !a.gh.j.ok():
		return "Approve + merge is off: " + a.gh.j.why + " (Settings > GitHub)"
	case a.gh.blocked[r.Owner+"/"+r.Name] != "":
		return a.gh.blocked[r.Owner+"/"+r.Name]
	}
	return ""
}

// ghPreviewReady reads the previews of the ready PRs that have none, or an
// older one than 60 seconds. Never from a frame's draw.
func (a *App) ghPreviewReady() {
	if !a.ghOn() || a.desk.view == nil {
		return
	}
	now := a.opts.Now()
	for _, p := range a.desk.view.Ready {
		r, ok := ghRef(p.Ref)
		if !ok || a.ghBlocked(r) != "" || a.mergeHidden(p.Ref) {
			continue
		}
		if pv := a.gh.previews[p.Ref]; pv != nil && (pv.reading > 0 || now.Sub(pv.at) < previewTTL) {
			continue
		}
		a.ghQueuePreview(p.Ref, r, nil)
	}
	a.ghPump()
}

func (a *App) ghQueuePreview(key string, r github.Ref, d *mergeDialog) {
	pv := a.gh.previews[key]
	if pv == nil {
		pv = &ghPreview{}
		a.gh.previews[key] = pv
	}
	pv.reading++
	a.gh.queue = append(a.gh.queue, ghReadReq{key: key, ref: r, dialog: d})
}

// ghPump starts the waiting preview reads, 4 at a time.
func (a *App) ghPump() {
	for a.gh.inflight < previewReads && len(a.gh.queue) > 0 {
		req := a.gh.queue[0]
		a.gh.queue = a.gh.queue[1:]
		a.gh.inflight++
		if a.gh.previews[req.key] == nil {
			a.gh.previews[req.key] = &ghPreview{reading: 1}
		}
		owner, epoch, apiFn := req.ref.Owner, a.gh.epoch[req.ref.Owner], a.gh.api
		gen := 0
		ghRead(a, "Reading "+req.key+" on GitHub", func(ctx context.Context) (github.Preview, error) {
			api, tokenGen, release, err := apiFn(ctx, owner)
			gen = tokenGen
			if err != nil {
				return github.Preview{}, err
			}
			if release != nil {
				defer release()
			}
			return api.Preview(ctx, req.ref)
		}, func(p github.Preview, err error) {
			a.jobErr(err, gen)
			a.gh.inflight--
			defer a.ghPump()
			pv := a.gh.previews[req.key]
			if pv != nil && pv.reading > 0 {
				pv.reading--
			}
			if epoch != a.gh.epoch[owner] || pv == nil {
				return // the token changed since: this read is the old one's
			}
			pv.at = a.opts.Now()
			if err != nil {
				pv.p, pv.err = nil, err.Error()
			} else {
				pv.p, pv.err = &p, ""
				if h, ok := a.gh.diff[req.key]; ok && h != p.Head {
					delete(a.gh.diff, req.key) // a new head: open its diff again
				}
			}
			if d := req.dialog; d != nil && d == a.gh.dialog && !d.started {
				for _, it := range d.items {
					if it.pr.Ref == req.key {
						it.reading = false
						it.prev, it.readErr = pv.p, pv.err
					}
				}
			}
		})
	}
}

// refused marks the pair of that job's generation spent on a 401: an older
// generation changes nothing, and the next lease refreshes.
func (a *App) refused(err error, tokenGen int) bool {
	var api *github.APIError
	if !errors.As(err, &api) || api.Status != 401 {
		return false
	}
	if auth := a.opts.GitHubAuth; auth != nil {
		auth.Invalidate(tokenGen)
	}
	a.dropPreviewsAll()
	return true
}

// dropPreviewsAll forgets every preview and every queued read: a refused
// sign-in is no basis for a card.
func (a *App) dropPreviewsAll() {
	for _, owner := range githubOwners {
		a.dropPreviews(owner)
	}
}

// mergeHidden says a ready PR's card leaves the Inbox: an attempt merged
// it, the journal holds that result, and GitHub has not said otherwise
// since. A merge whose journal write failed stays, locked, with Check
// again.
func (a *App) mergeHidden(ref string) bool {
	if !a.ghOn() || a.gh.j == nil {
		return false
	}
	o := a.gh.j.latest(ref)
	return o != nil && o.Result == "merged" && !o.unsaved && !a.gh.notMerged[ref]
}

// inRunningDialog says ref has a place in a dialog that runs.
func (a *App) inRunningDialog(ref string) bool {
	if d := a.gh.dialog; d != nil && d.started && !d.done {
		for _, it := range d.items {
			if it.pr.Ref == ref {
				return true
			}
		}
	}
	return false
}

// mergeLocked says a PR has an attempt in progress, an unknown outcome, or
// a place in a batch that runs: its buttons are off everywhere.
func (a *App) mergeLocked(ref string) bool {
	if a.inRunningDialog(ref) {
		return true
	}
	o := a.gh.j.latest(ref)
	return o != nil && o.Superseded == "" && (o.Result == "" || o.Result == "unknown" || o.unsaved)
}

// mergeCard is what a ready card shows of Approve + merge.
type mergeCard struct {
	ref      github.Ref
	blocked  string
	reading  bool
	readErr  string
	prev     *github.Preview
	newHead  bool
	op       *mergeOp
	running  bool
	locked   bool
	queued   bool
	risk     bool
	diffOK   bool
	eligible bool
	reason   string
}

// mergeCardOf works out the card of p; ok is false when Approve + merge
// does not apply to it at all.
func (a *App) mergeCardOf(p *pocket.ReadyPR) (mergeCard, bool) {
	if !a.ghOn() {
		return mergeCard{}, false
	}
	r, ok := ghRef(p.Ref)
	if !ok {
		return mergeCard{}, false
	}
	mc := mergeCard{ref: r, blocked: a.ghBlocked(r)}
	if a.gh.j != nil {
		mc.op = a.gh.j.latest(p.Ref)
	}
	if mc.op != nil {
		mc.running = a.gh.running[mc.op.ID]
		mc.queued = mc.op.Result == "queued"
	}
	if a.gh.j != nil {
		mc.locked = a.mergeLocked(p.Ref)
	}
	if pv := a.gh.previews[p.Ref]; pv != nil {
		mc.prev, mc.readErr = pv.p, pv.err
		mc.reading = pv.p == nil && pv.err == "" && pv.reading > 0
	}
	if mc.prev != nil {
		mc.risk = mc.prev.RiskHigh
		mc.diffOK = a.gh.diff[p.Ref] != "" && a.gh.diff[p.Ref] == mc.prev.Head
		if p.Head != "" && !strings.HasPrefix(mc.prev.Head, p.Head) {
			mc.newHead = true
		}
		mc.reason = mc.prev.Reason
		mc.eligible = mc.prev.Eligible && mc.blocked == "" && !mc.locked && !mc.queued
	}
	return mc, true
}

// batchable says the card can join a batch.
func (mc mergeCard) batchable() bool { return mc.eligible && !mc.risk }

// mergeControls draws the Approve + merge part of a ready card.
func (a *App) mergeControls(c *ui.Context, p *pocket.ReadyPR) {
	mc, ok := a.mergeCardOf(p)
	if !ok {
		return
	}
	ref := p.Ref
	if o := mc.op; o != nil {
		line, tone := opLine(o, mc.running)
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			if mc.running {
				ui.Spinner(c).Size(14, 14)
			}
			frostui.Toned(c, line, tone).Wrap().Grow(1)
		})
		if note := a.receiptNote(o); note != "" {
			frostui.Caption(c, note).Wrap()
		}
	}
	if mc.running || (mc.queued && !mc.op.unsaved) {
		return // the result line says it all; no button
	}
	if mc.newHead && mc.prev != nil {
		frostui.Caption(c, "new commit since the Desk read: GitHub's head is "+short(mc.prev.Head)).Wrap()
	}
	// A base the owner can bypass: the card says Pocket proves each rule
	// itself (ADR-0011).
	if mc.prev != nil && mc.prev.Eligible && mc.prev.Note != "" && mc.blocked == "" && !mc.locked {
		frostui.Caption(c, mc.prev.Note).Wrap()
	}
	ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
		switch {
		case a.inRunningDialog(ref):
			frostui.Caption(c, "In the approve + merge that runs").Grow(1)
		case mc.locked:
			ui.Spacer(c)
			busy := a.gh.busy[ref]
			if busy {
				ui.Spinner(c).Size(14, 14)
			}
			if ui.Button(c, "Check again").Label("Check again " + ref).Disabled(busy).Clicked() {
				a.mergeCheckAgain(ref)
			}
			if ui.Button(c, "Open on GitHub").Label("Open " + ref + " on GitHub").Clicked() {
				a.openURL(prURL(mc.ref))
			}
			if ui.PrimaryButton(c, "Try again").Label("Try again " + ref).Disabled(busy || mc.blocked != "").Clicked() {
				a.openMergeDialog([]pocket.ReadyPR{*p}, false)
			}
		case mc.blocked != "":
			frostui.Caption(c, mc.blocked).Wrap().Grow(1)
		case mc.reading:
			ui.Spinner(c).Size(14, 14)
			frostui.Caption(c, "Checking GitHub…").Grow(1)
		case mc.readErr != "":
			frostui.Problem(c, "GitHub: "+mc.readErr).Grow(1)
		case mc.prev == nil:
			ui.Spacer(c)
		case !mc.prev.Eligible:
			frostui.Muted(c, mc.reason).Wrap().Grow(1)
		case mc.risk:
			if mc.diffOK {
				frostui.Caption(c, "risk:high · diff opened at "+short(mc.prev.Head)).Wrap().Grow(1)
			} else {
				frostui.Caption(c, "risk:high: open the diff before you approve + merge").Wrap().Grow(1)
			}
			if ui.Button(c, "Open diff").Label("Open diff " + ref).Clicked() {
				a.openDiff(ref, *mc.prev)
			}
			if ui.PrimaryButton(c, "Approve + merge").Label("Approve + merge " + ref).Disabled(!mc.diffOK || a.gh.dialog != nil).Clicked() {
				a.openMergeDialog([]pocket.ReadyPR{*p}, false)
			}
		default:
			if a.settings.GitHubBatches {
				checked := a.gh.sel[ref]
				full := !checked && a.selectedCount() >= batchMax
				ui.Checkbox(c, &checked, "Select").Label("Select " + ref).Disabled(full)
				a.gh.sel[ref] = checked
				if !checked {
					delete(a.gh.sel, ref)
				}
			}
			frostui.Caption(c, methodLabel(*mc.prev)+" · "+short(mc.prev.Head)).Grow(1)
			if ui.PrimaryButton(c, "Approve + merge").Label("Approve + merge " + ref).Disabled(a.gh.dialog != nil).Clicked() {
				a.openMergeDialog([]pocket.ReadyPR{*p}, false)
			}
		}
	})
}

// opLine is an attempt's result in words.
func opLine(o *mergeOp, running bool) (string, frostui.Tone) {
	if o.unsaved {
		return o.Result + ": " + o.Reason + " · journal write failed", frostui.Danger
	}
	switch o.Result {
	case "":
		if running {
			return "Approving and merging as " + github.Login + "…", frostui.Accent
		}
		return "unknown: Pocket stopped during the attempt", frostui.Warning
	case "merged":
		return "merged (" + firstNonEmpty(o.Method, "merge") + ")", frostui.Success
	case "queued":
		return "In merge queue: GitHub merges it when its checks pass", frostui.Accent
	case "approved":
		return "approved, not merged: " + firstNonEmpty(o.Reason, "the merge did not happen"), frostui.Warning
	case "refused":
		return "refused: " + o.Reason, frostui.Danger
	case "failed":
		return "failed: " + o.Reason, frostui.Danger
	case "unknown":
		return "unknown: " + o.Reason, frostui.Warning
	}
	return o.Result, frostui.Neutral
}

// receiptNote says when the Desk has not recorded a result: a receipt
// that waits (its post failed, or none is under way), 1 Pocket gave up on,
// or 1 the Desk refused.
func (a *App) receiptNote(o *mergeOp) string {
	waits, gaveUp, rejected := false, false, false
	for _, r := range o.Receipts {
		switch r.State {
		case receiptPending:
			if r.Failed || a.gh.posting[o.ID+":"+r.Result] == nil {
				waits = true
			}
		case receiptGaveUp:
			gaveUp = true
		case receiptRejected:
			rejected = true
		}
	}
	switch {
	case waits:
		return "not recorded on the Desk"
	case gaveUp:
		return "not recorded on the Desk (gave up)"
	case rejected:
		return "the Desk refused the receipt"
	}
	return ""
}

func methodLabel(p github.Preview) string {
	if p.Queue {
		return "merge queue"
	}
	return firstNonEmpty(p.Method, "merge")
}

func short(oid string) string {
	if len(oid) > 7 {
		return oid[:7]
	}
	return oid
}

func prURL(r github.Ref) string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", r.Owner, r.Name, r.Number)
}

var oidPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// openDiff opens the three-dot compare of the shown head. Only when the
// opener reports no error does the head count as read.
func (a *App) openDiff(ref string, p github.Preview) {
	if !oidPattern.MatchString(p.BaseOid) || !oidPattern.MatchString(p.Head) {
		a.toast("Opening the diff: GitHub gave no commit to compare")
		return
	}
	u := fmt.Sprintf("https://github.com/%s/%s/compare/%s...%s", p.Ref.Owner, p.Ref.Name, p.BaseOid, p.Head)
	opener := frostui.Opener{Schemes: []string{"https"}, Launch: a.opts.OpenURL}
	post, head := a.post, p.Head
	go func() {
		err := opener.OpenURL(u)
		post(func() {
			a.invalidateDerived()
			if err != nil {
				a.console.Add(frostui.LevelWarn, "github", "Opening the diff of "+ref+": "+err.Error())
				a.toast("Opening the diff: " + err.Error())
				return
			}
			a.gh.diff[ref] = head
		})
	}()
}

// readyCards are the ready PRs the Inbox shows, in its order.
func (a *App) readyCards() []*pocket.ReadyPR {
	var out []*pocket.ReadyPR
	for _, it := range a.inboxItems() {
		if it.kind == inboxReady {
			out = append(out, it.pr)
		}
	}
	return out
}

// batchable are the cards that can join a batch, in Inbox order.
func (a *App) batchable() []*pocket.ReadyPR {
	var out []*pocket.ReadyPR
	for _, p := range a.readyCards() {
		if mc, ok := a.mergeCardOf(p); ok && mc.batchable() {
			out = append(out, p)
		}
	}
	return out
}

// selection is the selected cards that can still join a batch.
func (a *App) selection() []*pocket.ReadyPR {
	var out []*pocket.ReadyPR
	for _, p := range a.batchable() {
		if a.gh.sel[p.Ref] && len(out) < batchMax {
			out = append(out, p)
		}
	}
	return out
}

func (a *App) selectedCount() int { return len(a.selection()) }

// mergeHeading is the batch row under Ready to merge.
func (a *App) mergeHeading(c *ui.Context) {
	if !a.ghOn() || !a.settings.GitHubBatches {
		return
	}
	can := a.batchable()
	if len(can) == 0 {
		return
	}
	sel := a.selection()
	ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
		frostui.Caption(c, fmt.Sprintf("%d of %d eligible selected", len(sel), len(can))).Grow(1)
		if ui.Button(c, "Select all eligible").Disabled(a.gh.dialog != nil).Clicked() {
			clear(a.gh.sel)
			for i, p := range can {
				if i == batchMax {
					break
				}
				a.gh.sel[p.Ref] = true
			}
		}
		if ui.PrimaryButton(c, fmt.Sprintf("Approve + merge %d", len(sel))).Disabled(len(sel) == 0 || a.gh.dialog != nil).Clicked() {
			prs := make([]pocket.ReadyPR, len(sel))
			for i, p := range sel {
				prs[i] = *p
			}
			a.openMergeDialog(prs, true)
		}
	})
}

// mergeDialog is the confirm dialog, then the progress of its attempts.
type mergeDialog struct {
	// origin is the server and user the window showed when the dialog
	// opened: every attempt of the dialog records it, and its receipts go
	// only there. Each row keeps its own Desk thread (mergeItem.pr).
	origin   accountKey
	batch    bool
	items    []*mergeItem
	started  bool
	stopping bool
	done     bool
}

// mergeItem is 1 PR of the dialog.
type mergeItem struct {
	pr      pocket.ReadyPR
	ref     github.Ref
	prev    *github.Preview
	reading bool
	readErr string
	// state is "" (waiting), "working", "done" or "skipped".
	state string
	op    string
	res   github.Result
	note  string
	// tokenGen is the generation of the lease the attempt held: a 401 on it
	// invalidates that pair, never a newer one.
	tokenGen int
}

// ready says the item can start.
func (it *mergeItem) ready(batch bool, ack string) (bool, string) {
	switch {
	case it.reading:
		return false, "Checking GitHub…"
	case it.readErr != "":
		return false, it.readErr
	case it.prev == nil:
		return false, "no read of GitHub"
	case !it.prev.Eligible:
		return false, it.prev.Reason
	case batch && it.prev.RiskHigh:
		return false, "risk:high is never in a batch"
	case it.prev.RiskHigh && ack != it.prev.Head:
		return false, "open the diff at " + short(it.prev.Head) + " first"
	}
	return true, ""
}

// openMergeDialog opens the confirm dialog for prs and reads each again.
func (a *App) openMergeDialog(prs []pocket.ReadyPR, batch bool) {
	if !a.ghOn() || a.gh.dialog != nil || len(prs) == 0 {
		return
	}
	d := &mergeDialog{batch: batch, origin: a.account}
	for _, p := range prs {
		r, ok := ghRef(p.Ref)
		if !ok || (batch && len(d.items) == batchMax) {
			continue
		}
		d.items = append(d.items, &mergeItem{pr: p, ref: r, reading: true})
	}
	a.gh.dialog = d
	for _, it := range d.items {
		a.ghQueuePreview(it.pr.Ref, it.ref, d)
	}
	a.ghPump()
}

// otherAccount says why the dialog cannot start under the window's account
// now, or "".
func (a *App) otherAccount(d *mergeDialog) string {
	switch {
	case !d.origin.set():
		return "not signed in to a server when this opened: close and open again"
	case a.account != d.origin:
		return "signed in to another account: close and open again"
	}
	return ""
}

// confirmLabel is the dialog's 1 button, and whether it is on.
func (a *App) confirmLabel(d *mergeDialog) (string, bool) {
	label, on := a.confirmText(d)
	return label, on && a.otherAccount(d) == ""
}

func (a *App) confirmText(d *mergeDialog) (string, bool) {
	n := 0
	waiting := false
	for _, it := range d.items {
		if it.reading {
			waiting = true
		}
		if ok, _ := it.ready(d.batch, a.gh.diff[it.pr.Ref]); ok {
			n++
		}
	}
	if d.batch {
		return fmt.Sprintf("Approve + merge %d as %s", n, github.Login), n > 0 && !waiting
	}
	it := d.items[0]
	if it.prev != nil && it.prev.RiskHigh {
		return "I read the diff at " + short(it.prev.Head) + ": approve + merge", n == 1
	}
	return "Approve + merge as " + github.Login, n == 1
}

// mergeDialogView draws the dialog while it is open.
func (a *App) mergeDialogView(c *ui.Context) {
	d := a.gh.dialog
	if d == nil {
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(640).Gap(frostui.SpaceM).AlignItems(ui.Stretch).Children(func() { a.mergeDialogBody(c, d) })
	})
	// Esc and a click outside close it only before it starts, or once it
	// ended.
	if !open && (!d.started || d.done) {
		a.closeMergeDialog()
	}
}

func (a *App) closeMergeDialog() {
	d := a.gh.dialog
	if d == nil || (d.started && !d.done) {
		return
	}
	a.gh.dialog = nil
	a.invalidateDerived()
}

func (a *App) mergeDialogBody(c *ui.Context, d *mergeDialog) {
	title := "Confirm: approve + merge 1 PR"
	if d.batch {
		title = fmt.Sprintf("Confirm: approve + merge %d PRs", len(d.items))
	}
	frostui.Strong(c, title).Wrap()
	switch {
	case d.done:
		frostui.Muted(c, "Done: "+mergeTotals(d)).Wrap()
	case d.started:
		at := 0
		for i, it := range d.items {
			if it.state != "" {
				at = i + 1
			}
		}
		line := fmt.Sprintf("%d of %d", at, len(d.items))
		if d.stopping {
			line += " · stopping after this PR"
		}
		frostui.Muted(c, line)
	default:
		frostui.Muted(c, "Pocket reads each PR again, approves its head, and merges it with the method shown. Nothing starts until you confirm.").Wrap()
		if why := a.otherAccount(d); why != "" {
			frostui.Problem(c, why)
		}
	}
	ui.Scroll(c).MaxHeight(420).Children(func() {
		ui.Column(c).Gap(frostui.SpaceS).AlignItems(ui.Stretch).Children(func() {
			for i, it := range d.items {
				if i > 0 {
					frostui.RowDivider(c.Key(fmt.Sprint("div", i)))
				}
				a.mergeItemRow(c.Key(it.pr.Ref), d, it)
			}
		})
	})
	ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
		ui.Spacer(c)
		switch {
		case d.done:
			if ui.PrimaryButton(c, "Close").Clicked() {
				a.closeMergeDialog()
			}
		case d.started:
			if ui.Button(c, "Stop").Tooltip("Start no other PR; the PR in progress ends normally").Disabled(d.stopping).Clicked() {
				d.stopping = true
			}
		default:
			if ui.Button(c, "Cancel").Clicked() {
				a.closeMergeDialog()
			}
			label, on := a.confirmLabel(d)
			if ui.PrimaryButton(c, label).Disabled(!on).Clicked() {
				a.mergeConfirm(d)
			}
		}
	})
}

func (a *App) mergeItemRow(c *ui.Context, d *mergeDialog, it *mergeItem) {
	ui.Column(c).Gap(2).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			frostui.Mono(c, it.pr.Ref)
			if it.prev != nil && it.prev.RiskHigh {
				frostui.Pill(c, "risk:high", frostui.Danger)
			}
			if it.pr.Level != "" && it.prev != nil && it.prev.RiskHigh {
				frostui.Pill(c, it.pr.Level, levelTone(it.pr.Level))
			}
			frostui.Body(c, firstNonEmpty(titleOf(it), it.pr.Title)).SingleLine().Grow(1)
		})
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			switch {
			case it.state == "working":
				ui.Spinner(c).Size(14, 14)
				frostui.Caption(c, "Approving and merging…").Grow(1)
			case it.state == "done":
				line, tone := resultLine(it.res)
				frostui.Toned(c, line, tone).Wrap().Grow(1)
			case it.state == "skipped":
				frostui.Toned(c, it.note, frostui.Warning).Wrap().Grow(1)
			case it.reading:
				ui.Spinner(c).Size(14, 14)
				frostui.Caption(c, "Checking GitHub…").Grow(1)
			default:
				ok, why := it.ready(d.batch, a.gh.diff[it.pr.Ref])
				if it.prev != nil {
					facts := "head " + short(it.prev.Head) + " · " + methodLabel(*it.prev)
					if it.prev.RiskHigh {
						facts = "risk:high · head " + short(it.prev.Head) + " · " + methodLabel(*it.prev)
					}
					frostui.Caption(c, facts)
				}
				if !ok {
					frostui.Toned(c, why, frostui.Warning).Wrap().Grow(1)
				} else {
					ui.Spacer(c)
				}
			}
		})
	})
}

func titleOf(it *mergeItem) string {
	if it.prev != nil {
		return it.prev.Title
	}
	return ""
}

// resultLine is a Result in words.
func resultLine(r github.Result) (string, frostui.Tone) {
	o := &mergeOp{Result: r.Kind, Reason: r.Reason, Method: r.Method}
	if r.Kind == "" {
		o.Result = "unknown"
	}
	return opLine(o, false)
}

// mergeTotals counts every result of the dialog, "not started" too.
func mergeTotals(d *mergeDialog) string {
	counts := map[string]int{}
	for _, it := range d.items {
		switch it.state {
		case "done":
			counts[it.res.Kind]++
		case "skipped", "":
			counts["not started"]++
		}
	}
	var parts []string
	for _, k := range []struct{ kind, label string }{
		{"merged", "merged"}, {"queued", "queued"}, {"approved", "approved, not merged"}, {"refused", "refused"},
		{"failed", "failed"}, {"unknown", "unknown"}, {"not started", "not started"},
	} {
		if n := counts[k.kind]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, k.label))
		}
	}
	return strings.Join(parts, " · ")
}

// mergeSuccess is the toast of a dialog whose every PR merged or went
// into the merge queue, with its result saved in the journal; ok is false
// for any other end (a refusal, a failure, an unknown outcome, an
// approval without a merge, a PR not started, or a failed journal write).
func (a *App) mergeSuccess(d *mergeDialog) (string, bool) {
	if len(d.items) == 0 {
		return "", false
	}
	for _, it := range d.items {
		if it.state != "done" || (it.res.Kind != "merged" && it.res.Kind != "queued") {
			return "", false
		}
		if o := a.gh.j.find(it.op); o == nil || o.unsaved || o.Result != it.res.Kind {
			return "", false
		}
	}
	if len(d.items) > 1 {
		return "Approve + merge: " + mergeTotals(d), true
	}
	it := d.items[0]
	if it.res.Kind == "queued" {
		return "Queued " + it.pr.Ref + " in the merge queue", true
	}
	o := a.gh.j.find(it.op)
	return "Merged " + it.pr.Ref + " (" + firstNonEmpty(o.Method, "merge") + ")", true
}

// newOpID is a random UUID v4, lowercase.
func newOpID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// mergeConfirm is the owner's click on the confirm button: it freezes 1
// intent per PR from its fresh read, and works the list 1 PR at a time.
// It is the only path that starts a mutation.
func (a *App) mergeConfirm(d *mergeDialog) {
	if d != a.gh.dialog || d.started || !a.gh.j.ok() {
		return
	}
	if why := a.otherAccount(d); why != "" {
		a.console.Add(frostui.LevelWarn, "github", "Approve + merge did not start: "+why)
		return
	}
	if _, on := a.confirmLabel(d); !on {
		return
	}
	d.started = true
	for _, it := range d.items {
		if ok, why := it.ready(d.batch, a.gh.diff[it.pr.Ref]); !ok {
			it.state, it.note = "skipped", "not started: "+why
		}
	}
	a.mergeNext()
}

// mergeNext starts the next PR of the dialog, or ends it.
func (a *App) mergeNext() {
	d := a.gh.dialog
	if d == nil || !d.started || d.done {
		return
	}
	var next *mergeItem
	for _, it := range d.items {
		if it.state == "working" {
			return
		}
		if it.state == "" && next == nil {
			next = it
		}
	}
	if next != nil && d.stopping {
		for _, it := range d.items {
			if it.state == "" {
				it.state, it.note = "skipped", "not started: stopped"
			}
		}
		next = nil
	}
	if next == nil {
		d.done = true
		totals := mergeTotals(d)
		a.console.Add(frostui.LevelInfo, "github", "Approve + merge: "+totals)
		// A dialog where every PR merged or queued has nothing left to
		// read: it closes, and a toast says the result. Any other end
		// keeps it open, with each PR's line and its Close button.
		if msg, ok := a.mergeSuccess(d); ok {
			a.toast(msg)
			a.closeMergeDialog()
			return
		}
		a.toast(totals)
		return
	}
	if why := a.ghBlocked(next.ref); why != "" {
		next.state, next.note = "skipped", "not started: "+why
		a.mergeNext()
		return
	}
	a.mergeStart(d, next)
}

// mergeStart writes the attempt's start, then runs Act on a goroutine.
func (a *App) mergeStart(d *mergeDialog, it *mergeItem) {
	mode := "single"
	if d.batch {
		mode = "batch"
	}
	p := *it.prev
	ack := ""
	if p.RiskHigh {
		ack = a.gh.diff[it.pr.Ref]
	}
	in := github.Intent{Preview: p, Mode: mode, AckHead: ack}
	now := a.opts.Now()
	op := &mergeOp{
		ID:     newOpID(),
		Origin: opOrigin{Server: d.origin.server, User: d.origin.user, Thread: it.pr.Thread},
		Intent: opIntent{Ref: it.pr.Ref, NodeID: p.NodeID, BaseRef: p.BaseRef, BaseOid: p.BaseOid, Head: p.Head,
			Method: p.Method, Queue: p.Queue, Mode: mode, Risk: p.RiskHigh, AckHead: ack},
		Step: "start", At: now,
	}
	j := a.gh.j
	if prev := j.latest(it.pr.Ref); prev != nil && (prev.Result == "" || prev.Result == "unknown") && !a.gh.running[prev.ID] {
		prev.Superseded, prev.EndedAt = op.ID, now
	}
	j.ops = append(j.ops, op)
	it.state, it.op = "working", op.ID
	a.console.Add(frostui.LevelCommand, "github", fmt.Sprintf("Approve + merge %s at %s as %s (%s)", it.pr.Ref, short(p.Head), github.Login, methodLabel(p)))
	if err := j.save(); err != nil {
		a.console.Add(frostui.LevelError, "github", "journal write failed: "+err.Error())
		a.mergeFinished(it, op, github.Result{Op: op.ID, Kind: "refused", Step: "read", Reason: "journal write failed", Err: err})
		return
	}
	a.gh.running[op.ID] = true
	apiFn, owner, opID, post := a.gh.api, it.ref.Owner, op.ID, a.post
	gen := 0
	a.githubDo("Approve + merge "+it.pr.Ref, func(ctx context.Context) github.Result {
		api, tokenGen, release, err := apiFn(ctx, owner)
		gen = tokenGen
		if err != nil {
			return github.Result{Op: opID, Kind: "failed", Step: "read", Reason: "no GitHub client: " + err.Error(), Err: err}
		}
		if release != nil {
			defer release()
		}
		// Each step is written on the UI thread before Act goes on: a
		// failed write before a mutation stops it (fail closed).
		step := func(name string) error {
			ch := make(chan error, 1)
			post(func() { ch <- a.journalStep(opID, name) })
			select {
			case err := <-ch:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return api.Act(ctx, in, opID, step)
	}, func(res github.Result) {
		delete(a.gh.running, opID)
		it.tokenGen = gen
		a.jobErr(res.Err, gen)
		a.mergeFinished(it, op, res)
	})
}

// journalStep writes the checkpoint an attempt reached before a mutation:
// "about to approve" or "about to merge". Act also tells its final kind;
// that is no checkpoint, and is ignored: the result is saved by
// applyResult, and until then the last checkpoint must stay, so a crash
// after a merge was sent still reads as "about to merge".
func (a *App) journalStep(id, name string) error {
	if name != stepAboutToApprove && name != stepAboutToMerge {
		return nil
	}
	o := a.gh.j.find(id)
	if o == nil {
		return errors.New("the attempt is not in the journal")
	}
	o.Step = name
	if err := a.gh.j.save(); err != nil {
		a.console.Add(frostui.LevelError, "github", "journal write failed: "+err.Error())
		return err
	}
	return nil
}

// mergeFinished takes 1 attempt's result, then starts the next PR.
func (a *App) mergeFinished(it *mergeItem, op *mergeOp, res github.Result) {
	it.state, it.res = "done", res
	a.applyResult(op, res)
	d := a.gh.dialog
	var api *github.APIError
	if d != nil && errors.As(res.Err, &api) {
		switch {
		case api.Status == 401:
			for _, other := range d.items {
				if other.state == "" {
					other.state, other.note = "skipped", "not started: GitHub refused the sign-in"
				}
			}
		case (api.Status == 403 || api.Status == 429) && !api.RetryAfter.IsZero():
			until := api.RetryAfter.In(a.opts.Now().Location()).Format("15:04")
			for _, other := range d.items {
				if other.state == "" {
					other.state, other.note = "skipped", "not started: rate limit until "+until
				}
			}
		}
	}
	a.mergeNext()
}

// applyResult writes a result to its attempt, logs it and posts its
// receipt. A failed write keeps the PR locked.
func (a *App) applyResult(op *mergeOp, res github.Result) {
	now := a.opts.Now()
	op.Result, op.Reason, op.Method, op.MergeCommit = res.Kind, res.Reason, res.Method, res.MergeCommit
	if res.Step != "" {
		op.Step = res.Step
	}
	if op.Result == "merged" && op.Method == "" && !op.Intent.Queue {
		op.Method = op.Intent.Method // a direct merge uses the frozen method
	}
	op.EndedAt = now
	// 1 receipt per result: a new result adds its own, and never replaces
	// another result's (the Desk keys them <op>:<result>).
	if op.receipt(op.Result) == nil {
		op.Receipts = append(op.Receipts, newReceipt(op, now))
	}
	delete(a.gh.notMerged, op.Intent.Ref)
	line, _ := opLine(op, false)
	a.console.Add(frostui.LevelInfo, "github", op.Intent.Ref+": "+line)
	if err := a.gh.j.save(); err != nil {
		op.unsaved = true
		a.console.Add(frostui.LevelError, "github", "journal write failed: "+err.Error())
	} else {
		op.unsaved = false
	}
	a.invalidateDerived()
	a.postReceipt(op)
}

// The checkpoints Act tells its step callback before each mutation.
const (
	stepAboutToApprove = "about to approve"
	stepAboutToMerge   = "about to merge"
)

// mergeSent says the attempt may have sent its merge or enqueue.
func mergeSent(o *mergeOp) bool {
	switch o.Step {
	case stepAboutToMerge, "merge", "reconcile merge":
		return true
	}
	return false
}

func intentOf(o *mergeOp) (github.Intent, bool) {
	r, ok := ghRef(o.Intent.Ref)
	if !ok {
		return github.Intent{}, false
	}
	return github.Intent{Mode: o.Intent.Mode, AckHead: o.Intent.AckHead, Preview: github.Preview{
		Ref: r, NodeID: o.Intent.NodeID, BaseRef: o.Intent.BaseRef, BaseOid: o.Intent.BaseOid, Head: o.Intent.Head,
		Method: o.Intent.Method, Queue: o.Intent.Queue, RiskHigh: o.Intent.Risk, Eligible: true,
	}}, true
}

// settleOp reads, by reads only, what an attempt did, and gives the result
// to done on the UI thread.
func (a *App) settleOp(o *mergeOp, wait bool, done func(github.Result)) bool {
	in, ok := intentOf(o)
	if !ok || a.gh.busy["op:"+o.ID] {
		return false
	}
	a.gh.busy["op:"+o.ID] = true
	apiFn, owner, id := a.gh.api, in.Preview.Ref.Owner, o.ID
	gen := 0
	a.githubDo("Reading what "+o.Intent.Ref+" became", func(ctx context.Context) github.Result {
		api, tokenGen, release, err := apiFn(ctx, owner)
		gen = tokenGen
		if err != nil {
			return github.Result{Op: id, Kind: "unknown", Step: "settle", Reason: "no GitHub client", Err: err}
		}
		if release != nil {
			defer release()
		}
		return api.Settle(ctx, in, id, wait)
	}, func(res github.Result) {
		a.jobErr(res.Err, gen)
		delete(a.gh.busy, "op:"+id)
		done(res)
	})
	return true
}

// settleUnknown maps a settle of an unfinished or unknown attempt: a
// merge, a queue entry or a merge by another is that result; the owner's
// approval when the merge was never sent is "approved"; else unknown.
func (a *App) settleUnknown(o *mergeOp, res github.Result, approvedWhy string) {
	if a.gh.j.find(o.ID) != o || o.Superseded != "" {
		return
	}
	switch {
	case res.Kind == "merged" && !queueMethodKnown(o, res):
		// A queue merge with no method from the queue's rules: keep what
		// the journal says and read it again later.
	case res.Kind == "merged" || res.Kind == "queued" || res.Kind == "refused":
		a.applyResult(o, res)
	case res.Kind == "approved" && !mergeSent(o):
		res.Reason = approvedWhy
		a.applyResult(o, res)
	case o.Result == "":
		why := "Pocket stopped during the attempt"
		if mergeSent(o) {
			why = "Pocket stopped during the merge: merge may be pending"
		}
		a.applyResult(o, github.Result{Op: o.ID, Kind: "unknown", Step: o.Step, Reason: why})
	}
}

// queueMethodKnown says a merged result names a real method: on a queue
// base, the queue's own method from its rules, never one Pocket makes up.
func queueMethodKnown(o *mergeOp, res github.Result) bool {
	if !o.Intent.Queue {
		return true
	}
	switch res.Method {
	case "merge", "squash", "rebase":
		return true
	}
	return false
}

// reconcileJournal settles, at start, each attempt a crash left
// unfinished or unknown, and each queued one. A batch never resumes.
func (a *App) reconcileJournal() {
	if !a.gh.j.ok() {
		return
	}
	for _, o := range slices.Clone(a.gh.j.ops) {
		if o.Superseded != "" || a.gh.running[o.ID] {
			continue
		}
		switch o.Result {
		case "", "unknown":
			a.settleOp(o, false, func(res github.Result) { a.settleUnknown(o, res, "Pocket stopped before the merge") })
		case "queued":
			a.settleOp(o, false, func(res github.Result) { a.settleQueued(o, res) })
		}
	}
}

// mergeCheckAgain is the owner's Check again on an unknown attempt: reads
// only, with the waits of a reconciliation.
func (a *App) mergeCheckAgain(ref string) {
	o := a.gh.j.latest(ref)
	if o == nil {
		return
	}
	a.gh.busy[ref] = true
	if !a.settleOp(o, true, func(res github.Result) {
		delete(a.gh.busy, ref)
		if o.unsaved && o.Result != "" && o.Result != "unknown" {
			// The result is known; only its write failed: write it again.
			a.applyResult(o, github.Result{Op: o.ID, Kind: o.Result, Step: o.Step, Reason: o.Reason, Method: o.Method, MergeCommit: o.MergeCommit})
			return
		}
		a.settleUnknown(o, res, "approved, not merged")
	}) {
		delete(a.gh.busy, ref)
	}
}

// githubDeskRead follows a Desk read: previews, the state of merged and
// queued PRs, and the receipts that wait.
func (a *App) githubDeskRead() {
	if !a.ghOn() || a.desk.view == nil || a.gh.j == nil {
		return
	}
	a.ghPreviewReady()
	for _, p := range a.desk.view.Ready {
		if o := a.gh.j.latest(p.Ref); o != nil && o.Result == "merged" && !a.gh.notMerged[p.Ref] {
			a.mergedState(p.Ref)
		}
	}
	// Every queued attempt, whether the Desk still lists its PR or not:
	// the merge and its receipt must not wait on the Desk row.
	for _, o := range slices.Clone(a.gh.j.ops) {
		if o.Result == "queued" && o.Superseded == "" {
			a.settleOp(o, false, func(res github.Result) { a.settleQueued(o, res) })
		}
	}
	a.resaveJournal()
	a.retryReceipts()
}

// resaveJournal writes the journal again when the write of a result
// failed. On success those attempts leave the journal-write lock (an
// unknown result keeps its own lock), and retryReceipts posts their
// receipts.
func (a *App) resaveJournal() {
	if !a.gh.j.ok() {
		return
	}
	var unsaved []*mergeOp
	for _, o := range a.gh.j.ops {
		if o.unsaved {
			unsaved = append(unsaved, o)
		}
	}
	if len(unsaved) == 0 {
		return
	}
	if err := a.gh.j.save(); err != nil {
		a.console.Add(frostui.LevelError, "github", "journal write failed again: "+err.Error())
		return
	}
	for _, o := range unsaved {
		o.unsaved = false
		a.console.Add(frostui.LevelInfo, "github", o.Intent.Ref+": "+o.Result+" written to the journal")
	}
	a.invalidateDerived()
}

// mergedState reads a merged PR the Desk still lists: its card shows again
// when GitHub does not say MERGED.
func (a *App) mergedState(ref string) {
	r, ok := ghRef(ref)
	key := "state:" + ref
	if !ok || a.gh.busy[key] {
		return
	}
	a.gh.busy[key] = true
	apiFn := a.gh.api
	gen := 0
	ghRead(a, "Reading the state of "+ref, func(ctx context.Context) (github.PRState, error) {
		api, tokenGen, release, err := apiFn(ctx, r.Owner)
		gen = tokenGen
		if err != nil {
			return github.PRState{}, err
		}
		if release != nil {
			defer release()
		}
		return api.State(ctx, r)
	}, func(st github.PRState, err error) {
		a.jobErr(err, gen)
		delete(a.gh.busy, key)
		if err == nil && st.State != "MERGED" {
			a.gh.notMerged[ref] = true
			a.ghPreviewReady()
		}
	})
}

// settleQueued follows a queued PR: merged, still queued, or out of the
// queue (its card comes back with its gates).
func (a *App) settleQueued(o *mergeOp, res github.Result) {
	if a.gh.j.find(o.ID) != o || o.Result != "queued" {
		return
	}
	switch {
	case res.Kind == "merged":
		// Only with the queue's own method from its rules: never a made-up
		// one. Without it the attempt stays queued and is read again.
		if queueMethodKnown(o, res) {
			a.applyResult(o, res)
		}
	case res.Kind == "queued":
	case res.Kind == "refused":
		a.applyResult(o, res)
	case res.Kind == "approved" || (res.Kind == "unknown" && res.Err == nil):
		reason := "left the merge queue"
		if res.QueueExit != "" {
			reason += ": " + res.QueueExit
		}
		a.applyResult(o, github.Result{Op: o.ID, Kind: "approved", Step: "settle", Reason: reason})
		delete(a.gh.previews, o.Intent.Ref)
		a.ghPreviewReady()
	}
}

// retryReceipts posts each receipt that waits.
func (a *App) retryReceipts() {
	if a.gh.j == nil {
		return
	}
	for _, o := range a.gh.j.ops {
		a.postReceipt(o)
	}
}

// newReceipt is the receipt of the attempt's current result, pending.
func newReceipt(o *mergeOp, now time.Time) *opReceipt {
	r := &opReceipt{Result: o.Result, State: receiptPending, Since: now}
	switch o.Result {
	case "merged":
		r.Method = o.Method
	case "queued":
		r.Method = "queue"
	case "approved":
		r.Reason = o.Reason
	default:
		r.Reason = firstNonEmpty(o.Reason, o.Step, o.Result)
	}
	if rs := []rune(r.Reason); len(rs) > 300 {
		r.Reason = string(rs[:300])
	}
	return r
}

// receiptOf is the Desk body of 1 receipt of an attempt.
func receiptOf(o *mergeOp, r *opReceipt) pocket.MergeRecord {
	return pocket.MergeRecord{Op: o.ID, Ref: o.Intent.Ref, Head: o.Intent.Head, Result: r.Result, Method: r.Method, Reason: r.Reason}
}

// postReceipt posts each receipt of an attempt that waits to its Desk
// thread, with the attempt's op, only while the window is signed in to the
// server and user the attempt started under. It never undoes or hides the
// GitHub result.
func (a *App) postReceipt(o *mergeOp) {
	if o.unsaved {
		return
	}
	for _, r := range o.Receipts {
		a.postOne(o, r)
	}
}

func (a *App) postOne(o *mergeOp, r *opReceipt) {
	if r.State != receiptPending {
		return
	}
	if a.opts.Now().Sub(r.Since) > receiptRetry {
		r.State = receiptGaveUp
		a.console.Add(frostui.LevelWarn, "github", o.Intent.Ref+": gave up recording "+r.Result+" on the Desk")
		if err := a.gh.j.save(); err != nil {
			a.console.Add(frostui.LevelError, "github", "journal write failed: "+err.Error())
		}
		return
	}
	client := a.link.client
	if client == nil || a.account.server != o.Origin.Server || a.account.user != o.Origin.User || o.Origin.Thread == "" {
		return
	}
	key := o.ID + ":" + r.Result
	if a.gh.posting[key] == client {
		return
	}
	a.gh.posting[key] = client
	rec, thread, id, result := receiptOf(o, r), o.Origin.Thread, o.ID, r.Result
	a.request("Recording "+o.Intent.Ref+" ("+result+") on the Desk", func(ctx context.Context, c *pocket.Client) error {
		_, err := c.RecordMerge(ctx, thread, rec)
		return err
	}, func(err error) {
		if a.gh.posting[key] == client {
			delete(a.gh.posting, key)
		}
		o := a.gh.j.find(id)
		if o == nil {
			return
		}
		r := o.receipt(result)
		if r == nil || r.State != receiptPending {
			return
		}
		var api *pocket.APIError
		switch {
		case err == nil:
			r.State, r.Failed = receiptRecorded, false
		case errors.As(err, &api) && (api.Status == 400 || api.Status == 409):
			r.State = receiptRejected
			a.console.Add(frostui.LevelWarn, "github", "The Desk refused the receipt of "+o.Intent.Ref+": "+err.Error())
		default:
			r.Failed = true
		}
		if err := a.gh.j.save(); err != nil {
			a.console.Add(frostui.LevelError, "github", "journal write failed: "+err.Error())
		}
	})
}

// Settings > GitHub: the App's sign-in, the preflight, Batches, and the
// journal.

// readyRepos are the repositories of owner in the Ready list, each with
// its first ready PR, in the Desk's order.
func (a *App) readyRepos(owner string) []github.Ref {
	var out []github.Ref
	seen := map[string]bool{}
	if a.desk.view == nil {
		return nil
	}
	for _, p := range a.desk.view.Ready {
		r, ok := ghRef(p.Ref)
		if !ok || r.Owner != owner || seen[r.Name] {
			continue
		}
		seen[r.Name] = true
		out = append(out, r)
	}
	return out
}

// otherLogin is a sign-in that is not for bketelsen.
type otherLogin struct{ login string }

func (e *otherLogin) Error() string { return "this sign-in is for " + e.login }

// signedUp says this Pocket holds the sign-in lock and the keyring works:
// before that a sign-in could put a pair in the keyring without the lock.
func (a *App) signedUp() bool { return a.gh.open && !a.gh.held && a.gh.keyErr == "" }

// busySignIn says a sign-in is in progress, so its button is off.
func (a *App) busySignIn() bool {
	switch a.gh.phase {
	case signInStarting, signInCode, signInChecking, signInSaving:
		return true
	}
	return false
}

// githubSignIn is Sign in with GitHub: the device code, off the UI thread.
func (a *App) githubSignIn() {
	auth := a.opts.GitHubAuth
	if !a.ghOn() || a.busySignIn() || !a.signedUp() {
		return
	}
	a.stopPoll()
	a.gh.signInGen = auth.BeginSignIn()
	gen := a.gh.signInGen
	a.gh.fail = ""
	a.gh.phase = signInStarting
	a.syncStatus()
	a.ghReview()
	ghRead(a, "Asking GitHub for a sign-in code", func(ctx context.Context) (github.DeviceCode, error) {
		return auth.Start(ctx)
	}, func(dc github.DeviceCode, err error) {
		if gen != a.gh.signInGen {
			return // an old sign-in: its result saves nothing
		}
		if err != nil {
			a.gh.phase, a.gh.fail = signInNone, err.Error()
			a.ghReview()
			return
		}
		a.gh.phase, a.gh.code, a.gh.codeAt = signInCode, dc, a.opts.Now()
		a.ghReview()
		a.pollSignIn(dc, gen)
		a.countdown()
	})
}

// pollSignIn polls for the pair on its own goroutine and deadline (the
// code's expiry). Cancel, Sign out, a new sign-in and exit stop it;
// another page does not.
func (a *App) pollSignIn(dc github.DeviceCode, gen int) {
	auth := a.opts.GitHubAuth
	ctx, cancel := context.WithDeadline(context.Background(), dc.Expires)
	a.gh.pollID++
	id := a.gh.pollID
	a.gh.cancelPoll = cancel
	post := a.post
	go func() {
		tok, err := auth.Poll(ctx, dc)
		post(func() {
			cancel()
			// Only its own handle: a newer poll's stays.
			if a.gh.pollID == id {
				a.gh.cancelPoll = nil
			}
			if gen != a.gh.signInGen || a.gh.phase != signInCode {
				return // cancelled, signed out, or a newer sign-in
			}
			if err != nil {
				a.gh.phase, a.gh.fail = signInNone, err.Error()
				a.ghReview()
				return
			}
			a.checkPair(tok, gen)
		})
	}()
}

// countdown asks for a frame every second while the code shows: the view
// draws the time left from the clock.
func (a *App) countdown() {
	if a.gh.phase != signInCode {
		return
	}
	a.after(time.Second, func() {
		if a.gh.phase != signInCode {
			return
		}
		a.frame()
		a.countdown()
	})
}

// frame asks for 1 more view of the window.
func (a *App) frame() { a.post(func() {}) }

// stopPoll cancels a running poll, if 1 runs, and forgets its handle.
func (a *App) stopPoll() {
	if a.gh.cancelPoll != nil {
		a.gh.cancelPoll()
		a.gh.cancelPoll = nil
	}
}

// checkPair reads the App's installations with the new pair, and adopts it
// only when the sign-in is bketelsen's: nothing is kept otherwise.
func (a *App) checkPair(tok github.AppToken, gen int) {
	auth, newClient := a.opts.GitHubAuth, a.gh.newClient
	a.gh.phase = signInChecking
	a.ghReview()
	ghRead(a, "Checking the new GitHub sign-in", func(ctx context.Context) (github.AppCheck, error) {
		check, err := newClient(tok.Access).CheckApp(ctx, githubOwners)
		if err != nil {
			return github.AppCheck{}, err
		}
		if check.Login != github.Login {
			return check, &otherLogin{login: check.Login}
		}
		return check, nil
	}, func(check github.AppCheck, err error) {
		if gen != a.gh.signInGen {
			return
		}
		var other *otherLogin
		switch {
		case errors.As(err, &other):
			a.gh.phase, a.gh.fail = signInNone, other.Error()
			a.ghReview()
			return
		case err != nil:
			a.gh.phase, a.gh.fail = signInNone, err.Error()
			a.ghReview()
			return
		case tok.ClientID != github.AppClientID:
			a.gh.phase, a.gh.fail = signInNone, "this sign-in is for another build of Pocket"
			a.ghReview()
			return
		}
		// CheckApp names the person the pair acts as; the source keeps the
		// login with the pair.
		tok.Login = check.Login
		a.gh.phase = signInSaving
		a.ghReview()
		ghRead(a, "Saving the GitHub sign-in", func(context.Context) (struct{}, error) {
			return struct{}{}, auth.Adopt(tok, gen)
		}, func(_ struct{}, err error) {
			if gen != a.gh.signInGen || errors.Is(err, github.ErrStale) {
				return
			}
			if err != nil {
				a.gh.phase = signInNone
				a.gh.fail = "the keyring did not keep the sign-in: " + err.Error()
				a.syncStatus()
				a.ghReview()
				return
			}
			a.gh.phase, a.gh.fail = signInSignedIn, ""
			a.syncStatus()
			a.setOwners(check)
			a.forgetLegacy()
			a.dropPreviewsAll()
			a.ghReview()
			a.ghPreviewReady()
		})
	})
}

// cancelSignIn is Cancel on the code: the sign-in saves nothing.
func (a *App) cancelSignIn() {
	if !a.ghOn() {
		return
	}
	a.stopPoll()
	a.opts.GitHubAuth.CancelSignIn(a.gh.signInGen)
	a.gh.signInGen++
	a.gh.phase, a.gh.fail = signInNone, ""
	a.ghReview()
}

// forgetLegacy deletes the 2 old token keys that ADR-0010 replaces
// (ADR-0008) after a sign-in. A failure goes to Activity only: the
// sign-in stands.
func (a *App) forgetLegacy() {
	if !a.gh.legacy {
		return
	}
	auth := a.opts.GitHubAuth
	ghRead(a, "Deleting the 2 old GitHub tokens", func(context.Context) (struct{}, error) {
		return struct{}{}, auth.ForgetLegacy()
	}, func(_ struct{}, err error) {
		if err == nil {
			a.gh.legacy = false
			a.ghReview()
		}
	})
}

// syncStatus takes the sign-in's state; it never waits.
func (a *App) syncStatus() {
	if auth := a.opts.GitHubAuth; auth != nil {
		a.gh.status = auth.Status()
	}
}

// githubSignOut is Sign out: the pair leaves memory at once, and the entry
// goes off the UI thread.
func (a *App) githubSignOut() {
	auth := a.opts.GitHubAuth
	if !a.ghOn() {
		return
	}
	a.stopPoll()
	a.gh.signInGen++
	a.gh.phase, a.gh.fail = signInNone, ""
	a.unknownOwners()
	a.dropPreviewsAll()
	a.gh.preflight = map[string]*preflightResult{}
	a.syncStatus()
	a.ghReview()
	ghRead(a, "Signing out of GitHub", func(context.Context) (github.AppStatus, error) {
		err := auth.Forget()
		return auth.Status(), err
	}, func(st github.AppStatus, err error) {
		a.gh.status, a.gh.deleteWhy = st, ""
		if err != nil {
			a.gh.deleteWhy = err.Error()
			a.console.Add(frostui.LevelWarn, "github", "Signing out: "+err.Error())
		}
		a.ghReview()
	})
}

// githubSaveAgain saves the pair the keyring did not keep.
func (a *App) githubSaveAgain() {
	auth := a.opts.GitHubAuth
	if !a.ghOn() {
		return
	}
	ghRead(a, "Saving the GitHub sign-in again", func(context.Context) (github.AppStatus, error) {
		err := auth.SaveAgain()
		return auth.Status(), err
	}, func(st github.AppStatus, err error) {
		a.gh.status = st
		if err != nil {
			a.console.Add(frostui.LevelWarn, "github", "Saving the GitHub sign-in: "+err.Error())
		}
		if st.SignedIn && !st.Unsaved {
			a.gh.phase = signInSignedIn
		}
		a.ghReview()
	})
}

// githubDeleteAgain deletes the entry a Sign out could not delete.
func (a *App) githubDeleteAgain() {
	auth := a.opts.GitHubAuth
	if !a.ghOn() {
		return
	}
	ghRead(a, "Deleting the GitHub sign-in again", func(context.Context) (github.AppStatus, error) {
		err := auth.DeleteAgain()
		return auth.Status(), err
	}, func(st github.AppStatus, err error) {
		a.gh.status = st
		if err != nil {
			a.console.Add(frostui.LevelWarn, "github", "Deleting the GitHub sign-in: "+err.Error())
		}
		a.ghReview()
	})
}

// signInEnded stops the feature when a job finds the sign-in over: every
// owner is unknown again, and the previews and the preflight rows go.
func (a *App) signInEnded() {
	if !a.ghOn() {
		return
	}
	a.syncStatus()
	if a.gh.status.SignedIn {
		return // a result of an old pair, not of this sign-in
	}
	a.stopPoll()
	a.gh.phase = signInNone
	a.gh.signInGen++
	a.unknownOwners()
	a.dropPreviewsAll()
	a.gh.preflight = map[string]*preflightResult{}
	a.ghReview()
}

// jobErr takes a job's error: a 401 invalidates that generation, and an
// ended sign-in stops the feature until the person signs in again.
func (a *App) jobErr(err error, tokenGen int) {
	if err == nil {
		return
	}
	if a.refused(err, tokenGen) {
		return
	}
	if errors.Is(err, github.ErrSignIn) || errors.Is(err, github.ErrNotSignedIn) {
		a.signInEnded()
	}
}

// dropPreviews forgets the previews of owner's PRs, and the reads of them
// still in flight.
func (a *App) dropPreviews(owner string) {
	a.gh.epoch[owner]++
	for key := range a.gh.previews {
		if r, ok := ghRef(key); ok && r.Owner == owner {
			delete(a.gh.previews, key)
		}
	}
	a.gh.queue = slices.DeleteFunc(a.gh.queue, func(q ghReadReq) bool {
		return q.ref.Owner == owner && q.dialog == nil
	})
}

// preflightRun is what 1 preflight job read: the App's installations, and
// the rows of each owner's ready repositories.
type preflightRun struct {
	check github.AppCheck
	rows  map[string][]github.PreflightRow
	// gen is the generation of the token the job leased: its 401 or ended
	// sign-in is taken on the UI thread, never here.
	gen int
}

// preflightOf is the preflight table of 1 owner.
func (a *App) preflightOf(owner string) *preflightResult {
	pf := a.gh.preflight[owner]
	if pf == nil {
		pf = &preflightResult{}
		a.gh.preflight[owner] = pf
	}
	return pf
}

// checkingOwners says a read of the App's installations runs: the check of
// the start, or a preflight. The cards say so, and no owner is ready yet.
func (a *App) checkingOwners() bool { return a.gh.checkBusy || a.gh.preflightBusy }

// preflightBusy says a preflight job runs.
func (a *App) preflightBusy() bool {
	if a.gh.preflightBusy {
		return true
	}
	for _, owner := range githubOwners {
		if pf := a.gh.preflight[owner]; pf != nil && pf.busy {
			return true
		}
	}
	return false
}

// githubPreflight is Run preflight: 1 job on 1 lease reads the App's
// installations first, then 1 ready PR of each repository of each ready
// owner: rules, rulesets, branch and the PR query. Reads only. A
// repository that fails gets no button.
func (a *App) githubPreflight() {
	if !a.ghOn() || a.preflightBusy() {
		return
	}
	repos := map[string][]github.Ref{}
	for _, owner := range githubOwners {
		repos[owner] = a.readyRepos(owner)
	}
	apiFn := a.gh.api
	// The installations are checked again: the cards say so while it runs.
	a.gh.preflightBusy = true
	a.gh.preflightJob++
	job, signInGen := a.gh.preflightJob, a.gh.signInGen
	for _, owner := range githubOwners {
		pf := a.preflightOf(owner)
		pf.busy, pf.err = true, ""
	}
	// The job fills its own run, so the completion knows which token
	// generation it leased: the app's state changes only there, on the UI
	// thread.
	run := preflightRun{}
	ghRead(a, "Preflight of the GitHub App", func(ctx context.Context) (preflightRun, error) {
		api, gen, release, err := apiFn(ctx, githubOwners[0])
		run.gen = gen
		if err != nil {
			return run, err
		}
		if release != nil {
			defer release()
		}
		check, err := api.CheckApp(ctx, githubOwners)
		if err != nil {
			return run, err
		}
		run.check, run.rows = check, map[string][]github.PreflightRow{}
		for _, owner := range githubOwners {
			if check.Owners[owner] != "" {
				continue // not ready: its repositories are not read
			}
			for _, r := range repos[owner] {
				run.rows[owner] = append(run.rows[owner], api.Preflight(ctx, r))
			}
		}
		return run, nil
	}, func(got preflightRun, err error) {
		// A preflight that started before a Sign out, or a new sign-in,
		// says nothing of the sign-in there is now: only its own busy flags
		// go.
		if signInGen != a.gh.signInGen {
			if a.gh.preflightJob == job {
				a.gh.preflightBusy = false
				for _, owner := range githubOwners {
					a.preflightOf(owner).busy = false
				}
			}
			a.ghReview()
			return
		}
		a.jobErr(err, got.gen)
		a.refusedRows(got)
		if a.gh.preflightJob == job {
			a.gh.preflightBusy = false
		}
		for _, owner := range githubOwners {
			a.preflightOf(owner).busy = false
		}
		if err != nil {
			for _, owner := range githubOwners {
				a.preflightOf(owner).err = err.Error()
			}
			a.ghReview()
			return
		}
		a.setOwners(got.check)
		for _, owner := range githubOwners {
			pf := a.preflightOf(owner)
			pf.err = ""
			pf.rows = got.rows[owner]
			for _, row := range pf.rows {
				if row.Reason != "" {
					a.gh.blocked[row.Repo] = "preflight: " + row.Reason
				} else {
					delete(a.gh.blocked, row.Repo)
				}
			}
		}
		a.ghReview()
	})
}

// refusedRow takes the first refused read of a preflight's rows: a 401 there
// invalidates the lease the job held, which the rows alone cannot say.
func (a *App) refusedRows(run preflightRun) {
	for _, owner := range githubOwners {
		for _, row := range run.rows[owner] {
			if row.Err == nil {
				continue
			}
			if a.refused(row.Err, run.gen) {
				return
			}
		}
	}
}

// githubSettingsPage is Settings > GitHub.
func (a *App) githubSettingsPage() frostui.SettingsPage {
	return frostui.SettingsPage{ID: "github", Title: "GitHub", Icon: "pocket-branch",
		Keywords: "approve merge token pull request batches preflight journal keyring",
		Body:     a.githubSettingsBody}
}

func (a *App) githubSettingsBody(c *ui.Context) {
	a.syncStatus()
	ui.Column(c).Gap(frostui.SpaceM).AlignItems(ui.Stretch).Children(func() {
		if !a.ghOn() {
			frostui.Muted(c, "Approve + merge needs the desktop keyring.").Wrap()
			return
		}
		a.journalSettings(c)
		a.signInSettings(c)
		a.preflightSettings(c)
		frostui.SectionHeader(c, frostui.SectionHead{Title: "Batches"})
		ui.Row(c).Gap(frostui.SpaceM).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				frostui.Body(c, "Batches")
				frostui.Caption(c, fmt.Sprintf("Check boxes on eligible cards, Select all eligible, and Approve + merge N (up to %d). risk:high is never in a batch.", batchMax)).Wrap()
			})
			on := a.settings.GitHubBatches
			ui.Switch(c, &on).Label("Batches")
			if on != a.settings.GitHubBatches {
				a.settings.GitHubBatches = on
				if !on {
					clear(a.gh.sel)
				}
				a.saveSettings()
			}
		})
	})
}

// signInSettings draws where the App's sign-in is, and what it offers.
func (a *App) signInSettings(c *ui.Context) {
	frostui.SectionHeader(c, frostui.SectionHead{Title: "Sign-in", Note: "the GitHub App " + github.AppName})
	st := a.gh.status
	switch {
	case a.gh.keyErr != "":
		frostui.Muted(c, "Approve + merge needs the desktop keyring.").Wrap()
		frostui.Problem(c, a.gh.keyErr)
	case a.gh.held:
		frostui.Muted(c, "another Pocket holds the GitHub sign-in. Approve + merge is off in this window.").Wrap()
	case st.Unsaved:
		frostui.Problem(c, "the keyring did not keep the new GitHub sign-in").Wrap()
		a.signInButtons(c, "Save again", a.githubSaveAgain)
	case st.DeleteFailed && !st.SignedIn:
		why := "Signed out for this run, but the keyring still holds the sign-in"
		if a.gh.deleteWhy != "" {
			why += ": " + a.gh.deleteWhy
		}
		frostui.Problem(c, why).Wrap()
		a.signInButtons(c, "Delete again", a.githubDeleteAgain)
	case a.gh.phase == signInOpening:
		// Open has not answered: no button until it does, because a
		// sign-in without the lock could write the keyring.
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(14, 14)
			frostui.Caption(c, "Opening the GitHub sign-in…").Grow(1)
		})
	case a.gh.phase == signInCode:
		a.codeSettings(c)
	case a.busySignIn():
		// Cancel is off only while saving.
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(14, 14)
			label := "checking the sign-in…"
			if a.gh.phase == signInStarting {
				label = "asking GitHub for a code…"
			}
			if a.gh.phase == signInSaving {
				label = "Saving…"
			}
			frostui.Caption(c, label).Grow(1)
			if a.gh.phase != signInSaving {
				if ui.Button(c, "Cancel").Clicked() {
					a.cancelSignIn()
				}
			}
		})
	case !st.SignedIn:
		if st.Reason != "" {
			frostui.Problem(c, st.Reason+": sign in again").Wrap()
		}
		if a.gh.fail != "" {
			frostui.Problem(c, a.gh.fail).Wrap()
		}
		if a.gh.legacy {
			frostui.Muted(c, "Pocket now signs in through its GitHub App. Sign in, then delete your 2 old fine-grained tokens on GitHub.").Wrap()
		}
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			if ui.PrimaryButton(c, "Sign in with GitHub").Clicked() {
				a.githubSignIn()
			}
		})
	default:
		a.signedInSettings(c)
	}
}

// codeSettings shows the device code, with the time left and Cancel.
func (a *App) codeSettings(c *ui.Context) {
	left := a.gh.code.Expires.Sub(a.opts.Now()).Round(time.Second)
	if left < 0 {
		left = 0
	}
	frostui.Muted(c, "Enter this code only here, now. Check that GitHub names "+github.AppName+".").Wrap()
	ui.Row(c).Gap(frostui.SpaceM).AlignItems(ui.Center).Children(func() {
		frostui.Mono(c, a.gh.code.UserCode).FontSize(26).Bold()
		if ui.Button(c, "Copy code").Clicked() {
			c.WriteClipboard(a.gh.code.UserCode)
			a.toast("The code is on the clipboard")
		}
		if ui.Button(c, "Open github.com/login/device").Clicked() {
			a.openURL(deviceURL)
		}
	})
	frostui.Caption(c, "Time left: "+left.String()).Wrap()
	ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, "Cancel").Clicked() {
			a.cancelSignIn()
		}
	})
}

// signedInSettings shows the sign-in, each owner's state, the preflight and
// Sign out.
func (a *App) signedInSettings(c *ui.Context) {
	st := a.gh.status
	parts := []string{"Signed in as " + st.Login + " through " + github.AppName}
	for _, owner := range githubOwners {
		parts = append(parts, owner+": "+a.ownerWord(owner))
	}
	again := st.RefreshExpires.In(a.opts.Now().Location()).Format("2006-01-02 15:04")
	parts = append(parts, "sign in again by "+again)
	frostui.Body(c, strings.Join(parts, " · ")).Wrap()
	for _, owner := range githubOwners {
		o := a.gh.owners[owner]
		if o.known && o.reason != "" {
			frostui.Problem(c, owner+": "+o.reason).Wrap()
		}
		if o.note != "" {
			frostui.Caption(c, owner+": "+o.note).Wrap()
		}
	}
	if a.gh.fail != "" {
		frostui.Problem(c, a.gh.fail).Wrap()
	}
	ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
		if a.preflightBusy() {
			ui.Spinner(c).Size(14, 14)
		}
		if ui.Button(c, "Run preflight").Label("Run preflight").Disabled(a.preflightBusy()).Clicked() {
			a.githubPreflight()
		}
		if ui.Button(c, "Sign out").Clicked() {
			a.githubSignOut()
		}
	})
	frostui.Caption(c, "Sign out forgets the sign-in on this desktop. To end it on GitHub: Settings > Applications > Authorized GitHub Apps > "+
		github.AppName+" > Revoke.").Wrap()
}

// signInButtons draws 1 recovery button and Sign out.
func (a *App) signInButtons(c *ui.Context, label string, act func()) {
	ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, label).Clicked() {
			act()
		}
		if ui.Button(c, "Sign out").Clicked() {
			a.githubSignOut()
		}
	})
}

// ownerWord says an owner's state in 1 word: the reason itself shows once,
// in the line below the summary.
func (a *App) ownerWord(owner string) string {
	o := a.gh.owners[owner]
	switch {
	case !o.known:
		return "unknown"
	case o.reason != "":
		return "not ready"
	}
	return "ready"
}

// preflightSettings draws the preflight's tables, 1 owner at a time.
func (a *App) preflightSettings(c *ui.Context) {
	any := false
	for _, owner := range githubOwners {
		pf := a.gh.preflight[owner]
		if pf == nil {
			continue
		}
		any = true
		frostui.SectionHeader(c, frostui.SectionHead{Title: "Preflight: " + owner, Quiet: true})
		if pf.err != "" {
			frostui.Problem(c, pf.err)
		}
		if len(pf.rows) == 0 && !pf.busy && pf.err == "" {
			frostui.Caption(c, "no ready PR of "+owner+" to read")
			continue
		}
		if len(pf.rows) > 0 {
			preflightTable(c, pf.rows)
		}
	}
	if !any {
		frostui.Caption(c, "Run preflight reads 1 ready PR of each repository of each ready owner").Wrap()
	}
}

// journalSettings shows a journal that keeps the buttons off, with the
// owner's way out.
func (a *App) journalSettings(c *ui.Context) {
	j := a.gh.j
	if j == nil || j.ok() {
		return
	}
	ui.Column(c).Gap(frostui.SpaceS).AlignItems(ui.Stretch).Children(func() {
		frostui.Problem(c, "Approve + merge is off: "+j.why+".")
		ui.Row(c).Gap(frostui.SpaceS).Children(func() {
			switch j.state {
			case journalCorrupt:
				if ui.Button(c, "Start a new journal").Clicked() {
					if err := j.startNew(); err != nil {
						a.console.Add(frostui.LevelError, "github", "Starting a new journal: "+err.Error())
					}
					a.invalidateDerived()
					a.ghPreviewReady()
				}
			case journalLocked:
				if ui.Button(c, "Release the journal lock").Tooltip("Only when no other Pocket runs: a crash left the lock").Clicked() {
					j.releaseLock()
					a.invalidateDerived()
					if j.ok() {
						a.reconcileJournal()
						a.ghPreviewReady()
					}
				}
			}
		})
	})
}

// preflightTable is the preflight's rows: repository, readable, bypass,
// classic protection, and the missing permission or reason.
func preflightTable(c *ui.Context, rows []github.PreflightRow) {
	t := c.Theme()
	cols := []float32{150, 58, 58, 58}
	cell := func(c *ui.Context, i int, s string, head bool) {
		el := frostui.Caption(c, s)
		if head {
			el.TextColor(t.TextMuted).Bold()
		}
		if i < len(cols) {
			el.SingleLine().Width(cols[i])
		} else {
			el.Wrap().Grow(1).Selectable()
		}
	}
	ui.Column(c).Gap(4).AlignItems(ui.Stretch).Padding(frostui.SpaceS).Radius(8).Border(1, t.Border).Children(func() {
		ui.Row(c).Gap(frostui.SpaceS).Children(func() {
			for i, h := range []string{"Repository", "Readable", "Bypass", "Classic", "Missing permission / reason"} {
				cell(c.Key(i), i, h, true)
			}
		})
		for r, row := range rows {
			yes := map[bool]string{true: "yes", false: "no"}
			why := row.Reason
			if row.Missing != "" {
				why = row.Missing + ": " + why
			}
			if why == "" {
				why = "ready"
			}
			bypass := firstNonEmpty(row.Bypass, "—")
			ui.Row(c.Key(r)).Gap(frostui.SpaceS).AlignItems(ui.Start).Children(func() {
				for i, v := range []string{row.Repo, yes[row.Readable], bypass, yes[row.Classic], why} {
					cell(c.Key(i), i, v, false)
				}
			})
		}
	})
}
