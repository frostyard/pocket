package github

import (
	"fmt"
	"strings"
)

// Stage is when the gates run: before the approval, or after it.
type Stage int

const (
	// AtPreview is gates 1 to 10: a card's button, and read 1 at the click.
	AtPreview Stage = iota
	// AfterApproval is gates 1 to 8 and 10, the owner's approval at the
	// head, the review decision, and a merge state that merges.
	AfterApproval
)

const (
	labelReady = "ready-for-human"
	labelRisk  = "risk:high"
)

// queued says the base merges through a merge queue.
func queued(f PRFacts, r RepoRules) bool { return r.Queue || f.IsMergeQueueEnabled }

// Check runs the gates of ADR-0008 on 1 read, and gives the first that
// fails with a short reason.
func Check(f PRFacts, r RepoRules, stage Stage) (ok bool, reason string) {
	for _, gate := range []func() string{
		// 1. The owner's token.
		func() string {
			if f.Viewer != Login {
				return fmt.Sprintf("this token is for %s, not %s", f.Viewer, Login)
			}
			return ""
		},
		// 2. No bypass and no classic protection.
		func() string {
			if r.Bypass != "never" {
				return fmt.Sprintf("you can bypass a rule here (%s): Pocket refuses to merge", r.Bypass)
			}
			if r.Classic {
				return "classic branch protection: not supported"
			}
			return ""
		},
		// 3. Open, not a draft, not the viewer's, its head is its last commit.
		func() string {
			switch {
			case f.State != "OPEN":
				return "the PR is " + strings.ToLower(f.State)
			case f.IsDraft:
				return "the PR is a draft"
			case f.ViewerDidAuthor:
				return "you authored this PR"
			case f.CommitOid != f.HeadRefOid:
				return "the last commit is not the head"
			}
			return ""
		},
		// 4. Ready for the owner.
		func() string {
			if !f.hasLabel(labelReady) {
				return "no " + labelReady + " label"
			}
			return ""
		},
		// 5. Mergeable.
		func() string {
			switch f.Mergeable {
			case "MERGEABLE":
				return ""
			case "UNKNOWN":
				return "GitHub has not computed mergeability"
			case "CONFLICTING":
				return "the PR has conflicts"
			}
			return "mergeable is " + f.Mergeable
		},
		// 6. Every required check is on the head.
		func() string {
			for _, rc := range r.Required {
				if !present(rc, f.Checks) {
					return "required check " + rc.Context + " has not run"
				}
			}
			return ""
		},
		// 7. Every check and status is complete and green.
		func() string {
			for _, c := range f.Checks {
				if c.Type == "StatusContext" {
					switch c.State {
					case "SUCCESS":
					case "PENDING", "EXPECTED":
						return "status " + c.Name + " has not finished"
					default:
						return "status " + c.Name + ": " + strings.ToLower(c.State)
					}
					continue
				}
				if c.Status != "COMPLETED" {
					return "check " + c.Name + " has not finished"
				}
				switch c.Conclusion {
				case "SUCCESS", "NEUTRAL", "SKIPPED":
				default:
					return "check " + c.Name + ": " + strings.ToLower(c.Conclusion)
				}
			}
			return ""
		},
		// 8. No blocking review.
		func() string {
			for _, rv := range f.Reviews {
				if rv.State == "CHANGES_REQUESTED" {
					return rv.Author + " requested changes"
				}
			}
			if f.ReviewDecision == "CHANGES_REQUESTED" {
				return "changes are requested"
			}
			return ""
		},
		// 9. The merge state, before the approval.
		func() string {
			if stage != AtPreview {
				return ""
			}
			switch f.MergeStateStatus {
			case "BLOCKED", "CLEAN", "HAS_HOOKS":
				return ""
			case "BEHIND":
				if queued(f, r) {
					return ""
				}
			}
			return mergeStateReason(f.MergeStateStatus)
		},
		// 10. The merge method.
		func() string {
			_, why := mergeMethod(f, r)
			return why
		},
		// After the approval: the owner's approval at the head, the review
		// decision, and a merge state that merges.
		func() string {
			if stage != AfterApproval {
				return ""
			}
			v := f.ViewerLatestReview
			if v == nil || v.State != "APPROVED" || v.Commit != f.HeadRefOid {
				return "your approval at " + shortSHA(f.HeadRefOid) + " is not there"
			}
			if f.ReviewDecision != "" && f.ReviewDecision != "APPROVED" {
				return "the review decision is " + strings.ToLower(strings.ReplaceAll(f.ReviewDecision, "_", " "))
			}
			if queued(f, r) {
				switch f.MergeStateStatus {
				case "DIRTY", "DRAFT", "UNKNOWN":
					return mergeStateReason(f.MergeStateStatus)
				}
				return ""
			}
			switch f.MergeStateStatus {
			case "CLEAN", "HAS_HOOKS":
				return ""
			}
			return mergeStateReason(f.MergeStateStatus)
		},
	} {
		if why := gate(); why != "" {
			return false, why
		}
	}
	return true, ""
}

func mergeStateReason(s string) string {
	switch s {
	case "BEHIND":
		return "the PR is behind its base"
	case "DIRTY":
		return "the PR has conflicts"
	case "UNSTABLE":
		return "a check is failing"
	case "DRAFT":
		return "the PR is a draft"
	case "UNKNOWN":
		return "GitHub has not computed the merge state"
	case "BLOCKED":
		return "GitHub still blocks the merge"
	}
	return "the merge state is " + s
}

// present says a required check is on the head. A check with an
// integration ID is met only by a CheckRun of that app; a status shows no
// app, so it never meets one.
func present(rc RequiredCheck, checks []CheckContext) bool {
	for _, c := range checks {
		if c.Name != rc.Context {
			continue
		}
		if rc.IntegrationID == 0 {
			return true
		}
		if c.Type == "CheckRun" && c.App && c.AppID == rc.IntegrationID {
			return true
		}
	}
	return false
}

// mergeMethod is the method Pocket uses: the queue on a merge-queue base,
// else the owner's default in the repository, when it is allowed there.
func mergeMethod(f PRFacts, r RepoRules) (method, reason string) {
	if queued(f, r) {
		if r.queueMethod == "" {
			return "", "the merge queue's rule names no method Pocket can read"
		}
		return "queue", ""
	}
	var allowed bool
	switch f.DefaultMergeMethod {
	case "MERGE":
		method, allowed = "merge", f.MergeCommitAllowed
	case "SQUASH":
		method, allowed = "squash", f.SquashMergeAllowed
	case "REBASE":
		method, allowed = "rebase", f.RebaseMergeAllowed
	default:
		return "", "unknown merge method " + f.DefaultMergeMethod
	}
	if !allowed {
		return "", "your default merge method (" + method + ") is not allowed here"
	}
	return method, ""
}
