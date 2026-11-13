package app

import (
	"strings"
	"time"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type threadEditor struct {
	action, text, when string
	into               string
}

func shortDeskLine(text string) bool {
	text = strings.TrimSpace(text)
	return text != "" && len(utf16.Encode([]rune(text))) <= 200 && !strings.ContainsAny(text, "\r\n")
}

func canWatch(t *pocket.DeskThread, now time.Time) bool {
	if t.Origin == "plan" {
		return false
	}
	if t.State == "open" || t.State == "starting" {
		return true
	}
	return t.State == "closed" && t.Closed != nil && t.Closed.Why == "done" && now.Sub(t.Closed.At.Time()) <= 7*24*time.Hour
}

func dueWatch(t *pocket.DeskThread) bool {
	return t.State != "closed" && t.State != "parked" && t.Origin != "plan" && t.Watch != nil && t.Watch.Due && t.Watch.Result == nil
}

func (a *App) threadActions(c *ui.Context, t *pocket.DeskThread) {
	c = c.Key("actions:" + t.ID)
	if a.desk.editors == nil {
		a.desk.editors = map[string]*threadEditor{}
	}
	e := a.desk.editors[t.ID]
	if e == nil {
		e = &threadEditor{}
		a.desk.editors[t.ID] = e
	}
	busy := a.desk.busy["act:"+t.ID]
	toggle := func(action string) {
		if e.action == action {
			e.action = ""
		} else {
			*e = threadEditor{action: action}
		}
	}
	ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
		if t.Stale || (t.State == "closed" && canWatch(t, a.serverNow())) {
			if ui.Button(c, "Keep").Disabled(busy).Clicked() {
				a.threadAct(t, "keep", nil)
			}
		}
		if t.State == "open" && t.Origin != "found" {
			if ui.Button(c, "Park").Disabled(busy).Clicked() {
				a.threadAct(t, "park", nil)
			}
		}
		if t.State == "parked" {
			if ui.Button(c, "Wake").Disabled(busy).Clicked() {
				a.threadAct(t, "wake", nil)
			}
		}
		if t.State != "closed" {
			if t.Origin == "found" && ui.Button(c, "Attach…").Disabled(busy).Clicked() {
				toggle("attach")
			}
			if ui.Button(c, "Drop…").Disabled(busy).Clicked() {
				toggle("drop")
			}
		}
		if canWatch(t, a.serverNow()) {
			if ui.Button(c, "Watch…").Disabled(busy).Clicked() {
				toggle("watch")
			}
			if t.State != "closed" && t.Watch != nil {
				if ui.Button(c, "Clear watch").Disabled(busy).Clicked() {
					a.threadAct(t, "unwatch", nil)
				}
				if ui.Button(c, "Pass").Disabled(busy).Clicked() {
					a.threadAct(t, "result", map[string]any{"pass": true, "text": "Passed in Pocket."})
				}
				if ui.Button(c, "Fail…").Disabled(busy).Clicked() {
					toggle("fail")
				}
			}
		}
	})
	switch e.action {
	case "drop", "fail", "watch":
		label, button := "Reason for dropping", "Confirm drop"
		if e.action == "fail" {
			label, button = "What failed", "Record failure"
		}
		if e.action == "watch" {
			label, button = "What to check", "Set watch"
			ui.TextInput(c, &e.when).Label("Check date and optional time").Placeholder("YYYY-MM-DD or YYYY-MM-DD HH:MM").Disabled(busy).FillWidth()
			frostui.Caption(c, "Date and time use the server's timezone.").Wrap()
		}
		ui.TextInput(c, &e.text).Label(label).Placeholder(label + " (up to 200 characters)").Disabled(busy).FillWidth()
		ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
			if ui.Button(c, "Cancel").Disabled(busy).Clicked() {
				e.action = ""
			}
			if ui.PrimaryButton(c, button).Disabled(busy || !shortDeskLine(e.text) || (e.action == "watch" && strings.TrimSpace(e.when) == "")).Clicked() {
				body := map[string]any{"reason": strings.TrimSpace(e.text)}
				action := e.action
				if action == "fail" {
					action = "result"
					body = map[string]any{"pass": false, "text": strings.TrimSpace(e.text)}
				}
				if action == "watch" {
					body = map[string]any{"checkOn": strings.TrimSpace(e.when), "check": strings.TrimSpace(e.text)}
				}
				a.threadAct(t, action, body)
			}
		})
	case "attach":
		targets := map[string]string{}
		options := []string{"Choose a thread"}
		if a.desk.view != nil {
			for i := range a.desk.view.Threads {
				other := &a.desk.view.Threads[i]
				if other.ID != t.ID && other.State != "closed" {
					label := other.ID + " · " + other.Title
					targets[label] = other.ID
					options = append(options, label)
				}
			}
		}
		if targets[e.into] == "" {
			e.into = options[0]
		}
		ui.Select(c, &e.into, options).Label("Attach to thread").Disabled(busy).FillWidth()
		ui.Row(c).Gap(frostui.SpaceS).Children(func() {
			if ui.Button(c, "Cancel").Disabled(busy).Clicked() {
				e.action = ""
			}
			if ui.PrimaryButton(c, "Attach").Disabled(busy || targets[e.into] == "").Clicked() {
				a.threadAct(t, "attach", map[string]any{"into": targets[e.into]})
			}
		})
	}
}

func (a *App) watchItem(c *ui.Context, it *inboxItem) {
	a.card(c, it.key, frostui.Warning, it.thread.Title, func() {
		a.threadHead(c, it.thread)
		frostui.Body(c, it.thread.Watch.Check).Wrap()
		frostui.Caption(c, "Due "+it.thread.Watch.CheckOn+" "+it.thread.Watch.CheckTime)
		a.threadActions(c, it.thread)
	})
}
