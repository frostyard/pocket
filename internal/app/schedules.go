package app

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type scheduleForm struct {
	session                                   int
	open                                      bool
	schedule                                  pocket.Schedule
	when, zone, text, next, originalNext, err string
}

func (a *App) editSchedule(s pocket.Schedule) {
	if a.scheduleBusy || a.current == 0 {
		return
	}
	zone := firstNonEmpty(s.Zone, "UTC")
	loc, err := time.LoadLocation(zone)
	if err != nil {
		a.toast("Unknown schedule timezone: " + zone)
		return
	}
	next := s.Next.Time().In(loc).Format("2006-01-02 15:04")
	a.scheduleForm = &scheduleForm{session: a.current, open: true, schedule: s, text: s.Text, next: next, originalNext: next, zone: zone}
}

func (a *App) scheduleAction(s pocket.Schedule, op string) {
	if a.scheduleBusy || a.current == 0 || !a.canSteer() {
		return
	}
	id := a.current
	a.scheduleBusy = true
	a.request(op+" schedule", func(ctx context.Context, c *pocket.Client) error {
		if op == "Cancel" {
			return c.CancelSchedule(ctx, id, s.ID)
		}
		return c.PauseSchedule(ctx, id, s.ID, s.Paused == 0)
	}, func(err error) {
		a.scheduleBusy = false
		if err != nil {
			a.toast(op + " schedule: " + err.Error())
		}
	})
}

func (a *App) saveSchedule(f *scheduleForm) {
	if a.scheduleForm != f || !f.open || a.scheduleBusy || !a.canSteer() {
		return
	}
	f.err = ""
	s := f.schedule
	s.Text, s.Zone = strings.TrimSpace(f.text), strings.TrimSpace(f.zone)
	if s.ID != "" {
		if s.Text == "" {
			f.err = "Enter a message."
			return
		}
		if f.next != f.originalNext || s.Zone != f.schedule.Zone {
			loc, err := time.LoadLocation(s.Zone)
			if err != nil {
				f.err = "Enter a valid timezone, such as America/New_York or UTC."
				return
			}
			next, err := time.ParseInLocation("2006-01-02 15:04", f.next, loc)
			if err != nil {
				f.err = "Enter the next time as YYYY-MM-DD HH:MM."
				return
			}
			s.Next = pocket.Millis(next.UnixMilli())
		}
	} else if strings.TrimSpace(f.when) == "" {
		f.err = "Enter when and what Pi should receive."
		return
	}
	id, when, zone := f.session, f.when, f.zone
	a.scheduleBusy = true
	a.request("Saving schedule", func(ctx context.Context, c *pocket.Client) error {
		if s.ID == "" {
			return c.Schedule(ctx, id, when, zone)
		}
		return c.EditSchedule(ctx, id, s)
	}, func(err error) {
		a.scheduleBusy = false
		if a.scheduleForm != f {
			if err != nil {
				a.toast("Saving schedule: " + err.Error())
			}
			return
		}
		if err != nil {
			f.err = err.Error()
			return
		}
		f.open = false
	})
}

func (a *App) schedulesSection(c *ui.Context) {
	frostui.SectionHeader(c, frostui.SectionHead{Title: "Scheduled messages", Quiet: true})
	if a.canSteer() {
		if ui.Button(c, "Schedule a message").Disabled(a.scheduleBusy).Clicked() {
			a.editSchedule(pocket.Schedule{})
		}
	}
	if len(a.view.Schedules) == 0 {
		frostui.Caption(c, "Nothing scheduled.")
	}
	for _, s := range a.view.Schedules {
		ui.Column(c.Key(s.ID)).Gap(frostui.SpaceXS).AlignItems(ui.Stretch).Children(func() {
			frostui.Body(c, s.Text).Wrap()
			frostui.Caption(c, scheduleSummary(s)).Wrap()
			if !a.canSteer() {
				return
			}
			ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
				if s.Version != nil {
					if ui.Button(c, "Edit").Disabled(a.scheduleBusy).Clicked() {
						a.editSchedule(s)
					}
					label := "Pause"
					if s.Paused > 0 {
						label = "Resume"
					}
					if ui.Button(c, label).Disabled(a.scheduleBusy).Clicked() {
						a.scheduleAction(s, label)
					}
				}
				if ui.Button(c, "Cancel").Disabled(a.scheduleBusy).Clicked() {
					a.scheduleAction(s, "Cancel")
				}
			})
		})
	}
}

func (a *App) scheduleDialog(c *ui.Context) {
	f := a.scheduleForm
	if f == nil || !f.open {
		return
	}
	ui.Modal(c, &f.open, func() {
		ui.Column(c).Width(560).Gap(frostui.SpaceM).AlignItems(ui.Stretch).Children(func() {
			frostui.Strong(c, "Scheduled message")
			if f.schedule.ID == "" {
				frostui.Body(c, "When and what should Pi receive?")
				ui.TextInput(c, &f.when).Label("When and message").Placeholder("in 2h check the deploy").AutoFocus().Disabled(a.scheduleBusy)
				frostui.Caption(c, "Examples: tomorrow 9am check the build; every weekday 8:00 report progress.").Wrap()
			} else {
				ui.TextArea(c, &f.text).Label("Scheduled message").Height(100).Disabled(a.scheduleBusy)
				frostui.Body(c, "Next time (YYYY-MM-DD HH:MM)")
				ui.TextInput(c, &f.next).Label("Next scheduled time").Disabled(a.scheduleBusy)
				if f.schedule.Repeat != "" {
					frostui.Caption(c, "Repeats: "+f.schedule.Repeat+". This edit keeps the repeat rule.").Wrap()
				}
			}
			frostui.Body(c, "Timezone")
			ui.TextInput(c, &f.zone).Label("Schedule timezone").Disabled(a.scheduleBusy)
			if f.err != "" {
				frostui.Problem(c, f.err).Wrap()
			}
			ui.Row(c).Gap(frostui.SpaceS).Justify(ui.End).Children(func() {
				if ui.Button(c, "Close").Clicked() {
					f.open = false
				}
				if ui.PrimaryButton(c, "Save schedule").Disabled(a.scheduleBusy).Clicked() {
					a.saveSchedule(f)
				}
			})
		})
	})
}
