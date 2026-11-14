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

	"github.com/frostyard/pocket/internal/github"
	"github.com/frostyard/pocket/internal/pocket"
)

// Approve + merge on the Inbox's ready cards (ADR-0008): the owner approves
// and merges a ready PR as bketelsen, with his own token, from a click in
// this window. No server message, Desk field or stream event starts a
// mutation: only the confirm button of the dialog does (mergeConfirm).
// GitHub calls are built from the Desk row's ref, never from its url.

// TokenStore keeps the owner's GitHub tokens; config.GitHubTokens is one.
type TokenStore interface {
	Get(owner string) (string, error)
	Set(owner, token string) error
	Forget(owner string) error
}

// ghAPI is what the app uses of a *github.Client; tests give a fake.
type ghAPI interface {
	Preview(ctx context.Context, ref github.Ref) (github.Preview, error)
	Act(ctx context.Context, in github.Intent, op string, step func(name string) error) github.Result
	Settle(ctx context.Context, in github.Intent, op string, wait bool) github.Result
	State(ctx context.Context, ref github.Ref) (github.PRState, error)
	Whoami(ctx context.Context) (github.Identity, error)
	CheckRepo(ctx context.Context, owner, name string) error
	Preflight(ctx context.Context, ref github.Ref) github.PreflightRow
}

// githubOwners are the resource owners Pocket keeps a token for, in the
// order Settings shows them.
var githubOwners = []string{"bketelsen", "frostyard"}

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

// tokenForm is Settings > GitHub's state for 1 owner.
type tokenForm struct {
	text    string
	busy    bool
	login   string
	expires time.Time
	checked bool
	result  string
	fail    string
	missing string
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
	api       func(owner string) (ghAPI, error)
	newClient func(token string) ghAPI
	j         *journal

	kept   map[string]bool
	keyErr string
	bad    map[string]string
	epoch  map[string]int

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

	forms     map[string]*tokenForm
	preflight map[string]*preflightResult
	blocked   map[string]string
}

func newGHState() ghState {
	return ghState{
		newClient: func(token string) ghAPI { return github.New(token) },
		kept:      map[string]bool{}, bad: map[string]string{}, epoch: map[string]int{},
		previews: map[string]*ghPreview{}, sel: map[string]bool{}, diff: map[string]string{},
		running: map[string]bool{}, notMerged: map[string]bool{}, posting: map[string]*pocket.Client{},
		busy: map[string]bool{}, forms: map[string]*tokenForm{}, preflight: map[string]*preflightResult{},
		blocked: map[string]string{},
	}
}

// initGitHub wires Options.GitHub into the seam the app uses.
func (a *App) initGitHub() {
	a.gh = newGHState()
	if gh := a.opts.GitHub; gh != nil {
		a.gh.api = func(owner string) (ghAPI, error) {
			c, err := gh(owner)
			if err != nil {
				return nil, err
			}
			if c == nil {
				return nil, errors.New("no GitHub client for " + owner)
			}
			return c, nil
		}
	}
}

// ghOn says the feature is on: a keyring and a GitHub client.
func (a *App) ghOn() bool { return a.gh.api != nil && a.opts.GitHubTokens != nil }

// startGitHub opens the journal, learns which tokens the keyring keeps,
// and reconciles the attempts a crash left. Reads only.
func (a *App) startGitHub() {
	if !a.ghOn() || a.gh.j != nil {
		return
	}
	a.gh.j = newJournal(a.opts.Store.Dir(), a.opts.Now)
	a.gh.j.open()
	if !a.gh.j.ok() {
		a.console.Add(frostui.LevelWarn, "github", "Approve + merge is off: "+a.gh.j.why)
	}
	a.loadKeptTokens()
	a.reconcileJournal()
}

// closeGitHub removes the journal's lock.
func (a *App) closeGitHub() {
	if a.gh.j != nil {
		a.gh.j.close()
	}
}

// loadKeptTokens asks the keyring, off the UI thread, which owners have a
// token. The token itself stays there.
func (a *App) loadKeptTokens() {
	store := a.opts.GitHubTokens
	type kept struct{ has map[string]bool }
	ghRead(a, "Reading the GitHub tokens in the keyring", func(context.Context) (kept, error) {
		out := kept{has: map[string]bool{}}
		for _, owner := range githubOwners {
			tok, err := store.Get(owner)
			if err != nil {
				return out, github.Redact(err, tok)
			}
			out.has[owner] = tok != ""
		}
		return out, nil
	}, func(k kept, err error) {
		if err != nil {
			a.gh.keyErr = err.Error()
			return
		}
		a.gh.keyErr = ""
		a.gh.kept = k.has
		a.ghPreviewReady()
	})
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

// ghBlocked says why a PR of owner gets no button now, or "".
func (a *App) ghBlocked(r github.Ref) string {
	switch {
	case a.gh.keyErr != "":
		return "Approve + merge needs the desktop keyring (Settings > GitHub)"
	case a.gh.j == nil:
		return "Approve + merge starts with the journal"
	case !a.gh.j.ok():
		return "Approve + merge is off: " + a.gh.j.why + " (Settings > GitHub)"
	case a.gh.bad[r.Owner] != "":
		return a.gh.bad[r.Owner] + " (Settings > GitHub)"
	case !a.gh.kept[r.Owner]:
		return "Add a GitHub token for " + r.Owner + " in Settings > GitHub to approve + merge here"
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
		ghRead(a, "Reading "+req.key+" on GitHub", func(ctx context.Context) (github.Preview, error) {
			api, err := apiFn(owner)
			if err != nil {
				return github.Preview{}, err
			}
			return api.Preview(ctx, req.ref)
		}, func(p github.Preview, err error) {
			a.gh.inflight--
			defer a.ghPump()
			pv := a.gh.previews[req.key]
			if pv != nil && pv.reading > 0 {
				pv.reading--
			}
			if epoch != a.gh.epoch[owner] || pv == nil {
				return // the token changed since: this read is the old one's
			}
			a.ghTokenRefused(owner, err)
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

// ghTokenRefused marks owner's token bad on a 401.
func (a *App) ghTokenRefused(owner string, err error) bool {
	var api *github.APIError
	if errors.As(err, &api) && api.Status == 401 {
		a.gh.bad[owner] = "GitHub refused the token for " + owner + ": paste a new one"
		return true
	}
	return false
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
	a.githubDo("Approve + merge "+it.pr.Ref, func(ctx context.Context) github.Result {
		api, err := apiFn(owner)
		if err != nil {
			return github.Result{Op: opID, Kind: "failed", Step: "read", Reason: "no GitHub client: " + err.Error(), Err: err}
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
			a.ghTokenRefused(it.ref.Owner, res.Err)
			for _, other := range d.items {
				if other.state == "" && other.ref.Owner == it.ref.Owner {
					other.state, other.note = "skipped", "not started: token refused"
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
	a.githubDo("Reading what "+o.Intent.Ref+" became", func(ctx context.Context) github.Result {
		api, err := apiFn(owner)
		if err != nil {
			return github.Result{Op: id, Kind: "unknown", Step: "settle", Reason: "no GitHub client", Err: err}
		}
		return api.Settle(ctx, in, id, wait)
	}, func(res github.Result) {
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
	ghRead(a, "Reading the state of "+ref, func(ctx context.Context) (github.PRState, error) {
		api, err := apiFn(r.Owner)
		if err != nil {
			return github.PRState{}, err
		}
		return api.State(ctx, r)
	}, func(st github.PRState, err error) {
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
		a.applyResult(o, github.Result{Op: o.ID, Kind: "approved", Step: "settle", Reason: "left the merge queue"})
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

// Settings > GitHub: the tokens, the preflight, Batches, and the journal.

func (a *App) tokenForm(owner string) *tokenForm {
	f := a.gh.forms[owner]
	if f == nil {
		f = &tokenForm{}
		a.gh.forms[owner] = f
	}
	return f
}

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

type tokenCheck struct {
	login, fail, note, missing string
	expires                    time.Time
	saved                      bool
}

// githubCheck is Check: the typed token (or the kept one) must sign in as
// bketelsen and read 1 ready repository of owner. Only then is a typed
// token saved.
func (a *App) githubCheck(owner string) {
	f := a.tokenForm(owner)
	if f.busy || !a.ghOn() {
		return
	}
	typed := strings.TrimSpace(f.text)
	store, newClient := a.opts.GitHubTokens, a.gh.newClient
	var repo *github.Ref
	if repos := a.readyRepos(owner); len(repos) > 0 {
		repo = &repos[0]
	}
	f.busy, f.fail, f.result, f.missing = true, "", "", ""
	ghRead(a, "Checking the GitHub token for "+owner, func(ctx context.Context) (tokenCheck, error) {
		tok := typed
		if tok == "" {
			kept, err := store.Get(owner)
			if err != nil {
				return tokenCheck{}, github.Redact(err, kept)
			}
			if kept == "" {
				return tokenCheck{fail: "paste a token first"}, nil
			}
			tok = kept
		}
		c := newClient(tok)
		id, err := c.Whoami(ctx)
		if err != nil {
			out := tokenCheck{fail: "GitHub refused the token: " + github.Redact(err, tok).Error()}
			var api *github.APIError
			if errors.As(err, &api) {
				out.missing = api.Accepted
			}
			return out, nil
		}
		out := tokenCheck{login: id.Login, expires: id.Expires}
		if id.Login != github.Login {
			out.fail = "this token is for " + id.Login
			return out, nil
		}
		if repo == nil {
			// Without a ready repository the read cannot be proved: a typed
			// token is not kept; a kept one shows its login and expiry.
			if typed != "" {
				out.fail = "no ready PR of " + owner + " to check the token with: try again when 1 is ready"
				return out, nil
			}
			out.note = "no ready PR of " + owner + " to check"
		} else if err := c.CheckRepo(ctx, repo.Owner, repo.Name); err != nil {
			out.fail = "this token cannot read " + repo.Owner + "/" + repo.Name + ": " + github.Redact(err, tok).Error()
			var api *github.APIError
			if errors.As(err, &api) {
				out.missing = api.Accepted
			}
			return out, nil
		}
		if typed != "" {
			if err := store.Set(owner, typed); err != nil {
				return out, github.Redact(err, typed)
			}
			out.saved = true
		}
		return out, nil
	}, func(res tokenCheck, err error) {
		f.busy = false
		f.login, f.expires, f.missing = res.login, res.expires, res.missing
		f.checked = err == nil && res.fail == ""
		switch {
		case err != nil:
			f.fail = err.Error()
		case res.fail != "":
			f.fail = res.fail
		default:
			f.result = "Signs in as " + res.login
			if res.note != "" {
				f.result += "; " + res.note
			}
			if res.saved {
				f.text = ""
				f.result += "; kept in the keyring"
			}
			a.gh.kept[owner] = true
			delete(a.gh.bad, owner)
			a.gh.keyErr = ""
			a.dropPreviews(owner)
			a.githubPreflight(owner)
			a.ghPreviewReady()
		}
	})
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

// githubForget is Forget: the keyring drops owner's token.
func (a *App) githubForget(owner string) {
	f := a.tokenForm(owner)
	if f.busy || !a.ghOn() {
		return
	}
	store := a.opts.GitHubTokens
	f.busy = true
	ghRead(a, "Forgetting the GitHub token for "+owner, func(context.Context) (struct{}, error) {
		return struct{}{}, store.Forget(owner)
	}, func(_ struct{}, err error) {
		f.busy = false
		if err != nil {
			f.fail = err.Error()
			return
		}
		*f = tokenForm{result: "Forgotten"}
		a.gh.kept[owner] = false
		delete(a.gh.bad, owner)
		delete(a.gh.preflight, owner)
		for repo := range a.gh.blocked {
			if strings.HasPrefix(repo, owner+"/") {
				delete(a.gh.blocked, repo)
			}
		}
		a.dropPreviews(owner)
	})
}

// githubPreflight reads, with the kept token, 1 ready PR of each of
// owner's ready repositories: rules, rulesets, branch and the PR query.
// Reads only. A repository that fails gets no button.
func (a *App) githubPreflight(owner string) {
	pf := a.gh.preflight[owner]
	if pf == nil {
		pf = &preflightResult{}
		a.gh.preflight[owner] = pf
	}
	if pf.busy || !a.ghOn() {
		return
	}
	repos := a.readyRepos(owner)
	apiFn := a.gh.api
	pf.busy = true
	ghRead(a, "Preflight of "+owner+"'s repositories", func(ctx context.Context) ([]github.PreflightRow, error) {
		api, err := apiFn(owner)
		if err != nil {
			return nil, err
		}
		rows := make([]github.PreflightRow, 0, len(repos))
		for _, r := range repos {
			rows = append(rows, api.Preflight(ctx, r))
		}
		return rows, nil
	}, func(rows []github.PreflightRow, err error) {
		pf.busy = false
		pf.err = ""
		if err != nil {
			pf.err = err.Error()
			return
		}
		pf.rows = rows
		for _, row := range rows {
			if row.Reason != "" {
				a.gh.blocked[row.Repo] = "preflight: " + row.Reason
			} else {
				delete(a.gh.blocked, row.Repo)
			}
		}
	})
}

// githubSettingsPage is Settings > GitHub.
func (a *App) githubSettingsPage() frostui.SettingsPage {
	return frostui.SettingsPage{ID: "github", Title: "GitHub", Icon: "pocket-branch",
		Keywords: "approve merge token pull request batches preflight journal keyring",
		Body:     a.githubSettingsBody}
}

func (a *App) githubSettingsBody(c *ui.Context) {
	ui.Column(c).Gap(frostui.SpaceM).AlignItems(ui.Stretch).Children(func() {
		if !a.ghOn() {
			frostui.Muted(c, "Approve + merge needs the desktop keyring.").Wrap()
			return
		}
		if a.gh.keyErr != "" {
			frostui.Muted(c, "Approve + merge needs the desktop keyring.").Wrap()
			frostui.Problem(c, a.gh.keyErr)
			return
		}
		frostui.Muted(c, "Approve + merge acts as "+github.Login+" with a fine-grained token for each owner, kept in the desktop keyring only. "+
			"Permissions: Pull requests, Contents and Workflows read and write; Checks, Commit statuses and Metadata read.").Wrap()
		a.journalSettings(c)
		for _, owner := range githubOwners {
			a.ownerSettings(c.Key(owner), owner)
		}
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

func (a *App) ownerSettings(c *ui.Context, owner string) {
	f := a.tokenForm(owner)
	frostui.SectionHeader(c, frostui.SectionHead{Title: owner, Note: "token for " + owner + "'s repositories"})
	ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
		hint := "github_pat_…"
		if a.gh.kept[owner] {
			hint = "kept in the keyring"
		}
		ui.TextInput(c, &f.text).Password().Placeholder(hint).Label("GitHub token for " + owner).Disabled(f.busy).Grow(1)
		if f.busy {
			ui.Spinner(c).Size(14, 14)
		}
		if ui.Button(c, "Check").Label("Check " + owner).Disabled(f.busy || (strings.TrimSpace(f.text) == "" && !a.gh.kept[owner])).Clicked() {
			a.githubCheck(owner)
		}
		if ui.Button(c, "Forget").Label("Forget " + owner).Disabled(f.busy || !a.gh.kept[owner]).Clicked() {
			a.githubForget(owner)
		}
	})
	var facts []frostui.KeyValue
	if !a.gh.kept[owner] {
		facts = append(facts, frostui.KeyValue{Key: "Token", Value: "none: paste one and Check"})
	}
	if f.login != "" {
		facts = append(facts, frostui.KeyValue{Key: "Login", Value: f.login})
	}
	if f.checked || f.login != "" {
		exp := "unknown (GitHub named none)"
		if !f.expires.IsZero() {
			exp = f.expires.In(a.opts.Now().Location()).Format("2006-01-02 15:04")
		}
		facts = append(facts, frostui.KeyValue{Key: "Expires", Value: exp})
	}
	if f.result != "" {
		facts = append(facts, frostui.KeyValue{Key: "Check", Value: f.result})
	}
	if f.missing != "" {
		facts = append(facts, frostui.KeyValue{Key: "Missing permission", Value: f.missing, Mono: true})
	}
	if len(facts) > 0 {
		frostui.KeyValues(c, facts)
	}
	if bad := a.gh.bad[owner]; bad != "" {
		frostui.Problem(c, bad)
	}
	if f.fail != "" {
		frostui.Problem(c, f.fail)
	}
	pf := a.gh.preflight[owner]
	ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
		frostui.Caption(c, "Preflight: reads 1 ready PR of each repository, with this token").Grow(1)
		if pf != nil && pf.busy {
			ui.Spinner(c).Size(14, 14)
		}
		if ui.Button(c, "Run preflight").Label("Run preflight " + owner).Disabled(!a.gh.kept[owner] || (pf != nil && pf.busy)).Clicked() {
			a.githubPreflight(owner)
		}
	})
	if pf == nil {
		return
	}
	if pf.err != "" {
		frostui.Problem(c, pf.err)
	}
	if len(pf.rows) == 0 && !pf.busy && pf.err == "" {
		frostui.Caption(c, "no ready PR of "+owner+" to read")
		return
	}
	if len(pf.rows) > 0 {
		preflightTable(c, pf.rows)
	}
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
