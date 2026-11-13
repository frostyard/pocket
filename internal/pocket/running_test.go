package pocket

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRunningControlsProtocol(t *testing.T) {
	version := "7"
	for _, tc := range []struct {
		name, method, path, body string
		run                      func(*Client) error
	}{
		{"board", "GET", "/api/running/board", "", func(c *Client) error { _, err := c.Running(context.Background()); return err }},
		{"background", "GET", "/api/c/8/background", "", func(c *Client) error { _, err := c.Background(context.Background(), 8); return err }},
		{"output", "GET", "/api/c/8/background/bg-2/output?chars=20000", "", func(c *Client) error { _, err := c.BackgroundOutput(context.Background(), 8, "bg-2"); return err }},
		{"stop", "POST", "/api/c/8/background/42/stop", `{}`, func(c *Client) error {
			return c.StopRunning(context.Background(), RunningTask{ConversationID: 8, ID: 42, Kind: "pocket.background"})
		}},
		{"subagent", "POST", "/api/c/9/abort", `{}`, func(c *Client) error {
			return c.StopRunning(context.Background(), RunningTask{ConversationID: 9, Kind: "pi.generation", Subagent: "helper"})
		}},
		{"create", "POST", "/api/c/8/schedules", `{"when":"in 2h check build","zone":"UTC"}`, func(c *Client) error { return c.Schedule(context.Background(), 8, "in 2h check build", "UTC") }},
		{"cancel", "POST", "/api/c/8/schedules/s1/cancel", `{}`, func(c *Client) error { return c.CancelSchedule(context.Background(), 8, "s1") }},
		{"pause", "POST", "/api/c/8/schedules/s1/pause", `{"paused":true}`, func(c *Client) error { return c.PauseSchedule(context.Background(), 8, "s1", true) }},
		{"edit", "PUT", "/api/c/8/schedules/s1", `{"text":"check build","zone":"UTC","version":"7","next":123,"every":{"minutes":120}}`, func(c *Client) error {
			return c.EditSchedule(context.Background(), 8, Schedule{ID: "s1", Text: "check build", Zone: "UTC", Version: &version, Next: 123, Every: json.RawMessage(`{"minutes":120}`)})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := NewClient("https://example.org", "test")
			c.HTTP = &http.Client{Transport: sessionToolTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != tc.method || r.URL.RequestURI() != tc.path || r.Header.Get("Authorization") != "Bearer test" || r.Header.Get("X-Pocket") != "1" || r.Header.Get("Cookie") != "" {
					t.Errorf("request %s %s", r.Method, r.URL)
				}
				if tc.body != "" {
					var got, want any
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Error(err)
					}
					if err := json.Unmarshal([]byte(tc.body), &want); err != nil {
						t.Fatal(err)
					}
					g, _ := json.Marshal(got)
					w, _ := json.Marshal(want)
					if string(g) != string(w) {
						t.Errorf("body %s, want %s", g, w)
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
			})}
			if err := tc.run(c); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScheduleUpdatesAndOlderServers(t *testing.T) {
	v := NewView()
	for _, raw := range []string{
		`{"full":true,"conversation":{"id":1},"schedules":[{"id":"s1","text":"Check","version":"0"}]}`,
		`{"conversation":{"id":1},"schedules":[]}`,
	} {
		var u ViewUpdate
		if err := Decode([]byte(raw), &u); err != nil {
			t.Fatal(err)
		}
		if err := v.Apply(&u); err != nil {
			t.Fatal(err)
		}
		if len(v.Schedules) != len(*u.Schedules) {
			t.Fatal("schedule update not applied")
		}
	}
	c, _ := NewClient("https://example.org", "test")
	c.HTTP = &http.Client{Transport: sessionToolTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsupported control sent a request")
		return nil, nil
	})}
	if err := c.EditSchedule(context.Background(), 1, Schedule{ID: "old"}); err == nil {
		t.Fatal("unversioned edit accepted")
	}
	if err := c.StopRunning(context.Background(), RunningTask{Kind: "pi.tool"}); err == nil {
		t.Fatal("unsupported task stop accepted")
	}
}
