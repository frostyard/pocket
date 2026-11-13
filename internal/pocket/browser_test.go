package pocket

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type browserTransport func(*http.Request) (*http.Response, error)

func (f browserTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBrowserFrameProtocol(t *testing.T) {
	for _, status := range []int{200, 204, 401, 404} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c, _ := NewClient("https://example.org", "test")
			c.HTTP = &http.Client{Transport: browserTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || r.URL.Path != "/api/c/7/browser/frame" || r.URL.Query().Get("after") != "12" || r.Header.Get("X-Pocket") != "1" || r.Header.Get("Authorization") != "Bearer test" || r.Header.Get("Cookie") != "" {
					t.Errorf("wrong request: %s %s %v", r.Method, r.URL, r.Header)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"image/jpeg"}, "X-Seq": {"13"}, "X-Closed": {"1"}}, Body: io.NopCloser(strings.NewReader("frame")), Request: r}, nil
			})}
			frame, err := c.BrowserFrame(context.Background(), 7, 12)
			switch status {
			case 200:
				if err != nil || string(frame.Data) != "frame" || frame.Seq != 13 {
					t.Fatalf("frame %+v %v", frame, err)
				}
			case 204:
				if err != nil || !frame.Closed || len(frame.Data) != 0 {
					t.Fatalf("idle %+v %v", frame, err)
				}
			case 401:
				if !errors.Is(err, ErrSignedOut) {
					t.Fatalf("auth %v", err)
				}
			default:
				var api *APIError
				if !errors.As(err, &api) || api.Status != 404 {
					t.Fatalf("error %v", err)
				}
			}
		})
	}
}

func TestBrowserFrameIdleAndWrongType(t *testing.T) {
	for _, status := range []int{204, 200} {
		c, _ := NewClient("https://example.org", "test")
		c.HTTP = &http.Client{Transport: browserTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader("page")), Request: r}, nil
		})}
		frame, err := c.BrowserFrame(context.Background(), 7, 0)
		if status == 204 && (err != nil || frame.Closed || len(frame.Data) != 0) {
			t.Fatalf("idle: %+v %v", frame, err)
		}
		if status == 200 && err == nil {
			t.Fatal("accepted HTML as a frame")
		}
	}
}
