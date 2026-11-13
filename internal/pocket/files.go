package pocket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type ChangedFile struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Added   *int   `json:"added"`
	Removed *int   `json:"removed"`
	ByPi    bool   `json:"byPi"`
	Version string `json:"version"`
}
type Changes struct {
	Repo *struct {
		Root   string `json:"root"`
		Branch string `json:"branch"`
		Head   string `json:"head"`
	} `json:"repo"`
	Files  []ChangedFile `json:"files"`
	More   int           `json:"more"`
	PiOnly []struct {
		Path    string `json:"path"`
		EntryID int    `json:"entryId"`
	} `json:"piOnly"`
}
type FileList struct {
	Files     []string `json:"files"`
	Version   string   `json:"version"`
	Same      bool     `json:"same"`
	Truncated bool     `json:"truncated"`
}
type FileView struct {
	Path      string `json:"path"`
	Display   string `json:"display"`
	Kind      string `json:"kind"`
	Size      int64  `json:"size"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
	Entries   []struct {
		Name string `json:"name"`
		Dir  bool   `json:"dir"`
	} `json:"entries"`
}

func (c *Client) Changes(ctx context.Context, id int) (*Changes, error) {
	var out Changes
	err := c.Get(ctx, conv(id, "changes"), &out)
	return &out, err
}
func (c *Client) ChangeDiff(ctx context.Context, id int, path string) (string, error) {
	data, err := c.readFileBytes(ctx, conv(id, "changes/diff?path="+url.QueryEscape(path)), 2<<20)
	return string(data), err
}
func (c *Client) RevertChange(ctx context.Context, id int, path string) error {
	return c.Post(ctx, conv(id, "changes/revert"), map[string]any{"path": path}, nil)
}
func (c *Client) Files(ctx context.Context, id int, since string) (*FileList, error) {
	var out FileList
	err := c.Get(ctx, conv(id, "files?since="+url.QueryEscape(since)), &out)
	return &out, err
}
func (c *Client) ViewFile(ctx context.Context, id int, path string) (*FileView, error) {
	var out FileView
	err := c.Get(ctx, conv(id, "view?path="+url.QueryEscape(path)), &out)
	return &out, err
}
func (c *Client) FileImage(ctx context.Context, id int, path string) ([]byte, error) {
	return c.readFileBytes(ctx, conv(id, "file?path="+url.QueryEscape(path)), 25<<20)
}
func (c *Client) Download(ctx context.Context, id int, path string) ([]byte, error) {
	return c.readFileBytes(ctx, conv(id, "download?path="+url.QueryEscape(path)), 64<<20)
}

// Binary and text routes use the same authenticated request and redirect policy
// as JSON. A bounded response fails instead of silently saving a truncated file.
func (c *Client) readFileBytes(ctx context.Context, path string, limit int64) ([]byte, error) {
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrSignedOut
	}
	if resp.StatusCode/100 != 2 {
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if readErr != nil {
			return nil, readErr
		}
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		return nil, &APIError{Status: resp.StatusCode, Message: e.Error}
	}
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("file response exceeds %d MB", limit>>20)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file response exceeds %d MB", limit>>20)
	}
	return data, nil
}
