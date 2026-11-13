package pocket

import (
	"context"
	"errors"
	"net/http"
	"strconv"
)

// ShiftGoal is night-shift's goalView, including the server's readable deadline.
type ShiftGoal struct {
	Status        string `json:"status"`
	ReasonWord    string `json:"reasonWord"`
	Round         int    `json:"round"`
	MaxRounds     int    `json:"maxRounds"`
	Waiting       bool   `json:"waiting"`
	StartLabel    string `json:"startLabel"`
	DeadlineLabel string `json:"deadlineLabel"`
	Objective     string `json:"objective"`
	Stop          string `json:"stop"`
	Check         string `json:"check"`
	Worker        string `json:"worker"`
}

// ShiftGoal reads the session's goal. An absent module or goal is not an error.
func (c *Client) ShiftGoal(ctx context.Context, id int) (*ShiftGoal, error) {
	var out struct {
		Goal *ShiftGoal `json:"goal"`
	}
	err := c.Get(ctx, "/api/x/night-shift/goal/"+strconv.Itoa(id), &out)
	var api *APIError
	if errors.As(err, &api) && (api.Status == http.StatusNotFound || api.Status == http.StatusForbidden) {
		return nil, nil
	}
	return out.Goal, err
}

// TeamPinIdentity keeps a folder-derived identity only if it still matches.
func (c *Client) TeamPinIdentity(ctx context.Context, id int, agent string) error {
	return c.Post(ctx, "/api/x/team/session/"+strconv.Itoa(id)+"/pin", map[string]any{"agent": agent}, nil)
}
