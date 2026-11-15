package github

import (
	"context"
	"net/url"
)

// bypassQuery is the bypass query (PocketBypass). It is the text of
// testdata/bypass-query.graphql, which a test holds it to.
const bypassQuery = `query PocketBypass($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      headRefOid
      headRefName
      isCrossRepository
      commits(last: 1) {
        nodes {
          commit {
            oid
            associatedPullRequests(first: 20) {
              pageInfo {
                hasNextPage
              }
              nodes {
                number
                state
                headRefOid
              }
            }
          }
        }
      }
      author {
        __typename
        login
      }
      reviewThreads(first: 100) {
        pageInfo {
          hasNextPage
        }
        nodes {
          isResolved
        }
      }
      latestOpinionatedReviews(first: 100) {
        pageInfo {
          hasNextPage
        }
        nodes {
          state
          authorCanPushToRepository
          author {
            login
          }
          commit {
            oid
          }
        }
      }
    }
  }
}
`

// BypassFacts is what Pocket reads to prove the rules a bypass would skip
// (ADR-0011): the head, who wrote the PR, its unresolved review threads, the
// approvals at the head, who pushed last, and how far the base is ahead.
type BypassFacts struct {
	Head, HeadRef string
	CrossRepo     bool
	AuthorType    string   // __typename of the PR's author; "" when author is null
	Unresolved    int      // review threads not resolved
	Approvers     []string // distinct logins: latest opinionated review APPROVED, at Head, authorCanPushToRepository true
	LastPusher    string   // actor of the last push to the head branch; "" when not read or not proven
	Behind        int      // behind_by of base tip...head; -1 when not read
	// SameHead is the numbers of the other open PRs whose head is Head: their
	// reviews can block a merge GitHub refuses, and Pocket does not read them.
	SameHead []int
}

// The bypass answer: every field is a pointer, so a null shows.
type (
	rawBypassAnswer struct {
		Repository *struct {
			PullRequest *rawBypassPR `json:"pullRequest"`
		} `json:"repository"`
	}
	rawBypassPR struct {
		HeadRefOid        *string `json:"headRefOid"`
		HeadRefName       *string `json:"headRefName"`
		IsCrossRepository *bool   `json:"isCrossRepository"`
		Commits           *struct {
			Nodes *[]*struct {
				Commit *struct {
					Oid                    *string `json:"oid"`
					AssociatedPullRequests *struct {
						PageInfo *rawPageInfo `json:"pageInfo"`
						Nodes    *[]*struct {
							Number     *int    `json:"number"`
							State      *string `json:"state"`
							HeadRefOid *string `json:"headRefOid"`
						} `json:"nodes"`
					} `json:"associatedPullRequests"`
				} `json:"commit"`
			} `json:"nodes"`
		} `json:"commits"`
		Author *struct {
			Typename *string `json:"__typename"`
			Login    *string `json:"login"`
		} `json:"author"`
		ReviewThreads *struct {
			PageInfo *rawPageInfo `json:"pageInfo"`
			Nodes    *[]*struct {
				IsResolved *bool `json:"isResolved"`
			} `json:"nodes"`
		} `json:"reviewThreads"`
		LatestOpinionatedReviews *struct {
			PageInfo *rawPageInfo `json:"pageInfo"`
			Nodes    *[]*struct {
				State                     *string   `json:"state"`
				AuthorCanPushToRepository *bool     `json:"authorCanPushToRepository"`
				Author                    *rawLogin `json:"author"`
				Commit                    *rawOid   `json:"commit"`
			} `json:"nodes"`
		} `json:"latestOpinionatedReviews"`
	}
	// activityEntry is 1 entry of GET /repos/{owner}/{repo}/activity.
	activityEntry struct {
		Ref          *string `json:"ref"`
		After        *string `json:"after"`
		ActivityType *string `json:"activity_type"`
		Actor        *struct {
			Login *string `json:"login"`
		} `json:"actor"`
	}
)

// bypassFacts reads what Pocket needs to prove the rules of the rulesets
// the owner can bypass: the bypass query, then the branch's last push and
// the base's compare when the rules need them. Only a proven fact is read:
// a null, a truncated list or a head that moved refuses the read.
func (c *Client) bypassFacts(ctx context.Context, ref Ref, f PRFacts, r RepoRules) (BypassFacts, error) {
	out := BypassFacts{Behind: -1}
	vars := map[string]any{"owner": ref.Owner, "name": ref.Name, "number": ref.Number}
	var a rawBypassAnswer
	if err := c.GraphQL(ctx, bypassQuery, vars, &a); err != nil {
		return BypassFacts{}, err
	}
	if a.Repository == nil || a.Repository.PullRequest == nil {
		return BypassFacts{}, incomplete("pullRequest is null")
	}
	pr := a.Repository.PullRequest
	if pr.HeadRefOid == nil {
		return BypassFacts{}, incomplete("headRefOid is null")
	}
	if pr.HeadRefName == nil {
		return BypassFacts{}, incomplete("headRefName is null")
	}
	if pr.IsCrossRepository == nil {
		return BypassFacts{}, incomplete("isCrossRepository is null")
	}
	out.Head, out.HeadRef, out.CrossRepo = *pr.HeadRefOid, *pr.HeadRefName, *pr.IsCrossRepository

	// The head commit, and every PR GitHub says has the same head commit: an
	// open 1 of them can block the merge on a review Pocket does not read.
	co := pr.Commits
	if co == nil || co.Nodes == nil || len(*co.Nodes) != 1 {
		return BypassFacts{}, incomplete("commits is null")
	}
	node := (*co.Nodes)[0]
	if node == nil || node.Commit == nil || node.Commit.Oid == nil {
		return BypassFacts{}, incomplete("the last commit is null")
	}
	if *node.Commit.Oid != out.Head {
		return BypassFacts{}, incomplete("the last commit is not the head")
	}
	aps := node.Commit.AssociatedPullRequests
	if aps == nil || aps.PageInfo == nil || aps.Nodes == nil {
		return BypassFacts{}, incomplete("associatedPullRequests is null")
	}
	if aps.PageInfo.HasNextPage == nil {
		return BypassFacts{}, incomplete("associatedPullRequests.pageInfo.hasNextPage is null")
	}
	if *aps.PageInfo.HasNextPage {
		return BypassFacts{}, incomplete("too many associated pull requests")
	}
	for _, a := range *aps.Nodes {
		if a == nil || a.Number == nil || a.State == nil || a.HeadRefOid == nil {
			return BypassFacts{}, incomplete("an associated pull request is null")
		}
		if *a.State == "OPEN" && *a.HeadRefOid == out.Head && *a.Number != ref.Number {
			out.SameHead = append(out.SameHead, *a.Number)
		}
	}
	// A null author is a PR GitHub attributes to nobody: no type to read.
	if pr.Author != nil {
		if pr.Author.Typename == nil {
			return BypassFacts{}, incomplete("author.__typename is null")
		}
		out.AuthorType = *pr.Author.Typename
	}

	// The review threads: every node, not truncated.
	th := pr.ReviewThreads
	if th == nil || th.PageInfo == nil || th.Nodes == nil {
		return BypassFacts{}, incomplete("reviewThreads is null")
	}
	if th.PageInfo.HasNextPage == nil {
		return BypassFacts{}, incomplete("reviewThreads.pageInfo.hasNextPage is null")
	}
	if *th.PageInfo.HasNextPage {
		return BypassFacts{}, incomplete("too many review threads")
	}
	for _, t := range *th.Nodes {
		if t == nil || t.IsResolved == nil {
			return BypassFacts{}, incomplete("a review thread is null")
		}
		if !*t.IsResolved {
			out.Unresolved++
		}
	}

	// The reviews: every node, not truncated.
	rv := pr.LatestOpinionatedReviews
	if rv == nil || rv.PageInfo == nil || rv.Nodes == nil {
		return BypassFacts{}, incomplete("latestOpinionatedReviews is null")
	}
	if rv.PageInfo.HasNextPage == nil {
		return BypassFacts{}, incomplete("latestOpinionatedReviews.pageInfo.hasNextPage is null")
	}
	if *rv.PageInfo.HasNextPage {
		return BypassFacts{}, incomplete("too many reviews")
	}
	seen := map[string]bool{}
	for _, v := range *rv.Nodes {
		if v == nil || v.State == nil || v.AuthorCanPushToRepository == nil ||
			v.Author == nil || v.Author.Login == nil || v.Commit == nil || v.Commit.Oid == nil {
			return BypassFacts{}, incomplete("a review is null")
		}
		// A stale approval, and an approval by someone who cannot push,
		// count for nothing.
		if *v.State != "APPROVED" || !*v.AuthorCanPushToRepository || *v.Commit.Oid != out.Head {
			continue
		}
		if !seen[*v.Author.Login] {
			seen[*v.Author.Login] = true
			out.Approvers = append(out.Approvers, *v.Author.Login)
		}
	}

	// A head that moved while reading proves nothing.
	if out.Head != f.HeadRefOid {
		return BypassFacts{}, incomplete("the head changed while reading")
	}

	// Who pushed last, when a rule needs the last push's approval. A PR from
	// another repository has no branch of this repository to read.
	if r.Proof.LastPush && !out.CrossRepo {
		pusher, err := c.lastPusher(ctx, ref, out.Head, out.HeadRef)
		if err != nil {
			return BypassFacts{}, err
		}
		out.LastPusher = pusher
	}

	// How far the base is ahead, when a rule needs an up-to-date branch. The
	// queue brings the branch up to date itself.
	if r.Proof.Strict && !queued(f, r) {
		if !isOID(r.baseTip) {
			return BypassFacts{}, incomplete("the base branch's commit is missing")
		}
		if !isOID(out.Head) {
			return BypassFacts{}, incomplete("the head commit is missing")
		}
		var cmp struct {
			BehindBy *int `json:"behind_by"`
		}
		path := "/repos/" + url.PathEscape(ref.Owner) + "/" + url.PathEscape(ref.Name) +
			"/compare/" + r.baseTip + "..." + out.Head
		if err := c.REST(ctx, path, &cmp); err != nil {
			return BypassFacts{}, err
		}
		if cmp.BehindBy == nil {
			return BypassFacts{}, incomplete("the compare names no behind_by")
		}
		out.Behind = *cmp.BehindBy
	}
	return out, nil
}

// lastPusher reads the last entry of the head branch's activity, and gives
// its actor when it is a push of head: "" when the branch's last event is
// another, and so proves nothing.
func (c *Client) lastPusher(ctx context.Context, ref Ref, head, headRef string) (string, error) {
	path := "/repos/" + url.PathEscape(ref.Owner) + "/" + url.PathEscape(ref.Name) +
		"/activity?ref=" + url.QueryEscape("refs/heads/"+headRef) + "&per_page=1"
	// Only an explicit list says the branch saw nothing: a null page proves
	// nothing.
	var page *[]activityEntry
	if err := c.REST(ctx, path, &page); err != nil {
		return "", err
	}
	if page == nil {
		return "", incomplete("the branch activity is null")
	}
	if len(*page) == 0 {
		return "", nil
	}
	e := (*page)[0]
	if e.Ref == nil || *e.Ref != "refs/heads/"+headRef {
		return "", nil
	}
	if e.After == nil || *e.After != head {
		return "", nil
	}
	switch {
	case e.ActivityType == nil:
		return "", nil
	case *e.ActivityType == "push", *e.ActivityType == "force_push", *e.ActivityType == "branch_creation":
	default:
		return "", nil
	}
	if e.Actor == nil || e.Actor.Login == nil {
		return "", nil
	}
	return *e.Actor.Login, nil
}

// isOID says s is a full commit sha: 40 hexadecimal characters.
func isOID(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
