package pocket

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Me returns what hello carries, without a stream: the cheapest check
// that the token works.
func (c *Client) Me(ctx context.Context) (*Hello, error) {
	var h Hello
	if err := c.Get(ctx, "/api/me", &h); err != nil {
		return nil, err
	}
	return &h, nil
}

func conv(id int, rest string) string { return "/api/c/" + strconv.Itoa(id) + "/" + rest }

// Submit sends a message to session id. requestID makes a retry harmless.
// mode is "", "steer", "followUp" or "interrupt".
func (c *Client) Submit(ctx context.Context, id int, text, requestID, mode string) (*SubmitResult, error) {
	body := map[string]any{"text": text, "requestId": requestID}
	if mode != "" {
		body["mode"] = mode
	}
	var out SubmitResult
	if err := c.Post(ctx, conv(id, "submit"), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Abort stops the run of session id; its queue is held.
func (c *Client) Abort(ctx context.Context, id int) error {
	return c.Post(ctx, conv(id, "abort"), map[string]any{}, nil)
}

// Answer allows or denies an approval, or answers a question with values.
func (c *Client) Answer(ctx context.Context, approvalID string, allow bool, values map[string]string) error {
	body := map[string]any{"allow": allow}
	if values != nil {
		body["values"] = values
	}
	return c.Post(ctx, "/api/approvals/"+url.PathEscape(approvalID), body, nil)
}

// ModelRef names a model.
type ModelRef struct {
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// Configure changes a session's model, thinking level or folder; empty
// values are left as they are.
func (c *Client) Configure(ctx context.Context, id int, model *ModelRef, thinking, cwd string) error {
	body := map[string]any{}
	if model != nil {
		body["model"] = model
	}
	if thinking != "" {
		body["thinkingLevel"] = thinking
	}
	if cwd != "" {
		body["cwd"] = cwd
	}
	return c.Post(ctx, conv(id, "configure"), body, nil)
}

// NewSession makes a session in cwd ("" for the server's default) and
// returns its ID.
func (c *Client) NewSession(ctx context.Context, cwd, title string, worktree bool) (int, error) {
	body := map[string]any{}
	if cwd != "" {
		body["cwd"] = cwd
	}
	if title != "" {
		body["title"] = title
	}
	if worktree {
		body["worktree"] = true
	}
	var out struct {
		ID int `json:"id"`
	}
	if err := c.Post(ctx, "/api/sessions", body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// UpdateSession renames a session (title non-nil) or archives it.
func (c *Client) UpdateSession(ctx context.Context, id int, title *string, archived *bool) error {
	body := map[string]any{}
	if title != nil {
		body["title"] = *title
	}
	if archived != nil {
		body["archived"] = *archived
	}
	return c.Post(ctx, "/api/sessions/"+strconv.Itoa(id), body, nil)
}

// Prompt is a prompt template or skill the composer completes after /.
type Prompt struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argumentHint,omitempty"`
	Skill        bool   `json:"skill,omitempty"`
}

// Prompts lists session id's templates and skills.
func (c *Client) Prompts(ctx context.Context, id int) ([]Prompt, error) {
	var out []Prompt
	err := c.Get(ctx, conv(id, "prompts"), &out)
	return out, err
}

// Visibility tells the server whether this window shows: hidden, it lets
// notifications through for the session it shows.
func (c *Client) Visibility(ctx context.Context, tab string, visible bool) error {
	return c.Post(ctx, "/api/visibility", map[string]any{"tab": tab, "visible": visible}, nil)
}

// Compact compacts session id's context.
func (c *Client) Compact(ctx context.Context, id int) error {
	return c.Post(ctx, conv(id, "compact"), map[string]any{}, nil)
}

// Plan turns plan mode on or off.
func (c *Client) Plan(ctx context.Context, id int, on bool) error {
	return c.Post(ctx, conv(id, "plan"), map[string]any{"on": on}, nil)
}

// SpendReport is /api/spend.
type SpendReport struct {
	Total    *float64 `json:"total,omitempty"`
	Sessions []struct {
		ID    int     `json:"id"`
		Title string  `json:"title"`
		Spent float64 `json:"spent"`
	} `json:"sessions"`
}

// Spend returns what the sessions spent.
func (c *Client) Spend(ctx context.Context) (*SpendReport, error) {
	var out SpendReport
	err := c.Get(ctx, "/api/spend", &out)
	return &out, err
}

// Persona is a named agent of pi-pocket-team.
type Persona struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Role         string            `json:"role"`
	Model        string            `json:"model,omitempty"`
	Instructions string            `json:"instructions,omitempty"`
	Repositories map[string]string `json:"repositories,omitempty"`
	Workspace    string            `json:"workspace,omitempty"`
	Home         string            `json:"home,omitempty"`
}

// TeamAgents lists the team's named agents; ok is false when the server
// has no team module (or the person may not use it).
func (c *Client) TeamAgents(ctx context.Context) (agents []Persona, ok bool, err error) {
	var out struct {
		Agents []Persona `json:"agents"`
	}
	err = c.Get(ctx, "/api/x/team/agents", &out)
	if e, isAPI := err.(*APIError); isAPI && (e.Status == http.StatusNotFound || e.Status == http.StatusForbidden) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return out.Agents, true, nil
}

// Supervisor is the team's front door: the supervisor's session.
type Supervisor struct {
	Found          bool   `json:"found"`
	ConversationID int    `json:"conversationId"`
	Title          string `json:"title"`
	Name           string `json:"name"`
	Asks           int    `json:"asks"`
	Reason         string `json:"reason"`
	Text           string `json:"text"`
}

// TeamSupervisor finds the supervisor's session.
func (c *Client) TeamSupervisor(ctx context.Context) (*Supervisor, error) {
	var out Supervisor
	err := c.Get(ctx, "/api/x/team/supervisor", &out)
	return &out, err
}

// TeamIdentity is who session id is, for the team.
type TeamIdentity struct {
	Identity *Persona `json:"identity"`
	Implicit bool     `json:"implicit,omitempty"`
	Linked   []struct {
		ConversationID int    `json:"conversationId"`
		Address        string `json:"address"`
		Title          string `json:"title,omitempty"`
		Cwd            string `json:"cwd,omitempty"`
		Busy           bool   `json:"busy,omitempty"`
		Archived       bool   `json:"archived,omitempty"`
	} `json:"linked"`
}

// TeamSession returns session id's identity and linked sessions.
func (c *Client) TeamSession(ctx context.Context, id int) (*TeamIdentity, error) {
	var out TeamIdentity
	err := c.Get(ctx, "/api/x/team/session/"+strconv.Itoa(id), &out)
	return &out, err
}

// TeamSetIdentity makes session id be agent ("" clears it).
func (c *Client) TeamSetIdentity(ctx context.Context, id int, agent string) error {
	var a any
	if agent != "" {
		a = agent
	}
	return c.Post(ctx, "/api/x/team/session/"+strconv.Itoa(id)+"/identity", map[string]any{"agent": a}, nil)
}

// TeamStart starts a linked session as agent from session source, and
// returns the new session's ID. key makes a retry harmless.
func (c *Client) TeamStart(ctx context.Context, source int, agent, message, key, repository string, preferred bool) (int, error) {
	body := map[string]any{"agent": agent, "message": message, "key": key, "usePreferredModel": preferred}
	if repository != "" {
		body["repository"] = repository
	}
	var out struct {
		Session int    `json:"session"`
		Address string `json:"address"`
	}
	if err := c.Post(ctx, "/api/x/team/session/"+strconv.Itoa(source)+"/start", body, &out); err != nil {
		return 0, fmt.Errorf("starting %s: %w", agent, err)
	}
	return out.Session, nil
}

// Context loads up to rows transcript rows of session id before entry
// before, oldest first, with the entries between them.
func (c *Client) Context(ctx context.Context, id, before, rows int) ([]Entry, error) {
	var out []Entry
	err := c.Get(ctx, conv(id, "context?before="+strconv.Itoa(before)+"&rows="+strconv.Itoa(rows)), &out)
	return out, err
}

// PeekLine is one recent step of a session, as its peek tile shows it.
type PeekLine struct {
	Kind    string         `json:"kind"` // user, text, tool, shell, note, error, presented, event
	Text    string         `json:"text,omitempty"`
	From    string         `json:"from,omitempty"`
	Name    string         `json:"name,omitempty"`
	Args    map[string]any `json:"args,omitempty"`
	Status  string         `json:"status,omitempty"`
	Command string         `json:"command,omitempty"`
	Label   string         `json:"label,omitempty"`
	Summary string         `json:"summary,omitempty"`
}

// Line says the step in a line.
func (l PeekLine) Line() string {
	switch l.Kind {
	case "tool":
		subject := ""
		for _, k := range []string{"command", "path", "file_path", "pattern", "query", "url", "name", "task"} {
			if v, ok := l.Args[k].(string); ok && v != "" {
				subject = v
				break
			}
		}
		if subject == "" {
			return l.Name
		}
		return l.Name + " · " + subject
	case "shell":
		return "$ " + l.Command
	case "presented":
		if l.Summary != "" {
			return l.Label + ": " + l.Summary
		}
		return l.Label
	case "user":
		if l.From != "" {
			return l.From + ": " + l.Text
		}
	}
	return l.Text
}

// Peek is a live summary of a session: its last steps, the subagents at
// work in it, and the calls waiting there.
type Peek struct {
	ConversationID int        `json:"conversationId"`
	Lines          []PeekLine `json:"lines"`
	Agents         []struct {
		Name           string `json:"name"`
		ConversationID int    `json:"conversationId"`
		Asked          string `json:"asked,omitempty"`
	} `json:"agents"`
	Approvals []struct {
		ID             string `json:"id"`
		ConversationID int    `json:"conversationId"`
		Tool           string `json:"tool"`
		Subject        string `json:"subject"`
		Reason         string `json:"reason"`
		Form           bool   `json:"form,omitempty"`
	} `json:"approvals"`
}

// Peeks asks for peek events of up to 20 sessions on an open connection;
// an empty list stops them.
func (c *Client) Peeks(ctx context.Context, connection string, ids []int, seq int) error {
	if ids == nil {
		ids = []int{}
	}
	return c.Post(ctx, "/api/peeks", map[string]any{"connection": connection, "ids": ids, "seq": seq}, nil)
}
