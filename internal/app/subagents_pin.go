package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// subagentPin is the subagents bar's state, which belongs to the composer
// so that each session keeps its own: a session's list is folded or
// unfolded for that session, and put away for that account.
type subagentPin struct {
	// open is the session whose list is unfolded, 0 for none, as the web
	// app's subagentsOpen.
	open int
	// away is when each session's bar was put away, under the account it
	// was put away in: the server's time of the newest thing its subagents
	// had done then. It shows again once one does something newer, so the
	// marks belong to an account and a session, never to the ID alone.
	away map[accountKey]map[int]pocket.Millis
	// peeked are the working subagents last handed to watchPeeks, so a
	// frame asks for their peeks only when that set changes.
	peeked []int
}

// subagentState says where a subagent is: working, stopped, failed, or
// done.
func subagentState(sa pocket.Subagent) string {
	switch {
	case sa.Busy:
		return "working"
	case sa.Stopped:
		return "stopped"
	case sa.Failed:
		return "failed"
	}
	return "done"
}

// subagentNewest is the server's time of the newest thing a subagent did:
// it was asked, or it answered.
func subagentNewest(sa pocket.Subagent) pocket.Millis {
	return max(sa.AskedAt, sa.AnsweredAt)
}

// subagentRank orders the states, in the dots and in the rows: working
// first, then trouble, then done.
func subagentRank(state string) int {
	switch state {
	case "working":
		return 0
	case "failed":
		return 1
	case "stopped":
		return 2
	}
	return 3
}

// orderedSubagents returns a sorted copy of agents, as the web app's
// ordered: by state, the working ones newest asked first, the rest newest
// answered first.
func orderedSubagents(agents []pocket.Subagent) []pocket.Subagent {
	out := slices.Clone(agents)
	slices.SortStableFunc(out, func(x, y pocket.Subagent) int {
		rx, ry := subagentRank(subagentState(x)), subagentRank(subagentState(y))
		if rx != ry {
			return rx - ry
		}
		if subagentState(x) == "working" {
			return cmpMillisDesc(x.AskedAt, y.AskedAt)
		}
		return cmpMillisDesc(x.AnsweredAt, y.AnsweredAt)
	})
	return out
}

// cmpMillisDesc orders 2 server times newest first.
func cmpMillisDesc(x, y pocket.Millis) int {
	switch {
	case x > y:
		return -1
	case x < y:
		return 1
	}
	return 0
}

// subagentKinds is the order the kinds take in a summary: trouble before
// what went well, because the end of the line may not fit on a phone.
var subagentKinds = []string{"working", "failed", "stopped", "done"}

// subagentSummary says at a glance how many subagents are in each state:
// "3 subagents working" when one kind says it all, else "2 working · 1
// failed · 1 done".
func subagentSummary(agents []pocket.Subagent) string {
	count := map[string]int{}
	for _, sa := range agents {
		count[subagentState(sa)]++
	}
	var kinds []string
	for _, kind := range subagentKinds {
		if count[kind] > 0 {
			kinds = append(kinds, kind)
		}
	}
	if len(kinds) == 1 {
		word := "subagents"
		if len(agents) == 1 {
			word = "subagent"
		}
		return fmt.Sprintf("%d %s %s", len(agents), word, kinds[0])
	}
	parts := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		parts = append(parts, fmt.Sprintf("%d %s", count[kind], kind))
	}
	return strings.Join(parts, " · ")
}

// subagentElapsed says how long ago the server took d: "45s", "3m 05s",
// "1h 12m".
func subagentElapsed(d time.Duration) string {
	seconds := max(0, int(d.Round(time.Second)/time.Second))
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm %02ds", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%dh %02dm", seconds/3600, seconds/60%60)
}

// pinnedSubagents returns this session's subagents for the bar: those that
// work, those with a report on its way to Pi, and those that did something
// since the bar was put away. active reports whether one works or reports,
// which the bar cannot be put away while it does.
func (a *App) pinnedSubagents() (shown []pocket.Subagent, active bool) {
	if a.view == nil {
		return nil, false
	}
	away := a.composer.pin.away[a.account][a.current]
	for _, sa := range a.view.Subagents {
		if !sa.Busy && !sa.Reporting && subagentNewest(sa) <= away {
			continue
		}
		shown = append(shown, sa)
		if sa.Busy || sa.Reporting {
			active = true
		}
	}
	return shown, active
}

// subagentDoing says what a subagent does this moment, from its peek: the
// call it waits on, the limit it waits for, its last step, or that it works.
func (a *App) subagentDoing(sa pocket.Subagent) string {
	if sa.Limited != 0 {
		return "Waiting for a usage limit to clear"
	}
	peek := a.peeks[sa.ConversationID]
	if tool, subject, waiting := approvalOf(peek, sa.ConversationID); waiting {
		return strings.TrimSpace("Waiting for approval: " + tool + " " + subject)
	}
	if step := a.lastStep(sa.ConversationID); step != "" {
		return step
	}
	if peek == nil {
		// Before its peek arrives, nothing is known but that it works.
		return "Working…"
	}
	return "Thinking…"
}

// approvalOf returns the call a peek waits on, for the session it belongs
// to or the first one it names.
func approvalOf(peek *pocket.Peek, id int) (tool, subject string, ok bool) {
	if peek == nil || len(peek.Approvals) == 0 {
		return "", "", false
	}
	ap := peek.Approvals[0]
	for _, a := range peek.Approvals {
		if a.ConversationID == id {
			ap = a
			break
		}
	}
	return ap.Tool, ap.Subject, true
}

// subagentTone is the color of a state's dot: working on the accent, a
// failure in the danger color, a stop in the warning one, and done in the
// success one.
func subagentTone(sa pocket.Subagent) frostui.Tone {
	switch subagentState(sa) {
	case "working":
		return frostui.Accent
	case "failed":
		return frostui.Danger
	case "stopped":
		return frostui.Warning
	}
	return frostui.Success
}

// subagentStep is the job status a state draws, as the inspector's
// subagent rows draw it.
func subagentStep(sa pocket.Subagent) frostui.StepState {
	switch subagentState(sa) {
	case "working":
		return frostui.StepRunning
	case "failed":
		return frostui.StepFailed
	case "stopped":
		return frostui.StepSkipped
	}
	return frostui.StepDone
}

// subagentModel is the model a subagent runs on, with its thinking level
// but "off": "gpt-6.1-sol · max".
func subagentModel(sa pocket.Subagent) string {
	if sa.RunsOn == nil {
		return ""
	}
	text := sa.RunsOn.Name
	if sa.RunsOn.Thinking != "" && sa.RunsOn.Thinking != "off" {
		text += " · " + sa.RunsOn.Thinking
	}
	return text
}

// subagentWhen says when it is, in the session's own terms: how long a
// working one has run, and how long ago the others ended.
func (a *App) subagentWhen(sa pocket.Subagent) string {
	state := subagentState(sa)
	if sa.Busy {
		if sa.AskedAt != 0 {
			return subagentElapsed(a.serverNow().Sub(sa.AskedAt.Time()))
		}
		return ""
	}
	if sa.AnsweredAt == 0 {
		return ""
	}
	when := state + " " + ago(a.serverNow(), sa.AnsweredAt.Time())
	if state == "failed" && sa.Dealt {
		// A failure someone dealt with says so, as the board does.
		when += " · dismissed"
	}
	return when
}

// watchSubagentPeeks follows the peeks of the subagents that work, so the
// bar says what each does this moment: it asks again only when that set
// changes.
func (a *App) watchSubagentPeeks() {
	pin := &a.composer.pin
	var busy []int
	for _, sa := range a.view.Subagents {
		if sa.Busy {
			busy = append(busy, sa.ConversationID)
		}
	}
	slices.Sort(busy)
	if slices.Equal(busy, pin.peeked) {
		return
	}
	pin.peeked = busy
	a.watchPeeks()
}

// subagentsPin draws this session's subagents above the field, as the web
// app's subagents bar: it shows while one works or has a report on its way
// to Pi, and after they finish until it is put away. Folded, 1 line: how
// many are in each state and what the newest working one does. Unfolded, a
// row each.
func (a *App) subagentsPin(c *ui.Context) {
	if a.view == nil {
		return
	}
	a.watchSubagentPeeks()
	shown, active := a.pinnedSubagents()
	if len(shown) == 0 {
		return
	}
	ui.Column(c).AlignItems(ui.Center).Padding(frostui.SpaceS, frostui.SpaceXL, frostui.SpaceS, frostui.SpaceXL).Children(func() {
		ui.Column(c).MaxWidth(860).FillWidth().AlignItems(ui.Stretch).Gap(frostui.SpaceS).Children(func() {
			ui.Column(c).AlignItems(ui.Stretch).Radius(12).BorderWidth(1, 1, 1, 1).BorderColor(c.Theme().Border).
				Background(frostui.Panel(c.Theme())).Padding(frostui.SpaceS, frostui.SpaceM).Gap(frostui.SpaceXS).Children(func() {
				a.subagentsTop(c, shown, active)
				if a.composer.pin.open == a.current {
					ui.Scroll(c).MaxHeight(queueDockHeight).Children(func() {
						ui.Column(c).AlignItems(ui.Stretch).Gap(frostui.SpaceXS).Children(func() {
							for _, sa := range orderedSubagents(shown) {
								a.subagentPinRow(c.Key(sa.ConversationID), sa)
							}
						})
					})
				}
			})
		})
	})
}

// subagentsTop draws the bar's folded line, which unfolds the list, and the
// way to put it away once nothing works or reports.
func (a *App) subagentsTop(c *ui.Context, shown []pocket.Subagent, active bool) {
	t := c.Theme()
	pin := &a.composer.pin
	ordered := orderedSubagents(shown)
	summary := subagentSummary(shown)
	folded := pin.open != a.current
	var lead *pocket.Subagent
	for i := range ordered {
		if ordered[i].Busy {
			lead = &ordered[i]
			break
		}
	}
	// What the button says, the newest one's work too: the label stands for
	// everything in the bar.
	label := "Subagents: " + summary
	if lead != nil && folded {
		label += ". " + lead.Name + ": " + a.subagentDoing(*lead)
	}
	ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
		button := ui.ButtonBase(c).Row().Gap(frostui.SpaceS).AlignItems(ui.Center).
			Padding(4, frostui.SpaceS).Radius(6).Cursor(ui.CursorPointer).Label(label).Grow(1)
		if button.Hovered() {
			button.Background(t.SurfaceHover)
		}
		if button.Clicked() {
			if folded {
				pin.open = a.current
			} else {
				pin.open = 0
			}
		}
		button.Children(func() {
			ui.Row(c).Gap(3).AlignItems(ui.Center).Children(func() {
				for i, sa := range ordered {
					if i == 6 {
						frostui.Caption(c, fmt.Sprintf("+%d", len(ordered)-6))
						break
					}
					ui.Box(c).Size(7, 7).Radius(4).Background(frostui.ToneColor(t, subagentTone(sa)))
				}
			})
			frostui.Caption(c, summary).SingleLine()
			if lead != nil && folded {
				frostui.Muted(c, lead.Name+": "+a.subagentDoing(*lead)).SingleLine().Grow(1)
			} else {
				ui.Spacer(c)
			}
			if folded {
				frostui.Icon(c, "go-next")
				return
			}
			frostui.Icon(c, "pan-down")
		})
		if active {
			return
		}
		if frostui.IconButton(c, "window-close", "Put the subagents bar away").Clicked() {
			a.putAwaySubagents(shown)
		}
	})
}

// putAwaySubagents puts the bar away for the session shown, until one of
// its subagents does something newer.
func (a *App) putAwaySubagents(shown []pocket.Subagent) {
	pin := &a.composer.pin
	var newest pocket.Millis
	for _, sa := range shown {
		newest = max(newest, subagentNewest(sa))
	}
	if pin.away == nil {
		pin.away = map[accountKey]map[int]pocket.Millis{}
	}
	if pin.away[a.account] == nil {
		pin.away[a.account] = map[int]pocket.Millis{}
	}
	pin.away[a.account][a.current] = newest
	pin.open = 0
}

// subagentPinRow draws one subagent of the unfolded bar: where it is, what
// it was asked, what it does, and the way to open it or stop it.
func (a *App) subagentPinRow(c *ui.Context, sa pocket.Subagent) {
	t := c.Theme()
	state := subagentState(sa)
	ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
		row := ui.ButtonBase(c).Row().Gap(frostui.SpaceS).AlignItems(ui.Center).Padding(6, frostui.SpaceS).
			Radius(6).Cursor(ui.CursorPointer).Label("Open subagent " + sa.Name).Grow(1)
		if row.Hovered() {
			row.Background(t.SurfaceHover)
		}
		if row.Clicked() {
			a.open(sa.ConversationID)
		}
		row.Children(func() {
			ui.Column(c).Grow(1).Gap(2).AlignItems(ui.Stretch).Children(func() {
				ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
					frostui.JobStatus(c, frostui.RowStatus{State: subagentStep(sa), Label: sa.Name,
						Compact: true, Tooltip: sa.Error})
					frostui.Strong(c, sa.Name).SingleLine()
					if when := a.subagentWhen(sa); when != "" {
						frostui.Caption(c, when).SingleLine()
					}
					if model := subagentModel(sa); model != "" {
						frostui.Caption(c, model).SingleLine()
					}
					if sa.Reporting {
						frostui.Pill(c, "report on its way to Pi", frostui.Accent)
					}
				})
				if sa.Busy {
					frostui.Muted(c, a.subagentDoing(sa)).SingleLine()
				}
				if state == "failed" && sa.Error != "" {
					frostui.Toned(c, firstLine(sa.Error), frostui.Danger).SingleLine()
				}
				if sa.Asked != "" {
					frostui.Caption(c, truncate(firstLine(sa.Asked), 120)).SingleLine()
				}
			})
		})
		if !sa.Busy || !a.canSteer() {
			return
		}
		if frostui.IconButton(c, "pocket-stop", "Stop "+sa.Name).Clicked() {
			id := sa.ConversationID
			a.request("Stopping "+sa.Name, func(ctx context.Context, cl *pocket.Client) error {
				return cl.Abort(ctx, id)
			}, nil)
		}
	})
}

// parentPin draws the way back to the session that started the subagent
// shown, as the web app's parent session button.
func (a *App) parentPin(c *ui.Context) {
	if a.view == nil || a.view.Conversation.Parent == nil {
		return
	}
	t := c.Theme()
	parent := a.view.Conversation.Parent
	ui.Column(c).AlignItems(ui.Center).Padding(frostui.SpaceS, frostui.SpaceXL, 0, frostui.SpaceXL).Children(func() {
		ui.Column(c).MaxWidth(860).FillWidth().AlignItems(ui.Stretch).Children(func() {
			row := ui.ButtonBase(c).Row().Gap(frostui.SpaceS).AlignItems(ui.Center).
				Padding(frostui.SpaceS, frostui.SpaceM).Radius(12).Cursor(ui.CursorPointer).
				Background(frostui.Panel(t)).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).
				Label("Back to main session: " + parent.Title).Tooltip("Return to " + parent.Title)
			if row.Hovered() {
				row.Background(t.SurfaceHover)
			}
			if row.Clicked() {
				a.open(parent.ID)
			}
			row.Children(func() {
				frostui.Icon(c, "go-previous")
				frostui.Label(c, "Back to main session").SingleLine()
				frostui.Muted(c, parent.Title).SingleLine().Grow(1)
			})
		})
	})
}
