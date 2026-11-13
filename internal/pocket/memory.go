package pocket

import (
	"context"
	"net/url"
)

// CoreLesson is Pi Pocket's own lesson, separate from a named agent's memory.
type CoreLesson struct {
	ID          string `json:"id"`
	Text        string `json:"text"`
	Scope       string `json:"scope"`
	ProjectName string `json:"projectName"`
	Pending     bool   `json:"pending"`
	CreatedAt   Millis `json:"createdAt"`
	Proposal    *struct {
		Text   *string `json:"text"`
		Forget bool    `json:"forget"`
	} `json:"proposal"`
	From *struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	} `json:"from"`
}

func (l *CoreLesson) Expected() map[string]any {
	if !l.Pending && l.Proposal != nil {
		if l.Proposal.Forget {
			return map[string]any{"forget": true}
		}
		if l.Proposal.Text != nil {
			return map[string]any{"text": *l.Proposal.Text}
		}
	}
	return map[string]any{"text": l.Text}
}

type CoreMemory struct {
	PendingAll bool         `json:"pendingAll"`
	Lessons    []CoreLesson `json:"lessons"`
}

// PendingMemory uses the owner's private PiP session. Current servers refuse
// cross-project review from an ordinary session, even for the owner.
func (c *Client) PendingMemory(ctx context.Context, pip int) (*CoreMemory, error) {
	var out CoreMemory
	err := c.Get(ctx, conv(pip, "memory")+"?pending=all", &out)
	return &out, err
}

// ReviewMemory approves exactly the proposal shown, or discards it.
func (c *Client) ReviewMemory(ctx context.Context, pip int, lesson *CoreLesson, action string) error {
	body := map[string]any{}
	if action == "approve" {
		body = lesson.Expected()
	}
	return c.Post(ctx, conv(pip, "memory/"+url.PathEscape(lesson.ID)+"/"+action)+"?pending=all", body, nil)
}
