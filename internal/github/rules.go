package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
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
	// Proof is what the rules of the rulesets the viewer can bypass need,
	// which Pocket proves itself (ADR-0011); the zero value when Bypass is
	// "never".
	Proof BypassRules
	// Classic says classic branch protection is on.
	Classic bool

	// queueMethod is the merge queue's method ("merge", "squash" or
	// "rebase"), for the receipt of a PR that the queue merged.
	queueMethod string
	// baseTip is the base branch's commit, for a strict status checks rule
	// read over REST; "" when the branch's answer names none.
	baseTip string
}

// BypassRules is what Pocket must prove itself on a base where the owner
// can bypass a ruleset (ADR-0011). It holds only the rules of the rulesets
// whose `current_user_can_bypass` lets the owner act.
type BypassRules struct {
	Unchecked         string   // the first rule ("type" or "type.parameter") Pocket does not check; "" for none
	Approvals         int      // the largest required_approving_review_count
	ExtraUnattributed bool     // require_extra_approval_for_unattributed_changes
	LastPush          bool     // require_last_push_approval
	Threads           bool     // required_review_thread_resolution
	Linear            bool     // a required_linear_history rule
	Strict            bool     // strict_required_status_checks_policy
	Methods           []string // allowed_merge_methods, lower case, intersected over the rules
	MethodsSet        bool     // Methods applies (some rule had allowed_merge_methods)
	PullRequest       bool     // a pull_request rule is 1 of them
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
		// Only an explicit [] says no rules: a null page proves nothing.
		var got *[]rawRule
		path := fmt.Sprintf("%s/rules/branches/%s?per_page=%d&page=%d", repo, url.PathEscape(base), rulesPerPage, page)
		if err := c.REST(ctx, path, &got); err != nil {
			return RepoRules{}, err
		}
		if got == nil {
			return RepoRules{}, incomplete("a page of rules is null")
		}
		rules = append(rules, *got...)
		if len(*got) < rulesPerPage {
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
		if r.Type == "" {
			return RepoRules{}, incomplete("a rule has no type")
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
			// A null or missing list is no proof of "no required checks".
			var p *struct {
				Checks *[]struct {
					Context       *string `json:"context"`
					IntegrationID *int64  `json:"integration_id"`
				} `json:"required_status_checks"`
			}
			if len(r.Parameters) == 0 {
				return RepoRules{}, incomplete("the required checks rule has no parameters")
			}
			if err := json.Unmarshal(r.Parameters, &p); err != nil {
				return RepoRules{}, incomplete("the required checks do not parse")
			}
			if p == nil || p.Checks == nil {
				return RepoRules{}, incomplete("the required checks rule lists no checks")
			}
			for _, rc := range *p.Checks {
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

	// 1 value for each ruleset: a missing or empty one says nothing, so it
	// is "unknown".
	bypass := make(map[int64]string, len(ids))
	for _, id := range ids {
		var rs struct {
			Bypass *string `json:"current_user_can_bypass"`
		}
		if err := c.REST(ctx, fmt.Sprintf("%s/rulesets/%d", repo, id), &rs); err != nil {
			return RepoRules{}, err
		}
		mode := "unknown"
		if rs.Bypass != nil && *rs.Bypass != "" {
			mode = *rs.Bypass
		}
		bypass[id] = mode
	}
	out.Bypass = bypassOf(ids, bypass)

	// The rules of the rulesets the owner can bypass: Pocket proves each 1
	// itself (ADR-0011). Required, Queue and Classic stay as they are, from
	// every rule whatever the bypass.
	if out.Bypass != "never" {
		for _, r := range rules {
			switch bypass[*r.RulesetID] {
			case "always", "pull_requests_only":
				out.Proof.rule(r.Type, r.Parameters)
			}
		}
	}

	// `protected` is true on a branch with rulesets too: only
	// protection.enabled says classic protection is on.
	var br struct {
		Commit *struct {
			SHA *string `json:"sha"`
		} `json:"commit"`
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
	if br.Commit != nil && br.Commit.SHA != nil {
		out.baseTip = *br.Commit.SHA
	}
	out.Classic = *br.Protection.Enabled
	return out, nil
}

// bypassOf is the viewer's bypass over the rulesets, in their order:
// "never" when every ruleset says never; else the first value Pocket does
// not handle, so that a refusing value wins over a bypassable 1; else the
// first bypass.
func bypassOf(ids []int64, modes map[int64]string) string {
	out := "never"
	for _, id := range ids {
		switch modes[id] {
		case "never":
			continue
		case "always", "pull_requests_only":
			if out == "never" {
				out = modes[id]
			}
		default:
			return modes[id]
		}
	}
	return out
}

// unchecked keeps the first rule Pocket does not check: the later ones
// change nothing.
func (p *BypassRules) unchecked(name string) {
	if p.Unchecked == "" {
		p.Unchecked = name
	}
}

// What GitHub always sends with a rule: a rule that leaves 1 out proves
// nothing, so Pocket refuses it (ADR-0011, fail closed). The lists are
// sorted, so the first missing key is the same on every read.
var (
	pullRequestKeys = []string{
		"dismiss_stale_reviews_on_push",
		"require_code_owner_review",
		"require_last_push_approval",
		"required_approving_review_count",
		"required_review_thread_resolution",
	}
	statusCheckKeys = []string{
		"required_status_checks",
		"strict_required_status_checks_policy",
	}
)

// rule add 1 rule of a ruleset the owner can bypass. A type or a parameter
// Pocket does not check refuses, and names itself (ADR-0011).
func (p *BypassRules) rule(typ string, params json.RawMessage) {
	switch typ {
	case "deletion", "creation", "non_fast_forward":
		// A merge moves an existing branch to a descendant commit: it never
		// deletes, creates or force-pushes the branch. These rules take no
		// parameter.
		p.onlyKeys(typ, params, nil)
		return
	case "required_linear_history":
		p.Linear = true
		p.onlyKeys(typ, params, nil)
		return
	case "merge_queue":
		// Pocket enqueues, and the queue's own parameters only shape the
		// queue; a shape Pocket cannot read still proves nothing.
		p.mergeQueue(params)
		return
	case "required_status_checks":
		p.statusChecks(params)
		return
	case "pull_request":
		p.pullRequest(params)
		return
	}
	p.unchecked(typ)
}

// mergeQueue proves a merge_queue rule: its parameters shape the queue, not
// the merge Pocket makes, but each 1 has a shape Pocket reads, and any other
// key must be off.
func (p *BypassRules) mergeQueue(params json.RawMessage) {
	if trimmed := strings.TrimSpace(string(params)); trimmed == "" || trimmed == "null" {
		return
	}
	values, err := parameterValues(params)
	if err != nil {
		p.unchecked("merge_queue")
		return
	}
	for _, k := range keysOf(values) {
		name := "merge_queue." + k
		switch k {
		case "merge_method", "grouping_strategy":
			if v, ok := text(values[k]); !ok || v == "" {
				p.unchecked(name)
				return
			}
		case "max_entries_to_build", "min_entries_to_merge", "max_entries_to_merge",
			"min_entries_to_merge_wait_minutes", "check_response_timeout_minutes":
			if n, ok := count(values[k]); !ok || n < 0 {
				p.unchecked(name)
				return
			}
		default:
			if !off(values[k]) {
				p.unchecked(name)
				return
			}
		}
	}
}

// statusChecks proves a required_status_checks rule: the checks themselves
// are in Required (gates 6 and 7).
func (p *BypassRules) statusChecks(params json.RawMessage) {
	values, keys, ok := parameters(params)
	if !ok {
		p.unchecked("required_status_checks")
		return
	}
	for _, k := range statusCheckKeys {
		if _, ok := values[k]; !ok {
			p.unchecked("required_status_checks." + k)
			return
		}
	}
	for _, k := range keys {
		name := "required_status_checks." + k
		switch k {
		case "required_status_checks":
			// Gates 6 and 7 prove the checks themselves; Rules reads the
			// shape of the list.
		case "strict_required_status_checks_policy":
			v, ok := boolean(values[k])
			if !ok {
				p.unchecked(name)
				return
			}
			p.Strict = p.Strict || v
		case "do_not_enforce_on_create":
			// A boolean at most: it only shapes a branch creation, which a
			// merge is not.
			if _, ok := boolean(values[k]); !ok {
				p.unchecked(name)
				return
			}
		default:
			if !off(values[k]) {
				p.unchecked(name)
				return
			}
		}
	}
}

// pullRequest proves a pull_request rule, key by key.
func (p *BypassRules) pullRequest(params json.RawMessage) {
	p.PullRequest = true
	values, keys, ok := parameters(params)
	if !ok {
		p.unchecked("pull_request")
		return
	}
	for _, k := range pullRequestKeys {
		if _, ok := values[k]; !ok {
			p.unchecked("pull_request." + k)
			return
		}
	}
	for _, k := range keys {
		name := "pull_request." + k
		switch k {
		case "required_approving_review_count":
			n, ok := count(values[k])
			if !ok || n < 0 {
				p.unchecked(name)
				return
			}
			p.Approvals = max(p.Approvals, n)
		case "require_code_owner_review":
			v, ok := boolean(values[k])
			if !ok || v {
				// Pocket reads no code owners.
				p.unchecked(name)
				return
			}
		case "required_reviewers":
			// Absent and [] name no reviewer; null, a shape Pocket does not
			// know and a named reviewer refuse.
			var list []json.RawMessage
			if err := json.Unmarshal(values[k], &list); err != nil || list == nil || len(list) > 0 {
				p.unchecked(name)
				return
			}
		case "require_last_push_approval":
			v, ok := boolean(values[k])
			if !ok {
				p.unchecked(name)
				return
			}
			p.LastPush = p.LastPush || v
		case "required_review_thread_resolution":
			v, ok := boolean(values[k])
			if !ok {
				p.unchecked(name)
				return
			}
			p.Threads = p.Threads || v
		case "require_extra_approval_for_unattributed_changes":
			v, ok := boolean(values[k])
			if !ok {
				p.unchecked(name)
				return
			}
			p.ExtraUnattributed = p.ExtraUnattributed || v
		case "allowed_merge_methods":
			// Absent allows any method; null, a shape Pocket does not know
			// and a list that is not of strings refuse.
			var list []string
			if err := json.Unmarshal(values[k], &list); err != nil || list == nil {
				p.unchecked(name)
				return
			}
			p.methods(list)
		case "dismiss_stale_reviews_on_push":
			// No merge requirement, because Pocket counts only approvals at
			// the head; GitHub always sends it, so it must read.
			if _, ok := boolean(values[k]); !ok {
				p.unchecked(name)
				return
			}
		case "automatic_copilot_code_review_enabled":
			// No merge requirement, but only a boolean proves the rule is
			// off, as GitHub sends it.
			if _, ok := boolean(values[k]); !ok {
				p.unchecked(name)
				return
			}
		case "dismissal_restriction":
			// An object of any keys: it sets no merge requirement, only who
			// may dismiss a review.
			var shape map[string]json.RawMessage
			if err := json.Unmarshal(values[k], &shape); err != nil || shape == nil {
				p.unchecked(name)
				return
			}
		default:
			// A key Pocket does not know is accepted only when it is off.
			if !off(values[k]) {
				p.unchecked(name)
				return
			}
		}
	}
	// GitHub turns the extra approval on by default: an absent key is on.
	if _, ok := values["require_extra_approval_for_unattributed_changes"]; !ok {
		p.ExtraUnattributed = true
	}
}

// onlyKeys refuses a key of a rule whose parameters Pocket ignores, when the
// key is not 1 it knows and is on. Absent, null and {} parameters hold no
// key.
func (p *BypassRules) onlyKeys(typ string, params json.RawMessage, known map[string]bool) {
	if trimmed := strings.TrimSpace(string(params)); trimmed == "" || trimmed == "null" {
		return
	}
	values, err := parameterValues(params)
	if err != nil {
		// No object at all: no parameter Pocket can read.
		p.unchecked(typ)
		return
	}
	for _, k := range keysOf(values) {
		if known[k] {
			continue
		}
		if !off(values[k]) {
			p.unchecked(typ + "." + k)
			return
		}
	}
}

// methods keeps the methods every allowed_merge_methods rule allows, in
// lower case.
func (p *BypassRules) methods(list []string) {
	lower := make([]string, 0, len(list))
	for _, m := range list {
		lower = append(lower, strings.ToLower(m))
	}
	if !p.MethodsSet {
		p.Methods, p.MethodsSet = lower, true
		return
	}
	var kept []string
	for _, m := range p.Methods {
		for _, l := range lower {
			if m == l {
				kept = append(kept, m)
				break
			}
		}
	}
	p.Methods = kept
}

// parameters decodes a rule's parameters, and gives the keys in order, so
// that the first rule Pocket does not check is the same on every read.
// ok is false when they are no object Pocket can read.
func parameters(params json.RawMessage) (values map[string]json.RawMessage, keys []string, ok bool) {
	if len(params) == 0 {
		return nil, nil, false
	}
	values, err := parameterValues(params)
	if err != nil {
		return nil, nil, false
	}
	return values, keysOf(values), true
}

// parameterValues decodes a rule's parameters as an object; an error says
// they are no object Pocket can read (null, a list, a scalar).
func parameterValues(params json.RawMessage) (map[string]json.RawMessage, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(params, &values); err != nil {
		return nil, err
	}
	if values == nil {
		return nil, errors.New("no object")
	}
	return values, nil
}

// keysOf is the object's keys in order.
func keysOf(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// text is a JSON string; ok is false for any other value.
func text(raw json.RawMessage) (s string, ok bool) {
	var p *string
	if err := json.Unmarshal(raw, &p); err != nil || p == nil {
		return "", false
	}
	return *p, true
}

// boolean is a JSON true or false; ok is false for any other value.
func boolean(raw json.RawMessage) (v, ok bool) {
	var b *bool
	if err := json.Unmarshal(raw, &b); err != nil || b == nil {
		return false, false
	}
	return *b, true
}

// count is a JSON number that is a whole count of 0 or more.
func count(raw json.RawMessage) (int, bool) {
	var f *float64
	if err := json.Unmarshal(raw, &f); err != nil || f == nil || *f != math.Trunc(*f) || *f > math.MaxInt32 {
		return 0, false
	}
	return int(*f), true
}

// off says a value Pocket does not check is a feature that is off: null,
// false, 0, "", [] or {}.
func off(raw json.RawMessage) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	switch v := v.(type) {
	case nil:
		return true
	case bool:
		return !v
	case float64:
		return v == 0
	case string:
		return v == ""
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}
