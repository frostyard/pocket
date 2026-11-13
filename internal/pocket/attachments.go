package pocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const MaxUpload = 50 << 20

type Attachment struct {
	Path string `json:"path"`
	Name string `json:"name"`
	MIME string `json:"mime"`
	Size int64  `json:"size"`
}

type MessageDraft struct {
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments"`
}

type HeldMessage struct {
	Key  string `json:"key"`
	Mode string `json:"mode"`
	By   string `json:"by"`
	Text string `json:"text"`
}

// Upload sends raw file bytes, using the same bearer and redirect policy as JSON routes.
func (c *Client) Upload(ctx context.Context, id int, name, mime string, data []byte) (*Attachment, error) {
	if len(data) > MaxUpload {
		return nil, fmt.Errorf("files can be up to 50 MB")
	}
	path := conv(id, "upload?name="+url.QueryEscape(name))
	req, err := c.request(ctx, http.MethodPost, path, nil)
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Type", mime)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrSignedOut
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		return nil, &APIError{Status: resp.StatusCode, Message: e.Error}
	}
	var out Attachment
	if err := Decode(body, &out); err != nil {
		var drift *json.UnmarshalTypeError
		if !errors.As(err, &drift) {
			return nil, err
		}
		if c.Drift != nil {
			c.Drift(err)
		}
	}
	if out.Path == "" {
		return nil, fmt.Errorf("upload returned no file reference")
	}
	return &out, nil
}

func (c *Client) SubmitAttachments(ctx context.Context, id int, text, requestID, mode string, files []Attachment) (*SubmitResult, error) {
	body := map[string]any{"text": text, "requestId": requestID, "attachments": files}
	if mode != "" {
		body["mode"] = mode
	}
	var out SubmitResult
	err := c.Post(ctx, conv(id, "submit"), body, &out)
	return &out, err
}

func (c *Client) Withdraw(ctx context.Context, id, submission int) (string, error) {
	var out struct {
		Result string `json:"result"`
	}
	err := c.Post(ctx, conv(id, "withdraw"), map[string]any{"submissionId": submission}, &out)
	return out.Result, err
}
func (c *Client) EditQueued(ctx context.Context, id, submission int) (*MessageDraft, error) {
	var out MessageDraft
	err := c.Post(ctx, conv(id, "edit-queued"), map[string]any{"submissionId": submission}, &out)
	return &out, err
}
func (c *Client) Held(ctx context.Context, id int, action, key string) (*MessageDraft, error) {
	if action != "send" && action != "drop" && action != "edit" {
		return nil, fmt.Errorf("unknown held-message action")
	}
	var out MessageDraft
	err := c.Post(ctx, conv(id, "held"), map[string]any{"action": action, "key": key}, &out)
	return &out, err
}
