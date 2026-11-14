package github

// CheckIntent holds a read to what the owner confirmed: the head, the base
// branch, the method and the queue flag of the preview, and the rule for
// risk:high (never in a batch; alone, only after the diff at the shown head).
func CheckIntent(in Intent, f PRFacts, r RepoRules) (ok bool, reason string) {
	p := in.Preview
	if f.ID != p.NodeID {
		return false, "this is not the PR you confirmed"
	}
	if f.HeadRefOid != p.Head {
		return false, "new commit since you confirmed (" + shortSHA(f.HeadRefOid) + ")"
	}
	if f.BaseRefName != p.BaseRef {
		return false, "the base branch changed to " + f.BaseRefName
	}
	if queued(f, r) != p.Queue {
		if p.Queue {
			return false, "the merge queue is off now"
		}
		return false, "the base has a merge queue now"
	}
	if method, _ := mergeMethod(f, r); method != p.Method {
		return false, "the merge method changed"
	}
	risk := f.hasLabel(labelRisk)
	if risk && !p.RiskHigh {
		return false, labelRisk + " was added since you confirmed"
	}
	switch in.Mode {
	case "batch":
		if risk {
			return false, labelRisk + " is never in a batch"
		}
	case "single":
		if risk && (in.AckHead == "" || in.AckHead != p.Head) {
			return false, "open the diff at " + shortSHA(p.Head) + " first"
		}
	default:
		return false, "unknown mode " + in.Mode
	}
	return true, ""
}
