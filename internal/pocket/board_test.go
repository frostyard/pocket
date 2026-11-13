package pocket

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type boardTransport func(*http.Request) (*http.Response, error)

func (f boardTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBoardOperationsMatchOwnerRoutePayloads(t *testing.T) {
	for _, op := range []string{"pick", "unpick", "launch", "edit", "drop", "reopen", "resolve"} {
		t.Run(op, func(t *testing.T) {
			c, _ := NewClient("https://example.org", "test")
			row := BoardRow{ID: 2, Rev: 3}
			c.HTTP = &http.Client{Transport: boardTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/api/x/desk/board/7" {
					t.Errorf("path %s", r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["op"] != op || body["key"] != "123-test-key" {
					t.Errorf("body %v", body)
				}
				if op == "pick" || op == "unpick" || op == "launch" {
					rows, ok := body["rows"].([]any)
					if !ok || len(rows) != 1 {
						t.Errorf("missing rows: %v", body)
					} else if got := rows[0].(map[string]any); got["row"] != float64(2) || got["rev"] != float64(3) {
						t.Errorf("wrong row %v", got)
					}
				} else if body["row"] != float64(2) || body["rev"] != float64(3) {
					t.Errorf("missing revision %v", body)
				}
				if op == "edit" && body["fields"].(map[string]any)["max"] != float64(8) {
					t.Errorf("missing fields %v", body)
				}
				if op == "resolve" && body["thread"] != "T12" {
					t.Errorf("missing thread %v", body)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
			})}
			var fields map[string]any
			thread := ""
			if op == "edit" {
				fields = map[string]any{"max": 8}
			}
			if op == "resolve" {
				thread = "T12"
			}
			var launch []BoardRow
			if op == "launch" {
				launch = []BoardRow{row}
			}
			if _, err := c.BoardOp(context.Background(), 7, op, "123-test-key", &row, launch, fields, thread); err != nil {
				t.Fatal(err)
			}
		})
	}
}
