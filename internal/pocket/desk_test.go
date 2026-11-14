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

func TestRecordMerge(t *testing.T) {
	head := strings.Repeat("a", 40)
	rec := MergeRecord{Op: "0b6f1c1e-3a5d-4c7e-9f00-1234567890ab", Ref: "frostyard/pocket#12", Head: head, Result: "merged", Method: "squash"}
	for _, tc := range []struct {
		name     string
		status   int
		answer   string
		record   MergeRecord
		recorded bool
		fail     bool
		body     map[string]any
	}{
		{"new", 200, `{"recorded":true}`, rec, true, false, map[string]any{"op": rec.Op, "ref": rec.Ref, "head": head, "result": "merged", "method": "squash", "via": "pocket"}},
		{"repeat", 200, `{"recorded":false}`, rec, false, false, nil},
		{"refused, no method", 200, `{"recorded":true}`, MergeRecord{Op: rec.Op, Ref: rec.Ref, Head: head, Result: "refused", Reason: "no ready-for-human label"}, true, false,
			map[string]any{"op": rec.Op, "ref": rec.Ref, "head": head, "result": "refused", "reason": "no ready-for-human label", "via": "pocket"}},
		{"conflict", 409, `{"error":"receipt conflict"}`, rec, false, true, nil},
		{"no route", 404, `{"error":"not found"}`, rec, false, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := NewClient("https://example.org", "test")
			c.HTTP = &http.Client{Transport: sessionToolTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "POST" || r.URL.EscapedPath() != "/api/x/desk/threads/T%2012%2Fx/merges" ||
					r.Header.Get("Authorization") != "Bearer test" || r.Header.Get("X-Pocket") != "1" {
					t.Errorf("request %s %s", r.Method, r.URL.EscapedPath())
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if tc.body != nil && !equalJSON(body, tc.body) {
					t.Errorf("body %v, want %v", body, tc.body)
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.answer)), Header: make(http.Header)}, nil
			})}
			recorded, err := c.RecordMerge(context.Background(), "T 12/x", tc.record)
			if recorded != tc.recorded || (err != nil) != tc.fail {
				t.Fatalf("recorded %v, err %v", recorded, err)
			}
			var api *APIError
			if tc.status == 409 && (!errors.As(err, &api) || api.Status != 409 || api.Message != "receipt conflict") {
				t.Fatalf("err %v", err)
			}
		})
	}
}

func equalJSON(a, b map[string]any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
