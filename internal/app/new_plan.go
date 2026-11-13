package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type newPlanDraft struct {
	open, loading, busy           bool
	title, idea, lead, model, err string
	supervisor                    int
	roster                        *pocket.PlanRoster
	key                           planKey
}

func (a *App) openNewPlan() {
	if !a.planOwner() {
		return
	}
	s := &newPlanDraft{open: true, loading: true}
	a.newPlan = s
	var roster *pocket.PlanRoster
	a.request("Reading plan leads", func(ctx context.Context, c *pocket.Client) error {
		var err error
		roster, err = c.PlanRoster(ctx)
		return err
	}, func(err error) {
		if a.newPlan != s {
			return
		}
		s.loading = false
		if err != nil {
			s.err = err.Error()
			return
		}
		s.roster = roster
		if len(roster.Leads) > 0 {
			s.lead = roster.Leads[0].ID
		}
		if len(roster.Supervisors) > 0 {
			s.supervisor = roster.Supervisors[0]
		}
	})
}
func (a *App) startPlan() {
	s := a.newPlan
	if s == nil || !s.open || s.busy || !a.planOwner() || s.roster == nil || !s.roster.StartPlan || s.supervisor == 0 || s.lead == "" || strings.TrimSpace(s.title) == "" || strings.TrimSpace(s.idea) == "" {
		return
	}
	body := map[string]any{"title": strings.TrimSpace(s.title), "idea": strings.TrimSpace(s.idea), "lead": s.lead}
	if strings.TrimSpace(s.model) != "" {
		body["model"] = strings.TrimSpace(s.model)
	}
	values, _ := json.Marshal(body)
	fingerprint := fmt.Sprintf("%d:%s", s.supervisor, values)
	if s.key.values != fingerprint {
		s.key = planKey{values: fingerprint, key: boardKey(a.opts.Now())}
	}
	body["key"] = s.key.key
	supervisor := s.supervisor
	s.busy = true
	s.err = ""
	var id string
	a.requestWithin(2*time.Minute, "Starting plan", func(ctx context.Context, c *pocket.Client) error {
		var err error
		id, err = c.NewPlan(ctx, supervisor, body)
		return err
	}, func(err error) {
		if a.newPlan != s {
			return
		}
		s.busy = false
		if err != nil {
			s.err = err.Error()
			return
		}
		if id == "" {
			s.err = "The server returned no plan identifier"
			return
		}
		if !s.open {
			a.toast("Plan created: " + id)
			return
		}
		s.open = false
		a.openPlan(id)
		a.loadDesk()
	})
}
func (a *App) newPlanDialog(c *ui.Context) {
	s := a.newPlan
	if s == nil || !s.open {
		return
	}
	ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(580).Gap(frostui.SpaceM).AlignItems(ui.Stretch).Children(func() {
			frostui.Heading(c, "New plan")
			if s.err != "" {
				frostui.Problem(c, s.err).Wrap()
			}
			if s.loading {
				frostui.Loading(c, "Reading leads and supervisors…")
			}
			r := s.roster
			if r != nil {
				frostui.Caption(c, fmt.Sprintf("%d of %d plans open", r.Plans.Open, r.Plans.Max))
				if !r.StartPlan {
					frostui.Problem(c, "This server cannot start plans. Update the Team and Desk modules together.").Wrap()
				}
				leads := []string{}
				leadIDs := map[string]string{}
				selected := ""
				for _, lead := range r.Leads {
					label := lead.Name + " (" + lead.ID + ")"
					leads = append(leads, label)
					leadIDs[label] = lead.ID
					if s.lead == lead.ID {
						selected = label
					}
				}
				ui.Select(c, &selected, leads).Label("Plan lead").Disabled(s.busy)
				s.lead = leadIDs[selected]
				supervisors := []string{}
				ids := map[string]int{}
				selected = ""
				for _, id := range r.Supervisors {
					label := fmt.Sprintf("Session %d", id)
					if session := a.byID[id]; session != nil {
						label = fmt.Sprintf("%s (%d)", session.Title, id)
					}
					supervisors = append(supervisors, label)
					ids[label] = id
					if s.supervisor == id {
						selected = label
					}
				}
				ui.Select(c, &selected, supervisors).Label("Plan supervisor").Disabled(s.busy)
				s.supervisor = ids[selected]
				if len(r.Leads) == 0 || len(r.Supervisors) == 0 {
					frostui.Muted(c, "A lead with a home and a known supervisor session are required.").Wrap()
				}
			}
			ui.TextInput(c, &s.title).Label("Plan title").Placeholder("Title").Disabled(s.busy)
			ui.TextArea(c, &s.idea).Height(100).Label("Plan idea").Placeholder("What should this plan accomplish?").Disabled(s.busy)
			ui.TextInput(c, &s.model).Label("Plan model").Placeholder("Model (optional; use the lead's default)").Disabled(s.busy)
			ui.Row(c).Gap(frostui.SpaceS).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					s.open = false
				}
				if ui.PrimaryButton(c, "Create plan").Disabled(s.busy || r == nil || !r.StartPlan || r.Plans.Open >= r.Plans.Max || s.lead == "" || s.supervisor == 0 || strings.TrimSpace(s.title) == "" || strings.TrimSpace(s.idea) == "").Clicked() {
					a.startPlan()
				}
			})
		})
	})
}
