package app

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"time"

	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// link is the live connection to the server: one event stream, opened
// again with a growing wait after it drops.
type link struct {
	client *pocket.Client
	cancel context.CancelFunc
	tab    string
	// streamID names the open connection for /api/switch, from hello.
	streamID string
	// gen counts connections; events of an older one are dropped.
	gen int
	// switchSeq numbers switches, as the web app does.
	switchSeq int
	// switching is the seq of the switch the server has not confirmed
	// yet: session events until then are about the session left.
	switching int
	failures  int
}

// connect starts the link to server with auth, replacing any.
func (a *App) connect(server, auth string) {
	a.disconnect()
	client, err := pocket.NewClient(server, auth)
	if err != nil {
		a.signIn.err = err.Error()
		return
	}
	client.Drift = func(err error) {
		a.console.Add(frostui.LevelWarn, "api", "A field Pocket does not read: "+err.Error())
	}
	a.link.client = client
	a.link.tab = pocket.NewTabID()
	a.link.failures = 0
	host := server
	if u, err := url.Parse(server); err == nil {
		host = u.Host
	}
	a.conn = frostui.Connection{
		State: frostui.Connecting, Daemon: "Pi Pocket", Host: host,
		OnRetry:   a.reconnectNow,
		OnDetails: func() { a.shell.Console.Open = true },
	}
	a.console.Add(frostui.LevelInfo, "link", "Connecting to "+host)
	ctx, cancel := context.WithCancel(context.Background())
	a.link.cancel = cancel
	a.openStream(ctx)
}

// disconnect stops the link.
func (a *App) disconnect() {
	if a.link.cancel != nil {
		a.link.cancel()
		a.link.cancel = nil
	}
	a.link.gen++
	a.link.streamID = ""
}

// reconnectNow drops the wait and connects again.
func (a *App) reconnectNow() {
	if a.link.client == nil {
		return
	}
	a.connect(a.link.client.Base.String(), a.link.client.Auth)
}

// openStream runs one connection on a goroutine; when it ends, it waits
// and opens the next, as long as ctx lives.
func (a *App) openStream(ctx context.Context) {
	a.link.gen++
	gen := a.link.gen
	client := a.link.client
	opts := pocket.StreamOptions{Tab: a.link.tab, Conversation: a.current, Notify: true}
	go func() {
		retry, err := client.Events(ctx, opts, func(ev pocket.Event) {
			a.post(func() {
				if gen != a.link.gen {
					return
				}
				a.handle(ev)
			})
		})
		if ctx.Err() != nil {
			return
		}
		a.post(func() {
			if gen != a.link.gen {
				return
			}
			a.streamEnded(ctx, retry, err)
		})
	}()
}

// streamEnded decides what follows a connection that dropped.
func (a *App) streamEnded(ctx context.Context, retry time.Duration, err error) {
	a.link.streamID = ""
	if pocket.IsSignedOut(err) {
		a.console.Add(frostui.LevelError, "link", "The server no longer accepts this sign-in")
		a.signedOut("The server no longer accepts this device's sign-in. Paste a new sign-in link.")
		return
	}
	a.link.failures++
	wait := backoff(a.link.failures, retry)
	reason := "the connection closed"
	if err != nil && !errors.Is(err, context.Canceled) {
		reason = err.Error()
	}
	if a.conn.State == frostui.Connected {
		a.conn.State = frostui.Reconnecting
	}
	a.conn.Reason = fmt.Sprintf("%s; trying again in %s", reason, wait.Round(time.Second))
	a.console.Add(frostui.LevelWarn, "link", a.conn.Reason)
	time.AfterFunc(wait, func() {
		a.post(func() {
			if ctx.Err() != nil {
				return
			}
			a.openStream(ctx)
		})
	})
}

// backoff is the wait before the nth try: the server's retry at first,
// then doubling to 30 s, with jitter so many windows do not come at once.
func backoff(n int, retry time.Duration) time.Duration {
	base := retry
	if base <= 0 {
		base = 1500 * time.Millisecond
	}
	d := base << min(n-1, 5)
	d = min(d, 30*time.Second)
	return d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
}

// signedOut forgets the credential and shows the sign-in screen.
func (a *App) signedOut(why string) {
	server := a.settings.Server
	a.disconnect()
	a.link.client = nil
	if server != "" {
		_ = a.opts.Creds.Forget(server)
	}
	a.signIn.err = why
	a.reset()
}

// switchTo shows conversation id (0 for none) on the open connection, or
// opens a new connection that names it.
func (a *App) switchTo(id int) {
	a.current = id
	if a.link.client == nil {
		return
	}
	if a.link.streamID == "" || a.link.cancel == nil {
		return // the next connection names it
	}
	a.link.switchSeq++
	seq := a.link.switchSeq
	a.link.switching = seq
	client, stream := a.link.client, a.link.streamID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := client.Switch(ctx, stream, id, seq)
		if err == nil {
			return
		}
		a.post(func() {
			if a.link.switchSeq != seq {
				return
			}
			a.console.Add(frostui.LevelWarn, "link", "Switching sessions failed, reconnecting: "+err.Error())
			a.link.switching = 0
			a.reconnectNow()
		})
	}()
}
