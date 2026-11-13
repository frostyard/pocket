package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type newSessionDraft struct {
	open, busy bool
	cwd, err   string
}

// newSession lets the owner edit the server directory before creating anything.
func (a *App) newSession(cwd string) {
	if f := a.newSessionDraft; f != nil && (f.open || f.busy) {
		f.open = true
		return
	}
	if cwd == "" && a.hello != nil {
		cwd = a.hello.Server.DefaultCwd
	}
	a.newSessionDraft = &newSessionDraft{open: true, cwd: cwd}
}

func (a *App) newSessionDialog(c *ui.Context) {
	f := a.newSessionDraft
	if f == nil || !f.open {
		return
	}
	ui.Modal(c, &f.open, func() {
		ui.Column(c).Width(540).Gap(frostui.SpaceM).AlignItems(ui.Stretch).Children(func() {
			frostui.Strong(c, "New session")
			frostui.Muted(c, "Choose a working directory on the Pi Pocket server.").Wrap()
			frostui.Strong(c, "Working directory")
			field := ui.TextInput(c, &f.cwd).Label("Working directory").Placeholder("Server default").AutoFocus().Disabled(f.busy)
			frostui.Caption(c, "Leave empty to use the server's default directory.").Wrap()
			if f.err != "" {
				frostui.Problem(c, f.err).Wrap()
			}
			ui.Row(c).Gap(frostui.SpaceS).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Disabled(f.busy).Clicked() {
					f.open = false
				}
				label := "Create session"
				if f.busy {
					label = "Creating…"
				}
				if ui.PrimaryButton(c, label).Disabled(f.busy).Clicked() || field.Submitted() {
					a.createSession(f)
				}
			})
		})
	})
}

func (a *App) createSession(f *newSessionDraft) {
	if a.newSessionDraft != f || !f.open || f.busy {
		return
	}
	f.busy, f.err = true, ""
	cwd, view, selected := strings.TrimSpace(f.cwd), a.view, a.selected
	var id int
	a.console.Add(frostui.LevelCommand, "send", "New session "+cwd)
	a.request("Starting a session", func(ctx context.Context, c *pocket.Client) error {
		var err error
		id, err = c.NewSession(ctx, cwd, "", false)
		if err == nil && id <= 0 {
			err = fmt.Errorf("the server did not return a session ID")
		}
		return err
	}, func(err error) {
		if a.newSessionDraft != f {
			return
		}
		f.busy = false
		if err != nil {
			f.err = err.Error()
			if !f.open {
				a.toast("Starting a session: " + f.err)
			}
			return
		}
		show := f.open && a.view == view && a.selected == selected
		f.open = false
		if show {
			a.open(id)
		} else {
			a.toast(fmt.Sprintf("Created session %d", id))
		}
	})
}
