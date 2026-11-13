package pocket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Fork continues from the latest finished reply, without changing the source.
func (c *Client) Fork(ctx context.Context, id int) (int, error) {
	var out struct {
		ID int `json:"id"`
	}
	err := c.Post(ctx, conv(id, "fork"), map[string]any{}, &out)
	return out.ID, err
}

func (c *Client) ResetContext(ctx context.Context, id int) error {
	return c.Post(ctx, conv(id, "reset"), map[string]any{}, nil)
}

// Export reads the server's complete Markdown transcript, with the same auth
// and error behavior as JSON routes. A too-large export fails, never truncates.
func (c *Client) Export(ctx context.Context, id int) ([]byte, error) {
	req, err := c.request(ctx, http.MethodGet, conv(id, "export"), nil)
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
	data, err := io.ReadAll(io.LimitReader(resp.Body, (64<<20)+1))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		return nil, &APIError{Status: resp.StatusCode, Message: e.Error}
	}
	if len(data) > 64<<20 {
		return nil, fmt.Errorf("export exceeds 64 MB")
	}
	return data, nil
}
