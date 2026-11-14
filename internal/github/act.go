package github

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"
)

// Intent is what the owner's confirm click froze for 1 PR.
type Intent struct {
	Preview Preview
	Mode    string // "single" or "batch"
	// AckHead is the head at which the owner opened the diff of a risk:high
	// PR; "" when he did not.
	AckHead string
}

// Result is how 1 attempt ended.
type Result struct {
	Op string
	// Kind is "merged", "queued", "approved" (approved, not merged),
	// "refused", "failed" or "unknown".
	Kind                        string
	Reason, Method, MergeCommit string
	// Step is where the attempt ended: "read", "approve", "reconcile
	// approve", "read 2", "merge" or "reconcile merge".
	Step string
	// Err is GitHub's error (redacted) or the step callback's, when there is
	// 1: errors.As finds a 401 or a rate limit (*APIError) in it.
	Err error
}

// The steps that Act tells its callback before each mutation.
const (
	stepAboutToApprove = "about to approve"
	stepAboutToMerge   = "about to merge"
)

const journalFailed = "journal write failed"

// reconcileWaits are the waits before each read of a reconciliation.
var reconcileWaits = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}

// Act approves the PR of in at the shown head and merges or enqueues it,
// as the owner, after the gates and the intent check pass on a read before
// each mutation. step is told "about to approve" and "about to merge"
// before each mutation (an error sends nothing), and the result's Kind at
// the end (its error is ignored). A nil step does nothing.
func (c *Client) Act(ctx context.Context, in Intent, op string, step func(name string) error) Result {
	if step == nil {
		step = func(string) error { return nil }
	}
	res := c.act(ctx, in, step)
	res.Op = op
	res.Reason = capReason(res.Reason)
	res.Err = Redact(res.Err, c.Token)
	_ = step(res.Kind)
	return res
}

func (c *Client) act(ctx context.Context, in Intent, step func(string) error) Result {
	ref, head := in.Preview.Ref, in.Preview.Head
	if !in.Preview.Eligible || head == "" || in.Preview.NodeID == "" {
		return Result{Kind: "refused", Step: "read", Reason: "not eligible at the preview"}
	}

	// Read 1: gates 1 to 10 and the intent.
	f, r, err := c.read(ctx, ref)
	if err != nil {
		return Result{Kind: "failed", Step: "read", Reason: Redact(err, c.Token).Error(), Err: err}
	}
	if ok, why := Check(f, r, AtPreview); !ok {
		return Result{Kind: "refused", Step: "read", Reason: why}
	}
	if ok, why := CheckIntent(in, f, r); !ok {
		return Result{Kind: "refused", Step: "read", Reason: why}
	}

	// Approve at the shown head, unless the owner's approval is there.
	if !approvedAt(f, head) {
		if err := step(stepAboutToApprove); err != nil {
			return Result{Kind: "refused", Step: "approve", Reason: journalFailed, Err: err}
		}
		if err := c.approve(ctx, in); err != nil {
			seen := func(f PRFacts) bool { return approvedAt(f, head) }
			switch answerOf(ctx, err) {
			case answerNotSent:
				return Result{Kind: "failed", Step: "approve", Reason: Redact(err, c.Token).Error(), Err: err}
			case answerRefused:
				if !c.verify(ctx, ref, seen) {
					return Result{Kind: "failed", Step: "approve", Reason: Redact(err, c.Token).Error(), Err: err}
				}
			default:
				if !c.reconcile(ctx, ref, seen) {
					return Result{Kind: "unknown", Step: "reconcile approve", Reason: c.pending("approval may be pending", err), Err: err}
				}
			}
		}
	}

	// Read 2: gates 1 to 8 and 10, the approval, and the intent.
	f, r, err = c.read(ctx, ref)
	if err != nil {
		return Result{Kind: "approved", Step: "read 2", Reason: "could not read the PR", Err: err}
	}
	if ok, why := Check(f, r, AfterApproval); !ok {
		return Result{Kind: "approved", Step: "read 2", Reason: why}
	}
	if ok, why := CheckIntent(in, f, r); !ok {
		return Result{Kind: "approved", Step: "read 2", Reason: why}
	}

	// Merge, or enqueue on a merge-queue base.
	if err := step(stepAboutToMerge); err != nil {
		return Result{Kind: "approved", Step: "merge", Reason: journalFailed, Err: err}
	}
	res, err := c.merge(ctx, in)
	if err == nil {
		return res
	}
	switch answerOf(ctx, err) {
	case answerNotSent:
		return Result{Kind: "approved", Step: "merge", Reason: Redact(err, c.Token).Error(), Err: err}
	case answerRefused:
		var end *Result
		if c.verify(ctx, ref, func(f PRFacts) bool { end = claimMerge(f, in, r); return end != nil }) {
			end.Step = "merge"
			return *end
		}
		return Result{Kind: "approved", Step: "merge", Reason: Redact(err, c.Token).Error(), Err: err}
	}
	return c.reconcileMerge(ctx, in, r, err)
}

// What a mutation's error proves.
const (
	// answerUnproven: GitHub's outcome is unknown. This is no answer, a 5xx,
	// a redirect, and every 200: a GraphQL mutation can make its change and
	// then null its result on a later error, so even `errors` with a null
	// result does not prove a refusal. Reconcile it with 3 reads.
	answerUnproven = iota
	// answerRefused: an HTTP 4xx, the plan's row "GitHub answered an error
	// (4xx, 409)". 1 read at once (verify) only upgrades it.
	answerRefused
	// answerNotSent: no request went out (a mutation off the allowlist, no
	// token), so nothing changed on GitHub. No read.
	answerNotSent
)

func answerOf(ctx context.Context, err error) int {
	if errors.Is(err, errNotSent) {
		return answerNotSent
	}
	var api *APIError
	if errors.As(err, &api) && api.Status >= 400 && api.Status < 500 && ctx.Err() == nil {
		return answerRefused
	}
	return answerUnproven
}

// verify is 1 read at once after a 4xx to a mutation. GitHub answered with
// an explicit HTTP error, so the plan's row "GitHub answered an error"
// applies; this read only upgrades the result when the effect is seen. A
// failed read keeps the refusal.
func (c *Client) verify(ctx context.Context, ref Ref, seen func(PRFacts) bool) bool {
	f, err := c.pr(ctx, ref)
	return err == nil && seen(f)
}

// pending is the reason of an unknown outcome, with GitHub's own words when
// its answer had `errors`.
func (c *Client) pending(what string, err error) string {
	var g *gqlErrors
	if errors.As(err, &g) {
		return what + "; GitHub said: " + c.redact(g.msg)
	}
	return what
}

// maxReason is the longest reason the Desk's receipt route takes, in runes.
const maxReason = 300

// capReason cuts s to maxReason runes.
func capReason(s string) string {
	if utf8.RuneCountInString(s) <= maxReason {
		return s
	}
	return string([]rune(s)[:maxReason])
}

// claimMerge is what 1 read shows of a merge or an enqueue: a merge or a
// queue entry by the owner at the shown head, a merge by another actor or at
// another head (refused, never claimed), or nil for no effect. On a queue
// base, a merge it sees is the queue's, with the queue's method.
func claimMerge(f PRFacts, in Intent, r RepoRules) *Result {
	head := in.Preview.Head
	method := in.Preview.Method
	if in.Preview.Queue {
		method = r.queueMethod
	}
	switch {
	case f.State == "MERGED" && f.MergedBy == Login && f.HeadRefOid == head:
		return &Result{Kind: "merged", Method: method, MergeCommit: f.MergeCommit}
	case f.State == "MERGED":
		by := f.MergedBy
		if by == "" {
			by = "someone"
		}
		return &Result{Kind: "refused", Reason: "merged by " + by + " at " + shortSHA(f.HeadRefOid)}
	case f.IsInMergeQueue && f.QueueEntry != nil && f.QueueEntry.Enqueuer == Login && f.QueueEntry.Head == head:
		return &Result{Kind: "queued", Method: "queue"}
	}
	return nil
}

// approvedAt says the viewer's latest review approves head.
func approvedAt(f PRFacts, head string) bool {
	v := f.ViewerLatestReview
	return f.Viewer == Login && v != nil && v.State == "APPROVED" && v.Commit == head
}

func (c *Client) approve(ctx context.Context, in Intent) error {
	var out struct {
		Add *struct {
			Review *struct {
				State *string `json:"state"`
			} `json:"pullRequestReview"`
		} `json:"addPullRequestReview"`
	}
	vars := map[string]any{"input": map[string]any{
		"pullRequestId": in.Preview.NodeID,
		"commitOID":     in.Preview.Head,
		"event":         "APPROVE",
	}}
	if err := c.GraphQL(ctx, approveMutation, vars, &out); err != nil {
		return err
	}
	if out.Add == nil || out.Add.Review == nil || out.Add.Review.State == nil || *out.Add.Review.State != "APPROVED" {
		// GitHub answered with no approval it names: read what it did.
		return errors.Join(ErrNoAnswer, errors.New("the answer names no approval"))
	}
	return nil
}

var mergeMethods = map[string]string{"merge": "MERGE", "squash": "SQUASH", "rebase": "REBASE"}

// merge sends mergePullRequest, or enqueuePullRequest on a queue base. An
// answer that does not say it worked is no answer: reconcile reads it.
func (c *Client) merge(ctx context.Context, in Intent) (Result, error) {
	p := in.Preview
	if p.Queue {
		var out struct {
			Enqueue *struct {
				Entry *struct {
					ID *string `json:"id"`
				} `json:"mergeQueueEntry"`
			} `json:"enqueuePullRequest"`
		}
		vars := map[string]any{"input": map[string]any{
			"pullRequestId":   p.NodeID,
			"expectedHeadOid": p.Head,
		}}
		if err := c.GraphQL(ctx, enqueueMutation, vars, &out); err != nil {
			return Result{}, err
		}
		if out.Enqueue == nil || out.Enqueue.Entry == nil || out.Enqueue.Entry.ID == nil {
			return Result{}, errors.Join(ErrNoAnswer, errors.New("the answer names no queue entry"))
		}
		return Result{Kind: "queued", Step: "merge", Method: "queue"}, nil
	}
	method, ok := mergeMethods[p.Method]
	if !ok {
		return Result{Kind: "approved", Step: "merge", Reason: "unknown merge method " + p.Method}, nil
	}
	var out struct {
		Merge *struct {
			PR *struct {
				State       *string `json:"state"`
				MergeCommit *struct {
					Oid *string `json:"oid"`
				} `json:"mergeCommit"`
			} `json:"pullRequest"`
		} `json:"mergePullRequest"`
	}
	vars := map[string]any{"input": map[string]any{
		"pullRequestId":   p.NodeID,
		"expectedHeadOid": p.Head,
		"mergeMethod":     method,
	}}
	if err := c.GraphQL(ctx, mergeMutation, vars, &out); err != nil {
		return Result{}, err
	}
	if out.Merge == nil || out.Merge.PR == nil || out.Merge.PR.State == nil || *out.Merge.PR.State != "MERGED" {
		return Result{}, errors.Join(ErrNoAnswer, errors.New("the answer names no merge"))
	}
	res := Result{Kind: "merged", Step: "merge", Method: p.Method}
	if mc := out.Merge.PR.MergeCommit; mc != nil && mc.Oid != nil {
		res.MergeCommit = *mc.Oid
	}
	return res, nil
}

// reconcile reads the PR 3 times (after 2, 5 and 10 seconds) until seen
// holds. A read that fails counts as not seen.
func (c *Client) reconcile(ctx context.Context, ref Ref, seen func(PRFacts) bool) bool {
	for _, d := range reconcileWaits {
		if c.wait(ctx, d) != nil {
			return false
		}
		f, err := c.pr(ctx, ref)
		if err == nil && seen(f) {
			return true
		}
	}
	return false
}

// reconcileMerge reads what a merge or an enqueue with no answer did.
func (c *Client) reconcileMerge(ctx context.Context, in Intent, r RepoRules, cause error) Result {
	var end *Result
	c.reconcile(ctx, in.Preview.Ref, func(f PRFacts) bool {
		end = claimMerge(f, in, r)
		return end != nil
	})
	if end == nil {
		return Result{Kind: "unknown", Step: "reconcile merge", Reason: c.pending("merge may be pending", cause), Err: cause}
	}
	end.Step = "reconcile merge"
	return *end
}
