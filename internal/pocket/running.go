package pocket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

type RunningTask struct {
	ID             int    `json:"id"`
	Kind           string `json:"kind"`
	Label          string `json:"label"`
	Status         string `json:"status"`
	ConversationID int    `json:"conversationId"`
	Subagent       string `json:"subagent"`
	Since          Millis `json:"since"`
	Stalled        Millis `json:"stalled"`
}

func (t RunningTask) Stoppable() bool {
	return t.Kind == "pocket.shell" || t.Kind == "pocket.background" || t.Kind == "pocket.extension-command" || (t.Kind == "pi.generation" && t.Subagent != "")
}

type RunningSession struct {
	ID        int           `json:"id"`
	Title     string        `json:"title"`
	Busy      bool          `json:"busy"`
	Approvals int           `json:"approvals"`
	Tasks     []RunningTask `json:"tasks"`
}
type Schedule struct {
	ID           string          `json:"id"`
	Session      int             `json:"session"`
	SessionTitle string          `json:"sessionTitle"`
	Text         string          `json:"text"`
	Next         Millis          `json:"next"`
	Repeat       string          `json:"repeat"`
	Every        json.RawMessage `json:"every"`
	Zone         string          `json:"zone"`
	Version      *string         `json:"version"`
	Paused       Millis          `json:"paused"`
	Skipped      int             `json:"skipped"`
	SkippedAt    Millis          `json:"skippedAt"`
	Held         string          `json:"held"`
}
type RunningBoard struct {
	Sessions  []RunningSession `json:"sessions"`
	Schedules []Schedule       `json:"schedules"`
	Now       Millis           `json:"now"`
}

func (c *Client) Running(ctx context.Context) (*RunningBoard, error) {
	var out RunningBoard
	err := c.Get(ctx, "/api/running/board", &out)
	return &out, err
}

// StopRunning names only the kinds that the web Running now board can stop.
func (c *Client) StopRunning(ctx context.Context, t RunningTask) error {
	route := map[string]string{"pocket.shell": "shell", "pocket.background": "background", "pocket.extension-command": "extension-commands"}[t.Kind]
	if route != "" {
		return c.Post(ctx, conv(t.ConversationID, route+"/"+strconv.Itoa(t.ID)+"/stop"), map[string]any{}, nil)
	}
	if t.Kind == "pi.generation" && t.Subagent != "" {
		return c.Abort(ctx, t.ConversationID)
	}
	return fmt.Errorf("this task cannot be stopped individually")
}

type BackgroundTask struct {
	ID        string `json:"id"`
	TaskID    int    `json:"taskId"`
	Command   string `json:"command"`
	Status    string `json:"status"`
	Outcome   string `json:"outcome"`
	Code      *int   `json:"code"`
	StartedAt Millis `json:"startedAt"`
	EndedAt   Millis `json:"endedAt"`
}

func (t BackgroundTask) Live() bool { return t.Status == "running" || t.Status == "starting" }

type BackgroundOutput struct {
	Text    string         `json:"text"`
	From    int64          `json:"from"`
	To      int64          `json:"to"`
	Skipped int64          `json:"skipped"`
	Item    BackgroundTask `json:"item"`
}

func (c *Client) Background(ctx context.Context, id int) ([]BackgroundTask, error) {
	var out struct {
		Tasks []BackgroundTask `json:"tasks"`
	}
	err := c.Get(ctx, conv(id, "background"), &out)
	return out.Tasks, err
}
func (c *Client) BackgroundOutput(ctx context.Context, id int, task string) (*BackgroundOutput, error) {
	var out BackgroundOutput
	err := c.Get(ctx, conv(id, "background/"+url.PathEscape(task)+"/output?chars=20000"), &out)
	return &out, err
}
func (c *Client) Schedule(ctx context.Context, id int, when, zone string) error {
	return c.Post(ctx, conv(id, "schedules"), map[string]any{"when": when, "zone": zone}, nil)
}
func (c *Client) CancelSchedule(ctx context.Context, id int, sid string) error {
	return c.Post(ctx, conv(id, "schedules/"+url.PathEscape(sid)+"/cancel"), map[string]any{}, nil)
}
func (c *Client) PauseSchedule(ctx context.Context, id int, sid string, paused bool) error {
	return c.Post(ctx, conv(id, "schedules/"+url.PathEscape(sid)+"/pause"), map[string]any{"paused": paused}, nil)
}
func (c *Client) EditSchedule(ctx context.Context, id int, s Schedule) error {
	if s.Version == nil {
		return fmt.Errorf("this server does not support versioned schedule editing")
	}
	body := map[string]any{"text": s.Text, "next": s.Next, "zone": s.Zone, "version": *s.Version}
	if len(s.Every) > 0 && string(s.Every) != "null" {
		body["every"] = s.Every
	}
	return c.Do(ctx, http.MethodPut, conv(id, "schedules/"+url.PathEscape(s.ID)), body, nil)
}
