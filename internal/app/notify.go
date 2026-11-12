package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/config"
	"github.com/frostyard/pocket/internal/pocket"
)

// Kinds of notification, from the server's tags (alerts.ts, collab.ts,
// watches.ts) and the Desk.
const (
	noteApproval = "approval" // a call to allow, or a question
	noteDecision = "decision" // a Desk ask
	noteFinished = "done"     // a run ended
	noteChat     = "chat"     // people's chat, mentions
	noteLimit    = "limit"    // a run waits for a usage limit
	noteWatch    = "watch"    // a watched PR moved
)

// batchWindow is how long a burst of notifications is gathered into one.
const batchWindow = 10 * time.Second

// note is one notification on its way to the desktop.
type note struct {
	kind    string
	title   string
	body    string
	session int
	// inbox, when not empty, is the Inbox item a click chooses.
	inbox string
}

// notes is the queue of notifications in a burst.
type notes struct {
	// open is true while a burst's window runs: what comes waits in held.
	open bool
	held []note
	// quieted counts what do not disturb kept back since it began.
	quieted int
}

// noteKind reads a notification's kind from its tag ("done-12").
func noteKind(tag string) string {
	kind, _, _ := strings.Cut(tag, "-")
	return kind
}

// wants reports whether the person's settings show this kind for this
// session.
func (a *App) wants(n note) bool {
	ns := a.settings.Notes()
	if !ns.Enabled {
		return false
	}
	switch n.kind {
	case noteApproval:
		return ns.Approvals
	case noteDecision:
		return ns.Decisions
	case noteChat:
		return ns.Chat
	case noteLimit:
		return ns.Limits
	case noteWatch:
		return ns.Watches
	case noteFinished:
		switch ns.Finished {
		case config.FinishedAll:
			return true
		case config.FinishedNone:
			return false
		}
		return a.mine(n.session)
	}
	return true
}

// mine reports whether the person started session id themself (it was
// not delegated from another session) or pinned it.
func (a *App) mine(id int) bool {
	if a.pinned(id) {
		return true
	}
	s := a.byID[id]
	if s == nil {
		return false // a subagent's, which the list does not hold
	}
	if s.LinkedFrom != nil {
		return false
	}
	if a.hello != nil && s.CreatedBy != "" && s.CreatedBy != a.hello.User.ID {
		return false
	}
	return true
}

// quietNow says whether do not disturb holds now, and until when ("" for
// until turned off).
func (a *App) quietNow() (bool, string) {
	ns := a.settings.Notes()
	now := a.opts.Now()
	switch {
	case ns.DND:
		return true, ""
	case ns.SnoozeUntil > now.UnixMilli():
		return true, time.UnixMilli(ns.SnoozeUntil).Local().Format("15:04")
	case ns.Quiet && inQuietHours(now.Local(), ns.QuietFrom, ns.QuietTo):
		return true, ns.QuietTo
	}
	return false, ""
}

// inQuietHours reports whether t falls between from and to ("22:00",
// "07:00"), across midnight when to is earlier than from.
func inQuietHours(t time.Time, from, to string) bool {
	f, okf := minutesOf(from)
	e, oke := minutesOf(to)
	if !okf || !oke || f == e {
		return false
	}
	m := t.Hour()*60 + t.Minute()
	if f < e {
		return m >= f && m < e
	}
	return m >= f || m < e
}

func minutesOf(hhmm string) (int, bool) {
	var h, m int
	if _, err := fmt.Sscanf(strings.TrimSpace(hhmm), "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// deliver shows a notification, as the settings say: some kinds not at
// all, nothing but approvals while quiet, and a burst as one.
func (a *App) deliver(n note) {
	if a.opts.Notify == nil || !a.wants(n) {
		return
	}
	if quiet, _ := a.quietNow(); quiet && (n.kind != noteApproval || !a.settings.Notes().Urgent) {
		a.notes.quieted++
		return
	}
	a.console.Add(frostui.LevelInfo, "notify", n.title+": "+n.body)
	if !a.settings.Notes().Batch {
		a.show(n)
		return
	}
	if a.notes.open {
		a.notes.held = append(a.notes.held, n)
		return
	}
	// The first of a burst shows at once; what follows it in the window
	// waits and shows as one.
	a.show(n)
	a.notes.open = true
	a.after(batchWindow, a.flushNotes)
}

// flushNotes ends a burst's window: what came in it shows as one.
func (a *App) flushNotes() {
	held := a.notes.held
	a.notes.open, a.notes.held = false, nil
	switch len(held) {
	case 0:
		return
	case 1:
		a.show(held[0])
		return
	}
	sessions := map[int]bool{}
	for _, n := range held {
		sessions[n.session] = true
	}
	lines := make([]string, 0, 4)
	for i, n := range held {
		if i == 3 {
			lines = append(lines, fmt.Sprintf("and %d more", len(held)-3))
			break
		}
		lines = append(lines, "• "+n.title)
	}
	sum := note{kind: "batch", title: summaryTitle(held), body: strings.Join(lines, "\n")}
	if len(sessions) == 1 && held[0].session != 0 {
		sum.session = held[0].session
	}
	a.show(sum)
}

// summaryTitle says what a burst holds: "3 runs finished", "4 updates".
func summaryTitle(held []note) string {
	kind := held[0].kind
	for _, n := range held {
		if n.kind != kind {
			return fmt.Sprintf("%d updates from your team", len(held))
		}
	}
	switch kind {
	case noteFinished:
		return fmt.Sprintf("%d runs finished", len(held))
	case noteApproval:
		return fmt.Sprintf("%d calls wait for you", len(held))
	case noteDecision:
		return fmt.Sprintf("%d decisions wait for you", len(held))
	case noteChat:
		return fmt.Sprintf("%d new messages", len(held))
	}
	return fmt.Sprintf("%d updates", len(held))
}

// show hands a notification to the desktop; a click opens what it is
// about.
func (a *App) show(n note) {
	a.opts.Notify.Notify(n.title, n.body, func() {
		a.post(func() {
			switch {
			case n.session != 0:
				a.open(n.session)
			case n.inbox != "":
				a.goPlace("x:inbox")
				a.inboxSel = n.inbox
			default:
				a.goPlace("x:inbox")
			}
		})
	})
}

// after runs fn on the UI thread after d.
func (a *App) after(d time.Duration, fn func()) {
	if a.opts.After != nil {
		a.opts.After(d, fn)
		return
	}
	time.AfterFunc(d, func() { a.post(fn) })
}

// notify takes a notification the server sent.
func (a *App) notify(n pocket.Notification) {
	id := sessionFromURL(n.URL)
	if a.focused && id != 0 && id == a.current {
		return
	}
	// An approval's tag names that one call: the server may send it again
	// (a reconnect, a second device). Other tags name a session, whose next
	// run or message is news again.
	if noteKind(n.Tag) == noteApproval {
		if a.notified[n.Tag] {
			return
		}
		a.notified[n.Tag] = true
	}
	a.deliver(note{kind: noteKind(n.Tag), title: n.Title, body: n.Body, session: id})
}

// setQuiet turns do not disturb on or off; snooze, when not zero, keeps
// it on for that long instead.
func (a *App) setQuiet(on bool, snooze time.Duration) {
	ns := a.settings.Notes()
	ns.SnoozeUntil = 0
	ns.DND = false
	switch {
	case snooze > 0:
		ns.SnoozeUntil = a.opts.Now().Add(snooze).UnixMilli()
	case on:
		ns.DND = true
	}
	if !on && snooze == 0 {
		if a.notes.quieted > 0 {
			a.toast(plural(a.notes.quieted, "notification was held; the Inbox has what waits", "notifications were held; the Inbox has what waits"))
		}
		a.notes.quieted = 0
	}
	a.saveSettings()
}

// quietItem is the status bar's do-not-disturb item, while it holds.
func (a *App) quietItem() (frostui.StatusItem, bool) {
	quiet, until := a.quietNow()
	if !quiet {
		return frostui.StatusItem{}, false
	}
	text := "Do not disturb"
	if until != "" {
		text = "Quiet until " + until
	}
	if a.notes.quieted > 0 {
		text += fmt.Sprintf(" · %d held", a.notes.quieted)
	}
	return frostui.StatusItem{Icon: "pocket-night", Text: text, Progress: -1,
		Tooltip: "Notifications are held. Click to turn do not disturb off.", OnClick: func() {
			ns := a.settings.Notes()
			if ns.Quiet && !ns.DND && ns.SnoozeUntil <= a.opts.Now().UnixMilli() {
				// Quiet hours: open the settings rather than switch them off.
				a.openSettings("notifications")
				return
			}
			a.setQuiet(false, 0)
		}}, true
}
