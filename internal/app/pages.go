package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// inspectorPane is the right pane: what the session runs on, what it
// costs, and who works in it.
func (a *App) inspectorPane(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).Background(frostui.Chrome(t)).Children(func() {
		ui.Column(c).Padding(frostui.SpaceL).Gap(frostui.SpaceL).AlignItems(ui.Stretch).Children(func() {
			if a.current == 0 {
				a.cheatSheet(c)
				return
			}
			a.sessionIdentity(c)
			v := a.view
			var p *pocket.Persona
			if a.details != nil && a.details.identity != nil {
				p = a.details.identity.Identity
			}
			if p != nil {
				ui.Row(c).Gap(frostui.SpaceM).AlignItems(ui.Center).Children(func() {
					a.agentAvatar(c, p.ID, 36)
					ui.Column(c).Gap(2).Children(func() {
						frostui.Strong(c, p.Name)
						frostui.Caption(c, roleTitle(p.Role))
					})
					ui.Spacer(c)
					if frostui.IconButton(c, "pocket-agent", "Open "+p.Name+"'s page").Clicked() {
						a.openAgent(p.ID)
					}
				})
			}
			frostui.SectionHeader(c, frostui.SectionHead{Title: "Session", Quiet: true})
			kv := []frostui.KeyValue{{Key: "Model", Value: firstNonEmpty(v.Agent.ModelName, modelID(v.Agent))}}
			if v.Agent.ThinkingLevel != "" && v.Agent.ThinkingLevel != "off" {
				kv = append(kv, frostui.KeyValue{Key: "Thinking", Value: v.Agent.ThinkingLevel})
			}
			kv = append(kv, frostui.KeyValue{Key: "Folder", Value: a.homePath(firstNonEmpty(v.Agent.Cwd, v.Conversation.Cwd)), Mono: true})
			if v.Branch != nil {
				kv = append(kv, frostui.KeyValue{Key: "Branch", Value: firstNonEmpty(v.Branch.Branch, "detached "+v.Branch.Detached), Mono: true})
			}
			if v.Conversation.Parent != nil {
				kv = append(kv, frostui.KeyValue{Key: "Parent", Value: v.Conversation.Parent.Title})
			}
			if len(v.Viewers) > 0 {
				kv = append(kv, frostui.KeyValue{Key: "Here now", Value: strings.Join(v.Viewers, ", ")})
			}
			frostui.KeyValues(c, kv, frostui.KeyValuesStyle{KeyWidth: 72})
			if v.Conversation.Parent != nil {
				if ui.Button(c, "Open the parent session").Clicked() {
					a.open(v.Conversation.Parent.ID)
				}
			}
			if a.terms.on {
				id := a.current
				cwd := firstNonEmpty(v.Agent.Cwd, v.Conversation.Cwd)
				ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
					if frostui.LabeledButton(c, "pocket-terminal", "Open a terminal here", false).
						Disabled(a.liveTerminals() >= 4 || a.terms.busy).Tooltip("A shell on the server in " + a.homePath(cwd)).Clicked() {
						a.newTerminal(pocket.TerminalPlace{Session: id})
					}
					for _, tm := range a.terminalsIn(cwd) {
						tid := tm.ID
						if ui.Button(c.Key(tid), "Show terminal").Clicked() {
							a.goPlace("x:terminal")
							a.terms.sel = tid
						}
					}
				})
			}

			a.shiftGoalSection(c)
			frostui.SectionHeader(c, frostui.SectionHead{Title: "Usage", Quiet: true})
			stats := [][2]string{{"Spent", fmt.Sprintf("$%.2f", v.Conversation.Spend.Spent)}}
			if v.Stats.TokensPerSec > 0 {
				stats = append(stats, [2]string{"Speed", fmt.Sprintf("%.0f tok/s", v.Stats.TokensPerSec)})
			}
			if v.Stats.CacheRate > 0 {
				stats = append(stats, [2]string{"Cache", fmt.Sprintf("%.0f%%", v.Stats.CacheRate*100)})
			}
			ui.Row(c).Gap(frostui.SpaceS).Children(func() {
				for _, st := range stats {
					ui.Column(c.Key(st[0])).Grow(1).Basis(0).Gap(2).Padding(frostui.SpaceS, frostui.SpaceM).Radius(8).
						Background(frostui.Panel(t)).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
						frostui.Caption(c, st[0])
						frostui.Strong(c, st[1]).SingleLine()
					})
				}
			})
			if win := v.Agent.ContextWindow; win > 0 && v.Stats.ContextTokens > 0 {
				used := float64(v.Stats.ContextTokens) / float64(win)
				tone := frostui.Accent
				switch {
				case used > 0.85:
					tone = frostui.Danger
				case used > 0.65:
					tone = frostui.Warning
				}
				meter(c, "Context", fmt.Sprintf("%s of %s", tokens(v.Stats.ContextTokens), tokens(win)), used, tone)
			}
			if b := v.Conversation.Spend.Budget; b != nil && *b > 0 {
				meter(c, "Budget", fmt.Sprintf("$%.2f of $%.2f", v.Conversation.Spend.Spent, *b), v.Conversation.Spend.Spent / *b, frostui.Accent)
			}

			a.browserWatchPane(c)
			if ui.Button(c, "Background tasks").Clicked() {
				a.openBackground()
			}
			a.schedulesSection(c)
			if len(v.Subagents) > 0 {
				frostui.SectionHeader(c, frostui.SectionHead{Title: "Subagents", Aside: fmt.Sprint(len(v.Subagents)), Quiet: true})
				for _, sa := range v.Subagents {
					a.subagentRow(c.Key(sa.ConversationID), sa)
				}
			}
			if len(v.Inbox) > 0 {
				frostui.SectionHeader(c, frostui.SectionHead{Title: "Queued", Aside: fmt.Sprint(len(v.Inbox)), Quiet: true})
				for _, q := range v.Inbox {
					ui.Row(c.Key(q.ID)).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
						frostui.Pill(c, q.Mode, frostui.Neutral)
						frostui.Muted(c, truncate(firstLine(firstNonEmpty(q.Label, q.Text)), 80)).SingleLine().Grow(1)
					})
				}
			}
		})
	})
}

func (a *App) subagentRow(c *ui.Context, sa pocket.Subagent) {
	t := c.Theme()
	row := ui.ButtonBase(c).Row().Gap(frostui.SpaceS).AlignItems(ui.Center).Padding(6, frostui.SpaceS).Radius(6).
		Cursor(ui.CursorPointer).Label("Open subagent " + sa.Name)
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	if row.Clicked() {
		a.open(sa.ConversationID)
	}
	row.Children(func() {
		state := frostui.StepDone
		switch {
		case sa.Busy:
			state = frostui.StepRunning
		case sa.Failed:
			state = frostui.StepFailed
		case sa.Stopped:
			state = frostui.StepSkipped
		}
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			frostui.Strong(c, sa.Name).SingleLine()
			if sa.Asked != "" {
				frostui.Caption(c, truncate(firstLine(sa.Asked), 70)).SingleLine()
			}
			if step := a.lastStep(sa.ConversationID); step != "" {
				frostui.Caption(c, step).Wrap()
			}
			if session := a.byID[sa.ConversationID]; session != nil && session.UpdatedAt > 0 {
				frostui.Caption(c, "Last activity "+ago(a.opts.Now(), session.UpdatedAt.Time()))
			}
		})
		frostui.JobStatus(c, frostui.RowStatus{State: state, Compact: true, Tooltip: sa.Error})
	})
}

// meter is a labeled bar filled to share (0 to 1).
func meter(c *ui.Context, label, detail string, share float64, tone frostui.Tone) {
	t := c.Theme()
	share = max(0, min(1, share))
	ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			frostui.Label(c, label).Grow(1)
			frostui.Caption(c, detail)
		})
		ui.Row(c).Height(6).Radius(3).Clip().Background(frostui.ColorsOf(t).LineStrong).Children(func() {
			ui.Box(c).WidthPercent(float32(share * 100)).Height(6).Radius(3).Background(frostui.ToneColor(t, tone))
		})
	})
}

// cheatSheet fills the inspector while no session is open: the keys
// that make Pocket fast.
func (a *App) cheatSheet(c *ui.Context) {
	frostui.SectionHeader(c, frostui.SectionHead{Title: "Keys", Quiet: true})
	shortcuts := [][2]string{
		{"Cmd 1 … 8", "Inbox, In motion, Team, Shift board, Numbers, Wiki, Artifacts, Terminal"},
		{"Cmd Shift T", "New terminal, in the open session's folder"},
		{"J / K", "Next / previous in the Inbox"},
		{"1 … 6", "Answer a decision with that option"},
		{"Cmd K", "Everything: commands, sessions, agents"},
		{"Cmd P", "Go to a session"},
		{"Cmd Shift A", "Next session that needs you"},
		{"Cmd N", "New session"},
		{"Cmd ,", "Settings"},
		{"Cmd Shift D", "Do not disturb"},
		{"Enter", "Send · Shift Enter for a new line"},
		{"Cmd Enter", "Steer a run that is working"},
		{"Esc", "Stop the run"},
		{"Alt Y / Alt N", "Allow / deny the waiting call"},
		{"@name", "Give a named agent a task"},
		{"/", "Commands, templates and skills"},
	}
	for _, k := range shortcuts {
		ui.Column(c.Key(k[0])).Gap(2).AlignItems(ui.Start).Children(func() {
			frostui.Shortcut(c, strings.Fields(keys(k[0]))...)
			frostui.Caption(c, k[1]).Wrap()
		})
	}
}

func roleTitle(role string) string {
	switch role {
	case "supervisor":
		return "Supervisor"
	case "clerk":
		return "Clerk"
	}
	return "Specialist"
}

func modelID(ag pocket.Agent) string {
	if ag.Model == nil {
		return "No model chosen"
	}
	return ag.Model.ModelID
}

func tokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

// homePath writes a path under the server's home with ~.
func (a *App) homePath(p string) string {
	if a.hello != nil && a.hello.Server.Home != "" {
		if rest, ok := strings.CutPrefix(p, a.hello.Server.Home); ok {
			return "~" + rest
		}
	}
	return p
}

func greeting(hour int) string {
	switch {
	case hour < 5:
		return "Up late"
	case hour < 12:
		return "Good morning"
	case hour < 18:
		return "Good afternoon"
	}
	return "Good evening"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// sessionCard is a session on an agent's page.
func (a *App) sessionCard(c *ui.Context, s *pocket.Session) {
	t := c.Theme()
	st := a.stateOf(s)
	card := ui.ButtonBase(c).Column().Gap(frostui.SpaceXS).Padding(frostui.SpaceM).Radius(10).AlignItems(ui.Stretch).
		BorderWidth(1, 1, 1, 1).Cursor(ui.CursorPointer).Label("Open " + s.DisplayTitle())
	switch {
	case st == stateWaiting:
		card.Background(frostui.ToneFill(t, frostui.Warning)).BorderColor(frostui.ToneLine(t, frostui.Warning))
	case card.Hovered():
		card.Background(t.SurfaceHover).BorderColor(t.Border)
	default:
		card.Background(frostui.Panel(t)).BorderColor(t.Border)
	}
	if card.Clicked() {
		a.open(s.ID)
	}
	card.Children(func() {
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			if p := a.agentOf(s); p != nil {
				a.agentAvatar(c, p.ID, 20)
			}
			frostui.Strong(c, s.DisplayTitle()).SingleLine().Grow(1)
			switch st {
			case stateWorking:
				ui.Spinner(c).Size(14, 14)
			case stateFailed:
				frostui.Pill(c, "failed", frostui.Danger)
			case stateLimited:
				frostui.Pill(c, "limited", frostui.Warning)
			}
		})
		if st == stateWaiting && s.Approval != nil {
			what := s.Approval.Tool + ": " + s.Approval.Subject
			if s.Approval.Form {
				what = "Asks: " + s.Approval.Subject
			}
			frostui.Body(c, truncate(firstLine(what), 160)).SingleLine()
		} else if st == stateFailed && s.Failure != "" {
			frostui.Toned(c, truncate(s.Failure, 160), frostui.Danger).SingleLine()
		}
		meta := []string{a.homePath(s.Cwd)}
		if s.Model != "" {
			meta = append(meta, s.Model)
		}
		if s.Subagents != nil && s.Subagents.Working > 0 {
			meta = append(meta, plural(s.Subagents.Working, "subagent", "subagents"))
		}
		frostui.Caption(c, strings.Join(meta, " · ")).SingleLine()
	})
}

func (a *App) agentStatus(id, role string) string {
	n, waiting := 0, 0
	for _, s := range a.sessionsOf(id) {
		switch a.stateOf(s) {
		case stateWorking:
			n++
		case stateWaiting:
			waiting++
		}
	}
	switch {
	case waiting > 0:
		return plural(waiting, "waits for you", "wait for you")
	case n > 0:
		return plural(n, "task at work", "tasks at work")
	}
	return roleTitle(role)
}

// agentPage is the middle pane for a named agent: who they are, what
// they work on, and a field to give them a task.
type agentTask struct {
	agent string
	task  string
	repo  string
}

func (a *App) agentPane(c *ui.Context, id string) {
	t := c.Theme()
	p := a.team.agent(id)
	if p == nil {
		frostui.Empty(c, "pocket-agent", "No such agent", "The team's roster no longer has "+id+".").Grow(1)
		return
	}
	if a.agentDraft.agent != id {
		a.agentDraft = agentTask{agent: id}
	}
	ui.Scroll(c).Grow(1).Background(t.Background).Children(func() {
		ui.Column(c).AlignItems(ui.Center).Padding(frostui.SpaceXL).Children(func() {
			ui.Column(c).MaxWidth(860).FillWidth().Gap(frostui.SpaceL).AlignItems(ui.Stretch).Children(func() {
				ui.Row(c).Gap(frostui.SpaceL).AlignItems(ui.Center).Children(func() {
					a.agentAvatar(c, p.ID, 64)
					ui.Column(c).Gap(frostui.SpaceXS).Grow(1).Children(func() {
						frostui.Title(c, p.Name)
						meta := []string{roleTitle(p.Role)}
						if p.Model != "" {
							meta = append(meta, p.Model)
						}
						frostui.Muted(c, strings.Join(meta, " · "))
					})
				})
				a.agentTaskCard(c, p)
				sessions := a.sessionsOf(id)
				frostui.SectionHeader(c, frostui.SectionHead{Title: "Sessions", Aside: fmt.Sprint(len(sessions))})
				if len(sessions) == 0 {
					frostui.Muted(c, p.Name+" has no open sessions.")
				}
				for i, s := range sessions {
					if i == 12 {
						frostui.Caption(c, fmt.Sprintf("and %d more", len(sessions)-12))
						break
					}
					a.sessionCard(c.Key(s.ID), s)
				}
				a.agentLessons(c, id)
				var places []frostui.KeyValue
				if p.Home != "" {
					places = append(places, frostui.KeyValue{Key: "Home", Value: a.homePath(p.Home), Mono: true})
				}
				if p.Workspace != "" {
					places = append(places, frostui.KeyValue{Key: "Workspace", Value: a.homePath(p.Workspace), Mono: true})
				}
				repos := make([]string, 0, len(p.Repositories))
				for r := range p.Repositories {
					repos = append(repos, r)
				}
				slices.Sort(repos)
				for _, r := range repos {
					places = append(places, frostui.KeyValue{Key: r, Value: a.homePath(p.Repositories[r]), Mono: true})
				}
				if len(places) > 0 {
					frostui.SectionHeader(c, frostui.SectionHead{Title: "Places"})
					frostui.KeyValues(c, places, frostui.KeyValuesStyle{KeyWidth: 180})
				}
				if strings.TrimSpace(p.Instructions) != "" {
					open := a.tr.open["soul:"+id]
					if frostui.DisclosureCard(c, &open, frostui.Disclosure{Icon: "pocket-agent", Title: "Soul and responsibilities",
						Subtitle: "The profile " + p.Name + " works from", Body: func() {
							markdown.View(c, p.Instructions, &a.md)
						}}) {
						if a.tr.open == nil {
							a.tr.open, a.tr.closed = map[string]bool{}, map[string]bool{}
						}
						a.tr.open["soul:"+id] = open
					}
				}
			})
		})
	})
}

// agentTaskCard gives a named agent a task: a linked session in one of
// their places, started from the supervisor's session when there is one.
func (a *App) agentTaskCard(c *ui.Context, p *pocket.Persona) {
	t := c.Theme()
	d := &a.agentDraft
	source := a.taskSource()
	ui.Column(c).Gap(frostui.SpaceS).Padding(frostui.SpaceM).Radius(12).AlignItems(ui.Stretch).
		Background(frostui.Panel(t)).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
		frostui.Strong(c, "Give "+p.Name+" a task")
		ui.TextArea(c, &d.task).Lines(3, 10).Placeholder("What should " + p.Name + " do? It starts as a linked session in its own worktree.").Label("Task")
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			options := []string{"Default place"}
			if p.Home != "" {
				options = append(options, "home")
			}
			repos := make([]string, 0, len(p.Repositories))
			for r := range p.Repositories {
				repos = append(repos, r)
			}
			slices.Sort(repos)
			options = append(options, repos...)
			if d.repo == "" {
				d.repo = options[0]
			}
			if len(options) > 1 {
				ui.Select(c, &d.repo, options).Label("Where")
			}
			ui.Spacer(c)
			if source == 0 {
				frostui.Caption(c, "Open a session first: the task links to it.")
			}
			ok := strings.TrimSpace(d.task) != "" && source != 0
			if ui.PrimaryButton(c, "Start").Disabled(!ok).Clicked() {
				repo := d.repo
				if repo == "Default place" {
					repo = ""
				}
				a.startAgentTask(source, p, strings.TrimSpace(d.task), repo)
				d.task = ""
			}
		})
	})
}

// taskSource is the session a new agent task links to: the supervisor's
// when the desk has one, else the last one open.
func (a *App) taskSource() int {
	if sup := a.team.supervisor; sup != nil && sup.Found {
		return sup.ConversationID
	}
	if a.current != 0 {
		return a.current
	}
	if a.lastOpen != 0 {
		return a.lastOpen
	}
	return 0
}
