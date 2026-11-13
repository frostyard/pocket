package pocket

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestUploadRawBodyAndHeaders(t *testing.T) {
	c, _ := NewClient("https://example.org", "test")
	c.HTTP = &http.Client{Transport: sessionToolTransport(func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.Path != "/api/c/7/upload" || r.URL.Query().Get("name") != "a & b.txt" || string(data) != "raw bytes" || r.Header.Get("Content-Type") != "text/plain" || r.Header.Get("Authorization") != "Bearer test" || r.Header.Get("X-Pocket") != "1" || r.Header.Get("Cookie") != "" {
			t.Errorf("wrong upload %s %s %q", r.Method, r.URL, data)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"path":"/uploads/file","name":"file","size":"drift"}`)), Header: make(http.Header)}, nil
	})}
	file, err := c.Upload(context.Background(), 7, "a & b.txt", "text/plain", []byte("raw bytes"))
	if err != nil || file.Path != "/uploads/file" {
		t.Fatalf("upload drift lost file: %+v %v", file, err)
	}
}

func TestQueueProtocol(t *testing.T) {
	for _, action := range []string{"withdraw", "edit-queued", "send", "drop", "edit"} {
		t.Run(action, func(t *testing.T) {
			c, _ := NewClient("https://example.org", "test")
			c.HTTP = &http.Client{Transport: sessionToolTransport(func(r *http.Request) (*http.Response, error) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				route := action
				if action == "withdraw" || action == "edit-queued" {
					if body["submissionId"] != float64(42) {
						t.Errorf("body %v", body)
					}
				} else {
					route = "held"
					if body["action"] != action || body["key"] != "h1" {
						t.Errorf("body %v", body)
					}
				}
				if r.Method != "POST" || r.URL.Path != "/api/c/7/"+route {
					t.Errorf("route %s", r.URL)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"result":"aborted","text":"draft","attachments":[{"path":"/uploads/file"}]}`)), Header: make(http.Header)}, nil
			})}
			var err error
			switch action {
			case "withdraw":
				_, err = c.Withdraw(context.Background(), 7, 42)
			case "edit-queued":
				_, err = c.EditQueued(context.Background(), 7, 42)
			default:
				_, err = c.Held(context.Background(), 7, action, "h1")
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
