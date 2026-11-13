package pocket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
)

type BrowserState struct {
	Available bool   `json:"available"`
	Open      bool   `json:"open"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Loading   bool   `json:"loading"`
	Problem   string `json:"problem"`
}

func (c *Client) Browser(ctx context.Context, id int) (*BrowserState, error) {
	var out BrowserState
	err := c.Get(ctx, conv(id, "browser"), &out)
	return &out, err
}

type BrowserFrame struct {
	Data   []byte
	Seq    int
	Closed bool
}

// BrowserFrame waits for a JPEG newer than after. 204 is a normal idle or
// closed browser; it is not an image decode failure.
func (c *Client) BrowserFrame(ctx context.Context, id, after int) (BrowserFrame, error) {
	var out BrowserFrame
	req, err := c.request(ctx, http.MethodGet, conv(id, "browser/frame")+"?after="+strconv.Itoa(after), nil)
	if err != nil {
		return out, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return out, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return out, ErrSignedOut
	}
	if resp.StatusCode == http.StatusNoContent {
		out.Closed = resp.Header.Get("X-Closed") == "1"
		return out, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return out, err
	}
	if resp.StatusCode != http.StatusOK {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &problem)
		return out, &APIError{Status: resp.StatusCode, Message: problem.Error}
	}
	typ, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if typ != "image/jpeg" {
		return out, fmt.Errorf("browser frame is not a JPEG")
	}
	if len(data) > 8<<20 {
		return out, fmt.Errorf("browser frame exceeds 8 MB")
	}
	out.Data = data
	out.Seq, _ = strconv.Atoi(resp.Header.Get("X-Seq"))
	return out, nil
}
