package pocket

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// The terminal module of pi-pocket-team runs login shells on the server,
// each on a pseudo-terminal, for the owner alone. Its output comes by a
// long poll (at most 25 s) that names how much of it the client has; keys
// and resizes are small POSTs. A shell ends on DELETE, when it exits, or 60
// s after its last poll.

// TerminalInfo is a shell the server runs.
type TerminalInfo struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Cwd       string `json:"cwd"`
	StartedAt Millis `json:"startedAt"`
	Exited    bool   `json:"exited,omitempty"`
	ExitCode  *int   `json:"exitCode,omitempty"`
}

// Terminals lists the shells; ok is false when the server has no terminal
// module, or the person is not its owner.
func (c *Client) Terminals(ctx context.Context) (list []TerminalInfo, ok bool, err error) {
	err = c.Get(ctx, "/api/x/terminal/terminals", &list)
	if e, isAPI := err.(*APIError); isAPI && (e.Status == http.StatusNotFound || e.Status == http.StatusForbidden) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return list, true, nil
}

// TerminalPlace says where a new shell starts: the server user's home
// (all empty), a session's folder, or a path.
type TerminalPlace struct {
	Session int
	Path    string
}

// StartTerminal starts a shell of rows by cols, and returns its ID.
func (c *Client) StartTerminal(ctx context.Context, place TerminalPlace, rows, cols int) (string, error) {
	body := map[string]any{"rows": rows, "cols": cols, "folder": "home"}
	switch {
	case place.Session != 0:
		body["folder"], body["conversation"] = "session", place.Session
	case place.Path != "":
		body["folder"], body["path"] = "path", place.Path
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := c.Post(ctx, "/api/x/terminal/terminals", body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// EndTerminal ends a shell.
func (c *Client) EndTerminal(ctx context.Context, id string) error {
	return c.Do(ctx, http.MethodDelete, "/api/x/terminal/terminals/"+url.PathEscape(id), nil, nil)
}

// termPoll is one terminal's part of a poll's answer.
type termPoll struct {
	Data     string `json:"data"`
	From     int64  `json:"from"`
	To       int64  `json:"to"`
	Exited   bool   `json:"exited,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
	Gone     bool   `json:"gone,omitempty"`
}

// inputMax is the most characters one input POST takes (terminal.ts).
const inputMax = 32 * 1024

// TermConn is a shell on the server as a connection a terminal emulator
// reads and writes: Read gives its output, Write sends keys, Resize its
// size. Closing it stops reading; the server ends the shell 60 s later
// unless EndTerminal ends it at once.
type TermConn struct {
	c   *Client
	id  string
	tab string

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	pending []byte
	// from is how far into the output the client has read, in the
	// server's units (it counts UTF-16 code units: use its "to").
	from     int64
	ended    bool
	exitCode int
	// seq numbers the input chunks, so a chunk sent again is written once.
	seq   int64
	wmu   sync.Mutex
	retry time.Duration
}

// OpenTerminal connects to shell id, reading its output from the start of
// what the server keeps (its last 256 K characters).
func (c *Client) OpenTerminal(id string) *TermConn {
	ctx, cancel := context.WithCancel(context.Background())
	return &TermConn{c: c, id: id, tab: NewTabID(), ctx: ctx, cancel: cancel, retry: time.Second, exitCode: -1}
}

// ID is the shell's ID.
func (t *TermConn) ID() string { return t.id }

// ExitCode is the shell's exit code once Read has returned io.EOF for its
// exit; -1 when the connection ended without one (a disconnect, a sign-out,
// a shell that went).
func (t *TermConn) ExitCode() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.exitCode
}

// Read gives the shell's output as it comes, waiting for it; io.EOF once
// the shell exited or went, or the connection closed.
func (t *TermConn) Read(p []byte) (int, error) {
	for {
		t.mu.Lock()
		if len(t.pending) > 0 {
			n := copy(p, t.pending)
			t.pending = t.pending[n:]
			t.mu.Unlock()
			return n, nil
		}
		ended := t.ended
		from := t.from
		t.mu.Unlock()
		if ended || t.ctx.Err() != nil {
			return 0, io.EOF
		}
		ans, err := t.poll(from)
		if err != nil {
			if t.ctx.Err() != nil {
				return 0, io.EOF
			}
			if IsSignedOut(err) {
				return 0, err
			}
			// The network or the server failed: wait and poll again. The
			// server keeps the output, so nothing is lost.
			select {
			case <-t.ctx.Done():
				return 0, io.EOF
			case <-time.After(t.retry):
			}
			continue
		}
		t.mu.Lock()
		switch {
		case ans.Gone:
			t.ended, t.exitCode = true, -1
		default:
			if ans.Data != "" {
				t.pending = append(t.pending, ans.Data...)
			}
			if ans.To > t.from {
				t.from = ans.To
			}
			if ans.Exited {
				t.ended, t.exitCode = true, -1
				if ans.ExitCode != nil {
					t.exitCode = *ans.ExitCode
				}
			}
		}
		t.mu.Unlock()
	}
}

// poll waits for output past from.
func (t *TermConn) poll(from int64) (*termPoll, error) {
	q := url.Values{}
	// id.from.ack: what the client received, and what it drew, which is
	// the same here: the emulator takes what Read gives at once.
	q.Set("t", fmt.Sprintf("%s.%d.%d", t.id, from, from))
	q.Set("tab", t.tab)
	ctx, cancel := context.WithTimeout(t.ctx, 40*time.Second)
	defer cancel()
	var out struct {
		Terminals map[string]termPoll `json:"terminals"`
	}
	if err := t.c.Get(ctx, "/api/x/terminal/poll?"+q.Encode(), &out); err != nil {
		var api *APIError
		if errors.As(err, &api) && api.Status == http.StatusNotFound {
			return &termPoll{Gone: true}, nil
		}
		return nil, err
	}
	ans, ok := out.Terminals[t.id]
	if !ok {
		return &termPoll{From: from, To: from}, nil
	}
	return &ans, nil
}

// Write sends keys to the shell, in chunks the server takes, each sent
// again until it lands.
func (t *TermConn) Write(p []byte) (int, error) {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	data := []rune(string(p))
	for len(data) > 0 {
		n := min(len(data), inputMax/2) // a rune may be 2 UTF-16 units
		chunk := string(data[:n])
		data = data[n:]
		t.seq++
		body := map[string]any{"data": chunk, "tab": t.tab, "seq": t.seq}
		path := "/api/x/terminal/terminals/" + url.PathEscape(t.id) + "/input"
		if err := t.send(path, body); err != nil {
			// The keys did not land, and the emulator takes no more after
			// a failed write: end the connection, so the view says so
			// rather than taking keys that go nowhere.
			t.cancel()
			return 0, err
		}
	}
	return len(p), nil
}

// send posts to the shell, trying again for a while when the network
// fails: the sequence number makes a repeat harmless.
func (t *TermConn) send(path string, body map[string]any) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(t.ctx, 10*time.Second)
		err := t.c.Post(ctx, path, body, nil)
		cancel()
		var api *APIError
		switch {
		case err == nil:
			return nil
		case errors.As(err, &api), IsSignedOut(err), t.ctx.Err() != nil, time.Now().After(deadline):
			return err
		}
		select {
		case <-t.ctx.Done():
			return io.ErrClosedPipe
		case <-time.After(t.retry):
		}
	}
}

// Resize tells the shell its size, as the emulator lays it out.
func (t *TermConn) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(t.ctx, 10*time.Second)
	defer cancel()
	return t.c.Post(ctx, "/api/x/terminal/terminals/"+url.PathEscape(t.id)+"/resize",
		map[string]any{"rows": rows, "cols": cols}, nil)
}

// Close stops reading the shell.
func (t *TermConn) Close() error {
	t.cancel()
	return nil
}
