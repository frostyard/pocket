package app

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// The Inbox is one queue of everything that waits on the person, from
// every source: a decision the Desk asks for, a tool call to allow, a
// question Pi asks, a PR ready to merge, a run that failed, a thread gone
// quiet, a full desk. Each is answered where it shows.

type inboxKind int

const (
	inboxApproval inboxKind = iota // a call or question waiting in a session
	inboxAsk                       // a decision a Desk thread asks for
	inboxWatch                     // a dated check waiting on the owner
	inboxReady                     // a PR ready to merge
	inboxFailed                    // a session that failed
	inboxStale                     // a thread with no news for days
	inboxFound                     // work the Desk found with no thread
	inboxFull                      // the desk is at its cap
	inboxLesson                    // a lesson an agent wants to keep
)

type inboxItem struct {
	key      string
	agent    string
	lesson   *pocket.Lesson
	kind     inboxKind
	session  *pocket.Session
	thread   *pocket.DeskThread
	firstAsk bool
	groupAt  time.Time
	ask      *pocket.DeskAsk
	pr       *pocket.ReadyPR
	at       time.Time
}

// inboxItems gathers what waits, the most pressing first: answers before
// reviews before what went wrong, and older before newer in each.
func (a *App) inboxItems() []inboxItem {
	var items []inboxItem
	if v := a.desk.view; v != nil && v.Cap.Full {
		items = append(items, inboxItem{key: "full", kind: inboxFull})
	}
	for i := range a.sessions {
		s := &a.sessions[i]
		if s.Archived {
			continue
		}
		switch a.stateOf(s) {
		case stateWaiting:
			at := s.UpdatedAt.Time()
			if s.Approval != nil && s.Approval.At != 0 {
				at = s.Approval.At.Time()
			}
			items = append(items, inboxItem{key: "s:" + strconv.Itoa(s.ID), kind: inboxApproval, session: s, at: at})
		case stateFailed:
			items = append(items, inboxItem{key: "f:" + strconv.Itoa(s.ID), kind: inboxFailed, session: s, at: s.EndedAt.Time()})
		}
	}
	if v := a.desk.view; v != nil {
		for i := range v.Threads {
			t := &v.Threads[i]
			var oldest time.Time
			for j := range t.Asks {
				at := t.Asks[j].At.Time()
				if j == 0 || at.Before(oldest) {
					oldest = at
				}
			}
			for j := range t.Asks {
				items = append(items, inboxItem{key: "a:" + t.Asks[j].ID, kind: inboxAsk, thread: t, ask: &t.Asks[j], firstAsk: j == 0, groupAt: oldest, at: t.Asks[j].At.Time()})
			}
			if dueWatch(t) {
				items = append(items, inboxItem{key: "w:" + t.ID, kind: inboxWatch, thread: t, at: t.LastEventAt.Time()})
			}
			if len(t.Asks) > 0 || t.State == "closed" || t.State == "parked" {
				continue
			}
			switch t.Group() {
			case "stale":
				items = append(items, inboxItem{key: "t:" + t.ID, kind: inboxStale, thread: t, at: t.LastEventAt.Time()})
			case "found":
				items = append(items, inboxItem{key: "t:" + t.ID, kind: inboxFound, thread: t, at: t.LastEventAt.Time()})
			}
		}
		for i := range v.Ready {
			p := &v.Ready[i]
			items = append(items, inboxItem{key: "p:" + p.Ref, kind: inboxReady, pr: p, thread: a.desk.thread(p.Thread), at: p.At.Time()})
		}
	}
	for _, al := range a.waitingLessons() {
		items = append(items, inboxItem{key: "l:" + al.lesson.ID, kind: inboxLesson, agent: al.agent, lesson: al.lesson, at: al.lesson.CreatedAt.Time()})
	}
	rank := map[inboxKind]int{inboxFull: 0, inboxApproval: 1, inboxAsk: 2, inboxWatch: 3, inboxReady: 4, inboxFailed: 5, inboxFound: 6, inboxStale: 7, inboxLesson: 8}
	slices.SortStableFunc(items, func(x, y inboxItem) int {
		if rank[x.kind] != rank[y.kind] {
			return rank[x.kind] - rank[y.kind]
		}
		if x.kind == inboxAsk {
			// The stable sort preserves each thread's server filing order,
			// including ties between the oldest ask of different threads.
			return x.groupAt.Compare(y.groupAt)
		}
		if x.kind == inboxReady {
			if d := reviewOrder(x.pr.Level) - reviewOrder(y.pr.Level); d != 0 {
				return d
			}
			if d := x.at.Compare(y.at); d != 0 {
				return d
			}
			return strings.Compare(x.pr.Ref, y.pr.Ref)
		}
		return x.at.Compare(y.at)
	})
	return items
}

// inboxCount is what the Inbox's badge counts: what needs an answer.
func (a *App) inboxCount() int {
	n := 0
	for _, it := range a.inboxItems() {
		switch it.kind {
		case inboxApproval, inboxAsk, inboxWatch, inboxReady, inboxFull:
			n++
		}
	}
	return n
}

// inboxPane is the Inbox's page.
func (a *App) inboxPane(c *ui.Context) {
	t := c.Theme()
	items := a.inboxItems()
	// What was chosen went (answered, merged): choose the next one.
	if len(items) > 0 && !slices.ContainsFunc(items, func(it inboxItem) bool { return it.key == a.inboxSel }) {
		a.inboxSel = items[0].key
	}
	ui.Scroll(c).Grow(1).Background(t.Background).Children(func() {
		ui.Column(c).AlignItems(ui.Center).Padding(frostui.SpaceXL).Children(func() {
			ui.Column(c).MaxWidth(880).FillWidth().Gap(frostui.SpaceM).AlignItems(ui.Stretch).Children(func() {
				name := ""
				if a.hello != nil {
					name = ", " + a.hello.User.Name
				}
				frostui.PageHeader(c, greeting(a.opts.Now().Hour())+name, a.inboxLine(items), nil)
				if len(items) == 0 {
					ui.Column(c).Padding(frostui.SpaceXL*2, 0).Children(func() {
						frostui.Empty(c, "object-select", "Inbox zero", "Nothing waits for you. The team works on; In motion shows what they do.")
					})
					return
				}
				last := inboxKind(-1)
				for i := range items {
					it := &items[i]
					if it.kind != last {
						last = it.kind
						frostui.SectionHeader(c.Key(fmt.Sprint("h", it.kind)), frostui.SectionHead{Title: inboxHeading(it.kind), Quiet: true,
							Aside: fmt.Sprint(countKind(items, it.kind))})
					}
					a.inboxCard(c.Key(it.key), it)
				}
			})
		})
	})
}

func countKind(items []inboxItem, k inboxKind) int {
	n := 0
	for _, it := range items {
		if it.kind == k {
			n++
		}
	}
	return n
}

func inboxHeading(k inboxKind) string {
	switch k {
	case inboxFull:
		return "The desk is full"
	case inboxApproval:
		return "Waiting in sessions"
	case inboxAsk:
		return "Decisions"
	case inboxWatch:
		return "Checks due"
	case inboxReady:
		return "Ready to merge"
	case inboxFailed:
		return "Went wrong"
	case inboxFound:
		return "Found work"
	case inboxStale:
		return "Gone quiet"
	case inboxLesson:
		return "Lessons to review"
	}
	return ""
}

// inboxLine says what waits in a line.
func (a *App) inboxLine(items []inboxItem) string {
	counts := map[inboxKind]int{}
	for _, it := range items {
		counts[it.kind]++
	}
	var parts []string
	if n := counts[inboxAsk]; n > 0 {
		parts = append(parts, plural(n, "decision", "decisions"))
	}
	if n := counts[inboxApproval]; n > 0 {
		parts = append(parts, plural(n, "call to allow", "calls to allow"))
	}
	if n := counts[inboxWatch]; n > 0 {
		parts = append(parts, plural(n, "check due", "checks due"))
	}
	if n := counts[inboxReady]; n > 0 {
		parts = append(parts, plural(n, "PR to merge", "PRs to merge"))
	}
	if n := counts[inboxLesson]; n > 0 {
		parts = append(parts, plural(n, "lesson to review", "lessons to review"))
	}
	if n := counts[inboxFailed] + counts[inboxStale] + counts[inboxFound]; n > 0 {
		parts = append(parts, plural(n, "thing to look at", "things to look at"))
	}
	working := 0
	for i := range a.sessions {
		if a.stateOf(&a.sessions[i]) == stateWorking {
			working++
		}
	}
	if working > 0 {
		parts = append(parts, plural(working, "run at work", "runs at work"))
	}
	if len(parts) == 0 {
		return "Nothing waits for you."
	}
	return strings.Join(parts, " · ")
}

// card frames one inbox item; selected, its context shows on the right.
func (a *App) card(c *ui.Context, key string, tone frostui.Tone, label string, body func()) {
	t := c.Theme()
	sel := a.inboxSel == key
	card := ui.ButtonBase(c).Column().Gap(frostui.SpaceS).Padding(frostui.SpaceM, frostui.SpaceL).Radius(12).AlignItems(ui.Stretch).
		BorderWidth(1, 1, 1, 1).Label(label).AccessSelected(sel)
	line := t.Border
	if tone != frostui.Neutral {
		line = frostui.ToneLine(t, tone)
	}
	switch {
	case sel:
		card.Background(frostui.Panel(t)).BorderColor(t.Accent)
	case card.Hovered():
		card.Background(t.SurfaceHover).BorderColor(line)
	default:
		card.Background(frostui.Panel(t)).BorderColor(line)
	}
	if card.Clicked() {
		a.inboxSel = key
	}
	card.Children(body)
}

func (a *App) inboxCard(c *ui.Context, it *inboxItem) {
	switch it.kind {
	case inboxFull:
		a.card(c, it.key, frostui.Warning, "The desk is full", func() {
			v := a.desk.view
			ui.Row(c).Gap(frostui.SpaceM).AlignItems(ui.Center).Children(func() {
				frostui.Icon(c, "dialog-warning")
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					frostui.Strong(c, fmt.Sprintf("%d of %d threads count", v.Cap.Counted, v.Cap.Cap))
					frostui.Muted(c, "The supervisor cannot delegate more until one closes, or you allow it.").Wrap()
				})
				if ui.PrimaryButton(c, "Allow 1").Clicked() {
					a.grant()
				}
			})
		})
	case inboxApproval:
		a.approvalItem(c, it)
	case inboxAsk:
		a.askItem(c, it)
	case inboxWatch:
		a.watchItem(c, it)
	case inboxReady:
		a.readyItem(c, it)
	case inboxFailed:
		s := it.session
		a.card(c, it.key, frostui.Danger, s.DisplayTitle(), func() {
			a.cardHead(c, a.agentOf(s), s.DisplayTitle(), clock(it.at))
			frostui.Toned(c, firstNonEmpty(s.Failure, "The run failed."), frostui.Danger).Wrap()
			ui.Row(c).Gap(frostui.SpaceS).Children(func() {
				ui.Spacer(c)
				if ui.Button(c, "Dismiss").Clicked() {
					a.markSeen(s.ID)
				}
				if ui.PrimaryButton(c, "Open").Clicked() {
					a.open(s.ID)
				}
			})
		})
	case inboxLesson:
		a.lessonCard(c, it.agent, it.lesson, true)
	case inboxStale, inboxFound:
		t := it.thread
		what := "No news for days."
		if it.kind == inboxFound {
			what = "The Desk found work with no thread: select it to attach it or drop it."
		}
		a.card(c, it.key, frostui.Neutral, t.Title, func() {
			a.threadHead(c, t)
			frostui.Muted(c, what).Wrap()
			if it.kind == inboxStale {
				ui.Row(c).Gap(frostui.SpaceS).Children(func() {
					ui.Spacer(c)
					if ui.Button(c, "Park").Tooltip("Out of the cap until you wake it").Clicked() {
						a.threadAct(t, "park", nil)
					}
					if ui.PrimaryButton(c, "Keep").Tooltip("Still wanted: start the stale clock again").Clicked() {
						a.threadAct(t, "keep", nil)
					}
				})
			}
		})
	}
}

// cardHead is a card's first line: who, what, when.
func (a *App) cardHead(c *ui.Context, p *pocket.Persona, title, when string) {
	ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
		if p != nil {
			a.agentAvatar(c, p.ID, 22)
		}
		frostui.Strong(c, title).SingleLine().Grow(1)
		if when != "" {
			frostui.Caption(c, when)
		}
	})
}

// threadHead is a thread's first line: its id, title and agents.
func (a *App) threadHead(c *ui.Context, t *pocket.DeskThread) {
	ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
		for _, ag := range t.Agents {
			a.agentAvatar(c.Key(ag), ag, 22)
		}
		frostui.Mono(c, t.ID).TextColor(c.Theme().TextMuted)
		frostui.Strong(c, t.Title).SingleLine().Grow(1)
		if t.Repo != "" {
			frostui.Caption(c, t.Repo)
		}
	})
}

func (a *App) approvalItem(c *ui.Context, it *inboxItem) {
	s := it.session
	ap := s.Approval
	a.card(c, it.key, frostui.Warning, s.DisplayTitle(), func() {
		a.cardHead(c, a.agentOf(s), s.DisplayTitle(), clock(it.at))
		if ap == nil {
			frostui.Muted(c, "Something waits for you here.")
			return
		}
		if ap.Form {
			ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
				frostui.Icon(c, "pocket-question")
				frostui.Body(c, firstNonEmpty(ap.Subject, "Pi asks a question")).Wrap().Grow(1)
			})
		} else {
			ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
				frostui.Icon(c, "pocket-approval")
				frostui.Strong(c, "Allow "+ap.Tool+"?")
				if ap.ConversationID != s.ID {
					frostui.Caption(c, "in a subagent")
				}
				if ap.Count > 1 {
					frostui.Caption(c, fmt.Sprintf("1 of %d", ap.Count))
				}
			})
			if ap.Subject != "" {
				codeBox(c, ap.Subject, 120)
			}
			if ap.Reason != "" {
				frostui.Muted(c, ap.Reason).Wrap()
			}
		}
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			if step := a.lastStep(s.ID); step != "" {
				frostui.Caption(c, truncate(step, 90)).SingleLine().Grow(1)
			} else {
				ui.Spacer(c)
			}
			busy := a.composer.answered[ap.ID]
			if ap.Form {
				if ui.PrimaryButton(c, "Answer").Clicked() {
					a.open(ap.ConversationID)
				}
				return
			}
			if ui.Button(c, "Open").Clicked() {
				a.open(ap.ConversationID)
			}
			if ui.Button(c, "Deny").Disabled(busy).Clicked() {
				a.answerListed(ap, false)
			}
			if ui.PrimaryButton(c, "Allow").Disabled(busy).Clicked() {
				a.answerListed(ap, true)
			}
		})
	})
}

// answerListed answers the approval a session row names, from the Inbox.
func (a *App) answerListed(ap *pocket.SessionApproval, allow bool) {
	a.answer(&pocket.Approval{ID: ap.ID, ConversationID: ap.ConversationID, Tool: ap.Tool, Subject: ap.Subject}, allow, nil)
}

func (a *App) askItem(c *ui.Context, it *inboxItem) {
	t, ask := it.thread, it.ask
	key := askKey(ask)
	c = c.Key(key)
	if a.desk.answers == nil {
		a.desk.answers = map[string]string{}
	}
	a.card(c, it.key, frostui.Accent, t.Title+" · "+ask.ID, func() {
		if it.firstAsk {
			a.threadHead(c, t)
		}
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			frostui.Mono(c, ask.ID).TextColor(c.Theme().TextMuted)
			if ask.From != "" {
				frostui.Caption(c, "from "+a.agentName(ask.From))
			}
			ui.Spacer(c)
			frostui.Caption(c, clock(it.at))
		})
		markdown.View(c, linkArtifacts(ask.Text, a.artifactHint(t)), &a.md)
		a.artifactButtons(c, refIDs(ask.Text), a.artifactPrefer(t))
		busy := a.desk.busy[key]
		if len(ask.Options) > 0 {
			ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
				for i, opt := range ask.Options {
					label := opt
					if i < 9 {
						label = fmt.Sprintf("%d  %s", i+1, opt)
					}
					if ui.Button(c.Key(opt), label).Disabled(busy).Clicked() {
						a.answerAsk(ask, opt)
					}
				}
			})
		}
		words := a.desk.answers[key]
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			in := ui.TextInput(c, &words).Placeholder("Your answer, in words").Label("Answer " + ask.ID).Disabled(busy).Grow(1)
			a.desk.answers[key] = words
			if busy {
				ui.Spinner(c).Size(14, 14)
			}
			if (ui.PrimaryButton(c, "Answer").Disabled(busy || strings.TrimSpace(words) == "").Clicked() || in.Submitted()) && strings.TrimSpace(words) != "" {
				a.answerAsk(ask, words)
			}
		})
	})
}

func (a *App) readyItem(c *ui.Context, it *inboxItem) {
	p := it.pr
	a.card(c, it.key, frostui.Success, p.Ref, func() {
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			for _, ag := range p.Agents {
				a.agentAvatar(c.Key(ag), ag, 22)
			}
			if p.Level != "" {
				frostui.Pill(c, p.Level, levelTone(p.Level))
			}
			frostui.Mono(c, p.Ref)
			frostui.Strong(c, p.Title).SingleLine().Grow(1)
			frostui.Caption(c, clock(it.at))
		})
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			var facts []string
			if p.Checks != "" {
				facts = append(facts, "checks "+strings.ToLower(p.Checks))
			}
			if p.Head != "" {
				facts = append(facts, p.Head)
			}
			facts = append(facts, "thread "+p.Thread)
			frostui.Caption(c, strings.Join(facts, " · ")).Grow(1)
			if p.URL != "" && ui.PrimaryButton(c, "Review on GitHub").Clicked() {
				a.openURL(p.URL)
			}
		})
	})
}

// levelTone colors a review level: L1 asks the most care.
func levelTone(level string) frostui.Tone {
	switch {
	case strings.HasPrefix(level, "L1"):
		return frostui.Danger
	case strings.HasPrefix(level, "L2"):
		return frostui.Warning
	case strings.HasPrefix(level, "L3"):
		return frostui.Accent
	}
	return frostui.Neutral
}

// openURL opens a web address in the person's browser.
func (a *App) openURL(u string) {
	opener := frostui.Opener{Schemes: []string{"https", "http", "mailto"}, Launch: a.opts.OpenURL}
	go func() {
		if err := opener.OpenURL(u); err != nil {
			a.post(func() { a.toast("Opening the link: " + err.Error()) })
		}
	}()
}

// inboxContext is the right pane while the Inbox shows: what the chosen
// item is about.
func (a *App) inboxContext(c *ui.Context) {
	var it *inboxItem
	items := a.inboxItems()
	for i := range items {
		if items[i].key == a.inboxSel {
			it = &items[i]
		}
	}
	switch {
	case it == nil:
		a.cheatSheet(c)
	case it.thread != nil:
		a.threadContext(c, it.thread)
	case it.session != nil:
		a.sessionContext(c, it.session)
	default:
		a.cheatSheet(c)
	}
}

// sessionContext shows a session's last steps, from its peek.
func (a *App) sessionContext(c *ui.Context, s *pocket.Session) {
	ui.Row(c).Gap(frostui.SpaceM).AlignItems(ui.Center).Children(func() {
		if p := a.agentOf(s); p != nil {
			a.agentAvatar(c, p.ID, 32)
		}
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			frostui.Strong(c, s.DisplayTitle()).Wrap()
			frostui.Caption(c, a.homePath(s.Cwd)).Wrap()
		})
	})
	if ui.PrimaryButton(c, "Open the session").Clicked() {
		a.open(s.ID)
	}
	if p := a.peeks[s.ID]; p != nil && len(p.Lines) > 0 {
		frostui.SectionHeader(c, frostui.SectionHead{Title: "Lately", Quiet: true})
		for i, l := range p.Lines {
			ui.Row(c.Key(i)).Gap(frostui.SpaceS).AlignItems(ui.Start).Children(func() {
				frostui.Icon(c, peekIcon(l))
				frostui.Caption(c, truncate(firstLine(l.Line()), 160)).Wrap().Grow(1)
			})
		}
	}
}

func peekIcon(l pocket.PeekLine) string {
	switch l.Kind {
	case "tool":
		return toolIcon(l.Name)
	case "shell":
		return "pocket-terminal"
	case "user":
		return "pocket-session"
	case "error":
		return "dialog-error"
	case "text":
		return "pocket-sparkle"
	}
	return "info"
}

// threadContext shows a Desk thread: what keeps it open, its PRs, its
// events and the reports its agents filed.
func (a *App) threadContext(c *ui.Context, t *pocket.DeskThread) {
	ui.Column(c).Gap(frostui.SpaceXS).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			frostui.Mono(c, t.ID)
			frostui.Pill(c, threadStateLabel(t), threadTone(t))
		})
		frostui.Strong(c, t.Title).Wrap()
		var facts []string
		for _, ag := range t.Agents {
			facts = append(facts, a.agentName(ag))
		}
		if t.Repo != "" {
			facts = append(facts, t.Repo)
		}
		if t.Next != "" && t.Next != "none" {
			facts = append(facts, "next: "+a.agentName(t.Next))
		}
		frostui.Caption(c, strings.Join(facts, " · ")).Wrap()
	})
	if s := a.sessionForThread(t); s != nil {
		if ui.PrimaryButton(c, "Open the session").Clicked() {
			a.open(s.ID)
		}
		if step := a.lastStep(s.ID); step != "" {
			frostui.Caption(c, "Now: "+truncate(step, 120)).Wrap()
		}
	}
	a.threadActions(c, t)
	if len(t.Open) > 0 {
		frostui.SectionHeader(c, frostui.SectionHead{Title: "Keeps it open", Quiet: true})
		for i, o := range t.Open {
			frostui.Caption(c.Key(i), "• "+o).Wrap()
		}
	}
	if len(t.PRs) > 0 {
		frostui.SectionHeader(c, frostui.SectionHead{Title: "Pull requests", Quiet: true})
		for _, pr := range t.PRs {
			a.prRow(c.Key(pr.Ref), pr)
		}
	}
	if t.Watch != nil {
		frostui.SectionHeader(c, frostui.SectionHead{Title: "Watch", Quiet: true})
		frostui.Caption(c, strings.TrimSpace(t.Watch.CheckOn+" "+t.Watch.CheckTime)+": "+t.Watch.Check).Wrap()
	}
	if len(t.Resolved) > 0 {
		frostui.SectionHeader(c, frostui.SectionHead{Title: "Resolved or withdrawn", Quiet: true})
		for _, ask := range t.Resolved {
			ui.Column(c.Key("resolved:" + ask.ID)).Gap(2).Children(func() {
				frostui.Caption(c, ask.ID+" · "+ask.Text).Wrap().MaxLines(2).Tooltip(ask.Text)
				frostui.Body(c, ask.Why).Wrap().MaxLines(2).Tooltip(ask.Why)
				if ask.By != "" {
					frostui.Caption(c, "by "+ask.By)
				}
			})
		}
	}
	if len(t.Answered) > 0 {
		frostui.SectionHeader(c, frostui.SectionHead{Title: "Answered", Quiet: true})
		for _, an := range t.Answered {
			ui.Column(c.Key(an.ID)).Gap(2).AlignItems(ui.Stretch).Children(func() {
				frostui.Caption(c, an.ID+" · "+truncate(firstLine(an.Text), 80)).Wrap()
				frostui.Body(c, "→ "+an.Words).Wrap().MaxLines(2).Tooltip(an.Words)
			})
		}
	}
	d := a.threadDetail(t.ID)
	if d != nil && len(d.Reports) > 0 {
		frostui.SectionHeader(c, frostui.SectionHead{Title: "Reports", Aside: fmt.Sprint(len(d.Reports)), Quiet: true})
		for i := len(d.Reports) - 1; i >= 0 && i >= len(d.Reports)-3; i-- {
			r := d.Reports[i]
			a.disclosureRow(c.Key(fmt.Sprint("r", i)), "report:"+t.ID+":"+fmt.Sprint(i), "pocket-desk",
				truncate(firstLine(r.Text), 70), clock(r.At.Time()), func() {
					markdown.View(c, linkArtifacts(r.Text, a.artifactHint(t)), &a.md)
					a.artifactButtons(c, refIDs(r.Text), a.artifactPrefer(t))
				})
		}
	}
	events := t.Events
	if d != nil && len(d.Thread.Events) > 0 {
		events = d.Thread.Events
	}
	if len(events) > 0 {
		frostui.SectionHeader(c, frostui.SectionHead{Title: "Events", Quiet: true})
		for i := len(events) - 1; i >= 0; i-- {
			e := events[i]
			ui.Row(c.Key(fmt.Sprint("e", i))).Gap(frostui.SpaceS).AlignItems(ui.Start).Children(func() {
				frostui.Caption(c, clock(e.At.Time())).Width(64)
				frostui.Caption(c, e.Text).Wrap().MaxLines(2).Tooltip(e.Text).Grow(1)
			})
		}
	}
}

func (a *App) prRow(c *ui.Context, pr pocket.DeskPR) {
	t := c.Theme()
	row := ui.ButtonBase(c).Row().Gap(frostui.SpaceS).AlignItems(ui.Center).Padding(4, frostui.SpaceS).Radius(6).
		Cursor(ui.CursorPointer).Label("Open " + pr.Ref).Disabled(pr.URL == "")
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	if row.Clicked() && pr.URL != "" {
		a.openURL(pr.URL)
	}
	row.Children(func() {
		frostui.Pill(c, strings.ToLower(firstNonEmpty(pr.State, "?")), prTone(pr.State))
		frostui.Mono(c, pr.Ref).SingleLine()
		if pr.Ready || pr.Next == "brian" {
			frostui.Pill(c, "ready", frostui.Success)
		}
		if pr.Level != "" {
			frostui.Pill(c, pr.Level, levelTone(pr.Level))
		}
		frostui.Caption(c, pr.Title).SingleLine().Grow(1)
	})
}

func prTone(state string) frostui.Tone {
	switch state {
	case "MERGED":
		return frostui.Success
	case "OPEN":
		return frostui.Accent
	case "CLOSED", "MISSING":
		return frostui.Danger
	}
	return frostui.Neutral
}

func threadStateLabel(t *pocket.DeskThread) string {
	if t.Closed != nil {
		return firstNonEmpty(t.Closed.Outcome, t.Closed.Why)
	}
	switch t.Group() {
	case "need":
		return "needs you"
	case "review":
		return "in review"
	case "watching":
		return "watching"
	case "stale":
		return "quiet"
	case "parked":
		return "parked"
	case "found":
		return "found"
	case "plans":
		return "plan"
	}
	return "working"
}

func threadTone(t *pocket.DeskThread) frostui.Tone {
	switch t.Group() {
	case "need":
		return frostui.Warning
	case "working", "plans":
		return frostui.Accent
	case "closed":
		if t.Closed != nil && t.Closed.Why == "failed" {
			return frostui.Danger
		}
		return frostui.Success
	}
	return frostui.Neutral
}

// answerByNumber answers the chosen Desk ask with its option n (1-based).
func (a *App) answerByNumber(n int) {
	if !strings.HasPrefix(a.inboxSel, "a:") {
		return
	}
	for _, it := range a.inboxItems() {
		if it.key == a.inboxSel && it.ask != nil && n <= len(it.ask.Options) {
			a.answerAsk(it.ask, it.ask.Options[n-1])
			return
		}
	}
}

// moveInbox chooses the next (or previous) inbox item.
func (a *App) moveInbox(step int) {
	items := a.inboxItems()
	if len(items) == 0 {
		return
	}
	at := 0
	for i, it := range items {
		if it.key == a.inboxSel {
			at = i + step
		}
	}
	at = max(0, min(len(items)-1, at))
	a.inboxSel = items[at].key
}
