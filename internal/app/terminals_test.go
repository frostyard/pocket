package app

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"github.com/frostyard/pocket/internal/pocket"
)

// TestTerminalOptionsKeepTheServerOffTheDesktop is the guard of
// frostyard/pocket#3's review, blocking point 1: what a shell on the
// server prints may not write the desktop's clipboard (OSC 52), nor open
// links other than web and mail ones. It needs no emulator library.
func TestTerminalOptionsKeepTheServerOffTheDesktop(t *testing.T) {
	a, _ := teamApp(t)
	opts := a.termOptions(nil)
	if !opts.NoClipboardWrite {
		t.Error("the server's output may write the desktop's clipboard")
	}
	if opts.ClipboardRead != terminal.ClipboardDeny {
		t.Error("the server's programs may read the desktop's clipboard")
	}
	if opts.OpenLink == nil {
		t.Fatal("links from the server open with the system, whatever their scheme")
	}
	for link, want := range map[string]bool{
		"https://example.org/x":      true,
		"http://example.org":         true,
		"mailto:someone@example.org": true,
		"file:///etc/passwd":         false,
		"smb://example.org/share":    false,
		"vscode://open?x":            false,
		"https:no-host":              false,
		"/etc/hosts":                 false,
	} {
		if got := safeLink(link); got != want {
			t.Errorf("safeLink(%q) = %v, want %v", link, got, want)
		}
	}
	if !opts.OpenLink("file:///etc/passwd") {
		t.Error("a file link is left to the system to open")
	}
}

// oneShell is a pocket.Client transport that serves one shell's poll from
// memory: its output once open is closed, then a poll that waits for the
// request to end.
type oneShell struct {
	output string
	open   chan struct{}
}

func (s oneShell) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path != "/api/x/terminal/poll" {
		return reply(r, `{}`), nil
	}
	if strings.Contains(r.URL.Query().Get("t"), ".0.") {
		select {
		case <-s.open:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		to := len(utf16.Encode([]rune(s.output)))
		body, _ := json.Marshal(map[string]any{"terminals": map[string]any{"s1": map[string]any{"data": s.output, "from": 0, "to": to}}})
		return reply(r, string(body)), nil
	}
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func reply(r *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

// cachedEmulator points the plugin at a libghostty-vt already in the
// user's cache, or skips: unit tests download nothing.
func cachedEmulator(t *testing.T) {
	t.Helper()
	if os.Getenv("MYGO_GHOSTTY_VT") == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			t.Skip("no cache folder")
		}
		found, _ := filepath.Glob(filepath.Join(dir, "mygo", "natives", "*", "libghostty-vt.*"))
		if len(found) == 0 {
			t.Skip("libghostty-vt is not in the cache (go run ./cmd/pocket downloads it once)")
		}
		t.Setenv("MYGO_GHOSTTY_VT", found[0])
	}
	if err := terminal.Load(); err != nil {
		t.Skipf("the terminal library does not load: %v", err)
	}
}

// TestServerOutputCannotWriteTheClipboard feeds an OSC 52 write through a
// TermConn into Ghostty's emulator, as a shell on the server can print
// one, and checks that the desktop's clipboard keeps what it had with the
// options Pocket gives its terminals. A control run without the guard
// shows that the test sees such a write.
func TestServerOutputCannotWriteTheClipboard(t *testing.T) {
	cachedEmulator(t)
	a, _ := teamApp(t)
	const write = "\x1b]52;c;ZnJvbS1zZXJ2ZXI=\x1b\\é🙂 printed\r\n" // "from-server"
	run := func(control bool) string {
		client, _ := pocket.NewClient("http://pocket.example.org", "tok")
		open := make(chan struct{})
		client.HTTP = &http.Client{Transport: oneShell{output: write, open: open}}
		conn := client.OpenTerminal("s1")
		opts := a.termOptions(conn)
		if control {
			opts.NoClipboardWrite = false
		}
		term, err := terminal.New(opts)
		if err != nil {
			t.Fatal(err)
		}
		defer term.Close()
		tt := ui.NewTester(func(c *ui.Context) { terminal.View(c, term).Fill().AutoFocus() }, 640, 400)
		// The shell prints once the desktop's clipboard holds something:
		// the Tester's first frame already reads the terminal.
		tt.SetClipboard("local-secret")
		close(open)
		deadline := time.Now().Add(10 * time.Second)
		for !strings.Contains(term.Text(), "é🙂 printed") {
			if time.Now().After(deadline) {
				t.Fatalf("the output never showed: %q", term.Text())
			}
			tt.Advance(20 * time.Millisecond)
			tt.Frame()
		}
		for range 5 {
			tt.Advance(20 * time.Millisecond)
			tt.Frame()
		}
		return tt.Clipboard()
	}
	if got := run(true); got != "from-server" {
		t.Fatalf("control: without the guard the clipboard is %q; the test does not see an OSC 52 write", got)
	}
	if got := run(false); got != "local-secret" {
		t.Errorf("the server's output wrote the desktop's clipboard: %q", got)
	}
}
