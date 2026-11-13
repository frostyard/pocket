package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// In motion is every piece of work under way: the Desk's threads by who
// moves them next, and runs at work that no thread holds, each with what
// it does right now.

// motionGroups orders the threads' groups as the page shows them.
var motionGroups = []struct{ id, title, icon string }{
	{"need", "Waiting on you", "alarm-clock"},
	{"found", "Found work", "pocket-desk"},
	{"stale", "Gone quiet", "pocket-pin"},
	{"working", "Agents at work", "pocket-agent"},
	{"odrade", "With the supervisor", "pocket-desk"},
	{"review", "In review", "pocket-eye"},
	{"plans", "Plans", "view-list"},
	{"watching", "Watching", "alarm-clock"},
	{"parked", "Parked", "pocket-pin"},
}

func motionGroup(t *pocket.DeskThread) string {
	if dueWatch(t) {
		return "need"
	}
	g := t.Group()
	if g == "working" && t.Next == "odrade" {
		return "odrade"
	}
	return g
}

func (a *App) motionPane(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).Background(t.Background).Children(func() {
		ui.Column(c).AlignItems(ui.Center).Padding(frostui.SpaceXL).Children(func() {
			ui.Column(c).MaxWidth(980).FillWidth().Gap(frostui.SpaceM).AlignItems(ui.Stretch).Children(func() {
				v := a.desk.view
				line := ""
				if v != nil {
					line = fmt.Sprintf("%d open · %d with agents · %d in review · %d watching · %d of %d count",
						v.Counts.Open, v.Counts.Agent, v.Counts.Reviewer, v.Counts.Watching, v.Cap.Counted, v.Cap.Cap)
				}
				frostui.PageHeader(c, "In motion", line, func() {
					if a.desk.loading {
						ui.Spinner(c).Size(16, 16)
					}
					if frostui.IconButton(c, "view-refresh", "Read the Desk again").Clicked() {
						a.loadDesk()
					}
				})
				switch {
				case a.desk.err != "" && v != nil:
					// Keep what the Desk said last; say quietly that it is old.
					frostui.Caption(c, fmt.Sprintf("Showing the Desk as of %s: the last read failed (%s). Pocket reads it again on its next change.",
						clock(a.desk.at), a.desk.err)).Wrap()
				case a.desk.err != "":
					frostui.Problem(c, "Reading the Desk: "+a.desk.err)
				case v == nil && a.desk.loading:
					frostui.Loading(c, "Reading the Desk… it may check GitHub first, which takes a moment.")
				}
				if v != nil && v.GitHub != nil && !v.GitHub.OK && v.GitHub.Error != "" {
					frostui.Banner(c, frostui.Warning, "GitHub could not be read", v.GitHub.Error, "", false)
				}
				claimed := map[int]bool{}
				if v != nil {
					for _, g := range motionGroups {
						var threads []*pocket.DeskThread
						for i := range v.Threads {
							th := &v.Threads[i]
							if motionGroup(th) == g.id {
								threads = append(threads, th)
								if s := a.sessionForThread(th); s != nil {
									claimed[s.ID] = true
								}
							}
						}
						if len(threads) == 0 {
							continue
						}
						slices.SortFunc(threads, func(x, y *pocket.DeskThread) int { return int(y.LastEventAt - x.LastEventAt) })
						frostui.SectionHeader(c.Key(g.id), frostui.SectionHead{Icon: g.icon, Title: g.title, Aside: fmt.Sprint(len(threads))})
						ui.Column(c.Key(g.id+"-list")).AlignItems(ui.Stretch).Radius(12).Clip().BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
							for i, th := range threads {
								if i > 0 {
									frostui.RowDivider(c.Key(fmt.Sprint("d", th.ID)))
								}
								a.threadRow(c.Key(th.ID), th)
							}
						})
					}
				}
				// Runs at work that no thread holds.
				var runs []*pocket.Session
				for i := range a.sessions {
					s := &a.sessions[i]
					if a.stateOf(s) == stateWorking && !claimed[s.ID] {
						runs = append(runs, s)
					}
				}
				if len(runs) > 0 {
					frostui.SectionHeader(c.Key("runs"), frostui.SectionHead{Icon: "pocket-sparkle", Title: "Runs at work", Aside: fmt.Sprint(len(runs))})
					ui.Column(c.Key("runs-list")).AlignItems(ui.Stretch).Radius(12).Clip().BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
						for i, s := range runs {
							if i > 0 {
								frostui.RowDivider(c.Key(fmt.Sprint("d", s.ID)))
							}
							a.runRow(c.Key(s.ID), s)
						}
					})
				}
				if v == nil && len(runs) == 0 && !a.desk.loading {
					frostui.Empty(c, "pocket-sparkle", "Nothing in motion", "No run is at work. Start a session, or give an agent a task.")
				}
				if v != nil {
					a.deskExtras(c, v)
					a.closedThreads(c, v)
				}
			})
		})
	})
}

// threadRow is a thread in a dense list: who, what, what it does now.
func (a *App) threadRow(c *ui.Context, th *pocket.DeskThread) {
	t := c.Theme()
	sel := a.threadSel == th.ID
	row := ui.ButtonBase(c).Row().Gap(frostui.SpaceM).AlignItems(ui.Center).Padding(frostui.SpaceS, frostui.SpaceL).
		Cursor(ui.CursorPointer).Label(th.ID + " " + th.Title).AccessSelected(sel)
	switch {
	case sel:
		row.Background(t.Accent.Alpha(0.14))
	case row.Hovered():
		row.Background(t.SurfaceHover)
	default:
		row.Background(frostui.Panel(t))
	}
	if row.Clicked() {
		a.threadSel = th.ID
	}
	s := a.sessionForThread(th)
	if row.DoubleClicked() && s != nil {
		a.open(s.ID)
	}
	row.Children(func() {
		ui.Row(c).Width(48).Children(func() {
			for i, ag := range th.Agents {
				if i == 2 {
					break
				}
				a.agentAvatar(c.Key(ag), ag, 24)
			}
		})
		ui.Column(c).Grow(1).Gap(2).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
				frostui.Mono(c, th.ID).TextColor(t.TextMuted)
				if a.desk.seenReady && th.LastEventAt > a.desk.seenBefore {
					frostui.Pill(c, "new", frostui.Accent)
				}
				frostui.Strong(c, th.Title).SingleLine().Grow(1)
				if dueWatch(th) {
					frostui.Pill(c, "check due", frostui.Warning)
				}
				for _, pr := range th.PRs {
					if pr.State == "OPEN" || pr.Ready {
						tone := frostui.Accent
						if pr.Ready || pr.Next == "brian" {
							tone = frostui.Success
						}
						frostui.Pill(c.Key(pr.Ref), shortRef(pr.Ref), tone)
					}
				}
			})
			now := ""
			busy := false
			if s != nil {
				now = a.lastStep(s.ID)
				busy = a.stateOf(s) == stateWorking
			}
			ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
				if busy {
					ui.Spinner(c).Size(12, 12)
				}
				switch {
				case now != "":
					frostui.Caption(c, truncate(now, 140)).SingleLine().Grow(1)
				case th.Watch != nil:
					frostui.Caption(c, "checks "+th.Watch.CheckOn+": "+th.Watch.Check).SingleLine().Grow(1)
				case len(th.Open) > 0:
					frostui.Caption(c, th.Open[0]).SingleLine().Grow(1)
				default:
					frostui.Caption(c, th.Repo).SingleLine().Grow(1)
				}
			})
		})
		frostui.Caption(c, ago(a.serverNow(), th.LastEventAt.Time()))
	})
}

// shortRef shortens owner/name#12 to name#12.
func shortRef(ref string) string {
	if i := strings.Index(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

// runRow is a session at work that no thread holds.
func (a *App) runRow(c *ui.Context, s *pocket.Session) {
	t := c.Theme()
	row := ui.ButtonBase(c).Row().Gap(frostui.SpaceM).AlignItems(ui.Center).Padding(frostui.SpaceS, frostui.SpaceL).
		Cursor(ui.CursorPointer).Label("Open " + s.DisplayTitle()).Background(frostui.Panel(t))
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	if row.Clicked() {
		a.open(s.ID)
	}
	row.Children(func() {
		ui.Row(c).Width(48).Children(func() {
			if p := a.agentOf(s); p != nil {
				a.agentAvatar(c, p.ID, 24)
			} else {
				frostui.IconTile(c, "pocket-session", frostui.Neutral, 24)
			}
		})
		ui.Column(c).Grow(1).Gap(2).AlignItems(ui.Stretch).Children(func() {
			frostui.Strong(c, s.DisplayTitle()).SingleLine()
			ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(12, 12)
				now := a.lastStep(s.ID)
				if now == "" {
					now = a.homePath(s.Cwd)
				}
				frostui.Caption(c, truncate(now, 140)).SingleLine().Grow(1)
			})
		})
		if s.Subagents != nil && s.Subagents.Working > 0 {
			frostui.IconPill(c, "pocket-subagents", fmt.Sprint(s.Subagents.Working), frostui.Accent)
		}
	})
}

func (a *App) closedThreads(c *ui.Context, v *pocket.DeskView) {
	var closed []*pocket.DeskThread
	for i := range v.Threads {
		if v.Threads[i].Group() == "closed" {
			closed = append(closed, &v.Threads[i])
		}
	}
	if len(closed) == 0 {
		return
	}
	slices.SortFunc(closed, func(x, y *pocket.DeskThread) int { return int(closedAt(y) - closedAt(x)) })
	open := a.closedOpen
	if frostui.DisclosureCard(c, &open, frostui.Disclosure{Icon: "object-select", Title: "Closed this week",
		Subtitle: plural(len(closed), "thread", "threads"), Body: func() {
			ui.Column(c).AlignItems(ui.Stretch).Children(func() {
				for i, th := range closed {
					if i > 0 {
						frostui.RowDivider(c.Key(fmt.Sprint("cd", th.ID)))
					}
					a.threadRow(c.Key(fmt.Sprint("c", th.ID)), th)
				}
			})
		}}) {
		a.closedOpen = open
	}
}

func closedAt(t *pocket.DeskThread) pocket.Millis {
	if t.Closed != nil {
		return t.Closed.At
	}
	return t.LastEventAt
}

// ago says how long ago t was, shortly.
func ago(now, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// boardPane is the shift board: rows of unattended work the agents
// propose, the person picks, and code launches as they fit.
func (a *App) boardPane(c *ui.Context) {
	t := c.Theme()
	if !a.desk.boardLoaded && a.desk.board == nil && !a.desk.boardLoading {
		a.loadBoard()
	}
	b := a.desk.board
	ui.Scroll(c).Grow(1).Background(t.Background).Children(func() {
		ui.Column(c).AlignItems(ui.Center).Padding(frostui.SpaceXL).Children(func() {
			ui.Column(c).MaxWidth(980).FillWidth().Gap(frostui.SpaceM).AlignItems(ui.Stretch).Children(func() {
				line := ""
				var picked []pocket.BoardRow
				if b != nil {
					if b.Limits != nil {
						line = b.Limits.Line
					}
					for _, r := range b.Rows {
						if r.State == "picked" {
							picked = append(picked, r)
						}
					}
				}
				frostui.PageHeader(c, "Shift board", line, func() {
					if a.desk.boardLoading || a.desk.boardBusy {
						ui.Spinner(c).Size(16, 16)
					}
					if ui.Button(c, "Refresh board").Disabled(a.desk.boardLoading || a.desk.boardBusy).Clicked() {
						a.loadBoard()
					}
					label := "Launch picks"
					if len(picked) > 0 {
						label = fmt.Sprintf("Launch %d", len(picked))
					}
					if ui.PrimaryButton(c, label).Disabled(len(picked) == 0 || a.desk.boardBusy || a.desk.boardLoading).Clicked() {
						a.boardOp("launch", nil, picked)
					}
				})
				if b == nil {
					if a.desk.boardErr != "" {
						frostui.Problem(c, a.desk.boardErr).Wrap()
					} else {
						frostui.Loading(c, "Reading the board…")
					}
					return
				}
				a.boardSupervisor(c, b)
				if a.desk.boardErr != "" {
					frostui.Problem(c, a.desk.boardErr).Wrap()
				}
				if b.Limits != nil {
					ui.Row(c).Gap(frostui.SpaceM).Children(func() {
						l := b.Limits
						a.statTile(c, "Running", fmt.Sprintf("%d of %d", l.Running, l.AtOnce), "at once")
						a.statTile(c, "Desk", fmt.Sprintf("%d of %d", l.Counted, l.Cap), fmt.Sprintf("%d kept free", l.Reserve))
						a.statTile(c, "Rounds today", fmt.Sprintf("%d of %d", l.Rounds, l.RoundsMax), "")
					})
				}
				groups := []struct {
					title  string
					states []string
				}{
					{"Running", []string{"launched", "starting"}},
					{"Picked", []string{"picked", "waiting"}},
					{"Proposed", []string{"proposed"}},
					{"Failed", []string{"failed", "unknown"}},
				}
				var done []pocket.BoardRow
				for _, r := range b.Rows {
					if r.State == "dropped" {
						done = append(done, r)
					}
				}
				shown := 0
				for _, g := range groups {
					var rows []pocket.BoardRow
					for _, r := range b.Rows {
						if !slices.Contains(g.states, r.State) {
							continue
						}
						if r.State == "launched" && (r.ShiftEnded || r.ThreadState == "closed") {
							if g.title == "Running" {
								done = append(done, r)
							}
							continue
						}
						rows = append(rows, r)
					}
					if len(rows) == 0 {
						continue
					}
					shown += len(rows)
					slices.SortFunc(rows, func(x, y pocket.BoardRow) int { return x.Order - y.Order })
					frostui.SectionHeader(c.Key(g.title), frostui.SectionHead{Title: g.title, Aside: fmt.Sprint(len(rows))})
					for _, r := range rows {
						a.boardRow(c.Key(r.ID), r)
					}
				}
				if shown == 0 {
					frostui.Muted(c, "No row waits or runs. Agents propose rows; pick the ones to run tonight.")
				}
				if len(done) > 0 {
					slices.SortFunc(done, func(x, y pocket.BoardRow) int { return int(y.ChangedAt - x.ChangedAt) })
					open := a.boardDoneOpen
					if frostui.DisclosureCard(c, &open, frostui.Disclosure{Icon: "object-select", Title: "Done", Subtitle: plural(len(done), "shift", "shifts"),
						Body: func() {
							ui.Column(c).Gap(frostui.SpaceS).AlignItems(ui.Stretch).Children(func() {
								for _, r := range done {
									a.boardRow(c.Key(fmt.Sprint("done", r.ID)), r)
								}
							})
						}}) {
						a.boardDoneOpen = open
					}
				}
			})
		})
	})
}

func (a *App) statTile(c *ui.Context, label, value, note string, action ...func()) {
	t := c.Theme()
	var tile ui.Element
	if len(action) > 0 {
		tile = ui.ButtonBase(c.Key(label)).Column().Label("Records for " + label).Cursor(ui.CursorPointer)
		if tile.Clicked() {
			action[0]()
		}
	} else {
		tile = ui.Column(c.Key(label))
	}
	tile.Grow(1).Basis(0).Gap(2).Padding(frostui.SpaceM).Radius(10).
		Background(frostui.Panel(t)).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
		frostui.Caption(c, label)
		ui.Text(c, value).FontSize(22).Bold().SingleLine()
		frostui.Caption(c, firstNonEmpty(note, " "))
	})
}

func (a *App) boardRow(c *ui.Context, r pocket.BoardRow) {
	t := c.Theme()
	tone := frostui.Neutral
	switch r.State {
	case "picked", "waiting":
		tone = frostui.Accent
	case "launched", "starting":
		tone = frostui.Success
	case "failed", "unknown":
		tone = frostui.Danger
	}
	ui.Row(c).Gap(frostui.SpaceM).AlignItems(ui.Center).Padding(frostui.SpaceM, frostui.SpaceL).Radius(10).
		Background(frostui.Panel(t)).BorderWidth(1, 1, 1, 4).BorderColor(frostui.ToneLine(t, tone)).Children(func() {
		a.agentAvatar(c, r.Agent, 28)
		ui.Column(c).Grow(1).Gap(2).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
				frostui.Mono(c, fmt.Sprintf("#%d", r.ID)).TextColor(t.TextMuted)
				frostui.Strong(c, r.Title).SingleLine().Grow(1)
				frostui.Pill(c, r.Kind, frostui.Neutral)
			})
			facts := []string{a.agentName(r.Agent)}
			if r.Repo != "" {
				facts = append(facts, r.Repo)
			}
			facts = append(facts, fmt.Sprintf("max %d", r.Max), "until "+r.Until)
			if r.Worker != "" {
				facts = append(facts, r.Worker)
			}
			if r.Thread != "" {
				facts = append(facts, r.Thread)
			}
			frostui.Caption(c, strings.Join(facts, " · ")).SingleLine()
			if r.Error != "" {
				frostui.Toned(c, r.Error, frostui.Danger).Wrap()
			} else if r.Note != "" {
				frostui.Caption(c, r.Note).Wrap()
			}
		})
		if a.desk.boardOpen == nil {
			a.desk.boardOpen = map[int]bool{}
		}
		if ui.Button(c, "Details").Clicked() {
			a.desk.boardOpen[r.ID] = !a.desk.boardOpen[r.ID]
		}
		switch r.State {
		case "proposed":
			if ui.Button(c, "Pick").Disabled(a.desk.boardBusy || a.desk.boardLoading).Clicked() {
				a.boardOp("pick", &r, nil)
			}
		case "picked":
			if ui.Button(c, "Unpick").Disabled(a.desk.boardBusy || a.desk.boardLoading).Clicked() {
				a.boardOp("unpick", &r, nil)
			}
		case "launched":
			if th := a.desk.thread(r.Thread); th != nil {
				if ui.Button(c, "Thread").Clicked() {
					a.selected = "x:motion"
					a.threadSel = th.ID
				}
			}
		}
	})
	if a.desk.boardOpen[r.ID] {
		a.boardRowDetails(c.Key("details"), r)
	}
}

// numbersPane is the Desk's dashboard: how the team delivers.
func (a *App) numbersPane(c *ui.Context) {
	t := c.Theme()
	if a.desk.dash == nil && !a.desk.dashLoading {
		a.loadDashboard(30)
	}
	d := a.desk.dash
	ui.Scroll(c).Grow(1).Background(t.Background).Children(func() {
		ui.Column(c).AlignItems(ui.Center).Padding(frostui.SpaceXL).Children(func() {
			ui.Column(c).MaxWidth(980).FillWidth().Gap(frostui.SpaceL).AlignItems(ui.Stretch).Children(func() {
				sub := "Desk-tracked work, by UTC day"
				if d != nil {
					sub = fmt.Sprintf("%s to %s · as of %s", d.Range.From, d.Range.To, clock(d.AsOf.Time()))
				}
				frostui.PageHeader(c, "Numbers", sub, func() {
					if a.desk.dashLoading {
						ui.Spinner(c).Size(16, 16)
					}
					idx := map[int]int{7: 0, 30: 1, 90: 2}[a.desk.dashDays]
					before := idx
					frostui.PillTabs(c, &idx, []frostui.Tab{{Title: "7 days"}, {Title: "30 days"}, {Title: "90 days"}})
					if idx != before {
						a.desk.records = nil
						a.loadDashboard([]int{7, 30, 90}[idx])
					}
				})
				if a.desk.dashErr != "" {
					frostui.Problem(c, "Numbers: "+a.desk.dashErr+". Showing the last successful result.").Wrap()
				}
				if d == nil {
					frostui.Loading(c, "Counting…")
					return
				}
				a.numberFilters(c, d)
				m := d.Totals
				ui.Grid(c).Columns(4).Gap(frostui.SpaceM).Children(func() {
					rate := "—"
					if m.Completion.Rate != nil {
						rate = fmt.Sprintf("%.1f%%", *m.Completion.Rate*100)
					}
					a.statTile(c, "Completion", rate, fmt.Sprintf("%d of %d closures", m.Completion.Success, m.Completion.Denominator), func() { a.openNumberRecords(a.desk.dashShown, "Completion") })
					week := ""
					if m.Velocity.PerWeek != nil {
						week = fmt.Sprintf("%.0f a week", *m.Velocity.PerWeek)
					}
					a.statTile(c, "Closed well", fmt.Sprintf("%d", m.Velocity.Successes), week, func() { a.openNumberRecords(a.desk.dashShown, "Closed well") })
					cycle := "—"
					if m.CycleTime.MedianMs != nil {
						cycle = span(time.Duration(*m.CycleTime.MedianMs) * time.Millisecond)
					}
					a.statTile(c, "Cycle time", cycle, "median, start to close", func() { a.openNumberRecords(a.desk.dashShown, "Cycle time") })
					a.statTile(c, "In motion now", fmt.Sprint(m.Workload.Total), fmt.Sprintf("%d open · %d parked", m.Workload.Open, m.Workload.Parked), func() { a.openNumberRecords(a.desk.dashShown, "In motion now") })
					a.statTile(c, "PRs merged", fmt.Sprint(m.PRs.Merged), fmt.Sprintf("%d opened · %d open now", m.PRs.Created, m.PRs.OpenNow), func() { a.openNumberRecords(a.desk.dashShown, "PRs merged") })
					cost := "—"
					if m.Usage.Cost != nil {
						cost = fmt.Sprintf("$%.2f", *m.Usage.Cost)
					}
					a.statTile(c, "Model cost", cost, m.Usage.State)
					tokens := "—"
					if m.Usage.TotalTokens != nil {
						tokens = humanTokens(*m.Usage.TotalTokens)
					}
					a.statTile(c, "Tokens", tokens, plural(m.Usage.Sessions, "session", "sessions"))
					solve := "—"
					if m.TimeToSolve.MedianMs != nil {
						solve = span(time.Duration(*m.TimeToSolve.MedianMs) * time.Millisecond)
					}
					a.statTile(c, "Time to solve", solve, plural(m.TimeToSolve.Count, "issue", "issues"))
				})
				if len(d.Daily) > 0 {
					frostui.SectionHeader(c, frostui.SectionHead{Title: "Closed well, by day"})
					a.dailyBars(c, d)
				}
				if len(d.Rows.Projects) > 0 {
					frostui.SectionHeader(c, frostui.SectionHead{Title: "By repository", Aside: fmt.Sprint(len(d.Rows.Projects))})
					projects := slices.Clone(d.Rows.Projects)
					slices.SortFunc(projects, func(x, y pocket.ProjectRow) int {
						return y.Metrics.Velocity.Successes - x.Metrics.Velocity.Successes
					})
					ui.Column(c).AlignItems(ui.Stretch).Radius(10).Clip().BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
						for i, p := range projects {
							if i > 0 {
								frostui.RowDivider(c.Key(fmt.Sprint("pd", p.Repo)))
							}
							row := ui.ButtonBase(c.Key(p.Repo)).Row().Label("Records for " + p.Repo).Cursor(ui.CursorPointer)
							if row.Clicked() {
								query := a.desk.dashShown
								query.Owner = ""
								query.Repo = p.Repo
								a.openNumberRecords(query, "All records")
							}
							row.Gap(frostui.SpaceM).AlignItems(ui.Center).Padding(frostui.SpaceS, frostui.SpaceL).
								Background(frostui.Panel(t)).Children(func() {
								frostui.Mono(c, p.Repo).Grow(1).SingleLine()
								frostui.Caption(c, fmt.Sprintf("%d closed", p.Metrics.Velocity.Successes)).Width(90)
								frostui.Caption(c, fmt.Sprintf("%d merged", p.Metrics.PRs.Merged)).Width(90)
								cyc := "—"
								if p.Metrics.CycleTime.MedianMs != nil {
									cyc = span(time.Duration(*p.Metrics.CycleTime.MedianMs) * time.Millisecond)
								}
								frostui.Caption(c, cyc).Width(80)
							})
						}
					})
				}
			})
		})
	})
}

func humanTokens(n float64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1fB", n/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", n/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.0fk", n/1e3)
	}
	return fmt.Sprintf("%.0f", n)
}

// dailyBars draws the closures of each day as bars.
func (a *App) dailyBars(c *ui.Context, d *pocket.Dashboard) {
	t := c.Theme()
	most, total := 2, 0
	for _, day := range d.Daily {
		most = max(most, day.Successes)
		total += day.Successes
	}
	// Use integer ticks, including for an empty or single-closure range.
	most += most % 2
	ui.Column(c).Gap(frostui.SpaceXS).Padding(frostui.SpaceL).Radius(10).AlignItems(ui.Stretch).
		Background(frostui.Panel(t)).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
		frostui.Caption(c, "Threads closed well")
		readout := fmt.Sprintf("%d closed well across %d days", total, len(d.Daily))
		var hovered, focused string
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Start).Children(func() {
			ui.Box(c).Width(40).Height(140).Children(func() {
				for i, count := range []int{most, most / 2, 0} {
					frostui.Caption(c, fmt.Sprint(count)).Absolute().Right(0).TopPercent(float32(i)*50).TranslatePercent(0, -50)
				}
			})
			ui.Column(c).Grow(1).Basis(0).Gap(frostui.SpaceXS).AlignItems(ui.Stretch).Children(func() {
				ui.Row(c).Height(140).Gap(3).AlignItems(ui.End).Children(func() {
					for _, day := range d.Daily {
						h := float32(day.Successes) / float32(most) * 140
						label := fmt.Sprintf("%s: %d closed well", day.Day, day.Successes)
						bar := ui.Box(c.Key(day.Day)).Grow(1).Basis(0).Height(max(h, 2)).Radius(3).
							Background(frostui.ToneColor(t, frostui.Accent)).Label(label).Tooltip(label).Focusable()
						if bar.Hovered() {
							hovered = label
						}
						if bar.Focused() {
							focused = label
						}
					}
				})
				ui.Row(c).Children(func() {
					frostui.Caption(c, d.Daily[0].Day)
					ui.Spacer(c)
					frostui.Caption(c, d.Daily[len(d.Daily)-1].Day)
				})
			})
		})
		frostui.Caption(c, firstNonEmpty(hovered, focused, readout))
	})
}

// span says a long duration roughly: "54 min", "3.2 h", "2.5 days".
func span(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.0f s", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%.0f min", d.Minutes())
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f h", d.Hours())
	}
	return fmt.Sprintf("%.1f days", d.Hours()/24)
}
