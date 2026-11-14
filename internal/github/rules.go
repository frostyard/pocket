package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// RequiredCheck is a required status check of the rules on a branch.
type RequiredCheck struct {
	Context       string
	IntegrationID int64 // 0: none, any app or a status meets it
}

// RepoRules is what the rules, rulesets and branch protection of a base
// branch say, read with the owner's token.
type RepoRules struct {
	Required []RequiredCheck
	// Queue says a merge_queue rule applies.
	Queue bool
	// Bypass is "never" when the viewer can bypass no ruleset that applies;
	// else the first other `current_user_can_bypass` ("unknown" for none).
	Bypass string
	// Classic says classic branch protection is on.
	Classic bool

	// queueMethod is the merge queue's method ("merge", "squash" or
	// "rebase"), for the receipt of a PR that the queue merged.
	queueMethod string
}

// queueMethods maps a merge_queue rule's merge_method to a receipt's method.
var queueMethods = map[string]string{"MERGE": "merge", "SQUASH": "squash", "REBASE": "rebase"}

const (
	rulesPerPage = 100
	maxRulePages = 10
)

type rawRule struct {
	Type       string          `json:"type"`
	RulesetID  *int64          `json:"ruleset_id"`
	Parameters json.RawMessage `json:"parameters"`
}

// Rules reads the rules on base: every page of the rules that apply, the
// bypass of each of their rulesets, and the branch's classic protection.
func (c *Client) Rules(ctx context.Context, ref Ref, base string) (RepoRules, error) {
	if base == "" {
		return RepoRules{}, incomplete("no base branch")
	}
	repo := "/repos/" + url.PathEscape(ref.Owner) + "/" + url.PathEscape(ref.Name)
	out := RepoRules{Bypass: "never"}
	var rules []rawRule
	for page := 1; ; page++ {
		if page > maxRulePages {
			return RepoRules{}, incomplete("too many rules")
		}
		var got []rawRule
		path := fmt.Sprintf("%s/rules/branches/%s?per_page=%d&page=%d", repo, url.PathEscape(base), rulesPerPage, page)
		if err := c.REST(ctx, path, &got); err != nil {
			return RepoRules{}, err
		}
		rules = append(rules, got...)
		if len(got) < rulesPerPage {
			break
		}
	}

	var ids []int64
	seenID := map[int64]bool{}
	seenCheck := map[RequiredCheck]bool{}
	for _, r := range rules {
		if r.RulesetID == nil {
			return RepoRules{}, incomplete("a rule names no ruleset")
		}
		if !seenID[*r.RulesetID] {
			seenID[*r.RulesetID] = true
			ids = append(ids, *r.RulesetID)
		}
		switch r.Type {
		case "merge_queue":
			var p struct {
				Method *string `json:"merge_method"`
			}
			if err := json.Unmarshal(r.Parameters, &p); err != nil || p.Method == nil {
				return RepoRules{}, incomplete("the merge queue names no method")
			}
			m, ok := queueMethods[*p.Method]
			if !ok {
				return RepoRules{}, incomplete("the merge queue's method " + *p.Method + " is unknown")
			}
			out.Queue, out.queueMethod = true, m
		case "required_status_checks":
			var p struct {
				Checks []struct {
					Context       *string `json:"context"`
					IntegrationID *int64  `json:"integration_id"`
				} `json:"required_status_checks"`
			}
			if err := json.Unmarshal(r.Parameters, &p); err != nil {
				return RepoRules{}, incomplete("the required checks do not parse")
			}
			for _, rc := range p.Checks {
				if rc.Context == nil || *rc.Context == "" {
					return RepoRules{}, incomplete("a required check has no name")
				}
				check := RequiredCheck{Context: *rc.Context}
				if rc.IntegrationID != nil {
					check.IntegrationID = *rc.IntegrationID
				}
				if !seenCheck[check] {
					seenCheck[check] = true
					out.Required = append(out.Required, check)
				}
			}
		}
	}

	for _, id := range ids {
		var rs struct {
			Bypass *string `json:"current_user_can_bypass"`
		}
		if err := c.REST(ctx, fmt.Sprintf("%s/rulesets/%d", repo, id), &rs); err != nil {
			return RepoRules{}, err
		}
		if rs.Bypass == nil || *rs.Bypass != "never" {
			if out.Bypass == "never" {
				out.Bypass = "unknown"
				if rs.Bypass != nil && *rs.Bypass != "" {
					out.Bypass = *rs.Bypass
				}
			}
		}
	}

	// `protected` is true on a branch with rulesets too: only
	// protection.enabled says classic protection is on.
	var br struct {
		Protection *struct {
			Enabled *bool `json:"enabled"`
		} `json:"protection"`
	}
	if err := c.REST(ctx, repo+"/branches/"+url.PathEscape(base), &br); err != nil {
		return RepoRules{}, err
	}
	if br.Protection == nil || br.Protection.Enabled == nil {
		return RepoRules{}, incomplete("the branch protection is missing")
	}
	out.Classic = *br.Protection.Enabled
	return out, nil
}
