package app

import (
	"context"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// signIn is the first-run screen's state.
type signIn struct {
	link        string
	busy        bool
	err         string
	field       ui.Handle
	confirmLink string
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
			in := ui.TextInput(c, &s.link).Placeholder("https://example.org/login?token=…").
				AutoFocus().Password().Disabled(s.busy || s.confirmLink != "").Label("Sign-in link")
			in.Bind(&s.field)
			if s.err != "" {
				frostui.Problem(c, s.err)
			}
			if s.confirmLink != "" {
				frostui.Problem(c, "This server does not use HTTPS: the sign-in token travels in clear text.").Wrap()
			}
			ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
				if s.confirmLink != "" {
					if ui.Button(c, "Cancel").Clicked() {
						s.confirmLink = ""
					}
					if ui.PrimaryButton(c, "Connect anyway").Clicked() {
						link := s.confirmLink
						s.confirmLink = ""
						a.beginSignIn(link)
					}
					return
				}
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

// insecureSignIn asks before sending a credential over public HTTP. Literal
// loopback and private addresses, and localhost, are local development cases;
// hostnames are not resolved, so a DNS answer cannot silently waive the warning.
func insecureSignIn(link string) bool {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		return !ip.IsLoopback() && !ip.IsPrivate()
	}
	return true
}

// startSignIn checks transport security before any request leaves the device.
func (a *App) startSignIn() {
	if a.signIn.busy {
		return
	}
	link := a.signIn.link
	if insecureSignIn(link) {
		a.signIn.confirmLink = link
		return
	}
	a.beginSignIn(link)
}

// beginSignIn uses the exact link the person confirmed.
func (a *App) beginSignIn(link string) {
	s := &a.signIn
	s.busy, s.err = true, ""
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
