package github

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// prQuery is the PR query (PocketPR). It is the text of
// testdata/pr-query.graphql, which a test holds it to.
const prQuery = `query PocketPR($owner: String!, $name: String!, $number: Int!, $cursor: String) {
  viewer {
    login
  }
  repository(owner: $owner, name: $name) {
    viewerDefaultMergeMethod
    mergeCommitAllowed
    squashMergeAllowed
    rebaseMergeAllowed
    pullRequest(number: $number) {
      id
      number
      title
      state
      isDraft
      viewerDidAuthor
      baseRefName
      baseRefOid
      headRefOid
      mergeable
      mergeStateStatus
      reviewDecision
      isMergeQueueEnabled
      isInMergeQueue
      viewerLatestReview {
        state
        commit {
          oid
        }
      }
      labels(first: 100) {
        pageInfo {
          hasNextPage
        }
        nodes {
          name
        }
      }
      latestOpinionatedReviews(first: 100) {
        pageInfo {
          hasNextPage
        }
        nodes {
          state
          author {
            login
          }
          commit {
            oid
          }
        }
      }
      commits(last: 1) {
        nodes {
          commit {
            oid
            statusCheckRollup {
              contexts(first: 100, after: $cursor) {
                pageInfo {
                  hasNextPage
                  endCursor
                }
                nodes {
                  __typename
                  ... on CheckRun {
                    name
                    status
                    conclusion
                    checkSuite {
                      app {
                        databaseId
                      }
                    }
                  }
                  ... on StatusContext {
                    context
                    state
                  }
                }
              }
            }
          }
        }
      }
      mergedBy {
        login
      }
      mergeCommit {
        oid
      }
      mergedAt
      mergeQueueEntry {
        enqueuer {
          login
        }
        headCommit {
          oid
        }
      }
    }
  }
}
`

// maxCheckPages caps the check contexts Pocket reads (100 a page).
const maxCheckPages = 5

// PRFacts is what the PR query says of 1 pull request, checked against the
// Nulls rule of ADR-0008: a field that may be null is a pointer or says so.
type PRFacts struct {
	Viewer string

	DefaultMergeMethod string // MERGE, SQUASH or REBASE
	MergeCommitAllowed bool
	SquashMergeAllowed bool
	RebaseMergeAllowed bool

	ID                  string
	Number              int
	Title               string
	State               string
	IsDraft             bool
	ViewerDidAuthor     bool
	BaseRefName         string
	BaseRefOid          string
	HeadRefOid          string
	Mergeable           string
	MergeStateStatus    string
	ReviewDecision      string // "" when GitHub says null: no review is required
	IsMergeQueueEnabled bool
	IsInMergeQueue      bool

	ViewerLatestReview *Review // nil: the viewer has not reviewed
	Labels             []string
	Reviews            []Review // latestOpinionatedReviews

	CommitOid string // the oid of commits(last: 1)
	// Rollup is false when statusCheckRollup is null: no checks on the head.
	Rollup bool
	Checks []CheckContext

	MergedBy    string // "" when null
	MergeCommit string // "" when null
	MergedAt    string // "" when null
	QueueEntry  *QueueEntry
}

// Review is a review's state, author and commit.
type Review struct {
	State, Author, Commit string
}

// CheckContext is 1 CheckRun or StatusContext of the head.
type CheckContext struct {
	Type       string // CheckRun or StatusContext
	Name       string // a CheckRun's name or a StatusContext's context
	Status     string // CheckRun
	Conclusion string // CheckRun; "" until it completes
	App        bool   // CheckRun: checkSuite.app is not null
	AppID      int64  // CheckRun: checkSuite.app.databaseId
	State      string // StatusContext
}

// QueueEntry is the PR's merge queue entry.
type QueueEntry struct {
	Enqueuer string
	Head     string
}

// hasLabel says the PR has label name.
func (f PRFacts) hasLabel(name string) bool {
	for _, l := range f.Labels {
		if l == name {
			return true
		}
	}
	return false
}

// The raw answer: every field is a pointer, so a null (or a missing field)
// shows, and facts() refuses one where the Nulls rule allows none.
type (
	rawPRAnswer struct {
		Viewer *struct {
			Login *string `json:"login"`
		} `json:"viewer"`
		Repository *struct {
			ViewerDefaultMergeMethod *string `json:"viewerDefaultMergeMethod"`
			MergeCommitAllowed       *bool   `json:"mergeCommitAllowed"`
			SquashMergeAllowed       *bool   `json:"squashMergeAllowed"`
			RebaseMergeAllowed       *bool   `json:"rebaseMergeAllowed"`
			PullRequest              *rawPR  `json:"pullRequest"`
		} `json:"repository"`
	}
	rawPageInfo struct {
		HasNextPage *bool   `json:"hasNextPage"`
		EndCursor   *string `json:"endCursor"`
	}
	rawOid struct {
		Oid *string `json:"oid"`
	}
	rawLogin struct {
		Login *string `json:"login"`
	}
	rawPR struct {
		ID                  *string `json:"id"`
		Number              *int    `json:"number"`
		Title               *string `json:"title"`
		State               *string `json:"state"`
		IsDraft             *bool   `json:"isDraft"`
		ViewerDidAuthor     *bool   `json:"viewerDidAuthor"`
		BaseRefName         *string `json:"baseRefName"`
		BaseRefOid          *string `json:"baseRefOid"`
		HeadRefOid          *string `json:"headRefOid"`
		Mergeable           *string `json:"mergeable"`
		MergeStateStatus    *string `json:"mergeStateStatus"`
		ReviewDecision      *string `json:"reviewDecision"`
		IsMergeQueueEnabled *bool   `json:"isMergeQueueEnabled"`
		IsInMergeQueue      *bool   `json:"isInMergeQueue"`
		ViewerLatestReview  *struct {
			State  *string `json:"state"`
			Commit *rawOid `json:"commit"`
		} `json:"viewerLatestReview"`
		Labels *struct {
			PageInfo *rawPageInfo `json:"pageInfo"`
			Nodes    *[]*struct {
				Name *string `json:"name"`
			} `json:"nodes"`
		} `json:"labels"`
		LatestOpinionatedReviews *struct {
			PageInfo *rawPageInfo `json:"pageInfo"`
			Nodes    *[]*struct {
				State  *string   `json:"state"`
				Author *rawLogin `json:"author"`
				Commit *rawOid   `json:"commit"`
			} `json:"nodes"`
		} `json:"latestOpinionatedReviews"`
		Commits *struct {
			Nodes *[]*struct {
				Commit *struct {
					Oid               *string `json:"oid"`
					StatusCheckRollup *struct {
						Contexts *struct {
							PageInfo *rawPageInfo     `json:"pageInfo"`
							Nodes    *[]*rawCheckNode `json:"nodes"`
						} `json:"contexts"`
					} `json:"statusCheckRollup"`
				} `json:"commit"`
			} `json:"nodes"`
		} `json:"commits"`
		MergedBy        *rawLogin `json:"mergedBy"`
		MergeCommit     *rawOid   `json:"mergeCommit"`
		MergedAt        *string   `json:"mergedAt"`
		MergeQueueEntry *struct {
			Enqueuer   *rawLogin `json:"enqueuer"`
			HeadCommit *rawOid   `json:"headCommit"`
		} `json:"mergeQueueEntry"`
	}
	rawCheckNode struct {
		Typename   *string `json:"__typename"`
		Name       *string `json:"name"`
		Status     *string `json:"status"`
		Conclusion *string `json:"conclusion"`
		CheckSuite *struct {
			App *struct {
				DatabaseID *int64 `json:"databaseId"`
			} `json:"app"`
		} `json:"checkSuite"`
		Context *string `json:"context"`
		State   *string `json:"state"`
	}
)

func incomplete(what string) error { return fmt.Errorf("%w: %s", ErrIncomplete, what) }

// need fails when any field it is given is null.
type need struct{ missing string }

func (n *need) str(p *string, name string) string {
	if p == nil {
		n.miss(name)
		return ""
	}
	return *p
}

func (n *need) flag(p *bool, name string) bool {
	if p == nil {
		n.miss(name)
		return false
	}
	return *p
}

func (n *need) miss(name string) {
	if n.missing == "" {
		n.missing = name
	}
}

func (n *need) err() error {
	if n.missing == "" {
		return nil
	}
	return incomplete(n.missing + " is null")
}

// facts checks a page of the answer against the Nulls rule, and gives the
// facts, the check contexts of the page and the next cursor ("" for none).
func (a *rawPRAnswer) facts() (PRFacts, string, error) {
	var f PRFacts
	if a.Viewer == nil {
		return f, "", incomplete("viewer is null")
	}
	if a.Repository == nil {
		return f, "", incomplete("repository is null")
	}
	pr := a.Repository.PullRequest
	if pr == nil {
		return f, "", incomplete("pullRequest is null")
	}
	n := &need{}
	f.Viewer = n.str(a.Viewer.Login, "viewer.login")
	repo := a.Repository
	f.DefaultMergeMethod = n.str(repo.ViewerDefaultMergeMethod, "viewerDefaultMergeMethod")
	f.MergeCommitAllowed = n.flag(repo.MergeCommitAllowed, "mergeCommitAllowed")
	f.SquashMergeAllowed = n.flag(repo.SquashMergeAllowed, "squashMergeAllowed")
	f.RebaseMergeAllowed = n.flag(repo.RebaseMergeAllowed, "rebaseMergeAllowed")
	f.ID = n.str(pr.ID, "id")
	if pr.Number == nil {
		n.miss("number")
	} else {
		f.Number = *pr.Number
	}
	f.Title = n.str(pr.Title, "title")
	f.State = n.str(pr.State, "state")
	f.IsDraft = n.flag(pr.IsDraft, "isDraft")
	f.ViewerDidAuthor = n.flag(pr.ViewerDidAuthor, "viewerDidAuthor")
	f.BaseRefName = n.str(pr.BaseRefName, "baseRefName")
	f.BaseRefOid = n.str(pr.BaseRefOid, "baseRefOid")
	f.HeadRefOid = n.str(pr.HeadRefOid, "headRefOid")
	f.Mergeable = n.str(pr.Mergeable, "mergeable")
	f.MergeStateStatus = n.str(pr.MergeStateStatus, "mergeStateStatus")
	if pr.ReviewDecision != nil {
		f.ReviewDecision = *pr.ReviewDecision
	}
	f.IsMergeQueueEnabled = n.flag(pr.IsMergeQueueEnabled, "isMergeQueueEnabled")
	f.IsInMergeQueue = n.flag(pr.IsInMergeQueue, "isInMergeQueue")
	// A nullable object that is there needs its fields.
	if v := pr.ViewerLatestReview; v != nil {
		r := &Review{Author: f.Viewer, State: n.str(v.State, "viewerLatestReview.state")}
		if v.Commit == nil {
			n.miss("viewerLatestReview.commit")
		} else {
			r.Commit = n.str(v.Commit.Oid, "viewerLatestReview.commit.oid")
		}
		f.ViewerLatestReview = r
	}
	if pr.MergedBy != nil {
		f.MergedBy = n.str(pr.MergedBy.Login, "mergedBy.login")
	}
	if pr.MergeCommit != nil {
		f.MergeCommit = n.str(pr.MergeCommit.Oid, "mergeCommit.oid")
	}
	if pr.MergedAt != nil {
		f.MergedAt = *pr.MergedAt
	}
	if e := pr.MergeQueueEntry; e != nil {
		q := &QueueEntry{}
		if e.Enqueuer == nil {
			n.miss("mergeQueueEntry.enqueuer")
		} else {
			q.Enqueuer = n.str(e.Enqueuer.Login, "mergeQueueEntry.enqueuer.login")
		}
		if e.HeadCommit == nil {
			n.miss("mergeQueueEntry.headCommit")
		} else {
			q.Head = n.str(e.HeadCommit.Oid, "mergeQueueEntry.headCommit.oid")
		}
		f.QueueEntry = q
	}
	if err := n.err(); err != nil {
		return f, "", err
	}

	// Labels: every node, not truncated.
	if pr.Labels == nil || pr.Labels.PageInfo == nil || pr.Labels.Nodes == nil {
		return f, "", incomplete("labels is null")
	}
	if pr.Labels.PageInfo.HasNextPage == nil {
		return f, "", incomplete("labels.pageInfo.hasNextPage is null")
	}
	if *pr.Labels.PageInfo.HasNextPage {
		return f, "", incomplete("too many labels")
	}
	for _, l := range *pr.Labels.Nodes {
		if l == nil || l.Name == nil {
			return f, "", incomplete("a label is null")
		}
		f.Labels = append(f.Labels, *l.Name)
	}

	// Reviews: every node, not truncated.
	rv := pr.LatestOpinionatedReviews
	if rv == nil || rv.PageInfo == nil || rv.Nodes == nil {
		return f, "", incomplete("latestOpinionatedReviews is null")
	}
	if rv.PageInfo.HasNextPage == nil {
		return f, "", incomplete("latestOpinionatedReviews.pageInfo.hasNextPage is null")
	}
	if *rv.PageInfo.HasNextPage {
		return f, "", incomplete("too many reviews")
	}
	for _, r := range *rv.Nodes {
		if r == nil || r.State == nil || r.Author == nil || r.Author.Login == nil || r.Commit == nil || r.Commit.Oid == nil {
			return f, "", incomplete("a review is null")
		}
		f.Reviews = append(f.Reviews, Review{State: *r.State, Author: *r.Author.Login, Commit: *r.Commit.Oid})
	}

	// The last commit, and its checks.
	if pr.Commits == nil || pr.Commits.Nodes == nil || len(*pr.Commits.Nodes) != 1 {
		return f, "", incomplete("commits is null")
	}
	node := (*pr.Commits.Nodes)[0]
	if node == nil || node.Commit == nil || node.Commit.Oid == nil {
		return f, "", incomplete("the last commit is null")
	}
	f.CommitOid = *node.Commit.Oid
	cursor := ""
	if roll := node.Commit.StatusCheckRollup; roll != nil {
		f.Rollup = true
		if roll.Contexts == nil || roll.Contexts.PageInfo == nil || roll.Contexts.Nodes == nil {
			return f, "", incomplete("the check contexts are null")
		}
		if roll.Contexts.PageInfo.HasNextPage == nil {
			return f, "", incomplete("contexts.pageInfo.hasNextPage is null")
		}
		if *roll.Contexts.PageInfo.HasNextPage {
			if roll.Contexts.PageInfo.EndCursor == nil || *roll.Contexts.PageInfo.EndCursor == "" {
				return f, "", incomplete("contexts.pageInfo.endCursor is null")
			}
			cursor = *roll.Contexts.PageInfo.EndCursor
		}
		for _, c := range *roll.Contexts.Nodes {
			cc, err := c.check()
			if err != nil {
				return f, "", err
			}
			f.Checks = append(f.Checks, cc)
		}
	}

	// Every miss is an error, whatever field recorded it.
	return f, cursor, n.err()
}

func (c *rawCheckNode) check() (CheckContext, error) {
	if c == nil || c.Typename == nil {
		return CheckContext{}, incomplete("a check is null")
	}
	switch *c.Typename {
	case "CheckRun":
		if c.Name == nil || c.Status == nil || c.CheckSuite == nil {
			return CheckContext{}, incomplete("a check run is null")
		}
		cc := CheckContext{Type: "CheckRun", Name: *c.Name, Status: *c.Status}
		// A check that has not completed has no conclusion yet.
		if c.Conclusion != nil {
			cc.Conclusion = *c.Conclusion
		} else if cc.Status == "COMPLETED" {
			return CheckContext{}, incomplete("the conclusion of check " + cc.Name + " is null")
		}
		if app := c.CheckSuite.App; app != nil {
			// An app that is there needs its ID.
			if app.DatabaseID == nil {
				return CheckContext{}, incomplete("the app of check " + cc.Name + " has no databaseId")
			}
			cc.App, cc.AppID = true, *app.DatabaseID
		}
		return cc, nil
	case "StatusContext":
		if c.Context == nil || c.State == nil {
			return CheckContext{}, incomplete("a status is null")
		}
		return CheckContext{Type: "StatusContext", Name: *c.Context, State: *c.State}, nil
	}
	return CheckContext{}, incomplete("a check of an unknown kind")
}

// pr reads the PR query, every page of its check contexts (5 at most), all
// at the head of the first page.
func (c *Client) pr(ctx context.Context, ref Ref) (PRFacts, error) {
	var first PRFacts
	cursor := ""
	for page := 1; ; page++ {
		vars := map[string]any{"owner": ref.Owner, "name": ref.Name, "number": ref.Number, "cursor": nil}
		if cursor != "" {
			vars["cursor"] = cursor
		}
		var a rawPRAnswer
		if err := c.GraphQL(ctx, prQuery, vars, &a); err != nil {
			return PRFacts{}, refusedChecks(err)
		}
		f, next, err := a.facts()
		if err != nil {
			return PRFacts{}, err
		}
		if page == 1 {
			first = f
		} else {
			if f.HeadRefOid != first.HeadRefOid || f.CommitOid != first.CommitOid {
				return PRFacts{}, incomplete("the head changed while reading the checks")
			}
			if !f.Rollup {
				return PRFacts{}, incomplete("the checks changed while reading them")
			}
			first.Checks = append(first.Checks, f.Checks...)
		}
		if next == "" {
			return first, nil
		}
		if page == maxCheckPages {
			return PRFacts{}, incomplete("too many checks")
		}
		cursor = next
	}
}

// shortSHA is the 7-character form of oid.
func shortSHA(oid string) string {
	if len(oid) > 7 {
		return oid[:7]
	}
	return oid
}

// checksRefused is a token GitHub refuses the PR's check runs to. It is
// ErrChecksUnreadable, and ErrIncomplete too, with GitHub's own words: a
// refusal, so again no mutation is sent.
type checksRefused struct{ err error }

func (e *checksRefused) Error() string {
	return ErrChecksUnreadable.Error() + ", so Pocket cannot check the required checks: " + e.err.Error()
}

func (e *checksRefused) Is(target error) bool { return target == ErrChecksUnreadable }
func (e *checksRefused) Unwrap() error        { return e.err }

// refusedChecks names a GraphQL error that is GitHub refusing the check
// runs of the PR's rollup, under ADR-0009
// (docs/adr/0009-read-check-runs-only-with-a-token-that-can.md). Any other
// error comes back as it is.
func refusedChecks(err error) error {
	var g *gqlErrors
	if !errors.As(err, &g) {
		return err
	}
	for _, e := range g.list {
		if !refusesCheckRuns(e) {
			continue
		}
		return &checksRefused{err: err}
	}
	return err
}

// refusesCheckRuns says GitHub refused a node of statusCheckRollup: a
// FORBIDDEN entry, or a message that names no access, at a path there.
func refusesCheckRuns(e gqlError) bool {
	inRollup := false
	for _, seg := range e.segments() {
		if seg == "statusCheckRollup" {
			inRollup = true
		}
	}
	if !inRollup {
		return false
	}
	return e.Type == "FORBIDDEN" || strings.Contains(e.Message, "not accessible")
}
