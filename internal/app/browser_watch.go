package app

import (
	"context"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type browserWatch struct {
	session         int
	ctx             context.Context
	cancel          context.CancelFunc
	state           *pocket.BrowserState
	image           ui.ImageSource
	seq             int
	loading, active bool
	err             string
}

func (a *App) stopBrowserWatch() {
	if a.browser != nil && a.browser.cancel != nil {
		a.browser.cancel()
	}
	a.browser = nil
}

func (a *App) startBrowserWatch() {
	a.stopBrowserWatch()
	if a.current == 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.browser = &browserWatch{session: a.current, ctx: ctx, cancel: cancel, active: true}
	a.pollBrowser()
}

func (a *App) pollBrowser() {
	s := a.browser
	if s == nil || !s.active || s.loading || a.current != s.session {
		return
	}
	s.loading = true
	var state *pocket.BrowserState
	var frame pocket.BrowserFrame
	a.requestWithin(35*time.Second, "Watching the browser", func(ctx context.Context, c *pocket.Client) error {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(s.ctx, cancel)
		defer stop()
		var err error
		state, err = c.Browser(ctx, s.session)
		if err == nil && state.Open {
			frame, err = c.BrowserFrame(ctx, s.session, s.seq)
		}
		if s.ctx.Err() != nil {
			return nil
		}
		return err
	}, func(err error) {
		if a.browser != s || a.current != s.session {
			return
		}
		s.loading = false
		if err != nil {
			s.err = err.Error()
			s.active = false
			return
		}
		s.state = state
		if state == nil {
			return
		}
		if !state.Open || frame.Closed {
			s.image = nil
			s.seq = 0
		}
		if len(frame.Data) > 0 {
			img, decodeErr := ui.DecodeImage(frame.Data)
			if decodeErr != nil {
				s.err = "Could not decode the browser frame"
				s.active = false
				return
			}
			s.image = img
			s.seq = frame.Seq
		}
		delay := time.Second
		if !state.Open {
			delay = 5 * time.Second
		}
		a.after(delay, func() {
			if a.browser == s && a.current == s.session {
				a.pollBrowser()
			}
		})
	})
}

func (a *App) browserWatchPane(c *ui.Context) {
	if a.current == 0 || a.hello == nil || !slices.Contains(a.hello.Server.Extensions, "pocket-browser") {
		return
	}
	frostui.SectionHeader(c, frostui.SectionHead{Title: "Browser", Quiet: true})
	s := a.browser
	if s == nil || s.session != a.current {
		if ui.Button(c, "Watch browser").Clicked() {
			a.startBrowserWatch()
		}
		return
	}
	ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
		frostui.Caption(c, "Read-only")
		if s.loading {
			ui.Spinner(c).Size(12, 12)
		}
		if ui.Button(c, "Stop watching").Clicked() {
			a.stopBrowserWatch()
		}
		if !s.active && ui.Button(c, "Try again").Clicked() {
			a.startBrowserWatch()
		}
	})
	if s.err != "" {
		frostui.Problem(c, s.err).Wrap()
	}
	if state := s.state; state != nil {
		if state.Title != "" {
			frostui.Strong(c, state.Title).Wrap()
		}
		if state.URL != "" {
			frostui.Caption(c, state.URL).Wrap().MaxLines(2).Tooltip(state.URL)
		}
		if state.Problem != "" {
			frostui.Problem(c, state.Problem).Wrap()
		}
		if !state.Available {
			frostui.Muted(c, "No browser is available on the server.").Wrap()
		} else if !state.Open {
			frostui.Muted(c, "The agent has no browser page open.").Wrap()
		}
	}
	if s.image != nil {
		ui.Image(c, s.image).Fit(ui.ScaleDown).Height(180).FillWidth()
	}
}
