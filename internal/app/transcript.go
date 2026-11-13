package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// transcript is the conversation pane's state.
type transcript struct {
	find   sessionFind
	list   ui.ListState
	items  []item
	search []string
	rev    int
	live   int
	// open holds what the person opened: tool groups, calls, thoughts.
	open map[string]bool
	// closed holds what the person closed that opens by itself.
	closed  map[string]bool
	earlier bool
}

// itemKind says what a transcript row shows.
type itemKind int

const (
	itemUser itemKind = iota
	itemReport
	itemText
	itemThinking
	itemTools
	itemError
	itemNote
	itemCompaction
	itemShell
	itemLive
	itemEarlier
)

// item is one row of the transcript.
type item struct {
	kind  itemKind
	key   string
	entry *pocket.Entry
	text  string
	calls []call
	at    time.Time
}

// call is a tool call with what came of it.
type call struct {
	block  pocket.Block
	result *pocket.Entry
	slot   *pocket.ToolSlot
	asked  *pocket.Approval
	at     time.Time
}

// viewChanged rebuilds the rows when the view's entries changed.
func (tr *transcript) viewChanged(a *App) {
	v := a.view
	if v.Rev == tr.rev && v.LiveRev == tr.live && tr.items != nil {
		return
	}
	tr.rev, tr.live = v.Rev, v.LiveRev
	tr.items = buildItems(v)
	tr.search = nil
}

// buildItems flattens a view into rows: each assistant message's text and
// thoughts as rows of their own, and runs of tool calls folded together
// across messages, as a turn's activity.
func buildItems(v *pocket.View) []item {
	results := v.Results()
	var items []item
	if _, missing := v.Earliest(); missing {
		items = append(items, item{kind: itemEarlier, key: "earlier"})
	}
	var group *item
	flush := func() {
		if group != nil {
			items = append(items, *group)
			group = nil
		}
	}
	addCall := func(e *pocket.Entry, b pocket.Block) {
		if group == nil {
			group = &item{kind: itemTools, key: fmt.Sprintf("g:%d:%s", e.ID, b.ID), entry: e, at: e.At.Time()}
		}
		group.calls = append(group.calls, call{block: b, result: results[b.ID], slot: v.Slot(b.ID), asked: v.ApprovalFor(b.ID), at: e.At.Time()})
	}
	for _, e := range v.Rows() {
		switch e.Kind {
		case "user":
			flush()
			kind := itemUser
			if isReport(e.Text) {
				kind = itemReport
			}
			items = append(items, item{kind: kind, key: fmt.Sprintf("u:%d", e.ID), entry: e, text: e.Text, at: e.At.Time()})
		case "assistant":
			for i, b := range e.Blocks {
				switch b.Type {
				case "toolCall":
					addCall(e, b)
				case "thinking":
					if strings.TrimSpace(b.Text) == "" && !b.Redacted {
						continue
					}
					flush()
					items = append(items, item{kind: itemThinking, key: fmt.Sprintf("k:%d:%d", e.ID, i), entry: e, text: b.Text})
				case "text":
					if strings.TrimSpace(b.Text) == "" {
						continue
					}
					flush()
					items = append(items, item{kind: itemText, key: fmt.Sprintf("t:%d:%d", e.ID, i), entry: e, text: b.Text, at: e.At.Time()})
				}
			}
			if e.Error != "" || e.StopReason == "error" {
				flush()
				items = append(items, item{kind: itemError, key: fmt.Sprintf("x:%d", e.ID), entry: e, text: firstNonEmpty(e.Error, "The model stopped with an error.")})
			}
			if e.StopReason == "aborted" {
				flush()
				items = append(items, item{kind: itemNote, key: fmt.Sprintf("n:%d", e.ID), entry: e, text: "Stopped"})
			}
		case "note":
			flush()
			items = append(items, item{kind: itemNote, key: fmt.Sprintf("n:%d", e.ID), entry: e, text: e.Text})
		case "compaction":
			flush()
			items = append(items, item{kind: itemCompaction, key: fmt.Sprintf("c:%d", e.ID), entry: e, text: e.Summary})
		case "reset":
			flush()
			items = append(items, item{kind: itemNote, key: fmt.Sprintf("r:%d", e.ID), entry: e, text: firstNonEmpty(e.Text, "Context reset")})
		case "shell", "command":
			flush()
			items = append(items, item{kind: itemShell, key: fmt.Sprintf("sh:%d", e.ID), entry: e})
		case "fromPi":
			flush()
			items = append(items, item{kind: itemNote, key: fmt.Sprintf("p:%d", e.ID), entry: e, text: "Continued from Pi in the terminal: " + e.Title})
		}
	}
	flush()
	if v.Live.Busy || v.Live.Limit != nil {
		items = append(items, item{kind: itemLive, key: "live"})
	}
	return items
}

// isReport tells a message that a machine wrote into the conversation: a
// subagent's answer, a desk or shift note.
func isReport(text string) bool {
	for _, p := range []string{"[subagent ", "[desk]", "[shift]", "[plan-consult", "[linked ", "[from "} {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// conversationPane is the middle pane while a session is open.
func (a *App) conversationPane(c *ui.Context) {
	t := c.Theme()
	tr := &a.tr
	tr.viewChanged(a)
	if tr.open == nil {
		tr.open, tr.closed = map[string]bool{}, map[string]bool{}
	}
	ui.Column(c).Grow(1).AlignItems(ui.Stretch).Background(t.Background).Children(func() {
		a.findBar(c)
		if a.loading == a.current && len(tr.items) == 0 {
			frostui.Loading(c, "Opening the session…").Grow(1)
		} else {
			tr.list.Key = func(i int) any { return tr.items[i].key }
			tr.list.FollowEnd = true
			tr.list.FollowAnimation = &ui.Smooth
			ui.List(c, &tr.list, len(tr.items), func(i int) {
				ui.Column(c).AlignItems(ui.Center).Children(func() {
					ui.Column(c).MaxWidth(860).FillWidth().AlignItems(ui.Stretch).Children(func() {
						if tr.find.open && tr.find.picked == tr.items[i].key {
							ui.Column(c).FillWidth().BorderWidth(2, 2, 2, 2).BorderColor(t.Accent).Padding(4).Children(func() { a.itemView(c, &tr.items[i]) })
						} else {
							a.itemView(c, &tr.items[i])
						}
					})
				})
			}).Grow(1).Padding(frostui.SpaceL, frostui.SpaceXL).Gap(frostui.SpaceM).Justify(ui.End).Children(func() {
				if len(tr.items) == 0 {
					frostui.Empty(c, "pocket-sparkle", "A fresh start", "Tell Pi what to do. Enter sends, Shift+Enter starts a new line.").Margin(ui.Auto)
				}
			})
		}
		a.approvalsDock(c)
		a.composerView(c)
	})
}

// itemView draws one row.
func (a *App) itemView(c *ui.Context, it *item) {
	switch it.kind {
	case itemEarlier:
		a.earlierRow(c)
	case itemUser:
		a.userRow(c, it)
	case itemReport:
		a.reportRow(c, it)
	case itemText:
		markdown.View(c, it.text, &a.md)
	case itemThinking:
		a.thinkingRow(c, it, false)
	case itemTools:
		a.toolsRow(c, it, false)
	case itemError:
		frostui.Problem(c, it.text)
	case itemNote:
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			ui.Divider(c).Grow(1)
			frostui.Caption(c, it.text)
			ui.Divider(c).Grow(1)
		})
	case itemCompaction:
		a.disclosureRow(c, it.key, "edit-clear-all", "Context compacted", "", func() {
			markdown.View(c, it.text, &a.md)
		})
	case itemShell:
		a.shellRow(c, it.entry)
	case itemLive:
		a.liveRow(c)
	}
}

func (a *App) earlierRow(c *ui.Context) {
	tr := &a.tr
	ui.Row(c).Children(func() {
		ui.Spacer(c)
		label := "Show earlier messages"
		if tr.earlier {
			label = "Loading…"
		}
		if ui.Button(c, label).Disabled(tr.earlier).Clicked() {
			a.loadEarlier()
		}
		ui.Spacer(c)
	})
}

// loadEarlier loads the rows before the first one the view holds.
func (a *App) loadEarlier() {
	before, missing := a.view.Earliest()
	if !missing || a.tr.earlier {
		return
	}
	a.tr.earlier = true
	id, view := a.current, a.view
	var got []pocket.Entry
	a.request("Loading earlier messages", func(ctx context.Context, cl *pocket.Client) error {
		var err error
		got, err = cl.Context(ctx, id, before, 60)
		return err
	}, func(err error) {
		if id != a.current || a.view != view {
			return
		}
		a.tr.earlier = false
		if err != nil {
			return
		}
		a.view.AddEarlier(got)
		a.tr.list.HoldPlace()
		a.tr.viewChanged(a)
	})
}

// author returns the name and whether it is the person themself.
func (a *App) author(e *pocket.Entry) (string, bool) {
	me := ""
	if a.hello != nil {
		me = a.hello.User.Name
	}
	if e.From != "" {
		return e.From, e.From == me
	}
	if a.view.Authors != nil && a.hello != nil {
		if uid := a.view.Authors[fmt.Sprint(e.ID)]; uid != "" {
			for _, u := range a.hello.Users {
				if u.ID == uid {
					return u.Name, uid == a.hello.User.ID
				}
			}
		}
	}
	if me == "" {
		me = "You"
	}
	return me, true
}

const attachedMarker = "\n\nAttached files (saved on the server):\n"

func (a *App) userRow(c *ui.Context, it *item) {
	t := c.Theme()
	name, mine := a.author(it.entry)
	text, files, _ := strings.Cut(it.text, attachedMarker)
	ui.Row(c).Gap(frostui.SpaceM).AlignItems(ui.Start).Padding(frostui.SpaceM).Radius(10).
		Background(frostui.Panel(t)).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
		frostui.SquareAvatar(c, name, 28)
		ui.Column(c).Grow(1).Gap(4).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
				label := name
				if mine {
					label = name + " (you)"
				}
				frostui.Strong(c, label)
				if !it.at.IsZero() {
					frostui.Caption(c, clock(it.at))
				}
			})
			markdown.View(c, text, &a.md)
			if files != "" {
				ui.Row(c).Gap(frostui.SpaceXS).Wrap().Children(func() {
					for _, f := range strings.Split(strings.TrimSpace(files), "\n") {
						f = strings.TrimSpace(strings.TrimPrefix(f, "- "))
						if f != "" {
							frostui.IconPill(c.Key(f), "paperclip", baseName(f), frostui.Neutral)
						}
					}
				})
			}
		})
	})
}

// reportRow folds a message a machine wrote (a subagent's answer, a desk
// note) to its first line.
func (a *App) reportRow(c *ui.Context, it *item) {
	first, _, _ := strings.Cut(it.text, "\n")
	icon := "pocket-subagents"
	if strings.HasPrefix(it.text, "[desk]") {
		icon = "pocket-desk"
	} else if strings.HasPrefix(it.text, "[shift]") {
		icon = "pocket-night"
	}
	a.disclosureRow(c, it.key, icon, truncate(first, 140), clock(it.at), func() {
		markdown.View(c, it.text, &a.md)
	})
}

// disclosureRow is a quiet line that opens to show body.
func (a *App) disclosureRow(c *ui.Context, key, icon, title, aside string, body func()) {
	if a.tr.open == nil {
		a.tr.open = map[string]bool{}
	}
	t := c.Theme()
	open := a.tr.open[key]
	ui.Column(c).AlignItems(ui.Stretch).Radius(8).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
		head := ui.ButtonBase(c).Row().Gap(frostui.SpaceS).AlignItems(ui.Center).Padding(6, frostui.SpaceM).
			Cursor(ui.CursorPointer).Label(title).Expanded(open)
		if head.Hovered() {
			head.Background(t.SurfaceHover)
		}
		if head.Clicked() {
			a.tr.open[key] = !open
			a.tr.list.HoldPlace()
		}
		head.Children(func() {
			frostui.Icon(c, chevronFor(open))
			frostui.Icon(c, icon)
			frostui.Muted(c, title).SingleLine().Grow(1)
			if aside != "" {
				frostui.Caption(c, aside)
			}
		})
		if open {
			ui.Column(c).Padding(frostui.SpaceS, frostui.SpaceM, frostui.SpaceM, frostui.SpaceM).AlignItems(ui.Stretch).Children(body)
		}
	})
}

func chevronFor(open bool) string {
	if open {
		return "pan-down"
	}
	return "pan-end"
}

func (a *App) thinkingRow(c *ui.Context, it *item, live bool) {
	if live {
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			frostui.Icon(c, "pocket-thinking")
			shimmer(c, "Thinking…")
		})
		return
	}
	title := "Thought"
	if it.entry != nil && len(it.text) > 0 {
		title = "Thought · " + truncate(firstLine(it.text), 90)
	}
	a.disclosureRow(c, it.key, "pocket-thinking", title, "", func() {
		frostui.Muted(c, it.text).Wrap().Selectable()
	})
}

// shimmer is text that breathes while Pi works.
func shimmer(c *ui.Context, text string) ui.Element {
	el := frostui.Muted(c, text)
	v := el.Loop("shimmer", 1600*time.Millisecond, ui.EaseInOut)
	return el.Opacity(0.45 + 0.55*v)
}

// toolsRow draws a run of tool calls: one line that says what ran, which
// opens to list each call, and each call opens to show what came of it.
func (a *App) toolsRow(c *ui.Context, it *item, live bool) {
	t := c.Theme()
	key := it.key
	running, failed, waiting := 0, 0, 0
	for _, cl := range it.calls {
		switch callState(cl) {
		case frostui.StepRunning, frostui.StepQueued:
			running++
		case frostui.StepFailed:
			failed++
		case frostui.StepHeld:
			waiting++
		}
	}
	if len(it.calls) == 1 {
		ui.Column(c).AlignItems(ui.Stretch).Radius(8).Padding(2, frostui.SpaceXS).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
			a.callRow(c.Key(it.calls[0].block.ID), &it.calls[0])
		})
		return
	}
	// A run at work, or one waiting for the person, shows its calls.
	auto := (running > 0 || waiting > 0) && !a.tr.closed[key]
	open := a.tr.open[key] || auto
	ui.Column(c).AlignItems(ui.Stretch).Radius(8).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
		head := ui.ButtonBase(c).Row().Gap(frostui.SpaceS).AlignItems(ui.Center).Padding(6, frostui.SpaceM).
			Cursor(ui.CursorPointer).Label(toolsTitle(it.calls)).Expanded(open)
		if head.Hovered() {
			head.Background(t.SurfaceHover)
		}
		if head.Clicked() {
			if open {
				a.tr.open[key] = false
				a.tr.closed[key] = true
			} else {
				a.tr.open[key] = true
				a.tr.closed[key] = false
			}
			a.tr.list.HoldPlace()
		}
		head.Children(func() {
			frostui.Icon(c, chevronFor(open))
			frostui.Icon(c, "pocket-tool")
			frostui.Muted(c, toolsTitle(it.calls)).SingleLine().Grow(1)
			switch {
			case waiting > 0:
				frostui.Pill(c, "waiting for you", frostui.Warning)
			case running > 0:
				ui.Spinner(c).Size(14, 14)
			case failed > 0:
				frostui.Pill(c, fmt.Sprintf("%d failed", failed), frostui.Danger)
			}
			if d := it.duration(); d > time.Second && running == 0 {
				frostui.Caption(c, frostui.Duration(d))
			}
		})
		if !open {
			return
		}
		ui.Column(c).Padding(0, frostui.SpaceS, frostui.SpaceS, frostui.SpaceS).AlignItems(ui.Stretch).Children(func() {
			for i := range it.calls {
				a.callRow(c.Key(it.calls[i].block.ID), &it.calls[i])
			}
		})
	})
}

func (it *item) duration() time.Duration {
	if len(it.calls) == 0 {
		return 0
	}
	start := it.calls[0].at
	var end time.Time
	for _, cl := range it.calls {
		if cl.result != nil && cl.result.At.Time().After(end) {
			end = cl.result.At.Time()
		}
	}
	if start.IsZero() || end.IsZero() {
		return 0
	}
	return end.Sub(start)
}

// toolsTitle says what a run of calls did: "bash ×3, read ×2".
func toolsTitle(calls []call) string {
	if len(calls) == 1 {
		return toolLine(calls[0].block)
	}
	counts := map[string]int{}
	var order []string
	for _, cl := range calls {
		n := cl.block.Name
		if counts[n] == 0 {
			order = append(order, n)
		}
		counts[n]++
	}
	parts := make([]string, 0, len(order))
	for _, n := range order {
		if counts[n] > 1 {
			parts = append(parts, fmt.Sprintf("%s ×%d", n, counts[n]))
		} else {
			parts = append(parts, n)
		}
	}
	return fmt.Sprintf("%d tools · %s", len(calls), strings.Join(parts, ", "))
}

// toolLine says one call in a line: its name and what it works on.
func toolLine(b pocket.Block) string {
	if s := toolSubject(b); s != "" {
		return b.Name + " · " + truncate(firstLine(s), 120)
	}
	return b.Name
}

// toolSubject picks the argument that says what a call works on.
func toolSubject(b pocket.Block) string {
	for _, k := range []string{"command", "path", "file_path", "pattern", "query", "url", "name", "agent", "task", "description"} {
		if v, ok := b.Args[k].(string); ok && v != "" {
			return v
		}
	}
	keys := make([]string, 0, len(b.Args))
	for k := range b.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v, ok := b.Args[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func toolIcon(name string) string {
	switch name {
	case "bash", "background", "shell":
		return "pocket-terminal"
	case "read", "ls", "view":
		return "text-x-generic"
	case "edit", "write", "multi_edit":
		return "pocket-diff"
	case "grep", "find", "glob", "search", "tool_search":
		return "edit-find"
	case "subagent", "team_delegate", "team_work":
		return "pocket-subagents"
	case "ask":
		return "pocket-question"
	case "team_message", "desk":
		return "pocket-desk"
	case "browser", "web_fetch", "fetch":
		return "link"
	}
	return "pocket-tool"
}

func callState(cl call) frostui.StepState {
	switch {
	case cl.asked != nil:
		return frostui.StepHeld
	case cl.result != nil && cl.result.IsError:
		return frostui.StepFailed
	case cl.result != nil:
		return frostui.StepDone
	case cl.slot != nil && cl.slot.Status == "running":
		return frostui.StepRunning
	case cl.slot != nil:
		return frostui.StepQueued
	}
	return frostui.StepDone
}

func (a *App) callRow(c *ui.Context, cl *call) {
	t := c.Theme()
	key := "call:" + cl.block.ID
	open := a.tr.open[key]
	ui.Column(c).AlignItems(ui.Stretch).Children(func() {
		head := ui.ButtonBase(c).Row().Gap(frostui.SpaceS).AlignItems(ui.Center).Padding(4, frostui.SpaceS).Radius(6).
			Cursor(ui.CursorPointer).Label(toolLine(cl.block)).Expanded(open)
		if head.Hovered() {
			head.Background(t.SurfaceHover)
		}
		if head.Clicked() {
			a.tr.open[key] = !open
			a.tr.list.HoldPlace()
		}
		head.Children(func() {
			frostui.Icon(c, toolIcon(cl.block.Name))
			frostui.Mono(c, cl.block.Name).SingleLine()
			if s := toolSubject(cl.block); s != "" {
				frostui.Muted(c, truncate(firstLine(s), 140)).SingleLine().Grow(1)
			} else {
				ui.Spacer(c)
			}
			frostui.JobStatus(c, frostui.RowStatus{State: callState(*cl), Compact: true,
				Text: map[frostui.StepState]string{frostui.StepHeld: "waiting"}[callState(*cl)]})
		})
		if !open {
			return
		}
		ui.Column(c).Gap(frostui.SpaceS).Padding(frostui.SpaceXS, frostui.SpaceS, frostui.SpaceS, 30).AlignItems(ui.Stretch).Children(func() {
			if cmd, ok := cl.block.Args["command"].(string); ok && strings.Contains(cmd, "\n") {
				codeBox(c, cmd, 200)
			} else if len(cl.block.Args) > 0 && toolSubject(cl.block) == "" {
				b, _ := json.MarshalIndent(cl.block.Args, "", "  ")
				codeBox(c, string(b), 200)
			}
			switch {
			case cl.result != nil:
				if diff := diffOf(cl.result.Details); diff != "" {
					diffBox(c, diff)
				} else if strings.TrimSpace(cl.result.Text) != "" {
					codeBox(c, cl.result.Text, 320)
				}
			case cl.slot != nil && cl.slot.Output != "":
				codeBox(c, cl.slot.Output, 320)
			}
		})
	})
}

// diffOf reads the diff an edit's result carries in its details.
func diffOf(details json.RawMessage) string {
	if len(details) == 0 {
		return ""
	}
	var d struct {
		Diff string `json:"diff"`
	}
	if json.Unmarshal(details, &d) != nil {
		return ""
	}
	return d.Diff
}

// codeBox shows text in monospace, scrolled within max DIPs.
func codeBox(c *ui.Context, text string, max float32) {
	t := c.Theme()
	text = strings.TrimRight(text, "\n")
	if len(text) > 20000 {
		text = text[:10000] + "\n…\n" + text[len(text)-10000:]
	}
	ui.ScrollBoth(c).MaxHeight(max).Radius(6).Background(frostui.ColorsOf(t).Code).Children(func() {
		frostui.Mono(c, text).Selectable().Padding(frostui.SpaceS, frostui.SpaceM).NoWrap()
	})
}

// diffBox shows a unified diff with its lines colored.
func diffBox(c *ui.Context, diff string) {
	t := c.Theme()
	lines := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	if len(lines) > 400 {
		lines = append(lines[:400], fmt.Sprintf("… %d more lines", len(lines)-400))
	}
	ui.ScrollBoth(c).MaxHeight(360).Radius(6).Background(frostui.ColorsOf(t).Code).Children(func() {
		ui.Column(c).Padding(frostui.SpaceS, 0).AlignItems(ui.Stretch).Children(func() {
			for i, l := range lines {
				row := ui.Row(c.Key(i)).Padding(0, frostui.SpaceM)
				switch {
				case strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
					row.Background(frostui.ToneFill(t, frostui.Success))
				case strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---"):
					row.Background(frostui.ToneFill(t, frostui.Danger))
				case strings.HasPrefix(l, "@@"):
					row.Background(frostui.ToneFill(t, frostui.Accent))
				}
				row.Children(func() { frostui.Mono(c, l).NoWrap() })
			}
		})
	})
}

func (a *App) shellRow(c *ui.Context, e *pocket.Entry) {
	ui.Column(c).Gap(frostui.SpaceXS).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			frostui.Icon(c, "pocket-terminal")
			frostui.Mono(c, "$ "+firstLine(e.Command)).SingleLine().Grow(1)
			if e.By != "" {
				frostui.Caption(c, e.By)
			}
			if e.Status != "" && e.Status != "done" {
				frostui.Pill(c, e.Status, frostui.Warning)
			}
		})
		if strings.TrimSpace(e.Output) != "" {
			codeBox(c, e.Output, 240)
		}
	})
}

// liveRow is the run at work: the message being written, the calls at
// work, a wait for a usage limit, or a breathing "Working…".
func (a *App) liveRow(c *ui.Context) {
	v := a.view
	live := v.Live
	ui.Column(c).Gap(frostui.SpaceM).AlignItems(ui.Stretch).Children(func() {
		if live.Limit != nil {
			frostui.Banner(c, frostui.Warning, "Waiting for a usage limit",
				fmt.Sprintf("%s: %s. Pi tries again at %s.", live.Limit.Provider, live.Limit.Reason, clock(live.Limit.Until.Time())), "", false)
		}
		var blocks []pocket.Block
		if live.Generation != nil && live.Generation.Message != nil {
			blocks = live.Generation.Message.Blocks
		}
		if live.Generation != nil && live.Generation.Retry != nil {
			frostui.Toned(c, "Retrying: "+live.Generation.Retry.Error, frostui.Warning).Wrap()
		}
		var calls []call
		for i, b := range blocks {
			switch b.Type {
			case "thinking":
				if i == len(blocks)-1 {
					a.thinkingRow(c, &item{}, true)
				}
			case "text":
				markdown.Stream(c.Key(fmt.Sprintf("live-text-%d", i)), b.Text, &a.md)
			case "toolCall":
				calls = append(calls, call{block: b, slot: v.Slot(b.ID), asked: v.ApprovalFor(b.ID)})
			}
		}
		// Calls at work that the message no longer holds (it was stored).
		for i := range live.Tools {
			slot := &live.Tools[i]
			if slot.Status == "done" {
				continue
			}
			found := false
			for _, cl := range calls {
				if cl.block.ID == slot.CallID {
					found = true
				}
			}
			if !found && !a.callShown(slot.CallID) {
				calls = append(calls, call{block: pocket.Block{ID: slot.CallID, Name: slot.Name}, slot: slot, asked: v.ApprovalFor(slot.CallID)})
			}
		}
		if len(calls) > 0 {
			a.toolsRow(c, &item{key: "live-tools", calls: calls}, true)
		}
		switch {
		case len(v.Approvals) > 0:
			ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
				frostui.Icon(c, "pocket-waiting")
				frostui.Toned(c, "Waiting for you, below", frostui.Warning)
			})
		case live.Limit == nil && !a.streamingText(blocks):
			ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(14, 14)
				shimmer(c, a.workingLabel())
			})
		}
	})
}

// streamingText reports whether the message being written ends in text,
// which shows that Pi is at work without a label.
func (a *App) streamingText(blocks []pocket.Block) bool {
	return len(blocks) > 0 && blocks[len(blocks)-1].Type == "text"
}

// callShown reports whether a stored row already shows a call.
func (a *App) callShown(id string) bool {
	for _, it := range a.tr.items {
		for _, cl := range it.calls {
			if cl.block.ID == id {
				return true
			}
		}
	}
	return false
}

func (a *App) workingLabel() string {
	n := 0
	for _, s := range a.view.Subagents {
		if s.Busy {
			n++
		}
	}
	switch {
	case n == 1:
		return "Working, with 1 subagent…"
	case n > 1:
		return fmt.Sprintf("Working, with %d subagents…", n)
	}
	return "Working…"
}

func clock(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.Local()
	now := time.Now()
	if t.YearDay() == now.YearDay() && t.Year() == now.Year() {
		return t.Format("15:04")
	}
	if now.Sub(t) < 6*24*time.Hour {
		return t.Format("Mon 15:04")
	}
	return t.Format("Jan 2")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
