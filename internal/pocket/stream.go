package pocket

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Protocol is the version of the event stream's updates this client
// understands (web/store.js PROTOCOL): 5 takes live messages as deltas,
// only the newest rows' entries in a whole view (earlier ones come from
// Context), and light entries made whole in the next update.
const Protocol = 5

// NewTabID returns an ID for one window's connection.
func NewTabID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "pocket-" + hex.EncodeToString(b)
}

// StreamOptions says what one connection to the event stream asks for.
type StreamOptions struct {
	// Tab names this window to the server, the same across reconnects.
	Tab string
	// Conversation is the session shown, 0 for none.
	Conversation int
	// Notify asks the server for the person's notifications over this
	// connection (embedder-notify events), as an embedder that shows its
	// own does.
	Notify bool
}

// Events opens the event stream and calls handle for each event until the
// connection ends or ctx is done. It returns why the connection ended;
// ErrSignedOut when the server refused the credential. It returns the
// retry delay the server asked for, 0 when it named none.
func (c *Client) Events(ctx context.Context, opts StreamOptions, handle func(Event)) (time.Duration, error) {
	q := url.Values{}
	q.Set("tab", opts.Tab)
	q.Set("p", strconv.Itoa(Protocol))
	if opts.Conversation != 0 {
		q.Set("c", strconv.Itoa(opts.Conversation))
	}
	if opts.Notify {
		q.Set("embedder", "notify")
	}
	req, err := c.request(ctx, http.MethodGet, "/api/events?"+q.Encode(), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return 0, ErrSignedOut
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("the event stream answered %s", resp.Status)
	}
	// A stream that goes quiet for longer than the server's pings (every
	// 15 s) would leave it is dead: a sleeping laptop, a tunnel that
	// dropped it. Every read, pings included, counts as life.
	alive := make(chan struct{}, 1)
	r := newSSEReader(activity{resp.Body, alive})
	done := make(chan struct{})
	defer close(done)
	go func() {
		const quiet = 75 * time.Second
		t := time.NewTimer(quiet)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-alive:
				if !t.Stop() {
					select {
					case <-t.C:
					default:
					}
				}
				t.Reset(quiet)
			case <-t.C:
				_ = resp.Body.Close()
				return
			}
		}
	}()
	for {
		ev, err := r.Next()
		if err != nil {
			if ctx.Err() != nil {
				return r.retry, ctx.Err()
			}
			return r.retry, err
		}
		handle(ev)
	}
}

// Switch shows another session on an open connection: its events come on
// that connection after a "switched" event with seq.
func (c *Client) Switch(ctx context.Context, connection string, conversation int, seq int) error {
	body := map[string]any{"connection": connection, "seq": seq, "have": nil}
	if conversation == 0 {
		body["c"] = nil
	} else {
		body["c"] = conversation
	}
	return c.Post(ctx, "/api/switch", body, nil)
}

// IsSignedOut reports whether err means the credential is no good.
func IsSignedOut(err error) bool { return errors.Is(err, ErrSignedOut) }

// activity reports each read on alive, without waiting.
type activity struct {
	r     io.Reader
	alive chan<- struct{}
}

func (a activity) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	if n > 0 {
		select {
		case a.alive <- struct{}{}:
		default:
		}
	}
	return n, err
}
