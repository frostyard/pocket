package app

import (
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/frostyard/pocket/internal/github"
	"github.com/frostyard/pocket/internal/pocket"
)

// TestMergeSuccessClosesTheDialogWithAToast: when every PR of the dialog
// merged or went into the merge queue, the dialog closes at once and a
// toast says the result; a merged card leaves the Ready list.
func TestMergeSuccessClosesTheDialogWithAToast(t *testing.T) {
	for _, tc := range []struct {
		name   string
		refs   []string
		batch  bool
		result github.Result
		toast  string
		hidden bool
	}{
		{"merged", []string{"frostyard/core#1"}, false,
			github.Result{Kind: "merged", Step: "merge", Method: "squash"}, "Merged frostyard/core#1 (squash)", true},
		{"merged with the frozen method", []string{"frostyard/core#1"}, false,
			github.Result{Kind: "merged", Step: "merge"}, "Merged frostyard/core#1 (merge)", true},
		{"queued", []string{"frostyard/core#1"}, false,
			github.Result{Kind: "queued", Step: "merge", Method: "queue"}, "Queued frostyard/core#1 in the merge queue", false},
		{"batch", []string{"frostyard/core#1", "frostyard/core#2"}, true,
			github.Result{Kind: "merged", Step: "merge", Method: "merge"}, "Approve + merge: 2 merged", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, loop, gh, _ := mergeApp(t, tc.refs...)
			a.settings.GitHubBatches = tc.batch
			gh.act = func(github.Intent, string, func(string) error) github.Result { return tc.result }
			startMerge(t, a, loop)
			d := runDialog(t, a, loop, tc.batch, tc.refs...)
			if a.gh.dialog != nil {
				t.Fatalf("the dialog stays open after %v", dialogNotes(d))
			}
			if !slices.Contains(a.pendingToasts, tc.toast) {
				t.Errorf("toasts %q, want %q", a.pendingToasts, tc.toast)
			}
			for _, ref := range tc.refs {
				if a.mergeHidden(ref) != tc.hidden {
					t.Errorf("%s hidden = %v, want %v", ref, a.mergeHidden(ref), tc.hidden)
				}
				if o := a.gh.j.latest(ref); o == nil || o.Result != tc.result.Kind || o.unsaved {
					t.Errorf("%s journal %+v", ref, o)
				}
			}
		})
	}
}

// TestRiskHighMergeClosesTheDialogInTheWindow drives the window from the
// card's button to the toast, for a risk:high PR whose diff was opened:
// after the confirm click, the dialog is gone and the toast shows.
func TestRiskHighMergeClosesTheDialogInTheWindow(t *testing.T) {
	ref := "frostyard/core#7"
	a, loop, gh, _ := mergeApp(t, ref)
	p := gh.previews[ref]
	p.RiskHigh = true
	gh.previews[ref] = p
	a.opts.OpenURL = func(string) error { return nil }
	gh.act = func(github.Intent, string, func(string) error) github.Result {
		return github.Result{Kind: "merged", Step: "merge", Method: "squash"}
	}
	startMerge(t, a, loop)
	mc, _ := a.mergeCardOf(&a.desk.view.Ready[0])
	a.openDiff(ref, *mc.prev)
	loop.RunUntil(t, func() bool { mc, _ := a.mergeCardOf(&a.desk.view.Ready[0]); return mc.diffOK })
	tt := inboxTester(t, a, loop)
	if err := tt.Click("Approve + merge " + ref); err != nil {
		t.Fatal(err)
	}
	d := a.gh.dialog
	if d == nil {
		t.Fatal("no dialog")
	}
	loop.RunUntil(t, func() bool { return !d.items[0].reading })
	frames(tt, loop)
	if err := tt.Click("I read the diff at " + p.Head[:7] + ": approve + merge"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	loop.RunUntil(t, func() bool { return d.done })
	frames(tt, loop)
	if a.gh.dialog != nil || tt.HasText("Confirm: approve + merge 1 PR") {
		t.Errorf("the dialog stays: %q", tt.Texts())
	}
	if !tt.HasText("Merged " + ref + " (squash)") {
		t.Errorf("no toast: %q", tt.Texts())
	}
	writePNG(t, tt, "merge-success-toast")
}

// TestMergeProblemKeepsTheDialogOpen: a refusal, a failure, an unknown
// outcome, an approval without a merge, a batch with 1 problem, or a
// result the journal did not save keeps the dialog open with each PR's
// line and its Close button, as before.
func TestMergeProblemKeepsTheDialogOpen(t *testing.T) {
	for _, tc := range []struct {
		name    string
		refs    []string
		batch   bool
		act     func(github.Intent) github.Result
		failRes bool // the journal write of the result fails
		line    string
		toast   string
	}{
		{"refused", []string{"frostyard/core#1"}, false, func(github.Intent) github.Result {
			return github.Result{Kind: "refused", Step: "read", Reason: "new commit since you confirmed (eeeeeee)"}
		}, false, "refused: new commit since you confirmed (eeeeeee)", "1 refused"},
		{"failed", []string{"frostyard/core#1"}, false, func(github.Intent) github.Result {
			return github.Result{Kind: "failed", Step: "merge", Reason: "GitHub said 405: base branch policy"}
		}, false, "failed: GitHub said 405: base branch policy", "1 failed"},
		{"approved", []string{"frostyard/core#1"}, false, func(github.Intent) github.Result {
			return github.Result{Kind: "approved", Step: "read 2", Reason: "the PR is behind its base"}
		}, false, "approved, not merged: the PR is behind its base", "1 approved, not merged"},
		{"unknown", []string{"frostyard/core#1"}, false, func(github.Intent) github.Result {
			return github.Result{Kind: "unknown", Step: "reconcile merge", Reason: "merge may be pending"}
		}, false, "unknown: merge may be pending", "1 unknown"},
		{"batch with a refusal", []string{"frostyard/core#1", "frostyard/core#2"}, true, func(in github.Intent) github.Result {
			if in.Preview.Ref.Number == 2 {
				return github.Result{Kind: "refused", Step: "read", Reason: "checks are red"}
			}
			return github.Result{Kind: "merged", Step: "merge", Method: "merge"}
		}, false, "refused: checks are red", "1 merged · 1 refused"},
		{"journal write of the result failed", []string{"frostyard/core#1"}, false, func(github.Intent) github.Result {
			return github.Result{Kind: "merged", Step: "merge", Method: "squash"}
		}, true, "merged (squash)", "1 merged"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, loop, gh, _ := mergeApp(t, tc.refs...)
			a.settings.GitHubBatches = tc.batch
			gh.act = func(in github.Intent, _ string, _ func(string) error) github.Result { return tc.act(in) }
			startMerge(t, a, loop)
			if tc.failRes {
				writes := 0
				a.gh.j.rename = func(oldpath, newpath string) error {
					writes++
					if writes > 1 { // the start goes, the result fails
						return errors.New("disk full")
					}
					return os.Rename(oldpath, newpath)
				}
			}
			d := runDialog(t, a, loop, tc.batch, tc.refs...)
			if a.gh.dialog != d || !d.done {
				t.Fatalf("the dialog closed after %v", dialogNotes(d))
			}
			if !slices.Contains(a.pendingToasts, tc.toast) {
				t.Errorf("toasts %q, want %q", a.pendingToasts, tc.toast)
			}
			tt := inboxTester(t, a, loop)
			if !tt.HasText(tc.line) || !tt.HasText("Done: "+tc.toast) {
				t.Errorf("the dialog lacks %q: %q", tc.line, tt.Texts())
			}
			if err := tt.Click("Close"); err != nil {
				t.Fatalf("%v: %q", err, tt.Texts())
			}
			frames(tt, loop)
			if a.gh.dialog != nil {
				t.Error("Close did not close the dialog")
			}
		})
	}
}

// TestMergeSkippedPRKeepsTheDialogOpen: a batch where 1 PR did not start
// is no success, even when every PR that started merged.
func TestMergeSkippedPRKeepsTheDialogOpen(t *testing.T) {
	refs := []string{"frostyard/core#1", "frostyard/core#2"}
	a, loop, gh, _ := mergeApp(t, refs...)
	a.settings.GitHubBatches = true
	p := gh.previews[refs[1]]
	p.Eligible, p.Reason = false, "checks are red"
	gh.previews[refs[1]] = p
	gh.act = func(github.Intent, string, func(string) error) github.Result {
		return github.Result{Kind: "merged", Step: "merge", Method: "merge"}
	}
	startMerge(t, a, loop)
	a.openMergeDialog([]pocket.ReadyPR{a.desk.view.Ready[0], a.desk.view.Ready[1]}, true)
	d := a.gh.dialog
	loop.RunUntil(t, func() bool { return !d.items[0].reading && !d.items[1].reading })
	a.mergeConfirm(d)
	loop.RunUntil(t, func() bool { return d.done })
	if a.gh.dialog != d {
		t.Fatalf("the dialog closed after %v", dialogNotes(d))
	}
	if !slices.Contains(a.pendingToasts, "1 merged · 1 not started") {
		t.Errorf("toasts %q", a.pendingToasts)
	}
}
