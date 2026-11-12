package app

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// signIn is the first-run screen's state.
type signIn struct {
	link  string
	busy  bool
	err   string
	field ui.Handle
}

// signInView fills the window with the sign-in card.
func (a *App) signInView(c *ui.Context) {
	t := c.Theme()
	s := &a.signIn
	ui.Column(c).Fill().Center().Background(t.Background).Children(func() {
		ui.Column(c).Width(460).Gap(frostui.SpaceL).Padding(frostui.SpaceXL).AlignItems(ui.Stretch).
			Background(frostui.Panel(t)).Radius(12).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
			ui.Row(c).Gap(frostui.SpaceM).AlignItems(ui.Center).Children(func() {
				frostui.IconTile(c, "pocket-sparkle", frostui.Accent, 44)
				ui.Column(c).Gap(2).Children(func() {
					frostui.Title(c, "Connect to Pi Pocket")
					frostui.Muted(c, "Your agents, on your desktop.")
				})
			})
			frostui.Body(c, "Paste the sign-in link your Pi Pocket launcher printed (/login?token=…), or an invite link someone sent you. Pocket keeps the sign-in in your desktop's keyring.").Wrap()
			in := ui.TextInput(c, &s.link).Placeholder("https://pocket.example.ts.net/login?token=…").
				AutoFocus().Disabled(s.busy).Label("Sign-in link")
			in.Bind(&s.field)
			if s.err != "" {
				frostui.Problem(c, s.err)
			}
			ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
				if s.busy {
					ui.Spinner(c)
					frostui.Muted(c, "Signing in…")
				}
				ui.Spacer(c)
				ok := strings.TrimSpace(s.link) != "" && !s.busy
				if (ui.PrimaryButton(c, "Connect").Disabled(!ok).Clicked() || in.Submitted()) && ok {
					a.startSignIn()
				}
			})
		})
	})
}

// deviceName names this device to a server that redeems an invite.
func deviceName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return "Pocket on " + h
	}
	return "Pocket"
}

// startSignIn follows the link on a goroutine and connects with what it
// gets.
func (a *App) startSignIn() {
	s := &a.signIn
	s.busy, s.err = true, ""
	link := s.link
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		server, auth, err := pocket.SignIn(ctx, link, deviceName())
		a.post(func() {
			s.busy = false
			if err != nil {
				s.err = err.Error()
				return
			}
			if err := a.opts.Creds.SetToken(server, auth); err != nil {
				s.err = "Signed in, but the keyring would not keep it: " + err.Error()
				return
			}
			a.settings.Server = server
			a.saveSettings()
			s.link = ""
			a.connect(server, auth)
		})
	}()
}
