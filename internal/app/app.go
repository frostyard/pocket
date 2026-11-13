// Package app is Pocket's window: its state, which belongs to the UI
// thread, and the views that draw it with frostui and MyGo.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui/markdown"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/config"
	"github.com/frostyard/pocket/internal/pocket"
)

// Notifier shows desktop notifications.
type Notifier interface {
	Notify(title, body string, onClick func())
}

// Options are what the program gives the app.
type Options struct {
	Version string
	// Post runs a function on the UI thread; safe from any goroutine.
	Post   func(func())
	Store  *config.Store
	Creds  config.Credentials
	Notify Notifier
	Quit   func()
	Raise  func()
	Badge  func(int)
	// OpenURL opens a web address; nil opens it with the desktop's handler.
	OpenURL func(string) error
	// Web shows artifacts' pages inside the window; nil opens them in the
	// browser.
	Web WebPane
	// After runs a function on the UI thread after a while; nil uses
	// time.AfterFunc and Post. Tests give a clock of their own.
	After func(time.Duration, func())
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// App is the window's state.
type App struct {
	opts     Options
	post     func(func())
	settings config.Settings
	derived  *derived

	shell   frostui.PaneShell
	table   *frostui.CommandTable
	dialogs frostui.Dialogs
	console *frostui.Console
	conn    frostui.Connection

	signIn signIn
	link   link

	focused bool
	started bool

	// What the server said.
	hello    *pocket.Hello
	sessions []pocket.Session
	byID     map[int]*pocket.Session
	// skew is the server's clock minus ours.
	skew time.Duration

	// The session shown (0 for none) and its view.
	current int
	view    *pocket.View
	// loading is the session asked for whose view has not come yet.
	loading int

	details *sessionDetails
	team    team
	desk    desk
	core    *coreMemory
	lessons lessons
	wiki    wiki
	arts    artifacts
	terms   terms
	// peeks are live summaries of the sessions that wait or work.
	peeks   map[int]*pocket.Peek
	peekIDs []int
	peekSeq int
	// asksSeen are the Desk asks already known, so only new ones notify.
	asksSeen map[string]bool

	inboxSel      string
	deskFile      *deskFile
	threadSel     string
	closedOpen    bool
	boardDoneOpen bool

	// selected is what the sidebar shows chosen: "s:<id>", "a:<agent>",
	// or "" for the overview.
	selected string
	sidebar  frostui.SourceList[string]
	filter   string

	tr       transcript
	composer composer
	md       markdown.Options

	notified      map[string]time.Time
	notifiedTimer bool
	notes         notes
	prefs         prefs
	badge         int

	pendingToasts []string
	agentDraft    agentTask
	lastOpen      int
	focusComposer bool
}

// New returns the app's state, with the settings read.
func New(opts Options) (*App, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	st, err := opts.Store.Load()
	if err != nil {
		return nil, err
	}
	con := frostui.NewConsole(4000)
	con.SetNotify(frostui.NewWaker(opts.Post, 80*time.Millisecond).Wake)
	a := &App{
		opts: opts, post: opts.Post, settings: st, console: con,
		byID: map[int]*pocket.Session{}, view: pocket.NewView(), peeks: map[int]*pocket.Peek{},
		notified: map[string]time.Time{}, focused: true,
	}
	a.shell.Dialogs = &a.dialogs
	a.shell.Console.Title = "Activity"
	a.shell.Console.Subtitle = "what Pocket and the server did"
	a.shell.Console.EmptyNote = "The connection and every command Pocket sends show here."
	a.shell.Panes.Layout = frostui.PaneLayout{Widths: st.Widths, Collapsed: st.Collapsed}
	a.md = markdown.Options{Reveal: &markdown.Reveal{Unit: markdown.ByWord, CatchUp: 300 * time.Millisecond}, OnLink: a.followLink}
	a.setUpSidebar()
	a.setUpCommands()
	a.guardShellKeys()
	a.shell.Palette.Sources = []frostui.PaletteSource{a.sessionSource(), a.agentSource()}
	return a, nil
}

// Start connects with the kept sign-in, or shows the sign-in screen.
func (a *App) Start() {
	a.post(func() {
		a.started = true
		server := a.settings.Server
		if server == "" {
			return
		}
		token, err := a.opts.Creds.Token(server)
		if err != nil {
			a.signIn.err = "Reading the keyring: " + err.Error()
			return
		}
		if token == "" {
			return
		}
		a.connect(server, token)
	})
}

// Close stops the link and keeps the layout.
func (a *App) Close() {
	a.closeTerminals()
	a.disconnect()
	a.link.client = nil
	a.settings.Widths = a.shell.Panes.Layout.Widths
	a.settings.Collapsed = a.shell.Panes.Layout.Collapsed
	a.settings.LastSession = ""
	if a.current != 0 {
		a.settings.LastSession = fmt.Sprint(a.current)
	}
	a.saveSettings()
}

// SetFocused tells the app whether its window has the focus: the server
// then lets notifications through for the session it shows.
func (a *App) SetFocused(focused bool) {
	if a.focused == focused {
		return
	}
	a.focused = focused
	if focused && a.current != 0 {
		a.markSeen(a.current)
	}
	if a.link.client == nil {
		return
	}
	tab := a.link.tab
	a.request("Telling the server the window "+map[bool]string{true: "shows", false: "is hidden"}[focused],
		func(ctx context.Context, c *pocket.Client) error { return c.Visibility(ctx, tab, focused) }, func(error) {})
}

func (a *App) saveSettings() {
	if err := a.opts.Store.Save(a.settings); err != nil {
		a.console.Add(frostui.LevelWarn, "app", "Saving settings: "+err.Error())
	}
}

// reset forgets what the server said, as after signing out.
func (a *App) reset() {
	a.invalidateDerived()
	a.composer.resetRequests()
	a.tr = transcript{}
	a.hello = nil
	a.notified = map[string]time.Time{}
	a.sessions = nil
	a.byID = map[int]*pocket.Session{}
	a.view = pocket.NewView()
	a.current, a.loading = 0, 0
	a.team = team{}
	a.details = nil
	a.desk = desk{}
	a.deskFile = nil
	a.closeTerminals()
	a.core = nil
	a.lessons, a.wiki, a.arts, a.terms = lessons{}, wiki{}, artifacts{}, terms{}
	a.peeks, a.peekIDs, a.asksSeen = map[int]*pocket.Peek{}, nil, nil
	a.selected = ""
	a.setBadge(0)
}

// signedIn reports whether the app has a server to show.
func (a *App) signedIn() bool { return a.link.client != nil }

// serverNow is the server's clock.
func (a *App) serverNow() time.Time { return a.opts.Now().Add(a.skew) }

// handle applies one event of the stream.
func (a *App) handle(ev pocket.Event) {
	a.invalidateDerived()
	defer a.invalidateDerived()
	if a.link.switching != 0 {
		switch ev.Name {
		case "view", "chat", "notes", "presence", "browser", "missing":
			return // about the session left
		}
	}
	switch ev.Name {
	case "hello":
		var h pocket.Hello
		if !a.decode(ev, &h) {
			return
		}
		a.hello = &h
		a.link.streamID = h.Connection
		a.link.failures = 0
		if h.Server.Now != 0 {
			a.skew = time.UnixMilli(int64(h.Server.Now)).Sub(a.opts.Now())
		}
		first := a.conn.State == frostui.Connecting
		a.conn.State, a.conn.Reason = frostui.Connected, ""
		a.console.Addf(frostui.LevelInfo, "link", "Connected as %s to Pi Pocket %s", h.User.Name, h.Server.PocketVersion)
		if !a.focused {
			tab := a.link.tab
			a.request("Telling the server the window is hidden",
				func(ctx context.Context, c *pocket.Client) error { return c.Visibility(ctx, tab, false) }, func(error) {})
		}
		a.peekIDs = nil // a new connection watches nothing yet
		a.loadDesk()
		a.loadCoreMemory()
		a.loadWiki()
		a.loadTerminals()
		a.loadSessionIdentity()
		a.loadShiftGoal()
		a.loadTeam()
		if a.arts.loaded || a.arts.loading {
			a.loadArtifacts()
		}
		if first {
			if a.current == 0 && a.settings.LastSession != "" {
				var id int
				if _, err := fmt.Sscan(a.settings.LastSession, &id); err == nil && id != 0 {
					a.loading = id
				}
			}
		}
	case "pip":
		var pip *struct {
			ID int `json:"id"`
		}
		if a.decode(ev, &pip) {
			if pip == nil {
				a.core = nil
			} else if a.core == nil || a.core.pip != pip.ID {
				a.core = &coreMemory{pip: pip.ID}
				a.loadCoreMemory()
			}
		}
	case "memory":
		a.loadCoreMemory()
	case "sessions":
		var list []pocket.Session
		if !a.decode(ev, &list) {
			return
		}
		a.setSessions(list)
	case "switched":
		var s struct {
			Seq int `json:"seq"`
		}
		if a.decode(ev, &s) && s.Seq == a.link.switchSeq {
			a.link.switching = 0
		}
	case "view":
		var u pocket.ViewUpdate
		if !a.decode(ev, &u) {
			return
		}
		if u.Conversation.ID != a.current {
			return
		}
		if err := a.view.Apply(&u); err != nil {
			a.console.Add(frostui.LevelWarn, "link", "The session's view fell out of step; opening it again")
			a.reconnectNow()
			return
		}
		if a.loading == a.current {
			a.loading = 0
		}
		a.tr.viewChanged(a)
	case "peek":
		var p pocket.Peek
		if a.decode(ev, &p) && p.ConversationID != 0 {
			a.peeks[p.ConversationID] = &p
		}
	case "missing":
		var m struct {
			ConversationID int    `json:"conversationId"`
			Message        string `json:"message"`
		}
		if a.decode(ev, &m) && m.ConversationID == a.current {
			a.console.Add(frostui.LevelWarn, "link", "Session gone: "+m.Message)
			a.open(0)
		}
	case "embedder-notify", "desktop-notify":
		var n pocket.Notification
		if a.decode(ev, &n) {
			a.notify(n)
		}
	case "notice":
		var n struct {
			Level   string `json:"level"`
			Message string `json:"message"`
		}
		if a.decode(ev, &n) {
			level := frostui.LevelInfo
			switch n.Level {
			case "warning":
				level = frostui.LevelWarn
			case "error":
				level = frostui.LevelError
			}
			a.console.Add(level, "server", n.Message)
			a.toast(n.Message)
		}
	case "models":
		var models []pocket.Model
		if a.decode(ev, &models) && a.hello != nil {
			a.hello.Models = models
		}
	case "users":
		var users []pocket.User
		if a.decode(ev, &users) && a.hello != nil {
			a.hello.Users = users
		}
	case "ext":
		var x struct {
			Module string          `json:"module"`
			Event  string          `json:"event"`
			Data   json.RawMessage `json:"data"`
		}
		if !a.decode(ev, &x) {
			return
		}
		switch x.Module {
		case "team":
			switch x.Event {
			case "identity":
				var changed struct {
					ConversationID int `json:"conversationId"`
				}
				if json.Unmarshal(x.Data, &changed) == nil && changed.ConversationID == a.current {
					a.loadSessionIdentity()
				}
			case "memory":
				a.loadMemory()
			default:
				a.loadTeam()
			}
		case "homewiki":
			a.loadWiki()
		case "terminal":
			if x.Event == "terminals" {
				a.terminalsChanged(x.Data)
			}
		case "desk":
			a.deskChanged()
		case "night-shift":
			var changed struct {
				Conversation int `json:"conversation"`
			}
			if x.Event == "changed" && json.Unmarshal(x.Data, &changed) == nil && changed.Conversation == a.current {
				a.loadShiftGoal()
			}
			if a.desk.board != nil {
				a.loadBoard()
			}
		}
	case "closing":
		a.console.Add(frostui.LevelWarn, "link", "The server is stopping")
	}
}

func (a *App) decode(ev pocket.Event, v any) bool {
	if err := pocket.Decode(ev.Data, v); err != nil {
		var drift *json.UnmarshalTypeError
		if errors.As(err, &drift) {
			// One field in an unexpected shape: use the rest.
			a.console.Addf(frostui.LevelWarn, "link", "The server's %s event has a field Pocket does not read: %v", ev.Name, err)
			return true
		}
		a.console.Addf(frostui.LevelDebug, "link", "Reading %s: %v", ev.Name, err)
		return false
	}
	return true
}

// setSessions takes a new session list.
func (a *App) setSessions(list []pocket.Session) {
	a.invalidateDerived()
	a.sessions = list
	a.byID = make(map[int]*pocket.Session, len(list))
	for i := range list {
		a.byID[list[i].ID] = &a.sessions[i]
	}
	if a.loading != 0 && a.current == 0 {
		if _, ok := a.byID[a.loading]; ok {
			a.open(a.loading)
		} else {
			a.loading = 0
		}
	}
	if a.current != 0 && a.focused {
		a.markSeen(a.current)
	}
	a.watchPeeks()
}

func (a *App) setBadge(n int) {
	if n == a.badge {
		return
	}
	a.badge = n
	if a.opts.Badge != nil {
		a.opts.Badge(n)
	}
}

// open shows session id (0 for the overview).
func (a *App) open(id int) {
	if id == a.current && id != 0 {
		a.selected = sessionKey(id)
		return
	}
	a.current = id
	a.details = nil
	a.view = pocket.NewView()
	a.tr = transcript{}
	a.composer.reset(id)
	if id == 0 {
		a.selected = ""
		a.loading = 0
	} else {
		a.selected = sessionKey(id)
		a.loading = id
		a.markSeen(id)
	}
	a.sidebar.Select(a.sectionOf(a.selected), a.selected)
	a.switchTo(id)
	a.loadSessionIdentity()
	a.loadShiftGoal()
	a.settings.LastSession = ""
	if id != 0 {
		a.settings.LastSession = fmt.Sprint(id)
	}
}

// goPlace shows one of the window's places: x:inbox, x:motion, x:board,
// x:numbers.
func (a *App) goPlace(key string) {
	if a.current != 0 {
		a.open(0)
	}
	a.selected = key
	a.sidebar.Select("places", key)
	switch key {
	case "x:motion":
		a.desk.seenPending, a.desk.seenReady = true, false
		if a.desk.on {
			a.loadDesk()
		}
	case "x:board":
		if a.desk.on {
			a.loadBoard()
		}
	case "x:team":
		a.loadMemory()
	case "x:wiki":
		a.loadWiki()
	case "x:artifacts":
		a.loadArtifacts()
	case "x:terminal":
		a.loadTerminals()
	}
}

// openAgent shows an agent's page.
func (a *App) openAgent(id string) {
	a.open(0)
	a.selected = agentKey(id)
	a.sidebar.Select("agents", a.selected)
}

// markSeen notes that the person saw session id as it is now.
func (a *App) markSeen(id int) {
	a.invalidateDerived()
	if a.settings.Seen == nil {
		a.settings.Seen = map[string]int64{}
	}
	a.settings.Seen[fmt.Sprint(id)] = a.serverNow().UnixMilli()
}

// state is what a session's row says, as the web app's stateMark.
type state int

const (
	stateSeen state = iota
	stateFresh
	stateFailed
	stateWorking
	stateLimited
	stateWaiting
)

func (a *App) computeState(s *pocket.Session) state {
	// The open session's view comes before the list (which the server
	// sends at most every 400 ms): trust it for that one.
	if s.ID == a.current && a.view.Conversation.ID == s.ID && a.loading != s.ID {
		live := *s
		live.Busy = a.view.Live.Busy
		live.Waiting = len(a.view.Approvals) > 0 || (s.Approval != nil && s.Approval.ConversationID != s.ID)
		s = &live
	}
	switch {
	case s.Waiting:
		return stateWaiting
	case s.Limited != 0:
		return stateLimited
	case s.Working():
		return stateWorking
	}
	seen := pocket.Millis(a.settings.Seen[fmt.Sprint(s.ID)])
	unseen := s.EndedAt != 0 && s.EndedAt > seen && a.serverNow().Sub(s.EndedAt.Time()) < 30*24*time.Hour
	if s.Failed && (unseen || seen == 0) {
		return stateFailed
	}
	if unseen && s.ID != a.current {
		return stateFresh
	}
	return stateSeen
}

// sessionFromURL reads the session of a notification's address (/s/12).
func sessionFromURL(u string) int {
	rest, ok := strings.CutPrefix(u, "/s/")
	if !ok {
		return 0
	}
	var id int
	if _, err := fmt.Sscan(rest, &id); err != nil {
		return 0
	}
	return id
}

// request runs fn against the server on a goroutine and hands its error
// to done on the UI thread (done may be nil). A failure is logged to the
// console under what, and shown as a toast when done is nil.
func (a *App) request(what string, fn func(ctx context.Context, c *pocket.Client) error, done func(error)) {
	a.requestWithin(30*time.Second, what, fn, done)
}

// requestWithin is request with a time limit of its own, for routes that
// do slow work before they answer.
func (a *App) requestWithin(limit time.Duration, what string, fn func(ctx context.Context, c *pocket.Client) error, done func(error)) {
	client := a.link.client
	if client == nil {
		if done != nil {
			a.invalidateDerived()
			defer a.invalidateDerived()
			done(pocket.ErrSignedOut)
		}
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		defer cancel()
		err := fn(ctx, client)
		a.post(func() {
			if a.link.client != client {
				return // an answer from a sign-in the window has left
			}
			if pocket.IsSignedOut(err) {
				a.signedOut("The server no longer accepts this device's sign-in. Paste a new sign-in link.")
				return
			}
			if err != nil {
				a.console.Add(frostui.LevelError, "api", what+": "+err.Error())
				if done == nil {
					a.toast(what + ": " + err.Error())
				}
			}
			if done != nil {
				a.invalidateDerived()
				defer a.invalidateDerived()
				done(err)
			}
		})
	}()
}

// toast shows a short message at the top of the window on the next frame.
func (a *App) toast(msg string) { a.pendingToasts = append(a.pendingToasts, msg) }
