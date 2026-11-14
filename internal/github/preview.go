package github

import (
	"context"
	"time"
)

// Preview is what a card shows: the PR's identity and head, the method,
// and whether gates 1 to 10 pass. Head is the shown head.
type Preview struct {
	Ref                                   Ref
	NodeID, Title, BaseRef, BaseOid, Head string
	RiskHigh, Queue                       bool
	Method                                string
	Eligible                              bool
	Reason                                string
	ReadAt                                time.Time
}

// Preview reads ref (the PR query, then the rules on its base) and runs
// gates 1 to 10. A failed gate is no error: Eligible is false and Reason
// says why. A failed read is an error.
func (c *Client) Preview(ctx context.Context, ref Ref) (Preview, error) {
	f, r, err := c.read(ctx, ref)
	if err != nil {
		return Preview{Ref: ref}, Redact(err, c.Token)
	}
	method, _ := mergeMethod(f, r)
	p := Preview{
		Ref:      ref,
		NodeID:   f.ID,
		Title:    f.Title,
		BaseRef:  f.BaseRefName,
		BaseOid:  f.BaseRefOid,
		Head:     f.HeadRefOid,
		RiskHigh: f.hasLabel(labelRisk),
		Queue:    queued(f, r),
		Method:   method,
		ReadAt:   c.clock(),
	}
	p.Eligible, p.Reason = Check(f, r, AtPreview)
	return p, nil
}

// unknownWait is the wait before the 1 read again of a PR whose
// mergeability GitHub has not computed.
const unknownWait = 2 * time.Second

// read is 1 full read: the PR query, the rules on its base, and 1 more PR
// query after 2 seconds when mergeable is UNKNOWN.
func (c *Client) read(ctx context.Context, ref Ref) (PRFacts, RepoRules, error) {
	f, err := c.pr(ctx, ref)
	if err != nil {
		return PRFacts{}, RepoRules{}, err
	}
	r, err := c.Rules(ctx, ref, f.BaseRefName)
	if err != nil {
		return PRFacts{}, RepoRules{}, err
	}
	if f.Mergeable == "UNKNOWN" {
		if err := c.wait(ctx, unknownWait); err != nil {
			return PRFacts{}, RepoRules{}, err
		}
		again, err := c.pr(ctx, ref)
		if err != nil {
			return PRFacts{}, RepoRules{}, err
		}
		if again.BaseRefName != f.BaseRefName {
			return PRFacts{}, RepoRules{}, incomplete("the base branch changed while reading")
		}
		f = again
	}
	return f, r, nil
}
