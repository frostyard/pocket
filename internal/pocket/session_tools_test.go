package pocket

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type sessionToolTransport func(*http.Request) (*http.Response, error)

func (f sessionToolTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSessionToolsProtocol(t *testing.T) {
	for _, op := range []string{"fork", "reset", "export"} {
		t.Run(op, func(t *testing.T) {
			c, _ := NewClient("https://example.org", "test")
			c.HTTP = &http.Client{Transport: sessionToolTransport(func(r *http.Request) (*http.Response, error) {
				method := "POST"
				body := `{"id":8}`
				if op == "export" {
					method = "GET"
					body = "# Export\n"
				}
				if r.Method != method || r.URL.Path != "/api/c/7/"+op || r.Header.Get("Authorization") != "Bearer test" || r.Header.Get("X-Pocket") != "1" || r.Header.Get("Cookie") != "" {
					t.Errorf("request %s %s", r.Method, r.URL)
				}
				if method == "POST" {
					data, _ := io.ReadAll(r.Body)
					if string(data) != "{}" {
						t.Errorf("payload %s", data)
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			var err error
			switch op {
			case "fork":
				var id int
				id, err = c.Fork(context.Background(), 7)
				if id != 8 {
					t.Errorf("fork %d", id)
				}
			case "reset":
				err = c.ResetContext(context.Background(), 7)
			case "export":
				var data []byte
				data, err = c.Export(context.Background(), 7)
				if string(data) != "# Export\n" {
					t.Error("export changed")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestSessionExportUnauthorized(t *testing.T) {
	c, _ := NewClient("https://example.org", "test")
	c.HTTP = &http.Client{Transport: sessionToolTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("no"))}, nil
	})}
	if _, err := c.Export(context.Background(), 7); !errors.Is(err, ErrSignedOut) {
		t.Fatal(err)
	}
}
