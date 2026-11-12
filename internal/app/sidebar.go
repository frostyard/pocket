package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// Sidebar items are strings: "s:<id>" a session, "a:<agent>" an agent.
func sessionKey(id int) string     { return "s:" + strconv.Itoa(id) }
func agentKey(id string) string    { return "a:" + id }
func keySession(key string) int    { id, _ := strconv.Atoi(strings.TrimPrefix(key, "s:")); return id }
func keyAgent(key string) string   { return strings.TrimPrefix(key, "a:") }
func isSessionKey(key string) bool { return strings.HasPrefix(key, "s:") }

// team is what the pi-pocket-team module says, when the server has it.
type team struct {
	on         bool
	agents     []pocket.Persona
	supervisor *pocket.Supervisor
	loading    bool
}

func (t *team) agent(id string) *pocket.Persona {
	for i := range t.agents {
		if t.agents[i].ID == id {
			return &t.agents[i]
		}
	}
	return nil
}

// loadTeam reads the team's agents and supervisor, when the server has
// the team module.
func (a *App) loadTeam() {
	if a.team.loading {
		return
	}
	a.team.loading = true
	var agents []pocket.Persona
	var on bool
	var sup *pocket.Supervisor
	a.request("Reading the team", func(ctx context.Context, c *pocket.Client) error {
		var err error
		agents, on, err = c.TeamAgents(ctx)
		if err != nil || !on {
			return err
		}
		sup, err = c.TeamSupervisor(ctx)
		if err != nil {
			sup = nil // the desk may be off; the agents still show
		}
		return nil
	}, func(err error) {
		a.team.loading = false
		if err != nil {
			return
		}
		a.team.on, a.team.agents, a.team.supervisor = on, agents, sup
		if on {
			a.console.Addf(frostui.LevelInfo, "team", "%d named agents", len(agents))
			a.loadMemory()
		}
	})
}

// agentOf tells which named agent a session is, from its folder: a
// session in an agent's home is that agent, and a delegation's worktree is
// named <checkout>--<agent>-<slug>. It returns nil for none.
func (a *App) agentOf(s *pocket.Session) *pocket.Persona {
	if !a.team.on || s == nil {
		return nil
	}
	cwd := s.Cwd
	var best *pocket.Persona
	bestLen := 0
	for i := range a.team.agents {
		p := &a.team.agents[i]
		if p.Home != "" && (cwd == p.Home || strings.HasPrefix(cwd, strings.TrimRight(p.Home, "/")+"/")) && len(p.Home) > bestLen {
			best, bestLen = p, len(p.Home)
		}
	}
	if best != nil {
		return best
	}
	base := filepath.Base(cwd)
	if _, rest, ok := strings.Cut(base, "--"); ok {
		for i := range a.team.agents {
			if strings.HasPrefix(rest, a.team.agents[i].ID+"-") || rest == a.team.agents[i].ID {
				return &a.team.agents[i]
			}
		}
	}
	if a.team.supervisor != nil && a.team.supervisor.Found && a.team.supervisor.ConversationID == s.ID {
		for i := range a.team.agents {
			if a.team.agents[i].Role == "supervisor" {
				return &a.team.agents[i]
			}
		}
	}
	return nil
}

// sessionsOf lists an agent's sessions, newest first, archived left out.
func (a *App) sessionsOf(agent string) []*pocket.Session {
	var out []*pocket.Session
	for i := range a.sessions {
		s := &a.sessions[i]
		if s.Archived {
			continue
		}
		if p := a.agentOf(s); p != nil && p.ID == agent {
			out = append(out, s)
		}
	}
	return out
}

func (a *App) setUpSidebar() {
	l := &a.sidebar
	l.Label = "Sessions"
	l.Children = func(item string) []string {
		var list []*pocket.Session
		switch {
		case strings.HasPrefix(item, "a:"):
			list = a.sessionsOf(keyAgent(item))
		case isSessionKey(item):
			list = a.children(keySession(item))
		default:
			return nil
		}
		var ids []string
		for _, s := range list {
			ids = append(ids, sessionKey(s.ID))
			if len(ids) == 30 {
				break
			}
		}
		return ids
	}
	l.Row = func(item string) frostui.SourceRow {
		if strings.HasPrefix(item, "x:") {
			return a.placeRow(item)
		}
		if strings.HasPrefix(item, "a:") {
			return a.agentRow(keyAgent(item))
		}
		return a.sessionRow(a.byID[keySession(item)])
	}
	l.OnSelect = func(item string) {
		if strings.HasPrefix(item, "x:") {
			a.goPlace(item)
			return
		}
		if isSessionKey(item) {
			a.open(keySession(item))
			return
		}
		a.openAgent(keyAgent(item))
	}
	l.Menu = func(m *ui.Menu, item, section string) {
		if !isSessionKey(item) {
			return
		}
		id := keySession(item)
		s := a.byID[id]
		if s == nil {
			return
		}
		if m.Item("Open").Chosen() {
			a.open(id)
		}
		if m.Item("Rename").Shortcut(0, ui.KeyF2).Chosen() {
			l.StartRename(section, item)
		}
		if m.Item("Pinned").Checked(a.pinned(id)).Chosen() {
			a.togglePin(id)
		}
		if m.Item("Mark as read").Chosen() {
			a.markSeen(id)
		}
		m.Separator()
		if m.Item("Archive").Chosen() {
			a.archive(id, true)
		}
	}
	l.CanRename = func(item string) bool { return isSessionKey(item) }
	l.Rename = func(item, name string) { a.rename(keySession(item), name) }
}

func (a *App) pinned(id int) bool { return slices.Contains(a.settings.Pinned, strconv.Itoa(id)) }

func (a *App) togglePin(id int) {
	key := strconv.Itoa(id)
	if i := slices.Index(a.settings.Pinned, key); i >= 0 {
		a.settings.Pinned = slices.Delete(a.settings.Pinned, i, i+1)
	} else {
		a.settings.Pinned = append(a.settings.Pinned, key)
	}
	a.saveSettings()
}

func (a *App) rename(id int, name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if s := a.byID[id]; s != nil {
		s.Title = name // shows at once; the next list confirms it
	}
	a.request("Renaming the session", func(ctx context.Context, c *pocket.Client) error {
		return c.UpdateSession(ctx, id, &name, nil)
	}, nil)
}

func (a *App) archive(id int, archived bool) {
	if s := a.byID[id]; s != nil {
		s.Archived = archived
	}
	if archived && id == a.current {
		a.open(0)
	}
	a.request("Archiving the session", func(ctx context.Context, c *pocket.Client) error {
		return c.UpdateSession(ctx, id, nil, &archived)
	}, nil)
}

// places are the window's places, in the sidebar's first section.
func (a *App) places() []string {
	out := []string{"x:inbox", "x:motion"}
	if a.team.on {
		out = append(out, "x:team")
	}
	if a.desk.on {
		out = append(out, "x:board", "x:numbers")
	}
	if a.wiki.on {
		out = append(out, "x:wiki")
	}
	return append(out, "x:artifacts")
}

// children lists the sessions linked from session id (delegations a
// supervisor started), newest first.
func (a *App) children(id int) []*pocket.Session {
	var out []*pocket.Session
	for i := range a.sessions {
		s := &a.sessions[i]
		if s.LinkedFrom != nil && s.LinkedFrom.ID == id && !s.Archived {
			out = append(out, s)
		}
	}
	return out
}

// sections are the sidebar: places, the team, pinned sessions, and the
// recent sessions that no other session started (those it started nest
// under it). A filter searches every session instead.
func (a *App) sections() []frostui.SourceSection[string] {
	q := strings.ToLower(strings.TrimSpace(a.filter))
	if q != "" {
		var hits []string
		for i := range a.sessions {
			s := &a.sessions[i]
			if strings.Contains(strings.ToLower(s.DisplayTitle()+" "+s.Cwd), q) {
				hits = append(hits, sessionKey(s.ID))
			}
			if len(hits) == 100 {
				break
			}
		}
		return []frostui.SourceSection[string]{{ID: "found", Title: "Sessions", Items: hits}}
	}
	out := []frostui.SourceSection[string]{{ID: "places", Title: "Pocket", Items: a.places()}}
	if a.team.on && len(a.team.agents) > 0 {
		agents := make([]string, len(a.team.agents))
		for i, p := range a.team.agents {
			agents[i] = agentKey(p.ID)
		}
		out = append(out, frostui.SourceSection[string]{ID: "agents", Title: "Team", Items: agents})
	}
	var pinned []string
	for _, id := range a.settings.Pinned {
		var n int
		if _, err := fmt.Sscan(id, &n); err == nil {
			if s := a.byID[n]; s != nil && !s.Archived {
				pinned = append(pinned, sessionKey(n))
			}
		}
	}
	if len(pinned) > 0 {
		out = append(out, frostui.SourceSection[string]{ID: "pinned", Title: "Pinned", Items: pinned})
	}
	var recent []string
	for i := range a.sessions {
		s := &a.sessions[i]
		if s.Archived || a.pinned(s.ID) || (s.LinkedFrom != nil && a.byID[s.LinkedFrom.ID] != nil) {
			continue
		}
		recent = append(recent, sessionKey(s.ID))
		if len(recent) == 40 {
			break
		}
	}
	out = append(out, frostui.SourceSection[string]{ID: "recent", Title: "Recent", Items: recent})
	return out
}

// sectionOf names a sidebar section an item shows in.
func (a *App) sectionOf(key string) string {
	for _, sec := range a.sections() {
		if slices.Contains(sec.Items, key) {
			return sec.ID
		}
	}
	return "recent"
}

// placeRow is one of the window's places.
func (a *App) placeRow(key string) frostui.SourceRow {
	switch key {
	case "x:inbox":
		row := frostui.SourceRow{Title: "Inbox", Icon: "pocket-waiting"}
		if n := a.inboxCount(); n > 0 {
			row.Count, row.CountTone = fmt.Sprint(n), frostui.Warning
			row.Spoken = plural(n, "thing waits for you", "things wait for you")
		}
		return row
	case "x:motion":
		row := frostui.SourceRow{Title: "In motion", Icon: "pocket-sparkle"}
		n := 0
		for i := range a.sessions {
			if a.stateOf(&a.sessions[i]) == stateWorking {
				n++
			}
		}
		if n > 0 {
			row.Busy = true
			row.Count = fmt.Sprint(n)
			row.Spoken = plural(n, "run at work", "runs at work")
		}
		return row
	case "x:board":
		row := frostui.SourceRow{Title: "Shift board", Icon: "pocket-night"}
		if b := a.desk.board; b != nil {
			picked := 0
			for _, r := range b.Rows {
				if r.State == "picked" {
					picked++
				}
			}
			if picked > 0 {
				row.Count = fmt.Sprint(picked)
				row.Spoken = plural(picked, "row picked", "rows picked")
			}
		}
		return row
	case "x:numbers":
		return frostui.SourceRow{Title: "Numbers", Icon: "view-grid"}
	case "x:team":
		row := frostui.SourceRow{Title: "Team", Icon: "pocket-team"}
		if n := len(a.waitingLessons()); n > 0 {
			row.Count, row.CountTone = fmt.Sprint(n), frostui.Warning
			row.Spoken = plural(n, "lesson to review", "lessons to review")
		}
		return row
	case "x:wiki":
		return frostui.SourceRow{Title: "Wiki", Icon: "pocket-wiki"}
	case "x:artifacts":
		return frostui.SourceRow{Title: "Artifacts", Icon: "pocket-artifact"}
	}
	return frostui.SourceRow{Title: key}
}

func (a *App) sessionRow(s *pocket.Session) frostui.SourceRow {
	if s == nil {
		return frostui.SourceRow{Title: "…", Dim: true}
	}
	row := frostui.SourceRow{Title: s.DisplayTitle(), Icon: "pocket-session"}
	if p := a.agentOf(s); p != nil {
		if !strings.HasPrefix(s.Title, p.Name) && !strings.HasPrefix(s.Title, strings.Fields(p.Name)[0]) {
			row.Tag = p.Name
		}
	} else if s.Cwd != "" && s.LinkedFrom == nil {
		row.Tag = filepath.Base(s.Cwd)
	}
	switch a.stateOf(s) {
	case stateWaiting:
		row.Icon = "pocket-waiting"
		n := 1
		if s.Approval != nil && s.Approval.Count > 0 {
			n = s.Approval.Count
		}
		row.Count, row.CountTone = strconv.Itoa(n), frostui.Warning
		if s.Approval != nil {
			row.Spoken = "waits for you: " + s.Approval.Tool + " " + s.Approval.Subject
		}
	case stateLimited:
		row.Icon = "alarm-clock"
		row.Alert = "Waiting for a usage limit until " + s.Limited.Time().Local().Format("15:04")
	case stateWorking:
		row.Busy = true
		if s.Subagents != nil && s.Subagents.Working > 0 {
			row.Count = strconv.Itoa(s.Subagents.Working)
			row.Spoken = fmt.Sprintf("working, %d subagents", s.Subagents.Working)
		}
	case stateFailed:
		row.Alert = "Failed"
		if s.Failure != "" {
			row.Alert = s.Failure
		}
	case stateFresh:
		row.Count, row.CountTone = "new", frostui.Success
		row.Spoken = "finished, not read"
	default:
		row.Dim = a.serverNow().Sub(s.UpdatedAt.Time()) > 7*24*time.Hour
	}
	return row
}

func (a *App) agentRow(id string) frostui.SourceRow {
	p := a.team.agent(id)
	if p == nil {
		return frostui.SourceRow{Title: id, Icon: "pocket-agent", Dim: true}
	}
	row := frostui.SourceRow{Title: p.Name, Icon: "pocket-agent", Tooltip: a.agentHint(id)}
	if p.Role == "supervisor" || p.Role == "clerk" {
		row.Tag = p.Role
	}
	working, waiting := 0, 0
	for _, s := range a.sessionsOf(id) {
		switch a.stateOf(s) {
		case stateWorking:
			working++
		case stateWaiting:
			waiting++
		}
	}
	switch {
	case waiting > 0:
		row.Count, row.CountTone = strconv.Itoa(waiting), frostui.Warning
		row.Spoken = fmt.Sprintf("%d waiting for you", waiting)
	case working > 0:
		row.Busy = true
	}
	if p.Role == "supervisor" && a.team.supervisor != nil && a.team.supervisor.Asks > 0 && waiting == 0 {
		row.Count, row.CountTone = strconv.Itoa(a.team.supervisor.Asks), frostui.Neutral
		row.Spoken = fmt.Sprintf("%d open asks", a.team.supervisor.Asks)
	}
	return row
}

// sidebarPane is the left pane: a filter and the list.
func (a *App) sidebarPane(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).AlignItems(ui.Stretch).Background(frostui.Chrome(t)).Children(func() {
		ui.Row(c).Padding(frostui.SpaceS, frostui.SpaceS, 0, frostui.SpaceS).Children(func() {
			ui.SearchField(c, &a.filter).Placeholder("Filter sessions").Label("Filter sessions").Grow(1)
		})
		a.sidebar.View(c, a.sections()).Grow(1).Padding(frostui.SpaceS)
	})
}

// sidebarBar is the top bar's segment over the sidebar.
func (a *App) sidebarBar(c *ui.Context) {
	ui.Spacer(c)
	if frostui.IconButton(c, "pocket-new", "New session ("+keys("Cmd N")+")").Clicked() {
		a.newSession("")
	}
}
