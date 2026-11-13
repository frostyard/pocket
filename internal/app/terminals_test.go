package app

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// replayShell serves history once, then a live query after the test permits it.
// Seeing the second poll proves the emulator consumed all replay bytes; a typed
// marker queued afterward proves any earlier replies have drained too.
type replayShell struct {
	mu          sync.Mutex
	polls       int
	inputs      []string
	afterReplay chan struct{}
	live        chan struct{}
	typed       chan struct{}
	answered    chan struct{}
}

func (s *replayShell) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/input") {
		var body struct {
			Data string `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.inputs = append(s.inputs, body.Data)
		s.mu.Unlock()
		switch body.Data {
		case "typed":
			close(s.typed)
		case "\x1b[1;1R":
			select {
			case s.answered <- struct{}{}:
			default:
			}
		}
		return reply(r, `{}`), nil
	}
	if r.URL.Path != "/api/x/terminal/poll" {
		return reply(r, `{}`), nil
	}
	s.mu.Lock()
	s.polls++
	n := s.polls
	s.mu.Unlock()
	var output string
	switch n {
	case 1:
		output = "history prompt\r\n\x1b[6n\x1b[c\x1b]10;?\x07\x1b[>0q\x1b[H"
	case 2:
		close(s.afterReplay)
		select {
		case <-s.live:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		output = "\x1b[6n"
	default:
		<-r.Context().Done()
		return nil, r.Context().Err()
	}
	body, _ := json.Marshal(map[string]any{"terminals": map[string]any{"shell": map[string]any{"data": output, "from": n - 1, "to": n}}})
	return reply(r, string(body)), nil
}

func TestTerminalReplayDoesNotWriteHistoricalReplies(t *testing.T) {
	cachedEmulator(t)
	a, _ := teamApp(t)
	server := &replayShell{afterReplay: make(chan struct{}), live: make(chan struct{}), typed: make(chan struct{}), answered: make(chan struct{}, 1)}
	a.link.client.HTTP = &http.Client{Transport: server}
	ot := a.attach("shell")
	if ot == nil {
		t.Fatalf("attach failed: %s", a.terms.libErr)
	}
	t.Cleanup(func() { _ = ot.term.Close() })
	<-server.afterReplay
	if !strings.Contains(ot.term.Text(), "history prompt") {
		t.Fatal("history was not restored")
	}
	ot.term.Send([]byte("typed"))
	<-server.typed
	server.mu.Lock()
	got := append([]string(nil), server.inputs...)
	server.mu.Unlock()
	if len(got) != 1 || got[0] != "typed" {
		t.Fatalf("history wrote into live shell: %q", got)
	}
	close(server.live)
	<-server.answered
}
