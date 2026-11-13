package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// View draws the window.
func (a *App) View(c *ui.Context) {
	for _, msg := range a.pendingToasts {
		c.Toast(msg)
	}
	a.pendingToasts = nil
	if a.signedIn() {
		a.setBadge(a.inboxCount())
	}
	a.applyTheme(c)
	c.SetZoom(a.zoom())
	if !a.signedIn() {
		frostui.Adapt(c)
		a.signInView(c)
		return
	}
	conn := a.conn
	// A terminal takes keys as an editor does: the window's Ctrl K steps
	// aside there, and guardShellKeys keeps the shells' Ctrl keys for them.
	a.terms.focused = a.selected == "x:terminal" && c.FocusWithin(a.terms.field)
	a.table.Editing = func(c *ui.Context) bool {
		return c.FocusWithin(a.composer.field) || (a.selected == "x:terminal" && c.FocusWithin(a.terms.field))
	}
	a.shell.View(c, frostui.PaneFrame{
		SidebarPane: "sessions",
		Panes: []frostui.Pane{
			{ID: "sessions", Label: "Sessions", Width: 280, Min: 200, Max: 420, Collapsible: true,
				Bar: func() { a.sidebarBar(c) }, Content: func() { a.sidebarPane(c) }},
			{ID: "main", Label: "Conversation", Min: 420,
				Bar: func() { a.mainBar(c) }, Content: func() { a.mainPane(c) }},
			{ID: "inspector", Label: "Inspector", Width: 300, Min: 240, Max: 440, Collapsible: true,
				Bar: func() { a.inspectorBar(c) }, Content: func() { a.inspector(c) }},
		},
		Menu:        a.menu,
		Table:       a.table,
		Connection:  &conn,
		Status:      a.status(),
		StatusEnd:   a.statusEnd(),
		ConsoleData: a.console,
	})
	a.settingsDialog(c)
	a.deskFileDialog(c)
	a.numberRecordsDialog(c)
	a.newSessionDialog(c)
	a.backgroundDialog(c)
	a.scheduleDialog(c)
}

func (a *App) mainPane(c *ui.Context) {
	switch {
	case strings.HasPrefix(a.selected, "a:"):
		a.agentPane(c, keyAgent(a.selected))
	case a.current != 0:
		a.conversationPane(c)
	case a.selected == "x:motion":
		a.motionPane(c)
	case a.selected == "x:board":
		a.boardPane(c)
	case a.selected == "x:numbers":
		a.numbersPane(c)
	case a.selected == "x:team":
		a.teamPane(c)
	case a.selected == "x:wiki":
		a.wikiPane(c)
	case a.selected == "x:artifacts":
		a.artifactsPane(c)
	case a.selected == "x:terminal":
		a.terminalPane(c)
	default:
		a.inboxPane(c)
	}
}

// placeTitle names the place shown.
func (a *App) placeTitle() string {
	switch a.selected {
	case "x:motion":
		return "In motion"
	case "x:board":
		return "Shift board"
	case "x:numbers":
		return "Numbers"
	case "x:team":
		return "Team"
	case "x:wiki":
		return "Wiki"
	case "x:artifacts":
		return "Artifacts"
	case "x:terminal":
		return "Terminal"
	}
	return "Inbox"
}

// mainBar is the top bar over the conversation: its title and where it
// runs.
func (a *App) mainBar(c *ui.Context) {
	switch {
	case strings.HasPrefix(a.selected, "a:"):
		if p := a.team.agent(keyAgent(a.selected)); p != nil {
			ui.Text(c, p.Name).Bold().SingleLine()
		}
		return
	case a.current == 0:
		ui.Text(c, a.placeTitle()).Bold().SingleLine()
		return
	}
	a.sessionToolsMenu(c)
	v := a.view
	s := a.byID[a.current]
	title := v.Conversation.Title
	if title == "" && s != nil {
		title = s.DisplayTitle()
	}
	if v.Conversation.Kind == "subagent" && v.Conversation.SubagentName != "" {
		title = v.Conversation.SubagentName + " · " + firstNonEmpty(title, "subagent")
	}
	ui.Column(c).Grow(1).Children(func() {
		ui.Text(c, firstNonEmpty(title, "Session")).Bold().SingleLine()
		var meta []string
		if p := a.agentOf(s); p != nil {
			meta = append(meta, p.Name)
		}
		if cwd := firstNonEmpty(v.Agent.Cwd, v.Conversation.Cwd); cwd != "" {
			meta = append(meta, a.homePath(cwd))
		}
		if v.Branch != nil && v.Branch.Branch != "" {
			meta = append(meta, v.Branch.Branch)
		}
		frostui.Caption(c, strings.Join(meta, " · ")).SingleLine()
	})
	if v.Live.Busy {
		if frostui.IconButton(c, "pocket-stop", "Stop the run (Esc)").Clicked() {
			a.stop()
		}
	}
	if s != nil {
		icon := "pocket-pin"
		label := "Pin"
		if a.pinned(s.ID) {
			label = "Unpin"
		}
		if frostui.IconButton(c, icon, label).Clicked() {
			a.togglePin(s.ID)
		}
	}
}

// inspectorBar is the top bar over the right pane, which the window's own
// controls (the palette field, Activity, the menu, and on some desktops the
// window's buttons) cover: it holds nothing of its own.
func (a *App) inspectorBar(c *ui.Context) {
	ui.Spacer(c)
}

// inspector is the right pane: what the chosen thing is about.
func (a *App) inspector(c *ui.Context) {
	ui.Column(c).Grow(1).AlignItems(ui.Stretch).Background(frostui.Chrome(c.Theme())).Children(func() {
		frostui.Strong(c, "Details").Wrap().Padding(frostui.SpaceM, frostui.SpaceL).Shrink(0)
		frostui.RowDivider(c)
		a.inspectorBody(c)
	})
}

func (a *App) inspectorBody(c *ui.Context) {
	if a.current != 0 || strings.HasPrefix(a.selected, "a:") {
		a.inspectorPane(c)
		return
	}
	t := c.Theme()
	ui.Scroll(c).Grow(1).Background(frostui.Chrome(t)).Children(func() {
		ui.Column(c).Padding(frostui.SpaceL).Gap(frostui.SpaceM).AlignItems(ui.Stretch).Children(func() {
			switch a.selected {
			case "x:motion":
				if th := a.desk.thread(a.threadSel); th != nil {
					a.threadContext(c, th)
					return
				}
				if a.numberThreadContext(c) {
					return
				}
				frostui.Muted(c, "Choose a thread to see its PRs, events and reports. Double-click opens its session.").Wrap()
			case "x:wiki":
				a.wikiContext(c)
			case "x:board", "x:numbers", "x:team", "x:artifacts", "x:terminal":
				a.cheatSheet(c)
			default:
				a.inboxContext(c)
			}
		})
	})
}

// modelChip shows the session's model, and changes it.
func (a *App) modelChip(c *ui.Context) {
	if a.hello == nil {
		return
	}
	v := a.view
	label := firstNonEmpty(v.Agent.ModelName, modelID(v.Agent))
	if v.Agent.ThinkingLevel != "" && v.Agent.ThinkingLevel != "off" {
		label += " · " + v.Agent.ThinkingLevel
	}
	id := a.current
	ui.MenuButton(c, label, func(m *ui.Menu) {
		cur := ""
		if v.Agent.Model != nil {
			cur = v.Agent.Model.Provider + "/" + v.Agent.Model.ModelID
		}
		byProvider := map[string][]pocket.Model{}
		var providers []string
		for _, md := range a.hello.Models {
			if _, ok := byProvider[md.Provider]; !ok {
				providers = append(providers, md.Provider)
			}
			byProvider[md.Provider] = append(byProvider[md.Provider], md)
		}
		for _, prov := range providers {
			models := byProvider[prov]
			m.Submenu(prov, func(sm *ui.Menu) {
				for _, md := range models {
					ref := pocket.ModelRef{Provider: md.Provider, ModelID: md.ID}
					if sm.Item(firstNonEmpty(md.Name, md.ID)).Checked(cur == md.Provider+"/"+md.ID).Chosen() {
						a.console.Add(frostui.LevelCommand, "send", "Model "+md.ID)
						a.request("Changing the model", func(ctx context.Context, cl *pocket.Client) error {
							return cl.Configure(ctx, id, &ref, "", "")
						}, nil)
					}
				}
			})
		}
		if len(v.Agent.Levels) > 1 {
			m.Separator()
			m.Submenu("Thinking", func(sm *ui.Menu) {
				for _, lvl := range v.Agent.Levels {
					if sm.Item(lvl).Checked(lvl == v.Agent.ThinkingLevel).Chosen() {
						a.request("Changing thinking", func(ctx context.Context, cl *pocket.Client) error {
							return cl.Configure(ctx, id, nil, lvl, "")
						}, nil)
					}
				}
			})
		}
	}).Label("Model").Tooltip("Change the model")
}

// status is the status bar's start: what the team is doing.
func (a *App) status() []frostui.StatusItem {
	d := a.derivedData()
	waiting, working := d.waiting, d.working
	var items []frostui.StatusItem
	if q, ok := a.quietItem(); ok {
		items = append(items, q)
	}
	if waiting > 0 {
		items = append(items, frostui.StatusItem{Icon: "pocket-waiting", Text: plural(waiting, "needs you", "need you"),
			Tone: frostui.Warning, Progress: -1, OnClick: func() { a.goPlace("x:inbox") }, Tooltip: "Open the Inbox (" + keys("Cmd 1") + ")"})
	}
	if working > 0 {
		items = append(items, frostui.StatusItem{Icon: "pocket-sparkle", Text: plural(working, "at work", "at work"), Progress: -1,
			OnClick: func() { a.goPlace("x:motion") }})
	}
	return items
}

func (a *App) statusEnd() []frostui.StatusItem {
	var items []frostui.StatusItem
	if a.current != 0 && a.view.Conversation.ID == a.current {
		items = append(items, frostui.StatusItem{Icon: "pocket-spend", Text: fmt.Sprintf("$%.2f", a.view.Conversation.Spend.Spent),
			Progress: -1, Tooltip: "What this session has spent"})
	}
	if a.hello != nil {
		items = append(items, frostui.StatusItem{Text: "Pi Pocket " + a.hello.Server.PocketVersion, Progress: -1})
	}
	return items
}

// nextWaiting opens the next session that waits for the person.
func (a *App) nextWaiting() {
	var waiting []int
	for i := range a.sessions {
		if a.stateOf(&a.sessions[i]) == stateWaiting {
			waiting = append(waiting, a.sessions[i].ID)
		}
	}
	if len(waiting) == 0 {
		a.toast("Nothing waits for you")
		return
	}
	for i, id := range waiting {
		if id == a.current {
			a.open(waiting[(i+1)%len(waiting)])
			return
		}
	}
	a.open(waiting[0])
}

func (a *App) menu(m *ui.Menu) {
	a.table.Menu("new", "inbox", "motion", "team", "board", "numbers", "wiki", "artifacts", "terminal", "new-terminal", "next-waiting", "", "sidebar", "inspector", "")(m)
	a.table.MenuItem(m, "settings")
	a.table.MenuItem(m, "dnd")
	a.table.MenuItem(m, "shortcuts")
	a.table.MenuItem(m, "about")
	m.Separator()
	a.table.MenuItem(m, "sign-out")
	if a.opts.Quit != nil {
		a.table.MenuItem(m, "quit")
	}
}

func (a *App) setUpCommands() {
	inSession := func() (bool, string) { return a.current != 0, "No session is open" }
	busy := func() (bool, string) { return a.current != 0 && a.view.Live.Busy, "Pi is not working here" }
	waiting := func() (bool, string) {
		for _, ap := range a.view.Approvals {
			if ap.Form == nil {
				return true, ""
			}
		}
		return false, "Nothing waits for an answer here"
	}
	inInbox := func() (bool, string) {
		return a.current == 0 && (a.selected == "" || a.selected == "x:inbox"), "The Inbox is not shown"
	}
	var answers []frostui.Command
	for n := 1; n <= 6; n++ {
		answers = append(answers, frostui.Command{ID: fmt.Sprintf("answer-%d", n), Title: fmt.Sprintf("Answer with Option %d", n),
			Group: "Inbox", Unlisted: true, Enabled: inInbox,
			Bindings: []frostui.KeyBinding{{Key: ui.Key1 + ui.Key(n-1), Scope: frostui.OutsideText}},
			Run:      func() { a.answerByNumber(n) }})
	}
	a.table = frostui.NewCommandTable(
		frostui.Command{ID: "new", Title: "New Session", Group: "Sessions", Icon: "pocket-new",
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd, Key: ui.KeyN, Scope: frostui.Everywhere}},
			Run: func() {
				cwd := ""
				if s := a.byID[a.current]; s != nil {
					cwd = s.Cwd
				}
				a.newSession(cwd)
			}},
		frostui.Command{ID: "inbox", Title: "Inbox", Group: "Go to", Icon: "pocket-waiting",
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd, Key: ui.Key1, Scope: frostui.Everywhere}},
			Run:      func() { a.goPlace("x:inbox") }},
		frostui.Command{ID: "motion", Title: "In Motion", Group: "Go to", Icon: "pocket-sparkle",
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd, Key: ui.Key2, Scope: frostui.Everywhere}},
			Run:      func() { a.goPlace("x:motion") }},
		frostui.Command{ID: "board", Title: "Shift Board", Group: "Go to", Icon: "pocket-night",
			Enabled:  func() (bool, string) { return a.desk.on, "This server has no Desk" },
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd, Key: ui.Key4, Scope: frostui.Everywhere}},
			Run:      func() { a.goPlace("x:board") }},
		frostui.Command{ID: "numbers", Title: "Numbers", Group: "Go to", Icon: "view-grid",
			Enabled:  func() (bool, string) { return a.desk.on, "This server has no Desk" },
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd, Key: ui.Key5, Scope: frostui.Everywhere}},
			Run:      func() { a.goPlace("x:numbers") }},
		frostui.Command{ID: "team", Title: "Team", Group: "Go to", Icon: "pocket-team",
			Enabled:  func() (bool, string) { return a.team.on, "This server has no team" },
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd, Key: ui.Key3, Scope: frostui.Everywhere}},
			Run:      func() { a.goPlace("x:team") }},
		frostui.Command{ID: "wiki", Title: "Wiki", Group: "Go to", Icon: "pocket-wiki",
			Enabled:  func() (bool, string) { return a.wiki.on, "This server has no wiki" },
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd, Key: ui.Key6, Scope: frostui.Everywhere}},
			Run:      func() { a.goPlace("x:wiki") }},
		frostui.Command{ID: "artifacts", Title: "Artifacts", Group: "Go to", Icon: "pocket-artifact",
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd, Key: ui.Key7, Scope: frostui.Everywhere}},
			Run:      func() { a.goPlace("x:artifacts") }},
		frostui.Command{ID: "terminal", Title: "Terminal", Group: "Go to", Icon: "pocket-terminal",
			Enabled:  func() (bool, string) { return a.terms.on, "This server has no terminal module" },
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd, Key: ui.Key8, Scope: frostui.Everywhere}},
			Run:      func() { a.goPlace("x:terminal") }},
		frostui.Command{ID: "new-terminal", Title: "New Terminal", Group: "Terminal", Icon: "pocket-terminal",
			Enabled: func() (bool, string) {
				switch {
				case !a.terms.on:
					return false, "This server has no terminal module"
				case a.liveTerminals() >= 4:
					return false, "The server runs 4 terminals at most"
				}
				return true, ""
			},
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd | ui.Shift, Key: ui.KeyT, Scope: frostui.Everywhere}},
			Run: func() {
				place := pocket.TerminalPlace{}
				if a.current != 0 {
					place.Session = a.current
				}
				a.newTerminal(place)
			}},
		frostui.Command{ID: "inbox-next", Title: "Next in the Inbox", Group: "Inbox", Unlisted: true,
			Enabled:  inInbox,
			Bindings: []frostui.KeyBinding{{Key: ui.KeyJ, Scope: frostui.OutsideText}},
			Run:      func() { a.moveInbox(1) }},
		frostui.Command{ID: "inbox-prev", Title: "Previous in the Inbox", Group: "Inbox", Unlisted: true,
			Enabled:  inInbox,
			Bindings: []frostui.KeyBinding{{Key: ui.KeyK, Scope: frostui.OutsideText}},
			Run:      func() { a.moveInbox(-1) }},
		frostui.Command{ID: "next-waiting", Title: "Next Session That Needs You", Group: "Go to", Icon: "pocket-waiting",
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd | ui.Shift, Key: ui.KeyA, Scope: frostui.Everywhere}},
			Run:      a.nextWaiting},
		frostui.Command{ID: "switcher", Title: "Go to Session…", Group: "Go to", Icon: "system-search",
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd, Key: ui.KeyP, Scope: frostui.Everywhere}},
			Run:      func() { a.shell.Palette.ShowSource("sessions") }},
		frostui.Command{ID: "stop", Title: "Stop the Run", Group: "Session", Icon: "process-stop", Enabled: busy,
			Bindings: []frostui.KeyBinding{{Key: ui.KeyEscape, Scope: frostui.OutsideText}},
			Run:      a.stop},
		frostui.Command{ID: "allow", Title: "Allow the Waiting Call", Group: "Session", Icon: "pocket-approval", Enabled: waiting,
			Bindings: []frostui.KeyBinding{{Mods: ui.Alt, Key: ui.KeyY, Scope: frostui.Everywhere}},
			Run:      func() { a.answerFirst(true) }},
		frostui.Command{ID: "deny", Title: "Deny the Waiting Call", Group: "Session", Icon: "dialog-error", Enabled: waiting,
			Bindings: []frostui.KeyBinding{{Mods: ui.Alt, Key: ui.KeyN, Scope: frostui.Everywhere}},
			Run:      func() { a.answerFirst(false) }},
		frostui.Command{ID: "focus", Title: "Write a Message", Group: "Session", Icon: "square-pen", Enabled: inSession,
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd, Key: ui.KeyL, Scope: frostui.Everywhere}},
			Run:      func() { a.focusComposer = true }},
		frostui.Command{ID: "pin", Title: "Pin the Session", Group: "Session", Icon: "pocket-pin", Enabled: inSession,
			Checked: func() bool { return a.pinned(a.current) },
			Run:     func() { a.togglePin(a.current) }},
		frostui.Command{ID: "archive", Title: "Archive the Session", Group: "Session", Icon: "archive", Enabled: inSession,
			Run: func() { a.archive(a.current, true) }},
		frostui.Command{ID: "find-session", Title: "Find in Session", Group: "Session", Enabled: inSession, Bindings: []frostui.KeyBinding{{Mods: ui.Cmd, Key: ui.KeyF, Scope: frostui.Everywhere}}, Run: func() { a.tr.find.open = true; a.tr.find.focus = true }},
		frostui.Command{ID: "fork-session", Title: "Fork Session", Group: "Session", Enabled: inSession, Run: func() { a.sessionOperation("Fork session") }},
		frostui.Command{ID: "export-session", Title: "Export Session…", Group: "Session", Enabled: inSession, Run: func() { a.sessionOperation("Export session") }},
		frostui.Command{ID: "new-context", Title: "New Context…", Group: "Session", Enabled: inSession, Run: a.confirmNewContext},
		frostui.Command{ID: "compact", Title: "Compact the Context", Group: "Session", Icon: "edit-clear-all", Enabled: inSession,
			Run: func() { a.runBuiltin("/compact", "") }},
		frostui.Command{ID: "plan", Title: "Plan Mode", Group: "Session", Icon: "view-list", Enabled: inSession,
			Checked: func() bool { return a.view.Plan.On },
			Run:     func() { a.runBuiltin("/plan", "") }},
		frostui.Command{ID: "sidebar", Title: "Show Sessions", Group: "View", Icon: "sidebar-show",
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd, Key: ui.KeyB, Scope: frostui.Everywhere}},
			Checked:  func() bool { return a.shell.Panes.Shown("sessions") },
			Run:      func() { a.shell.Panes.Toggle("sessions") }},
		frostui.Command{ID: "inspector", Title: "Show Details", Group: "View", Icon: "sidebar-show-right",
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd | ui.Alt, Key: ui.KeyB, Scope: frostui.Everywhere}},
			Checked:  func() bool { return a.shell.Panes.Shown("inspector") },
			Run:      func() { a.shell.Panes.Toggle("inspector") }},
		frostui.Command{ID: "notify", Title: "Desktop Notifications", Group: "Notifications", Icon: "preferences-system-notifications",
			Checked: func() bool { return a.settings.NotifyOn() },
			Run: func() {
				a.settings.Notes().Enabled = !a.settings.NotifyOn()
				a.saveSettings()
			}},
		frostui.Command{ID: "dnd", Title: "Do Not Disturb", Group: "Notifications", Icon: "pocket-night",
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd | ui.Shift, Key: ui.KeyD, Scope: frostui.Everywhere}},
			Checked:  func() bool { q, _ := a.quietNow(); return q },
			Run: func() {
				q, _ := a.quietNow()
				a.setQuiet(!q, 0)
			}},
		frostui.Command{ID: "snooze", Title: "Snooze Notifications for an Hour", Group: "Notifications", Icon: "alarm-clock",
			Run: func() { a.setQuiet(true, time.Hour) }},
		frostui.Command{ID: "settings", Title: "Settings…", Group: "App", Icon: "preferences-system",
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd, Key: ui.KeyComma, Scope: frostui.Everywhere}},
			Run:      func() { a.openSettings("") }},
		frostui.Command{ID: "shortcuts", Title: "Keyboard Shortcuts", Group: "App", Icon: "keyboard",
			Bindings: []frostui.KeyBinding{{Key: ui.KeyF1, Scope: frostui.Everywhere}},
			Run: func() {
				a.shell.Info.Show("Keyboard shortcuts", frostui.ShortcutsBody(append(a.table.Shortcuts(),
					frostui.ShortcutEntry{Group: "Composer", Title: "Send", Keys: []string{"Enter"}},
					frostui.ShortcutEntry{Group: "Composer", Title: "New line", Keys: []string{"Shift", "Enter"}},
					frostui.ShortcutEntry{Group: "Composer", Title: "Steer the run now", Keys: []string{keyName("Ctrl"), "Enter"}},
					frostui.ShortcutEntry{Group: "Composer", Title: "Last message again", Keys: []string{"Up"}},
					frostui.ShortcutEntry{Group: "Composer", Title: "Complete / or @", Keys: []string{"Tab"}},
				)))
			}},
		frostui.Command{ID: "about", Title: "About Pocket", Group: "App", Icon: "help-about",
			Run: func() {
				a.shell.Info.Show("About Pocket", frostui.AboutBody(frostui.About{
					Name: "Pocket", Version: a.opts.Version, Summary: "A desktop home for your Pi Pocket agents.",
					Website: "https://github.com/frostyard/pocket", License: "MIT",
				}))
			}},
		frostui.Command{ID: "sign-out", Title: "Sign Out…", Group: "App", Icon: "log-out",
			Run: func() {
				a.dialogs.Ask(frostui.Confirm{Title: "Sign out of this server?", Body: "Pocket forgets this device's sign-in. You need the sign-in link again to come back.",
					Action: "Sign Out", Destructive: true, OnConfirm: func() { a.signedOut("") }})
			}},
		frostui.Command{ID: "quit", Title: "Quit", Group: "App", Icon: "application-exit",
			Bindings: []frostui.KeyBinding{{Mods: ui.Cmd, Key: ui.KeyQ, Scope: frostui.Everywhere}},
			Run: func() {
				if a.opts.Quit != nil {
					a.opts.Quit()
				}
			}},
	)
	a.table.Add(answers...)
	a.table.Add(a.zoomCommands()...)
}

// sessionSource finds sessions by title or folder for the palette.
func (a *App) sessionSource() frostui.PaletteSource {
	type found struct {
		id          int
		title, path string
		icon        string
	}
	return frostui.PaletteSource{ID: "sessions", Title: "Sessions", Placeholder: "Go to a session", Limit: 6, Query: func(q *frostui.PaletteQuery) {
		all := make([]found, 0, len(a.sessions))
		for i := range a.sessions {
			s := &a.sessions[i]
			icon := "pocket-session"
			switch a.stateOf(s) {
			case stateWaiting:
				icon = "pocket-waiting"
			case stateWorking:
				icon = "pocket-sparkle"
			}
			path := a.homePath(s.Cwd)
			if p := a.agentOf(s); p != nil {
				path = p.Name + " · " + path
			}
			if s.Archived {
				path += " · archived"
			}
			all = append(all, found{s.ID, s.DisplayTitle(), path, icon})
		}
		go func() {
			hits := frostui.Rank(all, q.Text, 60, func(f found) string { return f.title + " " + f.path })
			items := make([]frostui.PaletteItem, len(hits))
			for i, h := range hits {
				items[i] = frostui.PaletteItem{ID: fmt.Sprint(h.id), Title: h.title, Detail: h.path, Icon: h.icon,
					Run: func() { a.open(h.id) }}
			}
			a.post(func() { q.Reply(items) })
		}()
	}}
}

// agentSource finds named agents for the palette.
func (a *App) agentSource() frostui.PaletteSource {
	return frostui.PaletteSource{ID: "agents", Title: "Team", Placeholder: "Go to an agent", Limit: 4, Query: func(q *frostui.PaletteQuery) {
		agents := append([]pocket.Persona(nil), a.team.agents...)
		hits := frostui.Rank(agents, q.Text, 20, func(p pocket.Persona) string { return p.Name + " " + p.ID })
		items := make([]frostui.PaletteItem, len(hits))
		for i, p := range hits {
			items[i] = frostui.PaletteItem{ID: p.ID, Title: p.Name, Detail: roleTitle(p.Role) + " · " + a.agentStatus(p.ID, p.Role),
				Icon: "pocket-agent", Run: func() { a.openAgent(p.ID) }}
		}
		q.Reply(items)
	}}
}
