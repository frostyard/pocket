package pocket

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// Lesson is a lesson a named agent keeps (pi-pocket-team's agent memory):
// Pi proposes, the owner approves.
type Lesson struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Pending   bool   `json:"pending"`
	ByPi      bool   `json:"byPi"`
	ByYou     bool   `json:"byYou"`
	CreatedAt Millis `json:"createdAt"`
	UpdatedAt Millis `json:"updatedAt,omitempty"`
	// From is the session that proposed it.
	From int `json:"from,omitempty"`
	// Proposal is a change waiting on an approved lesson: new text, or
	// forgetting it.
	Proposal *struct {
		Text   *string `json:"text,omitempty"`
		Forget bool    `json:"forget,omitempty"`
		ByPi   bool    `json:"byPi"`
	} `json:"proposal,omitempty"`
}

// Waiting reports whether the lesson waits for the owner: a new one, or a
// change to an approved one.
func (l *Lesson) Waiting() bool { return l.Pending || l.Proposal != nil }

// Expected is what the owner saw of a waiting lesson: the server applies
// an approval only to that (team-web's expectedOf).
func (l *Lesson) Expected() map[string]any {
	if !l.Pending && l.Proposal != nil && l.Proposal.Forget {
		return map[string]any{"forget": true}
	}
	if !l.Pending && l.Proposal != nil && l.Proposal.Text != nil {
		return map[string]any{"text": *l.Proposal.Text}
	}
	return map[string]any{"text": l.Text}
}

// Memory is every agent's lessons, waiting ones first.
type Memory struct {
	Agents  map[string][]Lesson `json:"agents"`
	Pending int                 `json:"pending"`
	Limits  struct {
		Approved int `json:"approved"`
		Pending  int `json:"pending"`
		Text     int `json:"text"`
	} `json:"limits"`
}

// TeamMemory reads every agent's lessons.
func (c *Client) TeamMemory(ctx context.Context) (*Memory, error) {
	var out Memory
	err := c.Get(ctx, "/api/x/team/memory", &out)
	return &out, err
}

// LessonAct approves, discards, edits (text) or forgets one of agent's
// lessons.
func (c *Client) LessonAct(ctx context.Context, agent string, l *Lesson, action, text string) error {
	body := map[string]any{"action": action}
	if action == "edit" {
		body["text"] = text
	}
	if action == "approve" || action == "discard" {
		body["expected"] = l.Expected()
	}
	return c.Post(ctx, "/api/x/team/memory/"+url.PathEscape(agent)+"/"+url.PathEscape(l.ID), body, nil)
}

// LessonAdd gives agent a lesson of the owner's, which counts at once.
func (c *Client) LessonAdd(ctx context.Context, agent, text string) error {
	return c.Post(ctx, "/api/x/team/memory/"+url.PathEscape(agent), map[string]any{"text": text}, nil)
}

// WikiPage is a page of the homewiki module's wiki.
type WikiPage struct {
	Path     string `json:"path"`
	Title    string `json:"title"`
	Order    int    `json:"order"`
	Section  string `json:"section"`
	Updated  string `json:"updated,omitempty"`
	Body     string `json:"body,omitempty"`
	Modified Millis `json:"modified,omitempty"`
}

// Wiki is the wiki's pages and the checkout they come from.
type Wiki struct {
	Pages []WikiPage `json:"pages"`
	Head  struct {
		Commit string `json:"commit,omitempty"`
		Date   string `json:"date,omitempty"`
		Dirty  bool   `json:"dirty,omitempty"`
	} `json:"head"`
}

// WikiPages lists the wiki; ok is false when the server has no homewiki.
func (c *Client) WikiPages(ctx context.Context) (w *Wiki, ok bool, err error) {
	var out Wiki
	err = c.Get(ctx, "/api/x/homewiki/pages", &out)
	if e, isAPI := err.(*APIError); isAPI && (e.Status == http.StatusNotFound || e.Status == http.StatusForbidden) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &out, true, nil
}

// WikiRead reads one page of the wiki.
func (c *Client) WikiRead(ctx context.Context, path string) (*WikiPage, error) {
	var out WikiPage
	err := c.Get(ctx, "/api/x/homewiki/page?path="+url.QueryEscape(path), &out)
	return &out, err
}

// Artifact is an artifact a session made: an HTML page, a Markdown
// document or an SVG picture.
type Artifact struct {
	Session      int    `json:"session"`
	SessionTitle string `json:"sessionTitle"`
	ID           string `json:"id"`
	Title        string `json:"title"`
	Type         string `json:"type"` // html, markdown, svg
	Kind         string `json:"kind,omitempty"`
	Group        string `json:"group,omitempty"`
	Current      int    `json:"current"`
	Versions     int    `json:"versions"`
	Size         int    `json:"size"`
	UpdatedAt    Millis `json:"updatedAt"`
}

// Artifacts lists the artifacts of every session the person can see.
func (c *Client) Artifacts(ctx context.Context) ([]Artifact, error) {
	var out []Artifact
	err := c.Get(ctx, "/api/artifacts", &out)
	return out, err
}

// ArtifactURL is where the server shows a version of an artifact, as its
// own sandboxed page.
func (c *Client) ArtifactURL(a *Artifact, version int) string {
	if version == 0 {
		version = a.Current
	}
	return c.url("/a/" + strconv.Itoa(a.Session) + "/" + url.PathEscape(a.ID) + "/" + strconv.Itoa(version))
}

// ArtifactBody reads what the server serves of an artifact: an SVG's own
// source, or the page made of HTML or Markdown.
func (c *Client) ArtifactBody(ctx context.Context, a *Artifact) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.ArtifactURL(a, 0), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Auth)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the artifact answered %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}
