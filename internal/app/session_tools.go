package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type sessionFind struct {
	query, picked string
	open, focus   bool
}
type sessionOperation struct{ busy bool }

func (a *App) sessionToolsMenu(c *ui.Context) {
	ui.MenuButton(c, "Session", func(m *ui.Menu) {
		a.table.Menu("find-session", "fork-session", "export-session", "new-context")(m)
	}).Label("Session tools")
}

func (a *App) sessionOperation(op string) {
	id, view := a.current, a.view
	if id == 0 {
		return
	}
	if a.sessionOp != nil && a.sessionOp.busy {
		a.toast("Wait for the current session operation to finish")
		return
	}
	s := &sessionOperation{busy: true}
	a.sessionOp = s
	var fork int
	var saved bool
	save := a.opts.SaveFile
	a.requestWithin(2*time.Minute, op, func(ctx context.Context, c *pocket.Client) error {
		switch op {
		case "Fork session":
			var err error
			fork, err = c.Fork(ctx, id)
			return err
		case "New context":
			return c.ResetContext(ctx, id)
		case "Export session":
			data, err := c.Export(ctx, id)
			if err != nil {
				return err
			}
			if save == nil {
				return fmt.Errorf("file saving is unavailable")
			}
			saved, err = save(ctx, SaveFileOptions{Name: fmt.Sprintf("session-%d.md", id), Title: "Export session", Filter: "Markdown", Extensions: []string{"md"}}, data)
			return err
		}
		return fmt.Errorf("unknown session operation")
	}, func(err error) {
		if a.sessionOp != s {
			return
		}
		s.busy = false
		if err != nil {
			a.toast(op + ": " + err.Error())
			return
		}
		if op == "Fork session" && fork > 0 {
			if a.view == view && a.current == id {
				a.open(fork)
			} else {
				a.toast(fmt.Sprintf("Fork created: session %d", fork))
			}
		}
		if op == "Export session" && saved {
			a.toast("Session exported")
		}
		if op == "New context" {
			a.toast("New context started")
		}
	})
}

func (a *App) confirmNewContext() {
	id, view, client := a.current, a.view, a.link.client
	if id == 0 {
		return
	}
	a.dialogs.Ask(frostui.Confirm{Title: "Start a new context?", Body: "Pi will stop using the current context. The transcript stays in the session history. This cannot be undone.", Action: "New context", Destructive: true, OnConfirm: func() {
		if a.current == id && a.view == view && a.link.client == client {
			a.sessionOperation("New context")
		}
	}})
}

// Search all text represented by a loaded row, including folded calls/results.
func itemSearchText(it item) string {
	text := it.text
	if it.entry != nil {
		text += "\n" + strings.Join(it.entry.Files, "\n")
	}
	if it.kind == itemShell && it.entry != nil {
		text += "\n" + it.entry.Command + "\n" + it.entry.Output
	}
	for _, call := range it.calls {
		text += "\n" + call.block.Name + "\n" + searchValues(call.block.Args)
		if call.result != nil {
			text += "\n" + call.result.Text + "\n" + call.result.Command + "\n" + call.result.Output
		}
	}

	return strings.ToLower(text)
}

// Values retain literal quotes, operators and newlines; wire metadata is not searchable.
func searchValues(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case map[string]any:
		var parts []string
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts = append(parts, searchValues(value[key]))
		}
		return strings.Join(parts, "\n")
	case []any:
		var parts []string
		for _, child := range value {
			parts = append(parts, searchValues(child))
		}
		return strings.Join(parts, "\n")
	case nil:
		return ""
	default:
		return fmt.Sprint(value)
	}
}

func (a *App) findMatches() []int {
	q := strings.ToLower(strings.TrimSpace(a.tr.find.query))
	if q == "" {
		return nil
	}
	if a.tr.search == nil || len(a.tr.search) != len(a.tr.items) {
		a.tr.search = make([]string, len(a.tr.items))
		for i, it := range a.tr.items {
			a.tr.search[i] = itemSearchText(it)
		}
	}
	var found []int
	for i, text := range a.tr.search {
		if strings.Contains(text, q) {
			found = append(found, i)
		}
	}
	return found
}

func (a *App) chooseFind(matches []int, delta int) {
	if len(matches) == 0 {
		a.tr.find.picked = ""
		return
	}
	at := -1
	for n, i := range matches {
		if a.tr.items[i].key == a.tr.find.picked {
			at = n
			break
		}
	}
	if at < 0 {
		at = 0
	} else {
		at = (at + delta + len(matches)) % len(matches)
	}
	i := matches[at]
	a.tr.find.picked = a.tr.items[i].key
	if a.tr.open == nil {
		a.tr.open = map[string]bool{}
	}
	a.tr.open[a.tr.items[i].key] = true
	for _, call := range a.tr.items[i].calls {
		a.tr.open["call:"+call.block.ID] = true
	}
	a.tr.list.ScrollTo(i, ui.Center)
}

func (a *App) findBar(c *ui.Context) {
	f := &a.tr.find
	if !f.open {
		return
	}
	ui.Column(c).Padding(frostui.SpaceS).Gap(frostui.SpaceXS).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
			old := f.query
			field := ui.TextInput(c, &f.query).Label("Find in session").Placeholder("Find in loaded messages…").AutoFocus().Grow(1)
			if f.focus {
				field.Focus()
				f.focus = false
			}
			field.OnTextKey(func(k ui.TextKey) bool {
				switch k.Key {
				case ui.KeyEscape:
					f.open = false
					f.picked = ""
					return true
				case ui.KeyEnter:
					delta := 1
					if k.Mods&ui.Shift != 0 {
						delta = -1
					}
					a.chooseFind(a.findMatches(), delta)
					return true
				}
				return false
			})
			matches := a.findMatches()
			if old != f.query {
				f.picked = ""
				a.chooseFind(matches, 0)
			}
			frostui.Caption(c, plural(len(matches), "matching row", "matching rows"))
			if ui.Button(c, "Previous").Disabled(len(matches) == 0).Clicked() {
				a.chooseFind(matches, -1)
			}
			if ui.Button(c, "Next").Disabled(len(matches) == 0).Clicked() {
				a.chooseFind(matches, 1)
			}
			if ui.Button(c, "Close find").Clicked() {
				f.open = false
				f.picked = ""
			}
		})
		if _, missing := a.view.Earliest(); missing {
			if ui.Button(c, "Search earlier messages").Disabled(a.tr.earlier).Clicked() {
				a.loadEarlier()
			}
		}
		frostui.Caption(c, "Searches loaded messages in this context, including folded text and tool output.").Wrap()
	})
}
