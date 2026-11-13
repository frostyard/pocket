package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type backgroundState struct {
	session                    int
	open, loading, busy, timer bool
	tasks                      []pocket.BackgroundTask
	selected                   string
	output                     *pocket.BackgroundOutput
	err                        string
}

func (a *App) openBackground() {
	if a.current == 0 {
		return
	}
	a.background = &backgroundState{session: a.current, open: true}
	a.loadBackground()
}

func (a *App) loadBackground() {
	s := a.background
	if s == nil || !s.open || s.loading || s.session != a.current || a.link.client == nil {
		return
	}
	s.loading = true
	selected := s.selected
	var tasks []pocket.BackgroundTask
	var output *pocket.BackgroundOutput
	var listErr error
	a.request("Reading background tasks", func(ctx context.Context, c *pocket.Client) error {
		var err error
		tasks, listErr = c.Background(ctx, s.session)
		err = listErr
		if err == nil && selected != "" {
			output, err = c.BackgroundOutput(ctx, s.session, selected)
		}
		return err
	}, func(err error) {
		if a.background != s {
			return
		}
		s.loading = false
		if listErr == nil {
			s.tasks = tasks
		}
		if selected != s.selected {
			a.loadBackground()
			return
		}
		s.err = ""
		if err != nil {
			s.err = err.Error()
			var api *pocket.APIError
			if listErr == nil && errors.As(err, &api) && api.Status == 404 {
				s.selected, s.output = "", nil
			}
		} else {
			s.tasks, s.output = tasks, output
		}
		if !s.timer && s.open {
			s.timer = true
			a.after(3*time.Second, func() {
				if a.background == s {
					s.timer = false
					a.loadBackground()
				}
			})
		}
	})
}

func (a *App) stopBackground(task pocket.BackgroundTask) {
	s := a.background
	if s == nil || s.busy || !a.canSteer() || !task.Live() {
		return
	}
	s.busy = true
	a.request("Stopping background task", func(ctx context.Context, c *pocket.Client) error {
		return c.StopRunning(ctx, pocket.RunningTask{Kind: "pocket.background", ConversationID: s.session, ID: task.TaskID})
	}, func(err error) {
		if a.background != s {
			return
		}
		s.busy = false
		if err != nil {
			a.toast("Could not stop background task: " + err.Error())
		}
		a.loadBackground()
	})
}

func (a *App) backgroundDialog(c *ui.Context) {
	s := a.background
	if s == nil || !s.open {
		return
	}
	ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(700).Gap(frostui.SpaceM).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Children(func() {
				frostui.Strong(c, "Background tasks").Grow(1)
				if ui.Button(c, "Close").Clicked() {
					s.open = false
				}
			})
			if s.err != "" {
				frostui.Problem(c, s.err).Wrap()
			}
			if s.loading {
				frostui.Caption(c, "Refreshing…")
			}
			ui.Scroll(c).Height(180).Children(func() {
				ui.Column(c).AlignItems(ui.Stretch).Gap(frostui.SpaceS).Children(func() {
					for _, task := range s.tasks {
						ui.Row(c.Key(task.ID)).Gap(frostui.SpaceS).Children(func() {
							ui.Column(c).Grow(1).Basis(0).Children(func() {
								frostui.Mono(c, task.Command).Wrap()
								frostui.Caption(c, firstNonEmpty(task.Outcome, task.Status)).Wrap()
							})
							if ui.Button(c, "Output").Label("Output for " + task.ID).Clicked() {
								s.selected, s.output = task.ID, nil
								a.loadBackground()
							}
							if task.Live() && a.canSteer() {
								if ui.Button(c, "Stop").Label("Stop background " + task.ID).Disabled(s.busy).Clicked() {
									a.stopBackground(task)
								}
							}
						})
					}
					if len(s.tasks) == 0 && !s.loading {
						frostui.Muted(c, "No background tasks.")
					}
				})
			})
			if s.selected != "" {
				frostui.Strong(c, "Output · "+s.selected)
				if s.output != nil {
					if s.output.Skipped > 0 {
						frostui.Caption(c, fmt.Sprintf("Latest output; %d earlier bytes omitted", s.output.Skipped))
					}
					ui.Scroll(c).Height(260).Children(func() { frostui.Mono(c, plainOutput(s.output.Text)).Wrap().Selectable() })
				}
			}
		})
	})
}

// plainOutput keeps a log readable without interpreting terminal controls.
func plainOutput(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		b := text[i]
		if b == 0x1b {
			i++
			if i >= len(text) {
				break
			}
			switch text[i] {
			case '[':
				for i++; i < len(text) && (text[i] < 0x40 || text[i] > 0x7e); i++ {
				}
			case ']', 'P', 'X', '^', '_':
				for i++; i < len(text); i++ {
					if text[i] == 7 {
						break
					}
					if text[i] == 0x1b && i+1 < len(text) && text[i+1] == '\\' {
						i++
						break
					}
				}
			}
			continue
		}
		if b >= 0x20 || b == '\n' || b == '\t' {
			out.WriteByte(b)
		}
	}
	return out.String()
}
