package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// terms is the Terminal place's state: the shells the server runs for the
// owner (pi-pocket-team's terminal module), and those Pocket shows, each
// with Ghostty's emulator (MyGo's terminal plugin) over a TermConn.
type terms struct {
	on      bool
	list    []pocket.TerminalInfo
	open    map[string]*openTerm
	sel     string
	loading bool
	// probed is true once the module answered (or did not): the place
	// does not ask again each frame.
	probed bool
	busy   bool
	// libErr says why the emulator's library did not load.
	libErr string
	// field is the shown terminal's view, for its focus.
	field ui.Handle
	// focused is whether a terminal has the keyboard focus, read each
	// frame: the window's Ctrl shortcuts that shells use then stand aside.
	focused bool
}

// openTerm is a shell Pocket shows.
type openTerm struct {
	conn   *pocket.TermConn
	term   *terminal.Terminal
	title  string
	exited bool
	code   int
}

// loadTerminals lists the shells, and finds out whether the server has the
// terminal module.
func (a *App) loadTerminals() {
	if a.terms.loading {
		return
	}
	a.terms.loading = true
	var list []pocket.TerminalInfo
	var on bool
	a.request("Listing terminals", func(ctx context.Context, c *pocket.Client) error {
		var err error
		list, on, err = c.Terminals(ctx)
		return err
	}, func(err error) {
		a.terms.loading, a.terms.probed = false, true
		if err == nil {
			a.terms.on = on
			a.setTerminals(list)
		}
	})
}

// terminalsChanged takes the list the module sends when it changes.
func (a *App) terminalsChanged(data json.RawMessage) {
	var list []pocket.TerminalInfo
	if json.Unmarshal(data, &list) == nil {
		a.terms.on = true
		a.setTerminals(list)
	}
}

func (a *App) setTerminals(list []pocket.TerminalInfo) {
	a.terms.list = list
	listed := map[string]bool{}
	for _, t := range list {
		listed[t.ID] = true
	}
	// A shell that left the list (ended in the web app, or exited and
	// dropped by the module) leaves Pocket too, with its scrollback.
	for id, ot := range a.terms.open {
		if !listed[id] {
			_ = ot.term.Close()
			delete(a.terms.open, id)
		}
	}
	if !listed[a.terms.sel] {
		a.terms.sel = ""
		if len(list) > 0 {
			a.terms.sel = list[len(list)-1].ID
		}
	}
}

// liveTerminals counts the shells that have not exited.
func (a *App) liveTerminals() int {
	n := 0
	for _, t := range a.terms.list {
		if !t.Exited {
			n++
		}
	}
	return n
}

// attach shows shell id: a connection to it, and an emulator over that.
func (a *App) attach(id string) *openTerm {
	if ot := a.terms.open[id]; ot != nil {
		return ot
	}
	if a.link.client == nil {
		return nil
	}
	if err := terminal.Load(); err != nil {
		a.terms.libErr = err.Error()
		return nil
	}
	conn := a.link.client.OpenTerminal(id)
	ot := &openTerm{conn: conn, code: -1}
	opts := a.termOptions(conn)
	opts.OnTitle = func(title string) {
		a.post(func() { ot.title = title })
	}
	opts.OnExit = func(int) {
		code := conn.ExitCode()
		a.post(func() {
			ot.exited, ot.code = true, code
		})
	}
	term, err := terminal.New(opts)
	if err != nil {
		_ = conn.Close()
		a.terms.libErr = err.Error()
		return nil
	}
	ot.term = term
	if a.terms.open == nil {
		a.terms.open = map[string]*openTerm{}
	}
	a.terms.open[id] = ot
	return ot
}

// termOptions are the emulator's options for a shell on the server. What
// the server sends is the server's: it may not write the desktop's
// clipboard (OSC 52), which the next paste into a local terminal would
// run, and a link from it opens only on the web or in mail, never as a
// file or another program's address. Reads of the clipboard stay refused
// (the plugin's default).
func (a *App) termOptions(conn io.ReadWriteCloser) terminal.Options {
	return terminal.Options{
		Conn:             conn,
		NoClipboardWrite: true,
		OpenLink: func(link string) bool {
			if safeLink(link) {
				a.openURL(link)
			} else {
				a.toast("Only web and mail links open from a terminal")
			}
			return true // handled: nothing else opens it
		},
	}
}

// safeLink reports whether a link from a terminal may open: http, https
// or mailto, and nothing else (no file:, smb: or an app's own scheme).
func safeLink(link string) bool {
	u, err := url.Parse(link)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Host != ""
	case "mailto":
		return true
	}
	return false
}

// newTerminal starts a shell in place and shows it.
func (a *App) newTerminal(place pocket.TerminalPlace) {
	if a.terms.busy {
		return
	}
	a.terms.busy = true
	var id string
	a.console.Add(frostui.LevelCommand, "terminal", "New terminal")
	a.request("Starting a terminal", func(ctx context.Context, c *pocket.Client) error {
		var err error
		id, err = c.StartTerminal(ctx, place, 30, 100)
		return err
	}, func(err error) {
		a.terms.busy = false
		if err != nil {
			return
		}
		a.goPlace("x:terminal")
		a.terms.sel = id
		a.loadTerminals()
	})
}

// endTerminal ends a shell on the server.
func (a *App) endTerminal(id string) {
	if ot := a.terms.open[id]; ot != nil {
		_ = ot.term.Close()
		delete(a.terms.open, id)
	}
	a.request("Ending the terminal", func(ctx context.Context, c *pocket.Client) error {
		return c.EndTerminal(ctx, id)
	}, func(error) { a.loadTerminals() })
	if a.terms.sel == id {
		a.terms.sel = ""
		for _, t := range a.terms.list {
			if t.ID != id {
				a.terms.sel = t.ID
			}
		}
	}
}

// closeTerminals stops reading every shell: the server ends them a minute
// later unless something polls them again.
func (a *App) closeTerminals() {
	for id, ot := range a.terms.open {
		_ = ot.term.Close()
		delete(a.terms.open, id)
	}
}

// terminalPane is the Terminal place: a strip of the shells, and the one
// chosen.
func (a *App) terminalPane(c *ui.Context) {
	t := c.Theme()
	if !a.terms.probed && !a.terms.loading {
		a.loadTerminals()
	}
	ui.Column(c).Grow(1).AlignItems(ui.Stretch).Background(t.Background).Children(func() {
		ui.Row(c).Gap(frostui.SpaceXS).AlignItems(ui.Center).Padding(frostui.SpaceS, frostui.SpaceM).Background(frostui.Chrome(t)).Children(func() {
			for _, info := range a.terms.list {
				a.terminalTab(c.Key(info.ID), info)
			}
			live := a.liveTerminals()
			if a.terms.busy {
				ui.Spinner(c).Size(14, 14)
			}
			label := "New terminal in the server's home folder"
			if live >= 4 {
				label = "The server runs 4 terminals at most: end one first"
			}
			if frostui.IconButton(c, "list-add", label).Disabled(live >= 4 || a.terms.busy).Clicked() {
				a.newTerminal(pocket.TerminalPlace{})
			}
			ui.Spacer(c)
			frostui.Caption(c, a.terminalHint())
		})
		frostui.RowDivider(c)
		var info *pocket.TerminalInfo
		for i := range a.terms.list {
			if a.terms.list[i].ID == a.terms.sel {
				info = &a.terms.list[i]
			}
		}
		switch {
		case a.terms.probed && !a.terms.on:
			ui.Column(c).Grow(1).Children(func() {
				frostui.Empty(c, "pocket-terminal", "No terminal module", "This server's terminal module is off, or not installed.").Grow(1)
			})
		case a.terms.libErr != "":
			frostui.Problem(c, "The terminal emulator did not load: "+a.terms.libErr).Margin(frostui.SpaceL)
		case info == nil:
			ui.Column(c).Grow(1).Children(func() {
				frostui.Empty(c, "pocket-terminal", "No terminal", "A shell on the server, as you. Start one here, or from a session's details in its folder.").Grow(1)
			})
			if len(a.terms.list) == 0 {
				ui.Row(c).Padding(0, 0, frostui.SpaceXL, 0).Children(func() {
					ui.Spacer(c)
					if ui.PrimaryButton(c, "New terminal").Disabled(a.terms.busy).Clicked() {
						a.newTerminal(pocket.TerminalPlace{})
					}
					ui.Spacer(c)
				})
			}
		default:
			ot := a.attach(info.ID)
			if ot == nil {
				frostui.Loading(c, "Connecting…")
				return
			}
			ui.Box(c).Grow(1).Padding(frostui.SpaceS).AlignItems(ui.Stretch).Children(func() {
				terminal.View(c.Key(info.ID), ot.term).Grow(1).AutoFocus().Bind(&a.terms.field)
			})
			if ot.exited || info.Exited {
				code := ot.code
				if info.ExitCode != nil {
					code = *info.ExitCode
				}
				msg := fmt.Sprintf("The shell exited (code %d).", code)
				if code < 0 {
					msg = "Disconnected from the shell."
				}
				ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Padding(frostui.SpaceS, frostui.SpaceM).Children(func() {
					frostui.Toned(c, msg, frostui.Warning).Grow(1)
					if ui.Button(c, "Close").Clicked() {
						a.endTerminal(info.ID)
					}
				})
			}
		}
	})
}

// terminalHint says how to copy and paste in a terminal.
func (a *App) terminalHint() string {
	if runtime.GOOS == "darwin" {
		return "⌘C copy · ⌘V paste"
	}
	return "Ctrl Shift C copy · Ctrl Shift V paste"
}

func (a *App) terminalTab(c *ui.Context, info pocket.TerminalInfo) {
	t := c.Theme()
	sel := info.ID == a.terms.sel
	title := info.Title
	if ot := a.terms.open[info.ID]; ot != nil && ot.title != "" {
		title = ot.title
	}
	tab := ui.ButtonBase(c).Row().Gap(frostui.SpaceXS).AlignItems(ui.Center).Padding(4, frostui.SpaceS).Radius(6).
		Cursor(ui.CursorPointer).Label(title).AccessSelected(sel).Tooltip(a.homePath(info.Cwd))
	switch {
	case sel:
		tab.Background(t.Accent.Alpha(0.16))
	case tab.Hovered():
		tab.Background(t.SurfaceHover)
	}
	if tab.Clicked() {
		a.terms.sel = info.ID
	}
	tab.Children(func() {
		frostui.Icon(c, "pocket-terminal")
		if info.Exited {
			frostui.Muted(c, truncate(title, 28)).SingleLine()
		} else {
			ui.Text(c, truncate(title, 28)).SingleLine()
		}
		if frostui.IconButton(c, "window-close", "End this terminal").Clicked() {
			a.endTerminal(info.ID)
		}
	})
}

// shellKeys are the commands whose Ctrl keys shells use (history, clear,
// tmux's prefix, readline's Alt keys): they stand aside while a terminal
// has the focus, where Ctrl stands for Cmd. On macOS they are Cmd keys,
// which never reach a shell.
var shellKeys = []string{"new", "switcher", "allow", "deny", "focus", "sidebar", "quit"}

// guardShellKeys makes shellKeys stand aside while a terminal has the
// focus.
func (a *App) guardShellKeys() {
	if runtime.GOOS == "darwin" {
		return
	}
	for _, id := range shellKeys {
		cmd, ok := a.table.Command(id)
		if !ok {
			continue
		}
		enabled := cmd.Enabled
		cmd.Enabled = func() (bool, string) {
			if a.terms.focused {
				return false, "A terminal has the keys"
			}
			if enabled != nil {
				return enabled()
			}
			return true, ""
		}
		a.table.Add(cmd)
	}
}

// terminalsIn lists the shells in a folder, for a session's details.
func (a *App) terminalsIn(cwd string) []pocket.TerminalInfo {
	var out []pocket.TerminalInfo
	for _, t := range a.terms.list {
		if t.Cwd == cwd && !t.Exited {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(x, y pocket.TerminalInfo) int { return int(x.StartedAt - y.StartedAt) })
	return out
}
