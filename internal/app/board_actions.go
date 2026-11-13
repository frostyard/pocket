package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type boardEditor struct {
	row                        pocket.BoardRow
	action, max, until, thread string
}

func (a *App) boardSupervisor(c *ui.Context, b *pocket.Board) {
	if b.Supervisor == nil {
		frostui.Muted(c, "The Desk knows no supervisor yet.").Wrap()
		return
	}
	if len(b.Supervisors) < 2 {
		return
	}
	var options []string
	ids := map[string]int{}
	selected := ""
	for _, id := range b.Supervisors {
		title, _ := b.Titles[strconv.Itoa(id)].(string)
		label := fmt.Sprintf("%s (%d)", firstNonEmpty(title, "Supervisor"), id)
		options = append(options, label)
		ids[label] = id
		chosen := a.desk.boardChosen
		if chosen == 0 {
			chosen = *b.Supervisor
		}
		if id == chosen {
			selected = label
		}
	}
	old := selected
	ui.Select(c, &selected, options).Label("Board supervisor").Disabled(a.desk.boardBusy || a.desk.boardLoading)
	if selected != old && ids[selected] != 0 {
		a.desk.boardChosen = ids[selected]
		a.desk.boardEditors = nil
		a.desk.boardOpen = nil
		a.loadBoard()
	}
}

func (a *App) boardRowDetails(c *ui.Context, r pocket.BoardRow) {
	ui.Column(c).Padding(frostui.SpaceM).Gap(frostui.SpaceS).AlignItems(ui.Stretch).Children(func() {
		frostui.Caption(c, fmt.Sprintf("%s · revision %d", r.State, r.Rev))
		for _, part := range []struct{ label, text string }{{"Task", r.Task}, {"Objective", r.Objective}, {"Stop when", r.StopWhen}, {"Worker", r.Worker}, {"Start", r.Start}, {"Note", r.Note}} {
			if part.text != "" {
				frostui.Strong(c, part.label)
				frostui.Body(c, part.text).Wrap()
			}
		}
		if r.PRCheck != nil {
			frostui.Strong(c, "PR check")
			frostui.Body(c, fmt.Sprintf("%s #%d · checks %s · label %s", r.PRCheck.Repo, r.PRCheck.PR, r.PRCheck.Checks, r.PRCheck.Label)).Wrap()
		}
		busy := a.desk.boardBusy || a.desk.boardLoading
		if a.desk.boardEditors == nil {
			a.desk.boardEditors = map[int]*boardEditor{}
		}
		begin := func(action string) {
			a.desk.boardEditors[r.ID] = &boardEditor{row: r, action: action, max: strconv.Itoa(r.Max), until: r.Until}
		}
		ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
			switch r.State {
			case "proposed", "picked":
				if ui.Button(c, "Edit limits").Disabled(busy).Clicked() {
					begin("edit")
				}
				if ui.Button(c, "Drop…").Disabled(busy).Clicked() {
					begin("drop")
				}
			case "waiting":
				if ui.Button(c, "Drop…").Disabled(busy).Clicked() {
					begin("drop")
				}
			case "failed":
				if ui.Button(c, "Reopen").Disabled(busy).Clicked() {
					a.boardChange("reopen", &r, nil, nil, "")
				}
			case "unknown":
				if ui.Button(c, "Resolve…").Disabled(busy).Clicked() {
					begin("resolve")
				}
			}
		})
		e := a.desk.boardEditors[r.ID]
		if e == nil {
			return
		}
		frostui.Caption(c, fmt.Sprintf("Editing revision %d", e.row.Rev))
		switch e.action {
		case "edit":
			frostui.Caption(c, "Maximum rounds")
			ui.TextInput(c, &e.max).Label("Maximum rounds").Placeholder("Maximum rounds").Disabled(busy)
			frostui.Caption(c, "Until (HH:MM, server time)")
			ui.TextInput(c, &e.until).Label("Until").Placeholder("Until (HH:MM, server time)").Disabled(busy)
			max, err := strconv.Atoi(strings.TrimSpace(e.max))
			_, untilErr := time.Parse("15:04", strings.TrimSpace(e.until))
			if ui.PrimaryButton(c, "Save limits").Disabled(busy || err != nil || max < 1 || max > 100 || untilErr != nil).Clicked() {
				a.boardChange("edit", &e.row, nil, map[string]any{"max": max, "until": strings.TrimSpace(e.until)}, "")
			}
		case "drop":
			frostui.Body(c, "Drop this row? Dropped rows cannot be reopened.").Wrap()
			if ui.PrimaryButton(c, "Confirm drop").Disabled(busy).Clicked() {
				a.boardChange("drop", &e.row, nil, nil, "")
			}
		case "resolve":
			frostui.Muted(c, "Link the thread that launched. If none launched, mark this row failed so it can be reopened.").Wrap()
			ui.TextInput(c, &e.thread).Label("Launched thread").Placeholder("Thread id, such as T12").Disabled(busy)
			ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
				if ui.PrimaryButton(c, "Link thread").Disabled(busy || strings.TrimSpace(e.thread) == "").Clicked() {
					a.boardChange("resolve", &e.row, nil, nil, strings.TrimSpace(e.thread))
				}
				if ui.Button(c, "Mark failed").Disabled(busy).Clicked() {
					a.boardChange("resolve", &e.row, nil, nil, "")
				}
			})
		}
		if ui.Button(c, "Cancel").Disabled(busy).Clicked() {
			delete(a.desk.boardEditors, r.ID)
		}
	})
}
