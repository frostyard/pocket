package pocket

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFileRoutesAndDrift(t *testing.T) {
	c, _ := NewClient("https://example.org", "test")
	calls := map[string]int{}
	c.HTTP = &http.Client{Transport: sessionToolTransport(func(r *http.Request) (*http.Response, error) {
		route := strings.TrimPrefix(r.URL.Path, "/api/c/7/")
		calls[route]++
		if r.Header.Get("Authorization") != "Bearer test" || r.Header.Get("X-Pocket") != "1" || r.Header.Get("Cookie") != "" {
			t.Error("file route auth changed")
		}
		if route == "changes/revert" {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if r.Method != "POST" || body["path"] != "a & b.txt" {
				t.Errorf("revert %s %v", r.Method, body)
			}
		} else if r.Method != "GET" {
			t.Error(r.Method)
		}
		if route == "changes/diff" || route == "view" || route == "file" || route == "download" {
			if r.URL.Query().Get("path") != "a & b.txt" {
				t.Error(r.URL)
			}
		}
		if route == "files" && r.URL.Query().Get("since") != "v&1" {
			t.Error(r.URL)
		}
		body := map[string]string{"changes": `{"files":[{"path":"a & b.txt","added":"drift","kind":"modified"}],"more":2}`, "files": `{"version":"v&1","same":true}`, "view": `{"kind":"text","text":"hello","size":"drift"}`, "changes/diff": "@@ -1 +1 @@\n-before\n+after\n", "file": "image", "download": "raw\x00bytes"}[route]
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	ctx := context.Background()
	changes, err := c.Changes(ctx, 7)
	if err != nil || len(changes.Files) != 1 || changes.Files[0].Kind != "modified" {
		t.Fatalf("changes drift %+v %v", changes, err)
	}
	files, err := c.Files(ctx, 7, "v&1")
	if err != nil || !files.Same {
		t.Fatal(files, err)
	}
	view, err := c.ViewFile(ctx, 7, "a & b.txt")
	if err != nil || view.Text != "hello" {
		t.Fatal(view, err)
	}
	diff, err := c.ChangeDiff(ctx, 7, "a & b.txt")
	if err != nil || !strings.Contains(diff, "+after") {
		t.Fatal(diff, err)
	}
	if _, err = c.FileImage(ctx, 7, "a & b.txt"); err != nil {
		t.Fatal(err)
	}
	data, err := c.Download(ctx, 7, "a & b.txt")
	if err != nil || string(data) != "raw\x00bytes" {
		t.Fatal(data, err)
	}
	if err = c.RevertChange(ctx, 7, "a & b.txt"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 7 {
		t.Fatal(calls)
	}
}
func TestFileReadErrorsAndBounds(t *testing.T) {
	for _, status := range []int{200, 401, 403, 409} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c, _ := NewClient("https://example.org", "test")
			c.HTTP = &http.Client{Transport: sessionToolTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"error":"denied"}`))}, nil
			})}
			data, err := c.readFileBytes(context.Background(), "/api/c/7/download?path=a", 4)
			if err == nil || data != nil {
				t.Fatal("failed or oversized response became file")
			}
			if status == 401 && !errors.Is(err, ErrSignedOut) {
				t.Fatal(err)
			}
			if status == 403 || status == 409 {
				var api *APIError
				if !errors.As(err, &api) || api.Status != status || api.Message != "denied" {
					t.Fatal(err)
				}
			}
		})
	}
}
