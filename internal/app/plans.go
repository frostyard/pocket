package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type planDraft struct {
	words, why string
	marks      map[string]string
}
type planKey struct{ values, key string }
type planPage struct {
	id                       string
	view                     *pocket.PlanView
	loading, busy, reload    bool
	err, readErr             string
	drafts                   map[string]*planDraft
	keys                     map[string]planKey
	form, section, text, rev string
	formKey                  string
	formTexts                map[string]string
	tasks                    map[string]bool
	graph                    int
	reopen                   *pocket.PlanAsk
	md                       markdown.Options
}

func (a *App) planOwner() bool { return a.hello != nil && a.hello.User.Role == "owner" }
func (a *App) openPlan(id string) {
	if !a.planOwner() {
		return
	}
	a.goPlace("p:" + id)
	if a.plan != nil && a.plan.id == id {
		a.loadPlan()
		return
	}
	a.plan = &planPage{id: id, drafts: map[string]*planDraft{}, keys: map[string]planKey{}, md: markdown.Options{OnLink: a.followLink}}
	a.loadPlan()
}
func (a *App) loadPlan() {
	s := a.plan
	if s == nil || a.selected != "p:"+s.id {
		return
	}
	if s.loading || s.busy {
		s.reload = true
		return
	}
	s.reload = false
	s.loading = true
	id := s.id
	var view *pocket.PlanView
	a.request("Reading plan", func(ctx context.Context, c *pocket.Client) error {
		var err error
		view, err = c.DeskPlan(ctx, id)
		return err
	}, func(err error) {
		if a.plan != s {
			return
		}
		s.loading = false
		if s.reload {
			a.loadPlan()
			return
		}
		s.readErr = ""
		if err != nil {
			s.readErr = err.Error()
			return
		}
		s.view = view
	})
}
func (s *planPage) draft(ask pocket.PlanAsk) *planDraft {
	key := fmt.Sprintf("%s:%d", ask.ID, ask.Gen)
	d := s.drafts[key]
	if d == nil {
		d = &planDraft{marks: map[string]string{}}
		if d.marks == nil {
			d.marks = map[string]string{}
		}
		s.drafts[key] = d
	}
	return d
}

// Draft marks contain only edited cells; untouched cells follow the latest server view.
func planMarks(ask pocket.PlanAsk, d *planDraft) map[string]string {
	marks := maps.Clone(ask.Marks)
	if marks == nil {
		marks = map[string]string{}
	}
	for key, value := range d.marks {
		if value == "" {
			delete(marks, key)
		} else {
			marks[key] = value
		}
	}
	return marks
}
func (s *planPage) closeForm() {
	if s.form != "" {
		if s.formTexts == nil {
			s.formTexts = map[string]string{}
		}
		s.formTexts[s.formKey] = s.text
	}
	s.form = ""
}

// formSubmitKey names the form an action submits: the open form's key when the
// action is that form's own submit action, and nothing for any other action.
func (s *planPage) formSubmitKey(action string) string {
	if s.form == "" {
		return ""
	}
	if action == s.form || strings.HasPrefix(action, s.form+":") {
		return s.formKey
	}
	return ""
}
func (a *App) planBodyKey(s *planPage, action string, body map[string]any) {
	data, _ := json.Marshal(body)
	known := s.keys[action]
	if known.values != string(data) || known.key == "" {
		known = planKey{values: string(data), key: boardKey(a.opts.Now())}
		s.keys[action] = known
	}
	body["key"] = known.key
}
func (a *App) planAct(action string, body map[string]any) {
	s := a.plan
	if s == nil || s.view == nil || s.busy || s.loading || s.readErr != "" || !a.planOwner() || a.selected != "p:"+s.id {
		return
	}
	// Requests hold an immutable copy; fields can never change the request in flight.
	body = maps.Clone(body)
	keyed := action == "approve" || action == "changes" || action == "comment" || action == "revise" || strings.HasPrefix(action, "reopen:")
	if keyed {
		a.planBodyKey(s, action, body)
	}
	s.busy = true
	s.err = ""
	planID := s.id
	// Only the form this action submits loses its saved text on success.
	submitted := s.formSubmitKey(action)
	a.request("Updating plan", func(ctx context.Context, c *pocket.Client) error {
		kind, id, found := strings.Cut(action, ":")
		if found {
			switch kind {
			case "answer", "marks", "reopen":
				return c.PlanAskAction(ctx, id, kind, body)
			case "dismiss":
				return c.DismissPlanComment(ctx, planID, id)
			case "stop":
				return c.StopPlanConsult(ctx, planID, id)
			}
		}
		if action == "drop" {
			return c.DeskAct(ctx, planID, "drop", body)
		}
		return c.PlanAction(ctx, planID, action, body)
	}, func(err error) {
		if a.plan != s {
			return
		}
		s.busy = false
		if err != nil {
			s.err = err.Error()
			var api *pocket.APIError
			if errors.As(err, &api) && api.Status == 409 {
				s.err += " · Reading the latest plan. Review it and reopen the action before trying again."
				s.closeForm()
				a.loadPlan()
			} else if s.reload {
				a.loadPlan()
			}
			return
		}
		delete(s.keys, action)
		if strings.HasPrefix(action, "answer:") {
			id := strings.SplitN(action, ":", 2)[1]
			if gen, ok := body["gen"].(int); ok {
				delete(s.drafts, fmt.Sprintf("%s:%d", id, gen))
			}
		}
		if strings.HasPrefix(action, "marks:") {
			id := strings.TrimPrefix(action, "marks:")
			if gen, ok := body["gen"].(int); ok {
				if d := s.drafts[fmt.Sprintf("%s:%d", id, gen)]; d != nil {
					d.marks = map[string]string{}
				}
			}
		}
		if submitted != "" {
			delete(s.formTexts, submitted)
			if s.form != "" && s.formKey == submitted {
				s.form = ""
				s.text = ""
			}
		}
		a.loadPlan()
		a.loadDesk()
	})
}
func (a *App) beginPlanForm(kind, section string, ask *pocket.PlanAsk) {
	s := a.plan
	if s == nil || s.view == nil {
		return
	}
	s.closeForm()
	s.formKey = kind + ":" + section
	if ask != nil {
		s.formKey += fmt.Sprintf(":%s:%d", ask.ID, ask.Gen)
	}
	s.text = s.formTexts[s.formKey]
	s.form, s.section, s.rev = kind, section, s.view.Rev
	s.reopen = ask
	s.tasks = map[string]bool{}
	s.graph = 0
	if s.view.ApprovedGraph != nil {
		s.graph = s.view.ApprovedGraph.Version
	}
}
func (a *App) planPane(c *ui.Context) {
	s := a.plan
	if s == nil {
		return
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(frostui.SpaceL).Gap(frostui.SpaceM).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Gap(frostui.SpaceS).Children(func() {
				if ui.Button(c, "Back to In motion").Clicked() {
					a.goPlace("x:motion")
				}
				ui.Spacer(c)
				if ui.Button(c, "Refresh plan").Disabled(s.loading || s.busy).Clicked() {
					a.loadPlan()
				}
			})
			if s.loading {
				frostui.Loading(c, "Reading plan…")
			}
			if s.err != "" {
				frostui.Problem(c, s.err).Wrap()
			}
			if s.readErr != "" {
				frostui.Problem(c, s.readErr).Wrap()
			}
			p := s.view
			if p == nil {
				return
			}
			frostui.Title(c, p.ID+" · "+p.Title).Wrap()
			frostui.Caption(c, p.State+" · "+a.agentName(p.Lead)+" · revision "+p.Rev).Wrap()
			ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
				for _, step := range p.Progress.Steps {
					tone := frostui.Neutral
					switch step.State {
					case "current":
						tone = frostui.Accent
					case "done":
						tone = frostui.Success
					}
					frostui.Pill(c, step.Label, tone).Tooltip(step.State + " · " + step.Detail)
				}
			})
			if len(p.Waiting) > 0 {
				frostui.Muted(c, "Waiting: "+strings.Join(p.Waiting, " · ")).Wrap()
			}
			disabled := s.busy || s.loading || s.readErr != "" || !a.planOwner() || p.State == "done" || p.State == "dropped"
			ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
				if p.Session != 0 && ui.Button(c, "Planning session").Clicked() {
					a.open(p.Session)
				}
				if ui.PrimaryButton(c, "Approve plan").Disabled(disabled || !p.CanApprove || p.Graph == nil).Tooltip(p.ApproveOff).Clicked() {
					a.planAct("approve", map[string]any{"rev": p.Rev, "graph": p.Graph.Version})
				}
				if p.State == "review" && ui.Button(c, "Request changes…").Disabled(disabled).Clicked() {
					a.beginPlanForm("changes", "", nil)
				}
				if p.CanRevise && ui.Button(c, "Stop and revise…").Disabled(disabled).Clicked() {
					a.beginPlanForm("revise", "", nil)
				}
				if p.State != "done" && p.State != "dropped" && ui.Button(c, "Drop plan…").Disabled(disabled).Clicked() {
					a.beginPlanForm("drop", "", nil)
				}
			})
			if !p.CanApprove && p.ApproveOff != "" {
				frostui.Caption(c, p.ApproveOff).Wrap()
			}
			markdown.View(c.Key("preamble"), p.Preamble, &s.md)
			for _, section := range p.Sections {
				a.planSection(c.Key(section.ID), s, section, disabled)
			}
			if p.Graph != nil {
				a.planGraph(c, p.Graph, "Task graph")
			}
			if p.ApprovedGraph != nil && (p.Graph == nil || p.Graph.Version != p.ApprovedGraph.Version) {
				a.planGraph(c, p.ApprovedGraph, "Approved graph")
			}
			if len(p.Consults) > 0 {
				frostui.Heading(c, "Consults")
			}
			for _, consult := range p.Consults {
				ui.Column(c.Key(consult.ID)).Gap(frostui.SpaceS).Children(func() {
					frostui.Strong(c, a.agentName(consult.Agent)+" · "+consult.State).Wrap()
					frostui.Body(c, consult.Question).Wrap()
					if consult.CanStop && ui.Button(c, "Stop consult").Label("Stop consult "+consult.ID).Disabled(disabled).Clicked() {
						a.planAct("stop:"+consult.ID, map[string]any{})
					}
				})
			}
			for _, finding := range p.Findings {
				frostui.Strong(c, a.agentName(finding.Agent)+" · "+finding.Stance).Wrap()
				markdown.View(c.Key(finding.ID), finding.Summary, &s.md)
			}
		})
	})
}
func (a *App) planSection(c *ui.Context, s *planPage, section pocket.PlanSection, disabled bool) {
	frostui.Heading(c, section.Heading).Wrap()
	markdown.View(c, section.Text, &s.md)
	for _, ask := range section.Asks {
		a.planAsk(c.Key(ask.ID), s, ask, disabled)
	}
	for _, comment := range section.Comments {
		ui.Column(c.Key(comment.ID)).Gap(frostui.SpaceS).Children(func() {
			frostui.Caption(c, comment.By+" · "+comment.State).Wrap()
			frostui.Body(c, comment.Text).Wrap()
			if comment.State == "open" && ui.Button(c, "Dismiss comment").Label("Dismiss "+comment.ID).Disabled(disabled).Clicked() {
				a.planAct("dismiss:"+comment.ID, map[string]any{})
			}
		})
	}
	if !section.Desk && (s.view.State == "drafting" || s.view.State == "review") && ui.Button(c, "Comment…").Label("Comment on "+section.Heading).Disabled(disabled).Clicked() {
		a.beginPlanForm("comment", section.ID, nil)
	}
}
func (a *App) planAsk(c *ui.Context, s *planPage, ask pocket.PlanAsk, disabled bool) {
	frostui.Strong(c, ask.ID+" · "+ask.Text).Wrap()
	d := s.draft(ask)
	if !ask.Open {
		if ask.Answer != nil {
			frostui.Body(c, ask.Answer.Words+" · "+ask.Answer.By).Wrap()
		}
		if ask.Why != nil {
			frostui.Muted(c, ask.Why.Text).Wrap()
		}
		if ask.Resolved != nil {
			frostui.Caption(c, ask.Resolved.Why).Wrap()
		}
	}
	marks := planMarks(ask, d)
	if !ask.Open {
		marks = ask.Marks
	}
	if len(ask.Criteria) > 0 && len(ask.Options) > 0 {
		ui.ScrollHorizontal(c).Children(func() {
			ui.Column(c).Gap(frostui.SpaceS).Children(func() {
				ui.Row(c).Gap(frostui.SpaceS).Children(func() {
					frostui.Strong(c, "Option").Width(180)
					for _, criterion := range ask.Criteria {
						frostui.Caption(c, criterion.Label).Width(130).Wrap()
					}
				})
				for i, option := range ask.Options {
					ui.Row(c.Key(i)).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
						frostui.Body(c, option).Width(180).Wrap()
						for _, criterion := range ask.Criteria {
							key := fmt.Sprintf("%d/%s", i, criterion.ID)
							options := []string{"—", "Yes", "Partly", "No"}
							values := []string{"", "yes", "partly", "no"}
							at := max(0, slices.Index(values, marks[key]))
							before := at
							selected := options[at]
							ui.Select(c.Key(key), &selected, options).Width(130).Label(ask.ID + " · " + option + " · " + criterion.Label).Disabled(disabled || !ask.Open)
							at = slices.Index(options, selected)
							if at != before {
								d.marks[key] = values[at]
							}
						}
					})
				}
			})
		})
	}
	for _, finding := range ask.Findings {
		frostui.Caption(c, a.agentName(finding.Agent)+" · "+finding.Stance+": "+finding.Summary).Wrap()
	}
	if ask.Open {
		ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
			for _, option := range ask.Options {
				if ui.Button(c, option).Disabled(disabled).Clicked() {
					d.words = option
				}
			}
		})
		ui.TextInput(c, &d.words).Label("Answer " + ask.ID).Placeholder("Your answer").Disabled(disabled)
		ui.TextInput(c, &d.why).Label("Why " + ask.ID).Placeholder("Why (optional)").Disabled(disabled)
		ui.Row(c).Gap(frostui.SpaceS).Children(func() {
			if ui.PrimaryButton(c, "Answer").Label("Send answer " + ask.ID).Disabled(disabled || strings.TrimSpace(d.words) == "").Clicked() {
				body := map[string]any{"words": strings.TrimSpace(d.words), "why": strings.TrimSpace(d.why), "gen": ask.Gen}
				if len(d.marks) > 0 {
					body["marks"] = planMarks(ask, d)
				}
				a.planAct("answer:"+ask.ID, body)
			}
			if len(ask.Criteria) > 0 && ui.Button(c, "Save marks").Label("Save marks "+ask.ID).Disabled(disabled).Clicked() {
				a.planAct("marks:"+ask.ID, map[string]any{"gen": ask.Gen, "marks": planMarks(ask, d)})
			}
		})
	} else if ask.Answer != nil && s.view.State != "done" && s.view.State != "dropped" {
		if ui.Button(c, "Reopen decision…").Label("Reopen " + ask.ID).Disabled(disabled).Clicked() {
			a.beginPlanForm("reopen", "", &ask)
		}
	}
}
func (a *App) planGraph(c *ui.Context, g *pocket.PlanGraph, title string) {
	frostui.Heading(c, fmt.Sprintf("%s · v%d", title, g.Version))
	for _, task := range g.Tasks {
		ui.Column(c.Key(task.ID)).Gap(frostui.SpaceXS).Children(func() {
			frostui.Strong(c, task.ID+" · "+task.Title+" · "+task.State).Wrap()
			frostui.Caption(c, a.agentName(task.Agent)+" · "+task.Repo+" · depends on "+strings.Join(task.Deps, ", ")).Wrap()
			for _, line := range task.Acceptance {
				frostui.Body(c, "• "+line).Wrap()
			}
		})
	}
}
func (a *App) planForm(c *ui.Context, s *planPage, disabled bool) {
	scope := s.form == "revise" || (s.form == "reopen" && (s.view.State == "approved" || s.view.State == "building" || s.view.State == "verifying"))
	title := map[string]string{"changes": "Request changes", "comment": "Add comment", "revise": "Stop and revise", "reopen": "Reopen decision", "drop": "Drop plan"}[s.form]
	frostui.Strong(c, title).Wrap()
	if s.form == "changes" {
		names, ids := []string{"Whole plan"}, []string{""}
		for _, sec := range s.view.Sections {
			if sec.Desk {
				continue
			}
			names = append(names, sec.Heading)
			ids = append(ids, sec.ID)
		}
		at := max(0, slices.Index(ids, s.section))
		selected := names[at]
		ui.Select(c, &selected, names).Label("Changes section").Disabled(disabled)
		s.section = ids[max(0, slices.Index(names, selected))]
	}
	limit := 1000
	if scope || s.form == "drop" {
		limit = 300
		if s.form == "drop" {
			limit = 200
		}
		ui.TextInput(c, &s.text).Label("Plan action text").Placeholder("Reason").Disabled(disabled)
		frostui.Caption(c, fmt.Sprintf("One line, up to %d characters", limit))
	} else if s.form != "reopen" {
		ui.TextArea(c, &s.text).Height(90).Label("Plan action text").Placeholder("Comment (up to 1000 characters)").Disabled(disabled)
	}
	invalid := len(utf16.Encode([]rune(strings.TrimSpace(s.text)))) > limit || ((scope || s.form == "drop") && strings.ContainsAny(strings.TrimSpace(s.text), "\r\n"))
	if s.form == "reopen" && !scope {
		invalid = false
	}
	if invalid {
		frostui.Problem(c, "The text exceeds the limit or contains a line break.").Wrap()
	}
	if scope && s.view.ApprovedGraph != nil {
		frostui.Caption(c, "Tasks to stop for this scope change:")
		ui.Scroll(c).Height(float32(min(220, len(s.view.ApprovedGraph.Tasks)*34))).Children(func() {
			ui.Column(c).Children(func() {
				for _, task := range s.view.ApprovedGraph.Tasks {
					checked := s.tasks[task.ID]
					ui.Checkbox(c, &checked, task.ID+" · "+task.Title).Disabled(disabled)
					s.tasks[task.ID] = checked
				}
			})
		})
	}
	needsText := s.form == "comment" || s.form == "drop" || scope
	missingSection := s.form == "changes" && strings.TrimSpace(s.text) != "" && s.section == ""
	if missingSection {
		frostui.Caption(c, "Choose the section this comment is about.").Wrap()
	}
	ui.Row(c).Gap(frostui.SpaceS).Children(func() {
		if ui.Button(c, "Cancel action").Disabled(s.busy).Clicked() {
			s.closeForm()
		}
		if ui.PrimaryButton(c, title).Label("Submit " + strings.ToLower(title)).Disabled(disabled || invalid || missingSection || (needsText && strings.TrimSpace(s.text) == "")).Clicked() {
			body := map[string]any{}
			action := s.form
			switch s.form {
			case "comment":
				body = map[string]any{"text": strings.TrimSpace(s.text), "section": s.section, "rev": s.rev}
			case "changes":
				body = map[string]any{"rev": s.rev}
				if strings.TrimSpace(s.text) != "" {
					body["text"] = strings.TrimSpace(s.text)
					body["section"] = s.section
				}
			case "drop":
				body["reason"] = strings.TrimSpace(s.text)
			case "reopen":
				if s.reopen == nil {
					return
				}
				action = "reopen:" + s.reopen.ID
				body["gen"] = s.reopen.Gen
			}
			if scope {
				tasks := []string{}
				for id, on := range s.tasks {
					if on {
						tasks = append(tasks, id)
					}
				}
				slices.Sort(tasks)
				body["tasks"], body["graph"], body["why"] = tasks, s.graph, strings.TrimSpace(s.text)
			}
			if scope || action == "drop" {
				source, client := s, a.link.client
				description := "Revise plan " + s.view.ID + " · " + s.view.Title + " and stop the selected tasks? " + strings.TrimSpace(s.text)
				if action == "drop" {
					description = "End plan " + s.view.ID + " · " + s.view.Title + " and stop its tasks? This cannot be undone. " + strings.TrimSpace(s.text)
				}
				a.dialogs.Ask(frostui.Confirm{Title: title + "?", Body: description, Action: "Confirm " + strings.ToLower(title), Destructive: true, OnConfirm: func() {
					if a.plan == source && a.link.client == client {
						a.planAct(action, body)
					}
				}})
			} else {
				a.planAct(action, body)
			}
		}
	})
}

func (a *App) planActionDialog(c *ui.Context) {
	s := a.plan
	if s == nil || s.form == "" || a.selected != "p:"+s.id {
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(620).Gap(frostui.SpaceM).AlignItems(ui.Stretch).Children(func() { a.planForm(c, s, s.busy || s.loading || s.readErr != "" || !a.planOwner()) })
	})
	if !open {
		s.closeForm()
	}
}
