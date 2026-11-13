package app

import (
	"context"
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

// sessionDetails belongs to one visit to a session. Replacing its pointer also
// invalidates pending answers, including when the same session is reopened.
type sessionDetails struct {
	id                                           int
	identity                                     *pocket.TeamIdentity
	identityLoading, identityDirty, identityBusy bool
	goal                                         *pocket.ShiftGoal
	goalLoading, goalDirty                       bool
}

func (a *App) detailsForSession() *sessionDetails {
	if a.current == 0 || a.link.client == nil {
		return nil
	}
	if a.details == nil || a.details.id != a.current {
		a.details = &sessionDetails{id: a.current}
	}
	return a.details
}

func (a *App) loadSessionIdentity() {
	if !a.team.on {
		return
	}
	s := a.detailsForSession()
	if s == nil {
		return
	}
	if s.identityLoading {
		s.identityDirty = true
		return
	}
	s.identityLoading = true
	var identity *pocket.TeamIdentity
	a.request("Reading session identity", func(ctx context.Context, c *pocket.Client) error {
		var err error
		identity, err = c.TeamSession(ctx, s.id)
		return err
	}, func(err error) {
		if a.details != s {
			return
		}
		s.identityLoading = false
		if err == nil {
			s.identity = identity
		}
		if s.identityDirty {
			s.identityDirty = false
			a.loadSessionIdentity()
		}
	})
}

func (a *App) loadShiftGoal() {
	s := a.detailsForSession()
	if s == nil {
		return
	}
	if s.goalLoading {
		s.goalDirty = true
		return
	}
	s.goalLoading = true
	var goal *pocket.ShiftGoal
	a.request("Reading shift goal", func(ctx context.Context, c *pocket.Client) error {
		var err error
		goal, err = c.ShiftGoal(ctx, s.id)
		return err
	}, func(err error) {
		if a.details != s {
			return
		}
		s.goalLoading = false
		if err == nil {
			s.goal = goal
		}
		if s.goalDirty {
			s.goalDirty = false
			a.loadShiftGoal()
		}
	})
}

func (a *App) setSessionIdentity(agent string, pin bool) {
	s := a.detailsForSession()
	if s == nil || s.identityBusy {
		return
	}
	s.identityBusy = true
	a.request("Changing session identity", func(ctx context.Context, c *pocket.Client) error {
		if pin {
			return c.TeamPinIdentity(ctx, s.id, agent)
		}
		return c.TeamSetIdentity(ctx, s.id, agent)
	}, func(err error) {
		if a.details != s {
			return
		}
		s.identityBusy = false
		if err != nil {
			a.toast("Identity unchanged: " + err.Error())
			return
		}
		a.loadSessionIdentity()
	})
}

func (a *App) sessionIdentity(c *ui.Context) {
	if !a.team.on {
		return
	}
	s := a.details
	name, agent := "Choose an agent", ""
	if s != nil && s.identity != nil {
		name = "No named agent"
		if p := s.identity.Identity; p != nil {
			name, agent = p.Name, p.ID
		}
	}
	ui.MenuButton(c, name, func(m *ui.Menu) {
		if m.Item("No named agent").Checked(agent == "").Chosen() {
			a.setSessionIdentity("", false)
		}
		for _, p := range a.team.agents {
			if m.Item(p.Name).Checked(agent == p.ID).Chosen() {
				a.setSessionIdentity(p.ID, false)
			}
		}
	}).Label("Session identity").Disabled(s == nil || s.identityLoading || s.identityBusy)
	if s != nil && s.identity != nil && s.identity.Implicit && agent != "" {
		frostui.Caption(c, "From this session's folder").Wrap()
		if ui.Button(c, "Keep this identity").Disabled(s.identityBusy).Tooltip("Keep this agent if the session moves to another folder").Clicked() {
			a.setSessionIdentity(agent, true)
		}
	}
}

func (a *App) shiftGoalSection(c *ui.Context) {
	s := a.details
	if s == nil || s.goal == nil {
		return
	}
	g := s.goal
	frostui.SectionHeader(c, frostui.SectionHead{Title: "Shift", Quiet: true})
	frostui.Strong(c, g.Objective).Wrap()
	frostui.Body(c, "Stop when: "+g.Stop).Wrap()
	kv := []frostui.KeyValue{{Key: "State", Value: g.Status}, {Key: "Round", Value: fmt.Sprintf("%d of %d", g.Round, g.MaxRounds)}}
	for _, row := range []frostui.KeyValue{{Key: "Until", Value: g.DeadlineLabel}, {Key: "Start", Value: g.StartLabel}, {Key: "Worker", Value: g.Worker}, {Key: "Check", Value: g.Check}, {Key: "Stopped", Value: g.ReasonWord}} {
		if row.Value != "" {
			kv = append(kv, row)
		}
	}
	frostui.KeyValues(c, kv, frostui.KeyValuesStyle{KeyWidth: 72})
	frostui.Caption(c, "Use /team-shift status, stop or resume in the composer.").Wrap()
}
