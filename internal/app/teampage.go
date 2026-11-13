package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// lessons is the team's agent memory as Pocket holds it.
type lessons struct {
	memory  *pocket.Memory
	loading bool
	dirty   bool
	// err is a failed read of the lessons: it keeps a redraw from reading
	// again, until Try again, a refresh or an event clears it.
	err string
	// editing holds the text of lessons being edited, by lesson ID.
	editing map[string]string
	// adding holds the text of a new lesson, by agent.
	adding map[string]string
	busy   map[string]bool
}

// loadMemory reads every agent's lessons.
func (a *App) loadMemory() {
	if a.lessons.loading {
		a.lessons.dirty = true
		return
	}
	a.lessons.loading, a.lessons.err = true, ""
	var m *pocket.Memory
	a.request("Reading the agents' lessons", func(ctx context.Context, c *pocket.Client) error {
		var err error
		m, err = c.TeamMemory(ctx)
		return err
	}, func(err error) {
		a.lessons.loading = false
		if err != nil {
			a.lessons.err = err.Error()
		} else {
			a.lessons.memory = m
		}
		if a.lessons.dirty {
			a.lessons.dirty = false
			a.loadMemory()
		}
	})
}

// waitingLessons lists the lessons that wait for the owner, by agent in
// roster order.
func (a *App) waitingLessons() []agentLesson {
	m := a.lessons.memory
	if m == nil {
		return nil
	}
	var out []agentLesson
	seen := map[string]bool{}
	add := func(agent string) {
		seen[agent] = true
		for i := range m.Agents[agent] {
			if l := &m.Agents[agent][i]; l.Waiting() {
				out = append(out, agentLesson{agent: agent, lesson: l})
			}
		}
	}
	for _, p := range a.team.agents {
		add(p.ID)
	}
	rest := make([]string, 0)
	for agent := range m.Agents {
		if !seen[agent] {
			rest = append(rest, agent)
		}
	}
	slices.Sort(rest)
	for _, agent := range rest {
		add(agent)
	}
	return out
}

type agentLesson struct {
	agent  string
	lesson *pocket.Lesson
}

// lessonAct approves, discards, edits or forgets a lesson.
func (a *App) lessonAct(agent string, l *pocket.Lesson, action, text string) {
	if a.lessons.busy == nil {
		a.lessons.busy = map[string]bool{}
	}
	a.lessons.busy[l.ID] = true
	lesson := *l
	a.console.Add(frostui.LevelCommand, "team", fmt.Sprintf("%s %s's lesson: %s", action, a.agentName(agent), truncate(l.Text, 80)))
	a.request("Changing "+a.agentName(agent)+"'s lesson", func(ctx context.Context, c *pocket.Client) error {
		return c.LessonAct(ctx, agent, &lesson, action, text)
	}, func(err error) {
		delete(a.lessons.busy, lesson.ID)
		if err == nil {
			delete(a.lessons.editing, lesson.ID)
			a.loadMemory()
		}
	})
}

// lessonAdd gives an agent a lesson of the owner's.
func (a *App) lessonAdd(agent, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	a.request("Teaching "+a.agentName(agent), func(ctx context.Context, c *pocket.Client) error {
		return c.LessonAdd(ctx, agent, text)
	}, func(err error) {
		if err == nil {
			delete(a.lessons.adding, agent)
			a.loadMemory()
		}
	})
}

// agentHint is what hovering an agent says: who it is, and where it works.
func (a *App) agentHint(id string) string {
	p := a.team.agent(id)
	if p == nil {
		return a.agentName(id)
	}
	lines := []string{p.Name + " · " + roleTitle(p.Role)}
	if p.Model != "" {
		lines = append(lines, p.Model)
	}
	if p.Home != "" {
		lines = append(lines, "Home: "+a.homePath(p.Home))
	}
	if p.Workspace != "" {
		lines = append(lines, "Workspace: "+a.homePath(p.Workspace))
	}
	if len(p.Repositories) > 0 {
		repos := make([]string, 0, len(p.Repositories))
		for r := range p.Repositories {
			repos = append(repos, r)
		}
		slices.Sort(repos)
		lines = append(lines, "Repositories:")
		for _, r := range repos {
			lines = append(lines, "  "+r)
		}
	}
	return strings.Join(lines, "\n")
}

// agentAvatar is an agent's avatar, whose hover says who it is and where
// it works.
func (a *App) agentAvatar(c *ui.Context, id string, size float32) ui.Element {
	return frostui.SquareAvatar(c, a.agentName(id), size).Tooltip(a.agentHint(id))
}

// teamPane is the Team place: what the agents want to remember, waiting
// for the owner, and the roster.
func (a *App) teamPane(c *ui.Context) {
	t := c.Theme()
	if a.lessons.memory == nil && !a.lessons.loading && a.lessons.err == "" {
		a.loadMemory()
	}
	waiting := a.waitingLessons()
	ui.Scroll(c).Grow(1).Background(t.Background).Children(func() {
		ui.Column(c).AlignItems(ui.Center).Padding(frostui.SpaceXL).Children(func() {
			ui.Column(c).MaxWidth(980).FillWidth().Gap(frostui.SpaceL).AlignItems(ui.Stretch).Children(func() {
				line := plural(len(a.team.agents), "agent", "agents")
				if len(waiting) > 0 {
					line += " · " + plural(len(waiting), "lesson to review", "lessons to review")
				}
				frostui.PageHeader(c, "Your team", line, func() {
					if a.lessons.loading {
						ui.Spinner(c).Size(16, 16)
					}
				})
				if a.lessons.err != "" {
					frostui.Problem(c, "Reading the agents' lessons: "+a.lessons.err).Wrap()
					if ui.Button(c, "Try again").Clicked() {
						a.loadMemory()
					}
				}
				if len(waiting) > 0 {
					frostui.SectionHeader(c, frostui.SectionHead{Icon: "pocket-thinking", Title: "Lessons to review", Aside: fmt.Sprint(len(waiting)),
						Note: "What agents want to remember. Approved lessons go into their prompt in every session."})
					for _, al := range waiting {
						a.lessonCard(c.Key("w"+al.lesson.ID), al.agent, al.lesson, true)
					}
				}
				frostui.SectionHeader(c, frostui.SectionHead{Icon: "pocket-agent", Title: "Roster", Aside: fmt.Sprint(len(a.team.agents))})
				ui.Grid(c).Columns(3).Gap(frostui.SpaceM).Children(func() {
					for _, p := range a.team.agents {
						a.rosterCard(c.Key(p.ID), p)
					}
				})
			})
		})
	})
}

// rosterCard is an agent on the Team page.
func (a *App) rosterCard(c *ui.Context, p pocket.Persona) {
	t := c.Theme()
	card := ui.ButtonBase(c).Column().Gap(frostui.SpaceS).Padding(frostui.SpaceM).Radius(10).AlignItems(ui.Stretch).
		BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Cursor(ui.CursorPointer).Label("Open " + p.Name).Tooltip(a.agentHint(p.ID))
	if card.Hovered() {
		card.Background(t.SurfaceHover)
	} else {
		card.Background(frostui.Panel(t))
	}
	if card.Clicked() {
		a.openAgent(p.ID)
	}
	card.Children(func() {
		ui.Row(c).Gap(frostui.SpaceM).AlignItems(ui.Center).Children(func() {
			frostui.SquareAvatar(c, p.Name, 36)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				frostui.Strong(c, p.Name).SingleLine()
				frostui.Caption(c, a.agentStatus(p.ID, p.Role)).SingleLine()
			})
			if a.agentBusy(p.ID) {
				ui.Spinner(c).Size(14, 14)
			}
		})
		var facts []string
		if n := len(p.Repositories); n > 0 {
			facts = append(facts, plural(n, "repository", "repositories"))
		}
		n, waiting := a.lessonCounts(p.ID)
		if n > 0 {
			facts = append(facts, plural(n, "lesson", "lessons"))
		}
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			frostui.Caption(c, strings.Join(facts, " · ")).SingleLine().Grow(1)
			if waiting > 0 {
				frostui.Pill(c, fmt.Sprintf("%d to review", waiting), frostui.Warning)
			}
		})
	})
}

// agentBusy reports whether one of an agent's sessions is at work.
func (a *App) agentBusy(id string) bool {
	for _, s := range a.sessionsOf(id) {
		if a.stateOf(s) == stateWorking {
			return true
		}
	}
	return false
}

// lessonCounts are an agent's approved lessons and those waiting.
func (a *App) lessonCounts(agent string) (approved, waiting int) {
	if a.lessons.memory == nil {
		return 0, 0
	}
	for i := range a.lessons.memory.Agents[agent] {
		if a.lessons.memory.Agents[agent][i].Waiting() {
			waiting++
		} else {
			approved++
		}
	}
	return approved, waiting
}

// lessonCard is one lesson: one waiting shows what the owner approves;
// one approved can be edited or forgotten.
func (a *App) lessonCard(c *ui.Context, agent string, l *pocket.Lesson, showAgent bool) {
	t := c.Theme()
	if a.lessons.editing == nil {
		a.lessons.editing = map[string]string{}
	}
	busy := a.lessons.busy[l.ID]
	waiting := l.Waiting()
	tone := frostui.Neutral
	if waiting {
		tone = frostui.Warning
	}
	line := t.Border
	if tone != frostui.Neutral {
		line = frostui.ToneLine(t, tone)
	}
	ui.Column(c).Gap(frostui.SpaceS).Padding(frostui.SpaceM).Radius(10).AlignItems(ui.Stretch).
		Background(frostui.Panel(t)).BorderWidth(1, 1, 1, 1).BorderColor(line).Children(func() {
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			if showAgent {
				a.agentAvatar(c, agent, 22)
				frostui.Strong(c, a.agentName(agent))
			}
			switch {
			case l.Pending:
				frostui.Pill(c, "new", frostui.Warning)
			case l.Proposal != nil && l.Proposal.Forget:
				frostui.Pill(c, "forget?", frostui.Warning)
			case l.Proposal != nil:
				frostui.Pill(c, "change", frostui.Warning)
			}
			by := "you"
			if l.ByPi {
				by = "Pi"
			} else if !l.ByYou {
				by = "someone"
			}
			frostui.Caption(c, "by "+by)
			ui.Spacer(c)
			frostui.Caption(c, clock(l.CreatedAt.Time()))
		})
		edit, editing := a.lessons.editing[l.ID]
		switch {
		case editing:
			ui.TextArea(c, &edit).Lines(2, 6).Label("Lesson").Disabled(busy)
			a.lessons.editing[l.ID] = edit
		case l.Proposal != nil && l.Proposal.Text != nil:
			frostui.Muted(c, l.Text).Wrap().Strikethrough()
			frostui.Body(c, *l.Proposal.Text).Wrap()
		default:
			frostui.Body(c, l.Text).Wrap()
		}
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			if l.From != 0 {
				if ui.Button(c, "Where it came from").Clicked() {
					a.open(l.From)
				}
			}
			ui.Spacer(c)
			if busy {
				ui.Spinner(c).Size(14, 14)
			}
			switch {
			case editing:
				if ui.Button(c, "Cancel").Disabled(busy).Clicked() {
					delete(a.lessons.editing, l.ID)
				}
				if ui.PrimaryButton(c, "Save").Disabled(busy || strings.TrimSpace(edit) == "").Clicked() {
					a.lessonAct(agent, l, "edit", strings.TrimSpace(edit))
				}
			case waiting:
				if ui.Button(c, "Discard").Disabled(busy).Clicked() {
					a.lessonAct(agent, l, "discard", "")
				}
				if l.Pending && ui.Button(c, "Edit").Disabled(busy).Clicked() {
					a.lessons.editing[l.ID] = l.Text
				}
				if ui.PrimaryButton(c, "Approve").Disabled(busy).Clicked() {
					a.lessonAct(agent, l, "approve", "")
				}
			default:
				if ui.Button(c, "Edit").Disabled(busy).Clicked() {
					a.lessons.editing[l.ID] = l.Text
				}
				if ui.Button(c, "Forget").Disabled(busy).Clicked() {
					a.lessonAct(agent, l, "forget", "")
				}
			}
		})
	})
}

// agentLessons is the Lessons section of an agent's page.
func (a *App) agentLessons(c *ui.Context, id string) {
	if a.lessons.memory == nil && !a.lessons.loading && a.lessons.err == "" {
		a.loadMemory()
	}
	// A failed refresh keeps the lessons read before it, under the error.
	if a.lessons.err != "" {
		frostui.Problem(c, "Reading the agents' lessons: "+a.lessons.err).Wrap()
		if ui.Button(c, "Try again").Clicked() {
			a.loadMemory()
		}
	}
	if a.lessons.memory == nil {
		return
	}
	list := a.lessons.memory.Agents[id]
	max := a.lessons.memory.Limits.Approved
	approved, _ := a.lessonCounts(id)
	aside := fmt.Sprint(approved)
	if max > 0 {
		aside = fmt.Sprintf("%d of %d", approved, max)
	}
	frostui.SectionHeader(c, frostui.SectionHead{Title: "Lessons", Aside: aside,
		Note: "What " + a.agentName(id) + " remembers in every session."})
	for i := range list {
		a.lessonCard(c.Key(list[i].ID), id, &list[i], false)
	}
	if a.lessons.adding == nil {
		a.lessons.adding = map[string]string{}
	}
	text := a.lessons.adding[id]
	ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
		in := ui.TextInput(c, &text).Placeholder("Teach " + a.agentName(id) + " something to remember").Label("New lesson").Grow(1)
		a.lessons.adding[id] = text
		if (ui.Button(c, "Add").Disabled(strings.TrimSpace(text) == "").Clicked() || in.Submitted()) && strings.TrimSpace(text) != "" {
			a.lessonAdd(id, text)
		}
	})
}
