package pocket

import (
	"context"
	"fmt"
	"net/url"
)

type PlanAsk struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	Options  []string `json:"options"`
	Open     bool     `json:"open"`
	Written  bool     `json:"written"`
	Gen      int      `json:"gen"`
	Criteria []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	} `json:"criteria"`
	Marks  map[string]string `json:"marks"`
	Answer *struct {
		Words string `json:"words"`
		By    string `json:"by"`
		At    Millis `json:"at"`
	} `json:"answer"`
	Why *struct {
		Text string `json:"text"`
	} `json:"why"`
	Resolved *struct {
		Why string `json:"why"`
	} `json:"resolved"`
	Findings []PlanFinding `json:"findings"`
}
type PlanFinding struct {
	ID      string `json:"id"`
	Agent   string `json:"agent"`
	Stance  string `json:"stance"`
	Summary string `json:"summary"`
}
type PlanComment struct {
	ID      string `json:"id"`
	Section string `json:"section"`
	Text    string `json:"text"`
	By      string `json:"by"`
	State   string `json:"state"`
	Rev     string `json:"rev"`
}
type PlanTask struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Agent      string   `json:"agent"`
	Repo       string   `json:"repo"`
	State      string   `json:"state"`
	Acceptance []string `json:"acceptance"`
	Deps       []string `json:"deps"`
}
type PlanGraph struct {
	Version int        `json:"version"`
	State   string     `json:"state"`
	Rev     string     `json:"rev"`
	Tasks   []PlanTask `json:"tasks"`
}
type PlanSection struct {
	ID       string        `json:"id"`
	Heading  string        `json:"heading"`
	Text     string        `json:"text"`
	Desk     bool          `json:"desk"`
	Asks     []PlanAsk     `json:"asks"`
	Comments []PlanComment `json:"comments"`
}
type PlanView struct {
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	State         string        `json:"state"`
	Rev           string        `json:"rev"`
	Lead          string        `json:"lead"`
	Idea          string        `json:"idea"`
	File          string        `json:"file"`
	Session       int           `json:"session"`
	Preamble      string        `json:"preamble"`
	Waiting       []string      `json:"waiting"`
	Settled       bool          `json:"settled"`
	Sections      []PlanSection `json:"sections"`
	Graph         *PlanGraph    `json:"graph"`
	ApprovedGraph *PlanGraph    `json:"approvedGraph"`
	CanApprove    bool          `json:"canApprove"`
	ApproveOff    string        `json:"approveOff"`
	CanRevise     bool          `json:"canRevise"`
	Progress      struct {
		Steps []struct {
			ID     string `json:"id"`
			Label  string `json:"label"`
			State  string `json:"state"`
			Detail string `json:"detail"`
		} `json:"steps"`
	} `json:"progress"`
	Consults []struct {
		ID       string `json:"id"`
		Agent    string `json:"agent"`
		State    string `json:"state"`
		Question string `json:"question"`
		Age      Millis `json:"age"`
		CanStop  bool   `json:"canStop"`
	} `json:"consults"`
	Findings []PlanFinding `json:"findings"`
}
type PlanRoster struct {
	Leads []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"leads"`
	Supervisors []int `json:"supervisors"`
	StartPlan   bool  `json:"startPlan"`
	Plans       struct {
		Open int `json:"open"`
		Max  int `json:"max"`
	} `json:"plans"`
}

func (c *Client) DeskPlan(ctx context.Context, id string) (*PlanView, error) {
	var out PlanView
	err := c.Get(ctx, "/api/x/desk/plans/"+url.PathEscape(id), &out)
	return &out, err
}
func (c *Client) PlanAction(ctx context.Context, id, action string, body map[string]any) error {
	switch action {
	case "approve", "changes", "comment", "revise":
	default:
		return fmt.Errorf("unknown plan action")
	}
	return c.Post(ctx, "/api/x/desk/plans/"+url.PathEscape(id)+"/"+action, body, nil)
}
func (c *Client) PlanAskAction(ctx context.Context, id, action string, body map[string]any) error {
	switch action {
	case "answer", "marks", "reopen":
	default:
		return fmt.Errorf("unknown plan ask action")
	}
	return c.Post(ctx, "/api/x/desk/asks/"+url.PathEscape(id)+"/"+action, body, nil)
}
func (c *Client) DismissPlanComment(ctx context.Context, id, comment string) error {
	return c.Post(ctx, "/api/x/desk/plans/"+url.PathEscape(id)+"/comments/"+url.PathEscape(comment)+"/dismiss", map[string]any{}, nil)
}
func (c *Client) StopPlanConsult(ctx context.Context, id, consult string) error {
	return c.Post(ctx, "/api/x/desk/plans/"+url.PathEscape(id)+"/consults/"+url.PathEscape(consult)+"/stop", map[string]any{}, nil)
}
func (c *Client) PlanRoster(ctx context.Context) (*PlanRoster, error) {
	var out PlanRoster
	err := c.Get(ctx, "/api/x/desk/roster", &out)
	return &out, err
}
func (c *Client) NewPlan(ctx context.Context, session int, body map[string]any) (string, error) {
	var out struct {
		Plan string `json:"plan"`
	}
	err := c.Post(ctx, fmt.Sprintf("/api/x/desk/plans/new/%d", session), body, &out)
	return out.Plan, err
}
