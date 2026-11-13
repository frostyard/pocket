package app

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type runningState struct {
	data                        *pocket.RunningBoard
	loading, dirty, timer, busy bool
	err                         string
}

func (a *App) canSteer() bool { return a.hello != nil && a.hello.User.Role != "viewer" }

func (a *App) loadRunning() {
	if a.link.client == nil || a.selected != "x:motion" {
		return
	}
	if a.running == nil {
		a.running = &runningState{}
	}
	s := a.running
	if s.loading {
		s.dirty = true
		return
	}
	s.loading = true
	var data *pocket.RunningBoard
	a.request("Reading running work", func(ctx context.Context, c *pocket.Client) error {
		var err error
		data, err = c.Running(ctx)
		return err
	}, func(err error) {
		if a.running != s {
			return
		}
		s.loading = false
		s.err = ""
		if err != nil {
			s.err = err.Error()
		} else {
			s.data = data
		}
		if s.dirty {
			s.dirty = false
			a.loadRunning()
		}
		if !s.timer && a.selected == "x:motion" {
			s.timer = true
			a.after(5*time.Second, func() {
				if a.running == s {
					s.timer = false
					a.loadRunning()
				}
			})
		}
	})
}

func (a *App) stopRunning(task pocket.RunningTask) {
	s := a.running
	if s == nil || s.busy || !a.canSteer() || !task.Stoppable() {
		return
	}
	s.busy = true
	a.request("Stopping task", func(ctx context.Context, c *pocket.Client) error { return c.StopRunning(ctx, task) }, func(err error) {
		if a.running != s {
			return
		}
		s.busy = false
		if err != nil {
			a.toast("Could not stop task: " + err.Error())
		}
		a.loadRunning()
	})
}

func (a *App) runningSection(c *ui.Context) {
	frostui.SectionHeader(c, frostui.SectionHead{Title: "Running now"})
	s := a.running
	if s == nil {
		return
	}
	if s.err != "" {
		frostui.Problem(c, s.err).Wrap()
	}
	if s.data == nil {
		if s.loading {
			frostui.Loading(c, "Reading running work…")
		}
		return
	}
	shown := 0
	for _, session := range s.data.Sessions {
		for _, task := range session.Tasks {
			if task.Kind == "pocket.schedule" {
				continue
			}
			shown++
			ui.Row(c.Key(fmt.Sprintf("%d:%d", task.ConversationID, task.ID))).Gap(frostui.SpaceM).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Basis(0).Children(func() {
					frostui.Strong(c, session.Title).Wrap()
					label := task.Label
					if task.Subagent != "" {
						label = task.Subagent + ": " + label
					}
					frostui.Body(c, label).Wrap()
					status := task.Status
					if task.Since > 0 {
						status += " · since " + clock(task.Since.Time())
					}
					if task.Stalled > 0 {
						status += " · stalled since " + clock(task.Stalled.Time())
					}
					frostui.Caption(c, status).Wrap()
				})
				if ui.Button(c, "Open").Label(fmt.Sprintf("Open running session %d", task.ConversationID)).Clicked() {
					a.open(task.ConversationID)
				}
				if task.Stoppable() && a.canSteer() {
					if ui.Button(c, "Stop").Disabled(s.busy).Label(fmt.Sprintf("Stop task %d in session %d", task.ID, task.ConversationID)).Clicked() {
						a.stopRunning(task)
					}
				}
			})
		}
	}
	for _, schedule := range s.data.Schedules {
		shown++
		ui.Row(c.Key(fmt.Sprintf("schedule:%d:%s", schedule.Session, schedule.ID))).Gap(frostui.SpaceM).Children(func() {
			ui.Column(c).Grow(1).Basis(0).Children(func() {
				frostui.Strong(c, schedule.SessionTitle).Wrap()
				frostui.Body(c, schedule.Text).Wrap()
				frostui.Caption(c, scheduleSummary(schedule)).Wrap()
			})
			if ui.Button(c, "Open schedule").Clicked() {
				a.open(schedule.Session)
			}
		})
	}
	if shown == 0 {
		frostui.Muted(c, "No running tasks or scheduled messages.")
	}
}

func scheduleSummary(s pocket.Schedule) string {
	text := s.Next.Time().Format("2006-01-02 15:04 MST")
	if s.Zone != "" {
		if zone, err := time.LoadLocation(s.Zone); err == nil {
			text = s.Next.Time().In(zone).Format("2006-01-02 15:04 MST")
		}
	}
	if s.Repeat != "" {
		text += " · " + s.Repeat
	}
	if s.Paused > 0 {
		text += " · paused"
	}
	if s.Held != "" {
		text += " · held: " + s.Held
	}
	if s.Skipped > 0 {
		text += fmt.Sprintf(" · skipped %d times (previous message still queued)", s.Skipped)
	}
	return text
}
