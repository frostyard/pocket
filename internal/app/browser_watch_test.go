package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/jpeg"
	"net/http"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type browserHTTP func(*http.Request) (*http.Response, error)

func (f browserHTTP) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBrowserWatchCancelsFrameAndTimerOnLeaving(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	var timers []func()
	a.opts.After = func(_ time.Duration, fn func()) { timers = append(timers, fn) }
	entered := make(chan struct{})
	canceled := make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: browserHTTP(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/c/7/browser" {
			return reply(r, `{"available":true,"open":true,"title":"Example page"}`), nil
		}
		if r.URL.Path == "/api/c/7/browser/frame" {
			close(entered)
			<-r.Context().Done()
			close(canceled)
			return nil, r.Context().Err()
		}
		t.Errorf("unexpected route %s", r.URL)
		return reply(r, `{}`), nil
	})}
	a.startBrowserWatch()
	<-entered
	a.open(0)
	<-canceled
	loop.RunUntil(t, func() bool { return loop.Ran() > 0 })
	if a.browser != nil || len(timers) != 0 {
		t.Fatal("old browser response or timer survived leaving")
	}
}

func TestBrowserWatchIdleRefreshIsReadOnlyAndStops(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	var timers []func()
	a.opts.After = func(_ time.Duration, fn func()) { timers = append(timers, fn) }
	a.link.client.HTTP = &http.Client{Transport: browserHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Path != "/api/c/7/browser" {
			t.Errorf("watch changed the browser: %s %s", r.Method, r.URL)
		}
		return reply(r, `{"available":true,"open":false}`), nil
	})}
	a.startBrowserWatch()
	loop.RunUntil(t, func() bool { return a.browser.state != nil })
	if len(timers) != 1 {
		t.Fatalf("timers %d", len(timers))
	}
	a.stopBrowserWatch()
	timers[0]()
	if a.browser != nil {
		t.Fatal("timer restarted watch")
	}
}

func TestBrowserWatchSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	a.current = 7
	a.hello.Server.Extensions = []string{"pocket-browser"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	img, err := ui.ParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="400" height="240"><rect width="400" height="240" fill="#eef6ff"/><rect x="30" y="30" width="340" height="32" rx="6" fill="#1278a4"/><rect x="30" y="82" width="220" height="12" fill="#899aaa"/><rect x="30" y="106" width="300" height="12" fill="#899aaa"/></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	a.browser = &browserWatch{session: 7, ctx: ctx, cancel: cancel, active: true, state: &pocket.BrowserState{Available: true, Open: true, Title: "Example dashboard", URL: "https://example.org/dashboard"}, image: img}
	tt := ui.NewTesterWith(func(c *ui.Context) {
		frostui.Adapt(c)
		ui.Column(c).Padding(16).Gap(8).Children(func() { a.browserWatchPane(c) })
	}, 300, 440, ui.TesterOptions{})
	tt.Frame()
	if !tt.HasText("Read-only") || !tt.HasText("Example dashboard") {
		t.Fatal("browser context missing")
	}
	writePNG(t, tt, "browser-watch")
}

func TestBrowserFrameDecodeBounds(t *testing.T) {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	if _, err := decodeBrowserFrame(data); err != nil {
		t.Fatal(err)
	}
	oversized := append([]byte(nil), data...)
	for i := 0; i+9 < len(oversized); i++ {
		if oversized[i] == 0xff && oversized[i+1] == 0xc0 {
			binary.BigEndian.PutUint16(oversized[i+5:], 65535)
			binary.BigEndian.PutUint16(oversized[i+7:], 65535)
			break
		}
	}
	if _, err := decodeBrowserFrame(oversized); err == nil {
		t.Fatal("accepted excessive pixel allocation")
	}
	if _, err := decodeBrowserFrame([]byte("bad")); err == nil {
		t.Fatal("accepted invalid frame")
	}
}
func TestBrowserWatchSameSessionStaysOpen(t *testing.T) {
	a, _ := teamApp(t)
	a.current = 7
	s := &browserWatch{session: 7, active: true}
	a.browser = s
	a.open(7)
	if a.browser != s {
		t.Fatal("same session stopped watch")
	}
}
