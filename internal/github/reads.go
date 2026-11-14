package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The reads of Settings > GitHub and of a PR after an attempt: each sends
// GET requests and the PR query only, never a mutation.

// Identity is who a token signs in as, and when it expires.
type Identity struct {
	Login string
	// Expires is the token's expiry; zero when GitHub names none.
	Expires time.Time
}

// expiryLayouts are the forms of the header
// github-authentication-token-expiration ("2026-12-14 10:00:00 UTC" or
// "2026-12-14 10:00:00 -0500").
var expiryLayouts = []string{"2006-01-02 15:04:05 MST", "2006-01-02 15:04:05 -0700", time.RFC3339}

// parseExpiry reads the expiry header; zero when absent or unparsable.
func parseExpiry(v string) time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}
	}
	for _, layout := range expiryLayouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

// Whoami reads GET /user: the token's login and its expiry. Every error
// names the call.
func (c *Client) Whoami(ctx context.Context) (Identity, error) {
	data, h, err := c.doHeaders(ctx, http.MethodGet, "/user", nil)
	if err != nil {
		return Identity{}, Redact(onCall("GET /user", err), c.Token)
	}
	var u struct {
		Login *string `json:"login"`
	}
	if err := json.Unmarshal(data, &u); err != nil || u.Login == nil || *u.Login == "" {
		return Identity{}, onCall("GET /user", incomplete("the user has no login"))
	}
	return Identity{Login: *u.Login, Expires: parseExpiry(h.Get("Github-Authentication-Token-Expiration"))}, nil
}

// CheckRepo reads GET /repos/<owner>/<name>: the token can read it.
func (c *Client) CheckRepo(ctx context.Context, owner, name string) error {
	if owner == "" || name == "" {
		return notSent("no repository")
	}
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	return c.REST(ctx, path, nil)
}

// AppCheck is what the App's installations say after a sign-in: who the
// sign-in acts as, and for each owner whether the App is ready ("") or why
// it is not. Notes holds what does not refuse: an owner with no Merge
// queues permission gets a note, because an enqueue may fail.
type AppCheck struct {
	Login  string
	Owners map[string]string
	Notes  map[string]string
}

// installationsPerPage is the page size of GET /user/installations.
const installationsPerPage = 100

// maxInstallationPages caps the pages Pocket reads (100 installations a
// page).
const maxInstallationPages = 20

// installation is 1 installation of the App in GET /user/installations:
// its app, its account, its suspension and its permissions.
type installation struct {
	AppSlug     string            `json:"app_slug"`
	SuspendedAt *string           `json:"suspended_at"`
	Permissions map[string]string `json:"permissions"`
	Account     struct {
		Login *string `json:"login"`
	} `json:"account"`
}

// appPermissions are the App's repository permissions Pocket needs, with
// the level each needs; a write meets a read.
var appPermissions = []struct {
	Name, Need string
}{
	{"checks", "read"},
	{"statuses", "read"},
	{"contents", "write"},
	{"metadata", "read"},
	{"pull_requests", "write"},
	{"workflows", "write"},
}

// permissionLevels ranks a permission's level; 0 is no access.
var permissionLevels = map[string]int{"read": 1, "write": 2, "admin": 3}

// CheckApp reads GET /user and every page of GET /user/installations, and
// says for each owner whether the Pocket App is installed there, not
// suspended, and holds the permissions Pocket needs. It reports the login
// as it is: the caller refuses a login that is not Login.
func (c *Client) CheckApp(ctx context.Context, owners []string) (AppCheck, error) {
	check := AppCheck{Owners: map[string]string{}, Notes: map[string]string{}}
	id, err := c.Whoami(ctx)
	if err != nil {
		return check, err
	}
	check.Login = id.Login
	installs, err := c.installations(ctx)
	if err != nil {
		// A read that fails keeps nothing: the login alone proves no
		// owner's state.
		return AppCheck{}, err
	}
	for _, owner := range owners {
		reason, note := appOwner(installs, owner)
		check.Owners[owner] = reason
		if note != "" {
			check.Notes[owner] = note
		}
	}
	return check, nil
}

// installations reads every page of GET /user/installations, and holds the
// answer to the total_count GitHub gives: a short page before that count, or
// more installations than it, proves nothing.
func (c *Client) installations(ctx context.Context) ([]installation, error) {
	var out []installation
	total := -1
	for page := 1; ; page++ {
		if page > maxInstallationPages {
			return nil, incomplete("too many installations")
		}
		// Only an explicit list says there is none: a null page proves
		// nothing, and neither does a missing count.
		var got *struct {
			TotalCount    *int            `json:"total_count"`
			Installations *[]installation `json:"installations"`
		}
		path := fmt.Sprintf("/user/installations?per_page=%d&page=%d", installationsPerPage, page)
		if err := c.REST(ctx, path, &got); err != nil {
			return nil, err
		}
		if got == nil || got.Installations == nil {
			return nil, incomplete("a page of installations is null")
		}
		if got.TotalCount == nil || *got.TotalCount < 0 {
			return nil, incomplete("the installations name no total_count")
		}
		if total < 0 {
			total = *got.TotalCount
		} else if *got.TotalCount != total {
			return nil, incomplete("the total_count changed while reading the installations")
		}
		out = append(out, *got.Installations...)
		switch {
		case len(out) > total:
			return nil, incomplete(fmt.Sprintf("%d installations, but the total_count is %d", len(out), total))
		case len(out) == total:
			return out, nil
		case len(*got.Installations) < installationsPerPage:
			return nil, incomplete(fmt.Sprintf("a short page of installations: %d of %d", len(out), total))
		}
	}
}

// appOwner says whether the App is ready on owner ("" is ready), and the
// note that does not refuse.
func appOwner(installs []installation, owner string) (reason, note string) {
	for _, in := range installs {
		if in.AppSlug != AppSlug || in.Account.Login == nil || !strings.EqualFold(*in.Account.Login, owner) {
			continue
		}
		if in.SuspendedAt != nil {
			return "the App is suspended on " + owner, ""
		}
		for _, want := range appPermissions {
			if permissionLevels[in.Permissions[want.Name]] < permissionLevels[want.Need] {
				return "the App lacks " + want.Name + ": " + want.Need + " on " + owner, ""
			}
		}
		if permissionLevels[in.Permissions["merge_queues"]] < permissionLevels["write"] {
			return "", "an enqueue may fail: the App lacks merge_queues: write on " + owner
		}
		return "", ""
	}
	return "the App is not installed on " + owner, ""
}

// PreflightRow is what the read-only preflight found for 1 repository,
// read on 1 of its ready PRs with the owner's token. Reason is "" when the
// repository passes: readable, no bypass, no classic protection, the
// owner's login.
type PreflightRow struct {
	Repo     string
	Readable bool
	// Bypass is "never" when the viewer can bypass no ruleset that applies.
	Bypass  string
	Classic bool
	// Missing is the permission GitHub names in a refused read.
	Missing, Reason string
	// Err is the redacted error of a read that failed, and nil when the
	// repository passed: a caller tells a refused sign-in from a repository
	// that has no button.
	Err error
}

// Preflight runs the full read of ref (the PR query, rules, rulesets and
// branch) and gates 1 and 2 on it. It sends no mutation.
func (c *Client) Preflight(ctx context.Context, ref Ref) PreflightRow {
	row := PreflightRow{Repo: ref.Owner + "/" + ref.Name}
	f, r, err := c.read(ctx, ref)
	if err != nil {
		err = Redact(err, c.Token)
		row.Reason, row.Err = err.Error(), err
		var api *APIError
		if errors.As(err, &api) {
			row.Missing = api.Accepted
		}
		return row
	}
	row.Readable, row.Bypass, row.Classic = true, r.Bypass, r.Classic
	row.Reason = repoGate(f, r)
	return row
}

// PRState is what 1 PR query says of a PR after an attempt.
type PRState struct {
	State, MergedBy, Head string
	InQueue               bool
	QueueBy, QueueHead    string
}

// State reads ref with 1 PR query.
func (c *Client) State(ctx context.Context, ref Ref) (PRState, error) {
	f, err := c.pr(ctx, ref)
	if err != nil {
		return PRState{}, Redact(err, c.Token)
	}
	s := PRState{State: f.State, MergedBy: f.MergedBy, Head: f.HeadRefOid, InQueue: f.IsInMergeQueue}
	if f.QueueEntry != nil {
		s.QueueBy, s.QueueHead = f.QueueEntry.Enqueuer, f.QueueEntry.Head
	}
	return s, nil
}

// Settle reads what an earlier attempt of in did, by reads only: never a
// mutation. 1 read at once; with wait, when it settles nothing, 3 more
// reads (after 2, 5 and 10 seconds). A merge or a queue entry by the owner
// at the shown head is merged or queued; a merge by another actor or at
// another head is refused, never claimed. Else the owner's approval at the
// shown head is "approved" ("approved, not merged"), and nothing is
// "unknown" ("no effect seen"); Err holds a read's error.
func (c *Client) Settle(ctx context.Context, in Intent, op string, wait bool) Result {
	res := c.settle(ctx, in, wait)
	res.Op = op
	res.Reason = capReason(res.Reason)
	res.Err = Redact(res.Err, c.Token)
	if res.Err != nil && res.Kind == "unknown" {
		res.Reason = capReason(res.Reason + ": " + res.Err.Error())
	}
	return res
}

func (c *Client) settle(ctx context.Context, in Intent, wait bool) Result {
	ref := in.Preview.Ref
	// A queue base needs its rules for the method of a merge the queue made.
	// Without them nothing is claimed: a merge's method is never made up.
	var r RepoRules
	if in.Preview.Queue {
		got, err := c.Rules(ctx, ref, in.Preview.BaseRef)
		if err == nil && got.queueMethod == "" {
			err = incomplete("the rules name no merge queue method")
		}
		if err != nil {
			return Result{Kind: "unknown", Step: "settle", Reason: "could not read the merge queue's rules", Err: err}
		}
		r = got
	}
	var last *PRFacts
	var end *Result
	seen := func(f PRFacts) bool {
		last = &f
		end = claimMerge(f, in, r)
		return end != nil
	}
	f, err := c.pr(ctx, ref)
	if err == nil && seen(f) {
		end.Step = "settle"
		return *end
	}
	if wait && c.reconcile(ctx, ref, seen) {
		end.Step = "settle"
		return *end
	}
	if last != nil && approvedAt(*last, in.Preview.Head) {
		return Result{Kind: "approved", Step: "settle", Reason: "approved, not merged"}
	}
	if last == nil && err != nil {
		return Result{Kind: "unknown", Step: "settle", Reason: "could not read the PR", Err: err}
	}
	return Result{Kind: "unknown", Step: "settle", Reason: "no effect seen"}
}

// String is the row in words, for a log line.
func (p PreflightRow) String() string {
	if p.Reason != "" {
		return fmt.Sprintf("%s: %s", p.Repo, p.Reason)
	}
	return p.Repo + ": ready"
}
