package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// composer is the message field's state.
type composer struct {
	files              map[int][]*composerFile
	picking, queueBusy bool
	session            int
	draft              string
	drafts             map[int]string
	history            []string
	// owner is the account the drafts and history belong to; empty when
	// nobody is signed in.
	owner accountKey
	// kept is what each account left behind when the window signed out of
	// it, until it signs in again. Drafts never go to the disk, so another
	// server or user never sees them.
	kept           map[accountKey]composerKept
	sending        bool
	pendingText    string
	pendingSession int
	field          ui.Handle
	// sel is the suggestion chosen with the arrows.
	sel int
	// prompts are the session's templates and skills, for /.
	prompts    []pocket.Prompt
	promptsFor int
	// answers are what the person filled in on question cards, by
	// approval and field.
	answers  map[string]map[string]string
	answered map[string]bool
}

// composerKept is what the composer held for 1 account when the window left
// it.
type composerKept struct {
	drafts  map[int]string
	history []string
}

// adopt takes the drafts and history of account key, after moving the ones
// held now to the account they belong to. The same account, signed in
// again, gets its own back; another server or user gets nothing.
func (cp *composer) adopt(key accountKey) {
	if cp.owner == key {
		return
	}
	session := cp.session
	cp.stash()
	cp.session = session // the session shown stays; its text is the new account's
	kept := cp.kept[key]
	if cp.kept != nil {
		delete(cp.kept, key)
	}
	cp.owner = key
	cp.drafts, cp.history = kept.drafts, kept.history
	if cp.drafts == nil {
		cp.drafts = map[int]string{}
	}
	cp.draft = cp.drafts[cp.session]
}

// stash moves the drafts and history held now to kept, under the account
// they belong to, and empties the composer: the field's text is the draft of
// the session shown. What was held before any account signed in is dropped,
// because it belongs to no server.
func (cp *composer) stash() {
	if cp.drafts == nil {
		cp.drafts = map[int]string{}
	}
	cp.drafts[cp.session] = cp.draft
	if cp.owner.set() && cp.holdsDrafts() {
		if cp.kept == nil {
			cp.kept = map[accountKey]composerKept{}
		}
		cp.kept[cp.owner] = composerKept{drafts: cp.drafts, history: cp.history}
	}
	cp.owner = accountKey{}
	cp.session, cp.draft, cp.sel = 0, "", 0
	cp.drafts, cp.history = map[int]string{}, nil
}

// holdsDrafts reports whether the composer holds a draft or a history entry
// worth keeping.
func (cp *composer) holdsDrafts() bool {
	if len(cp.history) > 0 {
		return true
	}
	for _, d := range cp.drafts {
		if d != "" {
			return true
		}
	}
	return false
}

// reset keeps the draft of the session left and takes the one of id.
func (cp *composer) reset(id int) {
	if cp.drafts == nil {
		cp.drafts = map[int]string{}
	}
	cp.drafts[cp.session] = cp.draft
	cp.session = id
	cp.draft = cp.drafts[id]
	cp.sel = 0
}

// resetRequests releases state whose completion will be discarded on sign-out.
// Keep an unacknowledged send as a draft in its original session.
func (cp *composer) resetRequests() {
	if cp.pendingText != "" {
		if cp.drafts == nil {
			cp.drafts = map[int]string{}
		}
		draft := cp.drafts[cp.pendingSession]
		if cp.session == cp.pendingSession {
			draft = cp.draft
		}
		if strings.TrimSpace(draft) == "" {
			draft = cp.pendingText
		} else {
			draft = cp.pendingText + "\n\n" + draft
		}
		cp.drafts[cp.pendingSession] = draft
		if cp.session == cp.pendingSession {
			cp.draft = draft
		}
	}
	cp.sending, cp.pendingText, cp.pendingSession = false, "", 0
	// Uploaded paths belong to the old authenticated server, never a new sign-in.
	for _, files := range cp.files {
		for _, f := range files {
			if f.cancel != nil {
				f.cancel()
			}
		}
	}
	cp.files = nil
	cp.picking, cp.queueBusy = false, false
	cp.answered, cp.answers = nil, nil
	cp.prompts, cp.promptsFor = nil, 0
}

// suggestion is one completion of what the field starts with.
type suggestion struct {
	insert, title, detail, icon string
}

// builtins are the commands Pocket runs itself.
var builtins = []suggestion{
	{insert: "/new", title: "/new", detail: "Choose a folder for a new session", icon: "pocket-new"},
	{insert: "/stop", title: "/stop", detail: "Stop the run (Esc)", icon: "process-stop"},
	{insert: "/compact", title: "/compact", detail: "Compact the context", icon: "edit-clear-all"},
	{insert: "/plan", title: "/plan", detail: "Turn plan mode on or off", icon: "view-list"},
	{insert: "/rename ", title: "/rename", detail: "Rename the session", icon: "square-pen"},
	{insert: "/archive", title: "/archive", detail: "Archive the session", icon: "archive"},
}

// suggestions completes a / command or an @agent at the start of the draft.
func (a *App) suggestions() []suggestion {
	d := a.composer.draft
	if strings.ContainsAny(d, " \n") || len(d) == 0 {
		return nil
	}
	q := strings.ToLower(d[1:])
	var out []suggestion
	switch d[0] {
	case '/':
		all := slices.Clone(builtins)
		if a.hello != nil {
			for _, x := range a.hello.Server.ExtensionCommands {
				all = append(all, suggestion{insert: "/" + x.Name + " ", title: "/" + x.Name, detail: x.Description, icon: "pocket-sparkle"})
			}
		}
		for _, p := range a.composer.prompts {
			icon := "text-x-generic"
			if p.Skill {
				icon = "pocket-sparkle"
			}
			all = append(all, suggestion{insert: "/" + p.Name + " ", title: "/" + p.Name, detail: p.Description, icon: icon})
		}
		for _, s := range all {
			if _, ok := frostui.Match(strings.TrimPrefix(s.title, "/"), q); ok || q == "" {
				out = append(out, s)
			}
		}
	case '@':
		for _, p := range a.team.agents {
			if _, ok := frostui.Match(p.ID+" "+p.Name, q); ok || q == "" {
				detail := "Start a task for " + p.Name
				if p.Role != "agent" {
					detail += " (" + p.Role + ")"
				}
				out = append(out, suggestion{insert: "@" + p.ID + " ", title: "@" + p.ID, detail: detail, icon: "pocket-agent"})
			}
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

// loadPrompts reads the session's templates and skills once.
func (a *App) loadPrompts() {
	id := a.current
	if id == 0 || a.composer.promptsFor == id {
		return
	}
	a.composer.promptsFor = id
	var got []pocket.Prompt
	a.request("Reading the session's commands", func(ctx context.Context, c *pocket.Client) error {
		var err error
		got, err = c.Prompts(ctx, id)
		return err
	}, func(err error) {
		if err == nil && a.composer.promptsFor == id {
			a.composer.prompts = got
		}
	})
}

// composerView is the field at the foot of the conversation.
func (a *App) composerView(c *ui.Context) {
	t := c.Theme()
	cp := &a.composer
	busy := a.view.Live.Busy
	sugg := a.suggestions()
	if cp.sel >= len(sugg) {
		cp.sel = 0
	}
	ui.Column(c).AlignItems(ui.Center).Padding(0, frostui.SpaceXL, frostui.SpaceL, frostui.SpaceXL).Children(func() {
		ui.Column(c).MaxWidth(860).FillWidth().AlignItems(ui.Stretch).Gap(frostui.SpaceXS).Children(func() {
			if len(sugg) > 0 {
				a.suggestionList(c, sugg)
			}
			ui.Column(c).AlignItems(ui.Stretch).Radius(12).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).
				Background(frostui.Panel(t)).Padding(frostui.SpaceS, frostui.SpaceM).Gap(frostui.SpaceXS).Children(func() {
				a.attachmentChips(c)
				placeholder := "Message Pi…   / for commands, @ for an agent"
				if busy {
					placeholder = "Pi is working. Enter queues a message, " + keys("Ctrl") + "+Enter steers it now."
				}
				svc, id := c.Services(), a.current
				input := ui.TextAreaBase(c.Key(fmt.Sprint("composer", a.current)), &cp.draft).Lines(1, 12).Placeholder(placeholder).
					Label("Message").Bind(&cp.field).
					OnTextKey(func(k ui.TextKey) bool { return a.composerKey(k) }).
					OnPaste(func(string) bool {
						return a.pasteAttachments(id, svc.ReadClipboardData(transfer.FileList, transfer.PNG))
					})
				if paths := input.DroppedFiles(); len(paths) > 0 {
					a.attachFiles(id, paths)
				}
				if !a.tr.find.open {
					input.AutoFocus()
				}
				ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
					if a.opts.PickFiles != nil && a.canAttach(a.current) && ui.Button(c, "Attach files").Disabled(cp.picking).Clicked() {
						a.pickAttachments()
					}
					a.modelChip(c)
					if a.view.Plan.On {
						frostui.IconPill(c, "view-list", "Plan mode", frostui.Accent)
					}
					ui.Spacer(c)
					if busy {
						frostui.Caption(c, "Esc stops")
						if frostui.IconButton(c, "pocket-stop", "Stop the run (Esc)").Clicked() {
							a.stop()
						}
					}
					empty := strings.TrimSpace(cp.draft) == "" && len(cp.filesFor(a.current)) == 0
					if frostui.IconButton(c, "pocket-send", "Send (Enter)").Disabled(empty || cp.sending || !cp.filesReady() || !a.canSteer()).Clicked() {
						a.send(cp.draft, "")
					}
				})
			})
		})
	})
	a.loadPrompts()
}

func (a *App) suggestionList(c *ui.Context, sugg []suggestion) {
	t := c.Theme()
	ui.Column(c).AlignItems(ui.Stretch).Radius(10).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).
		Background(frostui.Panel(t)).Padding(frostui.SpaceXS).Children(func() {
		for i, s := range sugg {
			row := ui.ButtonBase(c.Key(s.title)).Row().Gap(frostui.SpaceS).AlignItems(ui.Center).
				Padding(5, frostui.SpaceS).Radius(6).Cursor(ui.CursorPointer).Label(s.title)
			if i == a.composer.sel {
				row.Background(t.Accent.Alpha(0.16))
			} else if row.Hovered() {
				row.Background(t.SurfaceHover)
			}
			if row.Clicked() {
				a.complete(s)
			}
			row.Children(func() {
				frostui.Icon(c, s.icon)
				frostui.Strong(c, s.title)
				frostui.Muted(c, s.detail).SingleLine().Grow(1)
				if i == a.composer.sel {
					frostui.Kbd(c, "Tab")
				}
			})
		}
	})
}

// complete puts a suggestion in the field.
func (a *App) complete(s suggestion) {
	a.composer.draft = s.insert
	a.composer.sel = 0
}

// composerKey takes the keys the field does not type: Enter sends, Tab
// and the arrows complete, Escape stops, Up brings back the last message.
func (a *App) composerKey(k ui.TextKey) bool {
	cp := &a.composer
	if k.Key == ui.KeyEnter && (k.Mods == 0 || k.Mods == ui.Cmd) && (cp.sending || !cp.filesReady() || !a.canSteer()) {
		return true
	}
	sugg := a.suggestions()
	switch {
	case len(sugg) > 0 && (k.Key == ui.KeyTab || (k.Key == ui.KeyEnter && k.Mods == 0)):
		s := sugg[min(cp.sel, len(sugg)-1)]
		// A command that takes nothing more runs at once with Enter.
		if k.Key == ui.KeyEnter && !strings.HasSuffix(s.insert, " ") {
			k.SetText("")
			cp.draft = ""
			a.postComposerSend(s.insert, "")
			return true
		}
		k.SetText(s.insert)
		cp.draft = s.insert
		cp.sel = 0
		return true
	case len(sugg) > 0 && k.Key == ui.KeyDown && k.Mods == 0:
		cp.sel = (cp.sel + 1) % len(sugg)
		return true
	case len(sugg) > 0 && k.Key == ui.KeyUp && k.Mods == 0:
		cp.sel = (cp.sel + len(sugg) - 1) % len(sugg)
		return true
	case k.Key == ui.KeyEnter && k.Mods == 0:
		text := k.Text()
		if strings.TrimSpace(text) == "" && len(cp.filesFor(a.current)) == 0 {
			return true
		}
		k.SetText("")
		cp.draft = ""
		a.postComposerSend(text, "")
		return true
	case k.Key == ui.KeyEnter && k.Mods == ui.Cmd:
		text := k.Text()
		if strings.TrimSpace(text) == "" && len(cp.filesFor(a.current)) == 0 {
			return true
		}
		k.SetText("")
		cp.draft = ""
		a.postComposerSend(text, "steer")
		return true
	case k.Key == ui.KeyEscape && k.Mods == 0 && len(sugg) > 0:
		k.SetText("")
		cp.draft = ""
		return true
	case k.Key == ui.KeyEscape && k.Mods == 0 && a.view.Live.Busy:
		a.post(a.stop)
		return true
	case k.Key == ui.KeyUp && k.Mods == 0 && k.Text() == "" && len(cp.history) > 0:
		last := cp.history[len(cp.history)-1]
		k.SetText(last)
		cp.draft = last
		return true
	}
	return false
}

func (a *App) postComposerSend(text, mode string) {
	id, client := a.current, a.link.client
	a.post(func() {
		if a.link.client != client {
			return
		}
		if a.current != id {
			a.composer.restoreDraft(id, text, nil)
			return
		}
		a.send(text, mode)
	})
}

func newRequestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "pocket-" + hex.EncodeToString(b)
}

// send sends what the person wrote: a command Pocket runs itself, a task
// for a named agent, or a message for Pi.
func (a *App) send(text, mode string) {
	text = strings.TrimSpace(text)
	cp := &a.composer
	id := a.current
	files := cp.filesFor(id)
	if id == 0 || cp.sending || !cp.filesReady() || !a.canSteer() || (text == "" && len(files) == 0) {
		return
	}
	// Local commands and agent tasks have different routes and cannot consume uploads.
	if len(files) > 0 && (strings.HasPrefix(text, "/") || strings.HasPrefix(text, "@")) {
		if strings.TrimSpace(cp.draft) != text {
			cp.restoreDraft(id, text, nil)
		}
		a.toast("Send attachments with a message to this session, without a slash command or @agent prefix")
		return
	}
	if strings.TrimSpace(cp.draft) == text {
		cp.draft = ""
	}
	cp.history = append(cp.history, text)
	if cmd, args, _ := strings.Cut(text, " "); strings.HasPrefix(text, "/") {
		if a.runBuiltin(cmd, strings.TrimSpace(args)) {
			return
		}
	}
	if strings.HasPrefix(text, "@") && a.team.on {
		name, task, _ := strings.Cut(text[1:], " ")
		if p := a.team.agent(name); p != nil && strings.TrimSpace(task) != "" {
			a.startAgentTask(id, p, strings.TrimSpace(task), "")
			return
		}
	}
	cp.sending = true
	cp.pendingText, cp.pendingSession = text, id
	cp.files[id] = nil
	var attachments []pocket.Attachment
	for _, f := range files {
		attachments = append(attachments, f.file)
	}
	reqID := newRequestID()
	a.console.Add(frostui.LevelCommand, "send", truncate(firstLine(text), 120))
	a.request("Sending", func(ctx context.Context, c *pocket.Client) error {
		if len(attachments) > 0 {
			_, err := c.SubmitAttachments(ctx, id, text, reqID, mode, attachments)
			return err
		}
		_, err := c.Submit(ctx, id, text, reqID, mode)
		return err
	}, func(err error) {
		cp.sending = false
		cp.pendingText, cp.pendingSession = "", 0
		if err != nil {
			cp.restoreDraft(id, text, files)
			a.toast("Not sent: " + err.Error())
		}
	})
}

// runBuiltin runs a command Pocket handles itself; false for others.
func (a *App) runBuiltin(cmd, args string) bool {
	id := a.current
	switch cmd {
	case "/new":
		cwd := ""
		if s := a.byID[id]; s != nil {
			cwd = s.Cwd
		}
		a.newSession(cwd)
	case "/stop":
		a.stop()
	case "/compact":
		a.request("Compacting", func(ctx context.Context, c *pocket.Client) error { return c.Compact(ctx, id) }, nil)
	case "/plan":
		on := !a.view.Plan.On
		a.request("Plan mode", func(ctx context.Context, c *pocket.Client) error { return c.Plan(ctx, id, on) }, nil)
	case "/rename":
		if args != "" {
			a.rename(id, args)
		}
	case "/archive":
		a.archive(id, true)
	default:
		return false
	}
	return true
}

// stop stops the run of the session shown.
func (a *App) stop() {
	id := a.current
	if id == 0 || !a.view.Live.Busy {
		return
	}
	a.console.Add(frostui.LevelCommand, "send", "Stop")
	a.request("Stopping", func(ctx context.Context, c *pocket.Client) error { return c.Abort(ctx, id) }, nil)
}

// startAgentTask starts a linked session as agent p from session source,
// and opens it.
func (a *App) startAgentTask(source int, p *pocket.Persona, task, repository string) {
	key := newRequestID()
	agent := p.ID
	var started int
	a.toast(fmt.Sprintf("Starting %s…", p.Name))
	a.request("Starting "+p.Name, func(ctx context.Context, c *pocket.Client) error {
		var err error
		started, err = c.TeamStart(ctx, source, agent, task, key, repository, true)
		return err
	}, func(err error) {
		if err == nil && started != 0 {
			a.loading = started
			a.open(started)
		}
	})
}

// approvalsDock shows what waits for the person in the session shown:
// tool calls to allow and questions to answer, above the field, so they
// never scroll away.
func (a *App) approvalsDock(c *ui.Context) {
	approvals := a.view.Approvals
	s := a.byID[a.current]
	sub := s != nil && s.Approval != nil && s.Approval.ConversationID != a.current
	if len(approvals) == 0 && !sub {
		return
	}
	ui.Column(c).AlignItems(ui.Center).Padding(frostui.SpaceS, frostui.SpaceXL, frostui.SpaceS, frostui.SpaceXL).Children(func() {
		ui.Column(c).MaxWidth(860).FillWidth().AlignItems(ui.Stretch).Gap(frostui.SpaceS).Children(func() {
			for i := range approvals {
				a.approvalCard(c.Key(approvals[i].ID), &approvals[i])
			}
			if sub {
				a.subagentWaits(c, s.Approval)
			}
		})
	})
}

func (a *App) subagentWaits(c *ui.Context, ap *pocket.SessionApproval) {
	t := c.Theme()
	ui.Row(c).Gap(frostui.SpaceM).AlignItems(ui.Center).Padding(frostui.SpaceM).Radius(10).
		Background(frostui.ToneFill(t, frostui.Warning)).BorderWidth(1, 1, 1, 1).BorderColor(frostui.ToneLine(t, frostui.Warning)).Children(func() {
		frostui.Icon(c, "pocket-subagents")
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			frostui.Strong(c, "A subagent waits for you")
			frostui.Muted(c, truncate(ap.Tool+": "+ap.Subject, 160)).SingleLine()
		})
		if ui.PrimaryButton(c, "Open").Clicked() {
			a.open(ap.ConversationID)
		}
	})
}

// approvalCard is one call to allow, or one question to answer.
func (a *App) approvalCard(c *ui.Context, ap *pocket.Approval) {
	t := c.Theme()
	cp := &a.composer
	if cp.answered == nil {
		cp.answered = map[string]bool{}
	}
	busy := cp.answered[ap.ID]
	tone := frostui.Warning
	ui.Column(c).AlignItems(ui.Stretch).Gap(frostui.SpaceS).Padding(frostui.SpaceM).Radius(10).
		Background(frostui.ToneFill(t, tone)).BorderWidth(1, 1, 1, 1).BorderColor(frostui.ToneLine(t, tone)).Children(func() {
		if ap.Form != nil {
			a.askForm(c, ap, busy)
			return
		}
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			frostui.Icon(c, "pocket-approval")
			frostui.Strong(c, "Allow "+ap.Tool+"?")
			if who := a.userName(ap.RequestedBy); who != "" {
				frostui.Caption(c, "for "+who)
			}
			ui.Spacer(c)
			frostui.Caption(c, clock(ap.CreatedAt.Time()))
		})
		if ap.Subject != "" {
			codeBox(c, ap.Subject, 160)
		}
		if ap.Reason != "" {
			frostui.Muted(c, ap.Reason).Wrap()
		}
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			ui.Spacer(c)
			if busy {
				ui.Spinner(c).Size(14, 14)
			}
			if ui.Button(c, "Deny").Disabled(busy).Tooltip("Deny (" + keys("Alt N") + ")").Clicked() {
				a.answer(ap, false, nil)
			}
			if ui.PrimaryButton(c, "Allow").Disabled(busy).Tooltip("Allow (" + keys("Alt Y") + ")").Clicked() {
				a.answer(ap, true, nil)
			}
		})
	})
}

// askForm draws a question Pi or a module asks.
func (a *App) askForm(c *ui.Context, ap *pocket.Approval, busy bool) {
	cp := &a.composer
	if cp.answers == nil {
		cp.answers = map[string]map[string]string{}
	}
	vals := cp.answers[ap.ID]
	if vals == nil {
		vals = map[string]string{}
		for _, f := range ap.Form.Fields {
			vals[f.Name] = f.Value
		}
		cp.answers[ap.ID] = vals
	}
	f := ap.Form
	ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
		frostui.Icon(c, "pocket-question")
		frostui.Strong(c, firstNonEmpty(f.Title, "Pi asks"))
	})
	if f.Text != "" {
		ui.Text(c, f.Text).Wrap().Selectable()
	}
	for i := range f.Fields {
		field := &f.Fields[i]
		ui.Column(c.Key(field.Name)).Gap(frostui.SpaceXS).AlignItems(ui.Stretch).Children(func() {
			if field.Header != "" {
				frostui.Caption(c, strings.ToUpper(field.Header))
			}
			frostui.Body(c, field.Label).Wrap()
			if len(field.Choices) > 0 {
				ui.Column(c).Gap(frostui.SpaceXS).AlignItems(ui.Stretch).Children(func() {
					for _, ch := range field.Choices {
						chosen := choiceChosen(vals[field.Name], ch.Label, field.Multiple)
						a.choiceButton(c.Key(ch.Label), ch, chosen, busy, func() {
							vals[field.Name] = toggleChoice(vals[field.Name], ch.Label, field.Multiple)
						})
					}
				})
				if field.Other {
					other := vals[field.Name]
					if slices.ContainsFunc(field.Choices, func(ch pocket.AskChoice) bool { return choiceChosen(other, ch.Label, field.Multiple) }) {
						other = ""
					}
					if ui.TextInput(c.Key("other"), &other).Placeholder(firstNonEmpty(field.Placeholder, "Or type an answer")).Label("Other answer").Disabled(busy).Changed() {
						vals[field.Name] = other
					}
				}
				return
			}
			v := vals[field.Name]
			var in ui.Element
			if field.Multiline {
				in = ui.TextArea(c, &v).Lines(2, 6)
			} else {
				in = ui.TextInput(c, &v)
			}
			in.Placeholder(field.Placeholder).Label(field.Label).Disabled(busy)
			if field.Secret {
				in.Password()
			}
			vals[field.Name] = v
		})
	}
	ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
		ui.Spacer(c)
		if busy {
			ui.Spinner(c).Size(14, 14)
		}
		if ui.Button(c, "Decline").Disabled(busy).Clicked() {
			a.answer(ap, false, nil)
		}
		complete := true
		for _, fl := range f.Fields {
			if strings.TrimSpace(vals[fl.Name]) == "" {
				complete = false
			}
		}
		if ui.PrimaryButton(c, firstNonEmpty(f.Submit, "Answer")).Disabled(busy || !complete).Clicked() {
			a.answer(ap, true, vals)
		}
	})
}

func (a *App) choiceButton(c *ui.Context, ch pocket.AskChoice, chosen, busy bool, toggle func()) {
	t := c.Theme()
	b := ui.ButtonBase(c).Row().Gap(frostui.SpaceS).AlignItems(ui.Center).Padding(frostui.SpaceS, frostui.SpaceM).
		Radius(8).BorderWidth(1, 1, 1, 1).Cursor(ui.CursorPointer).Label(ch.Label).Disabled(busy).AccessSelected(chosen)
	switch {
	case chosen:
		b.Background(t.Accent.Alpha(0.18)).BorderColor(t.Accent)
	case b.Hovered():
		b.Background(t.SurfaceHover).BorderColor(t.Border)
	default:
		b.Background(frostui.Panel(t)).BorderColor(t.Border)
	}
	if b.Clicked() {
		toggle()
	}
	b.Children(func() {
		if chosen {
			frostui.Icon(c, "object-select")
		}
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			frostui.Strong(c, ch.Label)
			if ch.Description != "" {
				frostui.Muted(c, ch.Description).Wrap()
			}
		})
	})
}

func choiceChosen(value, label string, multiple bool) bool {
	if !multiple {
		return value == label
	}
	return slices.Contains(strings.Split(value, "\n"), label)
}

func toggleChoice(value, label string, multiple bool) string {
	if !multiple {
		return label
	}
	var parts []string
	if value != "" {
		parts = strings.Split(value, "\n")
	}
	if i := slices.Index(parts, label); i >= 0 {
		parts = slices.Delete(parts, i, i+1)
	} else {
		parts = append(parts, label)
	}
	return strings.Join(parts, "\n")
}

// answer allows or denies an approval, or answers a question.
func (a *App) answer(ap *pocket.Approval, allow bool, values map[string]string) {
	cp := &a.composer
	if cp.answered == nil {
		cp.answered = map[string]bool{}
	}
	cp.answered[ap.ID] = true
	id := ap.ID
	verb := map[bool]string{true: "Allowed", false: "Denied"}[allow]
	if ap.Form != nil {
		verb = map[bool]string{true: "Answered", false: "Declined"}[allow]
	}
	a.console.Add(frostui.LevelCommand, "approve", verb+" "+ap.Tool+": "+truncate(ap.Subject, 100))
	a.request(verb, func(ctx context.Context, c *pocket.Client) error {
		return c.Answer(ctx, id, allow, values)
	}, func(err error) {
		if err != nil {
			delete(cp.answered, id)
			a.toast(verb + " failed: " + err.Error())
		}
	})
}

// answerFirst allows or denies the first call waiting in the session.
func (a *App) answerFirst(allow bool) {
	for i := range a.view.Approvals {
		ap := &a.view.Approvals[i]
		if ap.Form == nil && !a.composer.answered[ap.ID] {
			a.answer(ap, allow, nil)
			return
		}
	}
}

func (a *App) userName(id string) string {
	if id == "" || a.hello == nil {
		return ""
	}
	for _, u := range a.hello.Users {
		if u.ID == id {
			return u.Name
		}
	}
	return ""
}
