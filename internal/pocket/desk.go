package pocket

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// The Desk is pi-pocket-team's inbox of what the team has in motion:
// threads (one per delegation), the decisions they ask of the owner, the
// PRs they open, plans, and the shift board. Its routes are owner-only.

// DeskPR is a pull request on a thread.
type DeskPR struct {
	Ref     string `json:"ref"`
	URL     string `json:"url,omitempty"`
	Title   string `json:"title,omitempty"`
	State   string `json:"state,omitempty"` // OPEN, MERGED, CLOSED, FROZEN, MISSING
	Next    string `json:"next,omitempty"`
	Via     string `json:"via,omitempty"`
	Guessed bool   `json:"guessed,omitempty"`
	Ready   bool   `json:"ready,omitempty"`
	Level   string `json:"level,omitempty"`
}

// DeskAsk is a decision a thread waits on the owner for.
type DeskAsk struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	At      Millis   `json:"at"`
	Options []string `json:"options,omitempty"`
	From    string   `json:"from,omitempty"`
	Gen     *int     `json:"gen,omitempty"`
}

// DeskAnswered is an ask the owner answered.
type DeskAnswered struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	By    string `json:"by"`
	At    Millis `json:"at"`
	Words string `json:"words"`
}

// DeskResolved records why an ask no longer waits on the owner.
type DeskResolved struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Why  string `json:"why"`
	By   string `json:"by"`
	At   Millis `json:"at"`
}

// DeskWatch is a dated check on a thread.
type DeskWatch struct {
	CheckOn   string `json:"checkOn"`
	CheckTime string `json:"checkTime,omitempty"`
	Check     string `json:"check"`
	SetBy     string `json:"setBy,omitempty"`
	Due       bool   `json:"due"`
	Result    *struct {
		Pass bool   `json:"pass"`
		Text string `json:"text"`
		By   string `json:"by"`
		At   Millis `json:"at"`
	} `json:"result,omitempty"`
	// On is the event that makes it due early ("pr:owner/name#12 merged").
	On string `json:"on,omitempty"`
	// OnFired says how that event happened, once it did.
	OnFired string `json:"onFired,omitempty"`
}

// DeskThread is one delegation, plan or found piece of work.
type DeskThread struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Origin      string         `json:"origin"` // delegate, seed, found, ask, plan
	Agents      []string       `json:"agents"`
	Repo        string         `json:"repo,omitempty"`
	State       string         `json:"state"` // starting, open, parked, closed
	Next        string         `json:"next"`  // brian, agent, odrade, reviewer, none
	Stale       bool           `json:"stale"`
	Open        []string       `json:"open"`
	PRs         []DeskPR       `json:"prs"`
	Asks        []DeskAsk      `json:"asks"`
	Answered    []DeskAnswered `json:"answered"`
	Resolved    []DeskResolved `json:"resolved"`
	LastEventAt Millis         `json:"lastEventAt"`
	Events      []struct {
		At   Millis `json:"at"`
		Text string `json:"text"`
	} `json:"events"`
	Supervisor int        `json:"supervisor,omitempty"`
	HasSession bool       `json:"hasSession"`
	Session    *int       `json:"session,omitempty"`
	Watch      *DeskWatch `json:"watch,omitempty"`
	Asleep     bool       `json:"asleep"`
	Closed     *struct {
		At      Millis `json:"at"`
		Why     string `json:"why"`
		By      string `json:"by"`
		Outcome string `json:"outcome,omitempty"`
	} `json:"closed,omitempty"`
}

// Group places a thread in the Desk's lists, as the web page does.
func (t *DeskThread) Group() string {
	switch {
	case t.State == "closed":
		if len(t.Asks) > 0 {
			return "need"
		}
		return "closed"
	case t.State == "parked":
		return "parked"
	case len(t.Asks) > 0:
		return "need"
	case t.Origin == "plan":
		return "plans"
	case t.Asleep:
		return "watching"
	case t.Origin == "found":
		return "found"
	case t.Next == "brian":
		return "need"
	case t.Stale:
		return "stale"
	case t.Next == "reviewer":
		return "review"
	}
	return "working"
}

// ReadyPR is a pull request labelled ready for the owner to merge.
type ReadyPR struct {
	Ref    string   `json:"ref"`
	Title  string   `json:"title,omitempty"`
	URL    string   `json:"url,omitempty"`
	Head   string   `json:"head,omitempty"`
	Checks string   `json:"checks,omitempty"`
	Level  string   `json:"level,omitempty"`
	Thread string   `json:"thread"`
	Agents []string `json:"agents"`
	At     Millis   `json:"at"`
}

// PlanRow is a plan in the Desk's list.
type PlanRow struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	State   string   `json:"state"`
	Lead    string   `json:"lead"`
	Asks    int      `json:"asks"`
	Waiting []string `json:"waiting"`
	Session int      `json:"session,omitempty"`
}

// DeskView is GET threads: the whole Desk.
type DeskView struct {
	Threads  []DeskThread         `json:"threads"`
	Seen     Millis               `json:"seen"`
	Issues   *DeskIssues          `json:"issues"`
	OtherPRs map[string][]OtherPR `json:"otherPrs"`
	Cap      struct {
		Counted int  `json:"counted"`
		Cap     int  `json:"cap"`
		Full    bool `json:"full"`
		Grants  int  `json:"grants"`
		Waiting int  `json:"waiting"`
	} `json:"cap"`
	Ready     []ReadyPR      `json:"ready"`
	Plans     []PlanRow      `json:"plans"`
	PlansDone int            `json:"plansDone"`
	Others    map[string]int `json:"others"`
	GitHub    *struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	} `json:"github"`
	Counts struct {
		Open     int `json:"open"`
		Brian    int `json:"brian"`
		Agent    int `json:"agent"`
		Odrade   int `json:"odrade"`
		Reviewer int `json:"reviewer"`
		Stale    int `json:"stale"`
		Parked   int `json:"parked"`
		Watching int `json:"watching"`
		Ready    int `json:"ready"`
	} `json:"counts"`
}

// Desk reads the whole Desk; ok is false when the server has no desk
// module (or the person may not use it).
func (c *Client) Desk(ctx context.Context) (view *DeskView, ok bool, err error) {
	var out DeskView
	err = c.Get(ctx, "/api/x/desk/threads", &out)
	if e, isAPI := err.(*APIError); isAPI && (e.Status == http.StatusNotFound || e.Status == http.StatusForbidden) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &out, true, nil
}

// DeskThreadDetail is GET threads/:id.
type DeskThreadDetail struct {
	Thread  DeskThread `json:"thread"`
	Reports []struct {
		At   Millis `json:"at"`
		From string `json:"from"`
		Text string `json:"text"`
	} `json:"reports"`
}

// DeskThread reads one thread with its reports.
func (c *Client) DeskThread(ctx context.Context, id string) (*DeskThreadDetail, error) {
	var out DeskThreadDetail
	err := c.Get(ctx, "/api/x/desk/threads/"+url.PathEscape(id), &out)
	return &out, err
}

// DeskAnswer answers an ask with words (an option's text, or the owner's own).
func (c *Client) DeskAnswer(ctx context.Context, ask, words string, gen *int) error {
	body := map[string]any{"words": words}
	if gen != nil {
		body["gen"] = *gen
	}
	return c.Post(ctx, "/api/x/desk/asks/"+url.PathEscape(ask)+"/answer", body, nil)
}

// DeskAct runs a thread action: keep, park, wake, unwatch (body {}), or
// drop ({reason}).
func (c *Client) DeskAct(ctx context.Context, thread, action string, body map[string]any) error {
	if body == nil {
		body = map[string]any{}
	}
	return c.Post(ctx, "/api/x/desk/threads/"+url.PathEscape(thread)+"/"+action, body, nil)
}

// MergeRecord is 1 result of an approve + merge attempt (ADR-0008), for
// the Desk's receipt route.
type MergeRecord struct {
	Op     string `json:"op"`     // the attempt's id, a lowercase UUID v4
	Ref    string `json:"ref"`    // owner/name#N, a link of the thread
	Head   string `json:"head"`   // the shown head, 40 lowercase hex
	Result string `json:"result"` // approved, merged, queued, refused, failed, unknown
	Method string `json:"method,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// RecordMerge posts r to the thread's merge receipts. recorded is false when
// the Desk already had the same receipt; a receipt of the same op and result
// with other fields is an error (409).
func (c *Client) RecordMerge(ctx context.Context, thread string, r MergeRecord) (recorded bool, err error) {
	body := struct {
		MergeRecord
		Via string `json:"via"`
	}{r, "pocket"}
	var out struct {
		Recorded bool `json:"recorded"`
	}
	err = c.Post(ctx, "/api/x/desk/threads/"+url.PathEscape(thread)+"/merges", body, &out)
	return err == nil && out.Recorded, err
}

// DeskGrant lets the supervisor start count more threads past the cap.
func (c *Client) DeskGrant(ctx context.Context, count int) error {
	return c.Post(ctx, "/api/x/desk/grant", map[string]any{"count": count}, nil)
}

// DeskSeen stamps the owner's last look at the Desk.
func (c *Client) DeskSeen(ctx context.Context) error {
	return c.Post(ctx, "/api/x/desk/seen", map[string]any{}, nil)
}

// MetricsView is the part of the dashboard's metrics Pocket shows.
type MetricsView struct {
	Completion struct {
		Rate        *float64 `json:"rate"`
		Success     int      `json:"success"`
		Completed   int      `json:"completed"`
		Verified    int      `json:"verified"`
		Abandoned   int      `json:"abandoned"`
		Failed      int      `json:"failed"`
		Denominator int      `json:"denominator"`
	} `json:"completion"`
	Velocity struct {
		PerWeek   *float64 `json:"perWeek"`
		Successes int      `json:"successes"`
	} `json:"velocity"`
	CycleTime struct {
		MedianMs *float64 `json:"medianMs"`
		Samples  int      `json:"samples"`
	} `json:"cycleTime"`
	Workload struct {
		Total    int `json:"total"`
		Starting int `json:"starting"`
		Open     int `json:"open"`
		Parked   int `json:"parked"`
	} `json:"workload"`
	PRs struct {
		Created        int `json:"created"`
		Merged         int `json:"merged"`
		ClosedUnmerged int `json:"closedUnmerged"`
		OpenNow        int `json:"openNow"`
		Tracked        int `json:"tracked"`
	} `json:"prs"`
	Usage struct {
		State       string   `json:"state"`
		Input       *float64 `json:"input"`
		Output      *float64 `json:"output"`
		CacheRead   *float64 `json:"cacheRead"`
		TotalTokens *float64 `json:"totalTokens"`
		Cost        *float64 `json:"cost"`
		Sessions    int      `json:"sessions"`
	} `json:"usage"`
	TimeToSolve struct {
		MedianMs *float64 `json:"medianMs"`
		P90Ms    *float64 `json:"p90Ms"`
		Count    int      `json:"count"`
	} `json:"timeToSolve"`
}

// Dashboard is the Desk's delivery numbers over a range.
type Dashboard struct {
	AsOf    Millis `json:"asOf"`
	Choices struct {
		Owners []string `json:"owners"`
		Repos  []string `json:"repos"`
	} `json:"choices"`
	Coverage struct {
		Status      string `json:"status"`
		BoundaryDay string `json:"boundaryDay"`
		Limitations []struct {
			Code string `json:"code"`
			Text string `json:"text"`
		} `json:"limitations"`
	} `json:"coverage"`
	Range struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"range"`
	Totals MetricsView `json:"totals"`
	Daily  []struct {
		Day       string `json:"day"`
		Successes int    `json:"successes"`
	} `json:"daily"`
	Rows struct {
		Projects []ProjectRow `json:"projects"`
		Owners   []struct {
			Owner   string      `json:"owner"`
			Metrics MetricsView `json:"metrics"`
		} `json:"owners"`
	} `json:"rows"`
	// Meter is the Desk's subscription meter (pi-pocket-team's `MeterView`);
	// nil when the server sends none.
	Meter *MeterView `json:"meter,omitempty"`
}

// MeterView is the Desk's subscription meter: 1 row for each provider, as
// the server reads and projects it. Optional fields are absent when unknown.
type MeterView struct {
	At        Millis          `json:"at"`
	Providers []MeterProvider `json:"providers"`
	Coverage  MeterCoverage   `json:"coverage"`
	Hold      *float64        `json:"hold,omitempty"`
}

// MeterCoverage says how much of the usage ledger the server has read.
type MeterCoverage struct {
	Since    *Millis `json:"since,omitempty"`
	Complete bool    `json:"complete"`
	Done     int     `json:"done"`
	Total    int     `json:"total"`
}

// MeterProvider is 1 provider's subscription usage and ledger. Line is the
// server's text, with its times already in the server's zone.
type MeterProvider struct {
	Provider     string        `json:"provider"`
	Label        string        `json:"label"`
	Line         string        `json:"line"`
	Off          string        `json:"off,omitempty"`
	LastAttempt  *MeterAttempt `json:"lastAttempt,omitempty"`
	ReadAt       *Millis       `json:"readAt,omitempty"`
	MainWindowID string        `json:"mainWindowId,omitempty"`
	Windows      []MeterWindow `json:"windows"`
	Grants       []MeterGrant  `json:"grants,omitempty"`
	GrantsNote   string        `json:"grantsNote,omitempty"`
	Ledger       MeterLedger   `json:"ledger"`
}

// MeterAttempt is the server's last try to read a provider's report.
type MeterAttempt struct {
	At    Millis `json:"at"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// MeterWindow is 1 usage window of a provider (a week, a model's week).
// State is "fresh", "stale" or "awaiting".
type MeterWindow struct {
	ID               string   `json:"id"`
	Label            string   `json:"label"`
	Scoped           bool     `json:"scoped"`
	State            string   `json:"state"`
	Percent          *float64 `json:"percent,omitempty"`
	ResetsAt         *Millis  `json:"resetsAt,omitempty"`
	Severity         string   `json:"severity,omitempty"`
	Active           bool     `json:"active"`
	BurnPerHour      *float64 `json:"burnPerHour,omitempty"`
	BurnSpanMinutes  *float64 `json:"burnSpanMinutes,omitempty"`
	HoursToFull      *float64 `json:"hoursToFull,omitempty"`
	FullBeforeReset  *bool    `json:"fullBeforeReset,omitempty"`
	ProjectedAtReset *float64 `json:"projectedAtReset,omitempty"`
	ResetSeenAt      *Millis  `json:"resetSeenAt,omitempty"`
}

// MeterGrant is a grant of extra resets that a provider gave.
type MeterGrant struct {
	Label      string  `json:"label"`
	Type       string  `json:"type,omitempty"`
	ResetsLeft float64 `json:"resetsLeft"`
	EndsAt     *Millis `json:"endsAt,omitempty"`
}

// MeterLedger is a provider's usage at the API's list price, in US dollars.
type MeterLedger struct {
	LastHour    float64          `json:"lastHour"`
	Today       float64          `json:"today"`
	Days7       float64          `json:"days7"`
	Partial     bool             `json:"partial"`
	Top         []MeterModelCost `json:"top"`
	UnknownCost *int             `json:"unknownCost,omitempty"`
}

// MeterModelCost is 1 model's list price in a ledger's top list.
type MeterModelCost struct {
	Model string  `json:"model"`
	Cost  float64 `json:"cost"`
}

// ProjectRow is one repository's numbers.
type ProjectRow struct {
	Repo    string      `json:"repo"`
	Owner   string      `json:"owner"`
	Metrics MetricsView `json:"metrics"`
}

// DeskDashboard reads the numbers from the UTC days from to to
// (YYYY-MM-DD; empty for the Desk's default range).
func (c *Client) DeskDashboard(ctx context.Context, from, to, owner, repo string) (*Dashboard, error) {
	q := url.Values{}
	if from != "" {
		q.Set("from", from)
	}
	if to != "" {
		q.Set("to", to)
	}
	if owner != "" {
		q.Set("owner", owner)
	}
	if repo != "" {
		q.Set("repo", repo)
	}
	path := "/api/x/desk/dashboard"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out Dashboard
	err := c.Get(ctx, path, &out)
	return &out, err
}

// BoardRow is one row of shift work on the goal board.
type BoardRow struct {
	ID        int    `json:"id"`
	Rev       int    `json:"rev"`
	State     string `json:"state"` // proposed, picked, waiting, starting, launched, failed, unknown, dropped
	Agent     string `json:"agent"`
	Repo      string `json:"repo,omitempty"`
	Title     string `json:"title"`
	Task      string `json:"task"`
	Objective string `json:"objective,omitempty"`
	StopWhen  string `json:"stopWhen"`
	Max       int    `json:"max"`
	Until     string `json:"until"`
	Worker    string `json:"worker,omitempty"`
	Start     string `json:"start,omitempty"`
	PRCheck   *struct {
		PR     int    `json:"pr,omitempty"`
		Repo   string `json:"repo,omitempty"`
		Checks string `json:"checks,omitempty"`
		Label  string `json:"label,omitempty"`
	} `json:"prCheck,omitempty"`
	Kind        string `json:"kind"`
	Note        string `json:"note,omitempty"`
	Order       int    `json:"order"`
	By          string `json:"by"`
	Thread      string `json:"thread,omitempty"`
	LaunchedAt  Millis `json:"launchedAt,omitempty"`
	ChangedAt   Millis `json:"changedAt"`
	Error       string `json:"error,omitempty"`
	Next        string `json:"next,omitempty"`
	ThreadState string `json:"threadState,omitempty"`
	ShiftEnded  bool   `json:"shiftEnded,omitempty"`
}

// Board is the goal board of one supervisor.
type Board struct {
	Supervisors []int          `json:"supervisors"`
	Supervisor  *int           `json:"supervisor"`
	Titles      map[string]any `json:"titles"`
	Rows        []BoardRow     `json:"rows"`
	Limits      *struct {
		Line      string `json:"line"`
		AtOnce    int    `json:"atOnce"`
		Running   int    `json:"running"`
		Cap       int    `json:"cap"`
		Counted   int    `json:"counted"`
		Reserve   int    `json:"reserve"`
		Rounds    int    `json:"rounds"`
		RoundsMax int    `json:"roundsMax"`
	} `json:"limits"`
}

// DeskBoard reads the board of supervisor (0 for the Desk's choice).
func (c *Client) DeskBoard(ctx context.Context, supervisor int) (*Board, error) {
	path := "/api/x/desk/board"
	if supervisor != 0 {
		path += "?supervisor=" + strconv.Itoa(supervisor)
	}
	var out Board
	err := c.Get(ctx, path, &out)
	return &out, err
}

// BoardBody is the exact operation payload before its retry key. It excludes
// display-only row facts, so a refresh cannot change an uncertain retry's key.
func BoardBody(op string, row *BoardRow, launch []BoardRow, fields map[string]any, thread string) map[string]any {
	body := map[string]any{"op": op}
	if row != nil {
		body["row"], body["rev"] = row.ID, row.Rev
	}
	if op == "pick" || op == "unpick" {
		if row != nil {
			launch = []BoardRow{*row}
		}
		delete(body, "row")
		delete(body, "rev")
	}
	if fields != nil {
		body["fields"] = fields
	}
	if thread != "" {
		body["thread"] = thread
	}
	if op == "launch" || op == "pick" || op == "unpick" {
		rows := make([]map[string]int, len(launch))
		for i, r := range launch {
			rows[i] = map[string]int{"row": r.ID, "rev": r.Rev}
		}
		body["rows"] = rows
	}
	return body
}

type BoardResult struct {
	Text string   `json:"text"`
	Pass []string `json:"pass"`
}

// BoardOp sends one revision-checked owner operation with its retry key.
func (c *Client) BoardOp(ctx context.Context, supervisor int, op, key string, row *BoardRow, launch []BoardRow, fields map[string]any, thread string) (*BoardResult, error) {
	body := BoardBody(op, row, launch, fields, thread)
	body["key"] = key
	var out BoardResult
	err := c.Post(ctx, "/api/x/desk/board/"+strconv.Itoa(supervisor), body, &out)
	if err != nil {
		return nil, fmt.Errorf("board %s: %w", op, err)
	}
	return &out, nil
}

type OtherPR struct {
	Ref    string `json:"ref"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Author string `json:"author"`
}

type DeskIssues struct {
	OK    bool          `json:"ok"`
	Off   bool          `json:"off"`
	Error string        `json:"error"`
	At    Millis        `json:"at"`
	Items []QueuedIssue `json:"items"`
}

type QueuedIssue struct {
	Ref        string   `json:"ref"`
	Title      string   `json:"title"`
	URL        string   `json:"url"`
	Urgent     bool     `json:"urgent"`
	Agent      string   `json:"agent"`
	Owners     []string `json:"owners"`
	Names      []string `json:"names"`
	By         string   `json:"by"`
	FixedBy    []string `json:"fixedBy"`
	Unverified bool     `json:"unverified"`
	Threads    []struct {
		ID   string `json:"id"`
		Word string `json:"word"`
	} `json:"threads"`
}

type CompletedPlan struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Lead    string `json:"lead"`
	Outcome string `json:"outcome"`
	EndedAt Millis `json:"endedAt"`
	Rounds  int    `json:"rounds"`
	Failed  int    `json:"failed"`
	Session int    `json:"session"`
	Tasks   *struct {
		Done  int `json:"done"`
		Total int `json:"total"`
	} `json:"tasks"`
}

func (c *Client) CompletedPlans(ctx context.Context) ([]CompletedPlan, error) {
	var out struct {
		Plans []CompletedPlan `json:"plans"`
	}
	err := c.Get(ctx, "/api/x/desk/plans/completed", &out)
	return out.Plans, err
}

// DeskFile reads one exported Markdown file. The server validates the path,
// containment, file type and size; Pocket sends no local filesystem request.
func (c *Client) DeskFile(ctx context.Context, path string) (string, error) {
	var out struct {
		Text string `json:"text"`
	}
	err := c.Get(ctx, "/api/x/desk/file?path="+url.QueryEscape(path), &out)
	return out.Text, err
}

type DashboardQuery struct {
	From, To, Owner, Repo string
	Unattributed          bool
}

type DashboardRecords struct {
	Threads []struct {
		ID    string   `json:"id"`
		Repo  string   `json:"repo"`
		State string   `json:"state"`
		Kind  string   `json:"kind"`
		Gone  bool     `json:"gone"`
		PRs   []string `json:"prs"`
	} `json:"threads"`
	PRs []struct {
		Ref     string   `json:"ref"`
		State   string   `json:"state"`
		Events  []string `json:"events"`
		Threads []string `json:"threads"`
	} `json:"prs"`
	Truncated struct {
		Threads int `json:"threads"`
		PRs     int `json:"prs"`
	} `json:"truncated"`
}

func (c *Client) DashboardRecords(ctx context.Context, q DashboardQuery) (*DashboardRecords, error) {
	values := url.Values{"from": {q.From}, "to": {q.To}}
	if q.Owner != "" {
		values.Set("owner", q.Owner)
	}
	if q.Repo != "" {
		values.Set("repo", q.Repo)
	}
	if q.Unattributed {
		values.Set("unattributed", "1")
	}
	var out DashboardRecords
	err := c.Get(ctx, "/api/x/desk/dashboard/records?"+values.Encode(), &out)
	return &out, err
}
