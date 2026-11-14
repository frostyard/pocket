package app

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/frostui/frostuitest"

	"github.com/frostyard/pocket/internal/github"
	"github.com/frostyard/pocket/internal/pocket"
)

// The guards of the cross-review of frostyard/pocket#96 (record
// .memory/corrections/2026-11-14-pr-96-merge-journal-review.json).

// restartWith is a new Pocket on the same config folder's journal: it
// copies operations.json from a into a fresh app, as a restart reads it.
func restartWith(t *testing.T, data []byte, refs ...string) (*App, *frostuitest.Loop, *fakeGH) {
	t.Helper()
	b, loop, gh, _ := mergeApp(t, refs...)
	if err := os.WriteFile(filepath.Join(b.opts.Store.Dir(), journalName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return b, loop, gh
}

func readJournal(t *testing.T, a *App) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(a.opts.Store.Dir(), journalName))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestCrashAfterTheMergeCheckpointStaysUnknown: Act tells its final kind
// to the step callback; that must not replace the "about to merge"
// checkpoint, or a crash before the result is saved would read as "merge
// never sent" and settle a merge that may be pending as approved.
func TestCrashAfterTheMergeCheckpointStaysUnknown(t *testing.T) {
	ref := "frostyard/core#1"
	a, loop, gh, _ := mergeApp(t, ref)
	stepped, release := make(chan struct{}), make(chan struct{})
	gh.act = func(_ github.Intent, _ string, step func(string) error) github.Result {
		_ = step("about to approve")
		_ = step("about to merge")
		_ = step("unknown") // Act tells the result's kind at its end
		close(stepped)
		<-release
		return github.Result{Kind: "unknown", Step: "reconcile merge", Reason: "merge may be pending"}
	}
	startMerge(t, a, loop)
	a.openMergeDialog([]pocket.ReadyPR{a.desk.view.Ready[0]}, false)
	d := a.gh.dialog
	loop.RunUntil(t, func() bool { return !d.items[0].reading })
	a.mergeConfirm(d)
	loop.RunUntil(t, func() bool {
		select {
		case <-stepped:
			return true
		default:
			return false
		}
	})
	// Pocket "crashes" here: the result was never applied.
	crashed := readJournal(t, a)
	close(release)
	loop.RunUntil(t, func() bool { return d.done })

	b, loopB, ghB := restartWith(t, crashed, ref)
	ghB.settle = func(github.Intent, bool) github.Result {
		return github.Result{Kind: "approved", Reason: "approved, not merged"} // only the owner's approval shows
	}
	startMerge(t, b, loopB)
	loopB.RunUntil(t, func() bool { return len(b.gh.busy) == 0 })
	o := b.gh.j.latest(ref)
	if o.Step != "about to merge" || o.Result != "unknown" || !b.mergeLocked(ref) {
		t.Errorf("after the crash: step %q result %q locked %v", o.Step, o.Result, b.mergeLocked(ref))
	}
	if len(ghB.actsSoFar()) != 0 {
		t.Error("a mutation at start")
	}
}

// TestCorruptJournalStaysCorruptAfterARestart: the corrupt state survives
// a restart until the owner starts a new journal.
func TestCorruptJournalStaysCorruptAfterARestart(t *testing.T) {
	dir := t.TempDir()
	now := func() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) }
	if err := os.WriteFile(filepath.Join(dir, journalName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	j := newJournal(dir, now)
	j.open()
	kept := "operations.json.corrupt-20261114T120000Z"
	if j.state != journalCorrupt || j.corrupt != kept {
		t.Fatalf("state %d corrupt %q", j.state, j.corrupt)
	}
	if b, err := os.ReadFile(filepath.Join(dir, corruptName)); err != nil || string(b) != kept {
		t.Fatalf("marker %q %v", b, err)
	}
	if info, _ := os.Stat(filepath.Join(dir, corruptName)); info.Mode().Perm() != 0o600 {
		t.Errorf("marker mode %v", info.Mode())
	}
	j.close()

	again := newJournal(dir, now)
	again.open()
	if again.state != journalCorrupt || !strings.Contains(again.why, kept) {
		t.Fatalf("after a restart: state %d why %q", again.state, again.why)
	}
	if err := again.startNew(); err != nil || !again.ok() {
		t.Fatalf("start new: %v state %d", err, again.state)
	}
	if _, err := os.Stat(filepath.Join(dir, corruptName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the marker stays: %v", err)
	}
	again.close()

	third := newJournal(dir, now)
	third.open()
	defer third.close()
	if !third.ok() {
		t.Errorf("after a new journal and a restart: state %d %q", third.state, third.why)
	}

	// A marker that cannot be written leaves the buttons off.
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, journalName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	j2 := newJournal(dir2, now)
	j2.rename = func(string, string) error { return errors.New("disk full") }
	j2.open()
	defer j2.close()
	if j2.state != journalBroken || j2.ok() {
		t.Errorf("state %d", j2.state)
	}
}

// TestDialogKeepsTheOriginItOpenedWith: a dialog's attempts record the
// account it opened under, and it does not start under another.
func TestDialogKeepsTheOriginItOpenedWith(t *testing.T) {
	refs := []string{"frostyard/core#1", "frostyard/core#2"}
	a, loop, gh, _ := mergeApp(t, refs...)
	a.desk.view.Ready[1].Thread = "T2"
	a.invalidateDerived()
	home := a.account
	other := accountKey{server: "https://other.example.org", user: "u9"}
	started, release := make(chan struct{}, 2), make(chan struct{})
	gh.act = func(in github.Intent, _ string, _ func(string) error) github.Result {
		if in.Preview.Ref.Number == 1 {
			started <- struct{}{}
			<-release
		}
		return github.Result{Kind: "merged", Step: "merge", Method: "merge"}
	}
	startMerge(t, a, loop)

	// Another account between the open and the confirm: no start.
	a.openMergeDialog([]pocket.ReadyPR{a.desk.view.Ready[0]}, false)
	d := a.gh.dialog
	loop.RunUntil(t, func() bool { return !d.items[0].reading })
	a.account = other
	if _, on := a.confirmLabel(d); on {
		t.Error("the confirm button is on under another account")
	}
	a.mergeConfirm(d)
	if d.started || len(gh.actsSoFar()) != 0 || a.otherAccount(d) != "signed in to another account: close and open again" {
		t.Fatalf("started %v acts %v why %q", d.started, gh.actsSoFar(), a.otherAccount(d))
	}
	a.account = home
	a.closeMergeDialog()

	// Another account between 2 PRs of a batch: both keep the origin.
	var prs []pocket.ReadyPR
	prs = append(prs, a.desk.view.Ready...)
	a.openMergeDialog(prs, true)
	d = a.gh.dialog
	loop.RunUntil(t, func() bool { return !d.items[0].reading && !d.items[1].reading })
	a.mergeConfirm(d)
	<-started
	a.account = other
	close(release)
	loop.RunUntil(t, func() bool { return d.done })
	for i, ref := range refs {
		o := a.gh.j.latest(ref)
		want := opOrigin{Server: home.server, User: home.user, Thread: []string{"T1", "T2"}[i]}
		if o.Result != "merged" || o.Origin != want {
			t.Errorf("%s: result %q origin %+v, want %+v", ref, o.Result, o.Origin, want)
		}
	}
}

// TestQueuedOpSettlesWithoutItsDeskRow: a queued attempt is read at each
// Desk read even when the Desk no longer lists its PR.
func TestQueuedOpSettlesWithoutItsDeskRow(t *testing.T) {
	ref := "frostyard/core#1"
	a, loop, gh, _ := mergeApp(t, ref)
	h := &receiptHTTP{}
	a.link.client.HTTP = &http.Client{Transport: h}
	gh.act = func(github.Intent, string, func(string) error) github.Result {
		return github.Result{Kind: "queued", Step: "merge", Method: "queue"}
	}
	startMerge(t, a, loop)
	runDialog(t, a, loop, false, ref)
	a.closeMergeDialog()
	o := a.gh.j.latest(ref)
	loop.RunUntil(t, func() bool { return rcpt(o).State == receiptRecorded })
	// The Desk swept the PR away before Pocket read it again.
	a.desk.view.Ready = nil
	a.invalidateDerived()
	gh.settle = func(github.Intent, bool) github.Result {
		return github.Result{Kind: "merged", Method: "squash", MergeCommit: oid('c')}
	}
	a.githubDeskRead()
	loop.RunUntil(t, func() bool { return o.Result == "merged" && rcpt(o).State == receiptRecorded })
	if h.count() != 2 || h.bodies[1]["op"] != o.ID || h.bodies[1]["result"] != "merged" || h.bodies[1]["method"] != "squash" {
		t.Errorf("receipts %v", h.bodies)
	}
}

// TestEachResultKeepsItsOwnReceipt: a queued receipt that failed is not
// lost when the attempt becomes merged; after a restart both go, with the
// same op.
func TestEachResultKeepsItsOwnReceipt(t *testing.T) {
	ref := "frostyard/core#1"
	a, loop, gh, _ := mergeApp(t, ref)
	down := &receiptHTTP{status: 502}
	a.link.client.HTTP = &http.Client{Transport: down}
	gh.act = func(github.Intent, string, func(string) error) github.Result {
		return github.Result{Kind: "queued", Step: "merge", Method: "queue"}
	}
	startMerge(t, a, loop)
	runDialog(t, a, loop, false, ref)
	a.closeMergeDialog()
	o := a.gh.j.latest(ref)
	loop.RunUntil(t, func() bool { return rcpt(o).Failed })
	gh.settle = func(github.Intent, bool) github.Result { return github.Result{Kind: "merged", Method: "squash"} }
	a.githubDeskRead()
	loop.RunUntil(t, func() bool { return o.Result == "merged" && rcpt(o).Failed })
	if len(o.Receipts) != 2 || o.receipt("queued") == nil || o.receipt("queued").State != receiptPending {
		t.Fatalf("receipts %+v", o.Receipts)
	}
	if a.receiptNote(o) != "not recorded on the Desk" {
		t.Errorf("note %q", a.receiptNote(o))
	}
	a.Close()

	b, loopB, _ := restartWith(t, readJournal(t, a), ref)
	up := &receiptHTTP{}
	b.link.client.HTTP = &http.Client{Transport: up}
	startMerge(t, b, loopB)
	loopB.RunUntil(t, func() bool { return up.count() == 2 })
	got := map[string]string{}
	for _, body := range up.bodies {
		if body["op"] != o.ID {
			t.Errorf("op %v, want %s", body["op"], o.ID)
		}
		got[body["result"].(string)] = body["method"].(string)
	}
	if got["queued"] != "queue" || got["merged"] != "squash" {
		t.Errorf("receipts %v", up.bodies)
	}
	bo := b.gh.j.latest(ref)
	loopB.RunUntil(t, func() bool { return bo.settled() })
	if b.receiptNote(bo) != "" {
		t.Errorf("note %q", b.receiptNote(bo))
	}
}

// TestJournalReadsTheVersion1Receipt migrates the single receipt of a
// version 1 file into the list.
func TestJournalReadsTheVersion1Receipt(t *testing.T) {
	dir := t.TempDir()
	v1 := `{"version":1,"ops":[{"id":"op-1","origin":{"server":"https://example.org","user":"u1","thread":"T1"},` +
		`"intent":{"ref":"frostyard/core#1","nodeId":"PR_x","baseRef":"main","head":"` + oid('a') + `","method":"squash","queue":false,"mode":"single","risk":false},` +
		`"step":"merge","result":"merged","method":"squash","receipt":{"state":"pending","since":"2026-11-14T11:00:00Z","failed":true},` +
		`"at":"2026-11-14T10:00:00Z","endedAt":"2026-11-14T11:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(dir, journalName), []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	j := newJournal(dir, func() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) })
	j.open()
	defer j.close()
	if !j.ok() || len(j.ops) != 1 {
		t.Fatalf("state %d ops %d", j.state, len(j.ops))
	}
	o := j.ops[0]
	r := o.receipt("merged")
	if o.Receipt != nil || len(o.Receipts) != 1 || r == nil || r.Method != "squash" || r.State != receiptPending || !r.Failed {
		t.Fatalf("receipts %+v", o.Receipts)
	}
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, journalName))
	if !strings.Contains(string(data), `"version": 2`) || strings.Contains(string(data), `"receipt":`) {
		t.Errorf("saved:\n%s", data)
	}
}

// TestReviewOnGitHubOpensTheBuiltURL: the card opens the URL Pocket builds
// from the ref, never the Desk's url; a ref that does not parse gets no
// button.
func TestReviewOnGitHubOpensTheBuiltURL(t *testing.T) {
	a, loop := teamApp(t)
	a.desk.view.Ready[0].URL = "https://example.org/somewhere-else"
	a.desk.view.Ready = append(a.desk.view.Ready, pocket.ReadyPR{Ref: "not a ref", Title: "Odd row", URL: "https://example.org/odd", Thread: "T1450"})
	a.invalidateDerived()
	opened := make(chan string, 4)
	a.opts.OpenURL = func(u string) error { opened <- u; return nil }
	tt := inboxTester(t, a, loop)
	if _, ok := tt.Find("Review not a ref on GitHub"); ok {
		t.Error("a ref that does not parse has a Review button")
	}
	if err := tt.Click("Review frostyard/frostmail#288 on GitHub"); err != nil {
		t.Fatal(err)
	}
	frames(tt, loop)
	if got := <-opened; got != "https://github.com/frostyard/frostmail/pull/288" {
		t.Errorf("opened %q", got)
	}
}

// TestQueueMergeWithoutAMethodStaysQueued: a merge the queue made, read
// with no method from the queue's rules, is not applied with a made-up
// method; the attempt stays queued and no merged receipt goes.
func TestQueueMergeWithoutAMethodStaysQueued(t *testing.T) {
	ref := "frostyard/core#1"
	a, loop, gh, _ := mergeApp(t, ref)
	p := gh.previews[ref]
	p.Queue = true
	gh.previews[ref] = p
	h := &receiptHTTP{}
	a.link.client.HTTP = &http.Client{Transport: h}
	gh.act = func(github.Intent, string, func(string) error) github.Result {
		return github.Result{Kind: "queued", Step: "merge", Method: "queue"}
	}
	startMerge(t, a, loop)
	runDialog(t, a, loop, false, ref)
	a.closeMergeDialog()
	o := a.gh.j.latest(ref)
	loop.RunUntil(t, func() bool { return rcpt(o).State == receiptRecorded })
	for _, res := range []github.Result{
		{Kind: "merged"}, // no method
		{Kind: "merged", Method: "queue"},
		{Kind: "unknown", Reason: "could not read the merge queue's rules", Err: errors.New("GitHub answered 403")},
	} {
		gh.settle = func(github.Intent, bool) github.Result { return res }
		a.githubDeskRead()
		loop.RunUntil(t, func() bool { return len(a.gh.busy) == 0 })
		if o.Result != "queued" {
			t.Fatalf("%+v: result %q method %q", res, o.Result, o.Method)
		}
	}
	for _, body := range h.bodies {
		if body["result"] != "queued" {
			t.Errorf("a receipt with a made-up result or method: %v", body)
		}
	}
	if !slices.Equal(gh.actsSoFar(), []string{ref}) {
		t.Errorf("acts %v", gh.actsSoFar())
	}
}
