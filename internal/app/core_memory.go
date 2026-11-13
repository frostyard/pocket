package app

import (
	"context"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type coreMemory struct {
	pip                    int
	data                   *pocket.CoreMemory
	loading, dirty, loaded bool
	err                    string
	busy                   map[string]bool
}

func (a *App) coreMemoryAvailable() bool {
	return a.hello != nil && a.hello.User.Role == "owner" && slices.Contains(a.hello.Server.Extensions, "pocket-memory")
}

func (a *App) loadCoreMemory() {
	s := a.core
	if !a.coreMemoryAvailable() {
		a.core = nil
		return
	}
	if s == nil || s.pip == 0 {
		return
	}
	if s.loading {
		s.dirty = true
		return
	}
	s.loading = true
	client := a.link.client
	var data *pocket.CoreMemory
	a.request("Reading Pi Pocket memory", func(ctx context.Context, c *pocket.Client) error {
		var err error
		data, err = c.PendingMemory(ctx, s.pip)
		return err
	}, func(err error) {
		if a.core != s || a.link.client != client {
			return
		}
		s.loading, s.loaded = false, true
		switch {
		case err != nil:
			s.err = err.Error()
		case !data.PendingAll:
			s.data = nil
			s.err = "This server needs an update or restart to review memory across projects."
		default:
			s.data, s.err = data, ""
		}
		if s.dirty {
			s.dirty = false
			a.loadCoreMemory()
		}
	})
}

func (a *App) reviewCoreLesson(lesson *pocket.CoreLesson, action string) {
	s := a.core
	if s == nil || s.pip == 0 {
		return
	}
	if s.busy == nil {
		s.busy = map[string]bool{}
	}
	if s.busy[lesson.ID] {
		return
	}
	s.busy[lesson.ID] = true
	client := a.link.client
	a.request("Reviewing Pi Pocket memory", func(ctx context.Context, c *pocket.Client) error { return c.ReviewMemory(ctx, s.pip, lesson, action) }, func(err error) {
		if a.core != s || a.link.client != client {
			return
		}
		delete(s.busy, lesson.ID)
		if err != nil {
			a.toast("Lesson unchanged: " + err.Error())
		}
		a.loadCoreMemory()
	})
}

func (a *App) coreLessonCard(c *ui.Context, it *inboxItem) {
	l := it.coreLesson
	a.card(c, it.key, frostui.Neutral, "Pi Pocket memory: "+l.Text, func() {
		frostui.Strong(c, "Pi Pocket memory")
		frostui.Caption(c, firstNonEmpty(l.ProjectName, l.Scope)).Wrap()
		frostui.Body(c, l.Text).Wrap()
		if !l.Pending && l.Proposal != nil {
			if l.Proposal.Forget {
				frostui.Muted(c, "Proposes forgetting this lesson.").Wrap()
			} else if l.Proposal.Text != nil {
				frostui.Strong(c, "Proposed change")
				frostui.Body(c, *l.Proposal.Text).Wrap()
			}
		}
		busy := a.core != nil && a.core.busy[l.ID]
		ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
			if l.From != nil && ui.Button(c, "Open source session").Clicked() {
				a.open(l.From.ID)
			}
			if ui.Button(c, "Discard").Disabled(busy).Clicked() {
				a.reviewCoreLesson(l, "discard")
			}
			if ui.PrimaryButton(c, "Approve").Disabled(busy).Clicked() {
				a.reviewCoreLesson(l, "approve")
			}
		})
	})
}

func (a *App) coreMemoryNotice(c *ui.Context) {
	if !a.coreMemoryAvailable() {
		return
	}
	if a.core == nil || a.core.pip == 0 {
		frostui.Muted(c, "Pi Pocket memory: open your private PiP in the web app once to review its pending lessons here.").Wrap()
	} else if a.core.err != "" {
		frostui.Problem(c, "Pi Pocket memory: "+a.core.err).Wrap()
	}
}
