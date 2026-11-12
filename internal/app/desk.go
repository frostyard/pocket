package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// desk is what the Desk module says, when the server has it.
type desk struct {
	on      bool
	view    *pocket.DeskView
	loading bool
	// dirty asks for another read once the one under way ends.
	dirty bool
	at    time.Time
	err   string

	details map[string]*pocket.DeskThreadDetail
	answers map[string]string
	busy    map[string]bool

	dash        *pocket.Dashboard
	dashDays    int
	dashLoading bool

	board        *pocket.Board
	boardLoading bool
	boardBusy    bool
}

func (d *desk) thread(id string) *pocket.DeskThread {
	if d.view == nil {
		return nil
	}
	for i := range d.view.Threads {
		if d.view.Threads[i].ID == id {
			return &d.view.Threads[i]
		}
	}
	return nil
}

// loadDesk reads the Desk; a read asked for while one is under way runs
// once it ends, so a burst of changes costs two reads.
func (a *App) loadDesk() {
	if a.desk.loading {
		a.desk.dirty = true
		return
	}
	a.desk.loading = true
	var view *pocket.DeskView
	var on bool
	// The Desk may sweep GitHub and git before it answers, when nothing
	// swept for 5 minutes: give it time.
	a.requestWithin(3*time.Minute, "Reading the Desk", func(ctx context.Context, c *pocket.Client) error {
		var err error
		view, on, err = c.Desk(ctx)
		return err
	}, func(err error) {
		a.desk.loading = false
		if err != nil {
			a.desk.err = err.Error()
		} else {
			a.desk.err = ""
			a.desk.on = on
			if on {
				a.desk.view = view
				a.desk.at = a.opts.Now()
				a.desk.details = nil // read again as they show
				a.notifyNewAsks(view)
			}
		}
		if a.desk.dirty {
			a.desk.dirty = false
			a.loadDesk()
		}
	})
}

// deskChanged follows the Desk's "changed" signal.
func (a *App) deskChanged() {
	a.loadDesk()
	if a.desk.board != nil {
		a.loadBoard()
	}
}

// threadDetail returns a thread's reports and events, reading them once.
func (a *App) threadDetail(id string) *pocket.DeskThreadDetail {
	if d := a.desk.details[id]; d != nil {
		return d
	}
	if a.desk.details == nil {
		a.desk.details = map[string]*pocket.DeskThreadDetail{}
	}
	if a.desk.busy == nil {
		a.desk.busy = map[string]bool{}
	}
	key := "detail:" + id
	if a.desk.busy[key] {
		return nil
	}
	a.desk.busy[key] = true
	var got *pocket.DeskThreadDetail
	a.request("Reading thread "+id, func(ctx context.Context, c *pocket.Client) error {
		var err error
		got, err = c.DeskThread(ctx, id)
		return err
	}, func(err error) {
		delete(a.desk.busy, key)
		if err == nil {
			a.desk.details[id] = got
		}
	})
	return nil
}

// answerAsk answers a Desk ask with words.
func (a *App) answerAsk(ask *pocket.DeskAsk, words string) {
	words = strings.TrimSpace(words)
	if words == "" {
		return
	}
	if a.desk.busy == nil {
		a.desk.busy = map[string]bool{}
	}
	a.desk.busy[ask.ID] = true
	id, gen := ask.ID, ask.Gen
	a.console.Add(frostui.LevelCommand, "desk", "Answered "+id+": "+truncate(words, 80))
	a.request("Answering "+id, func(ctx context.Context, c *pocket.Client) error {
		return c.DeskAnswer(ctx, id, words, gen)
	}, func(err error) {
		delete(a.desk.busy, id)
		if err != nil {
			a.toast("Not answered: " + err.Error())
			return
		}
		delete(a.desk.answers, id)
		a.toast(id + " answered")
		a.loadDesk()
	})
}

// threadAct runs keep, park, wake or drop on a thread.
func (a *App) threadAct(t *pocket.DeskThread, action string, body map[string]any) {
	id := t.ID
	a.console.Add(frostui.LevelCommand, "desk", action+" "+id)
	a.request(strings.ToUpper(action[:1])+action[1:]+" "+id, func(ctx context.Context, c *pocket.Client) error {
		return c.DeskAct(ctx, id, action, body)
	}, func(err error) {
		if err == nil {
			a.loadDesk()
		}
	})
}

// grant lets the supervisor start one more thread past the cap.
func (a *App) grant() {
	a.request("Allowing 1 more thread", func(ctx context.Context, c *pocket.Client) error {
		return c.DeskGrant(ctx, 1)
	}, func(err error) {
		if err == nil {
			a.loadDesk()
		}
	})
}

// sessionForThread finds the session that works on a thread: the Desk's
// view names none, but each delegation's session is titled after its
// thread, and is the agent's.
func (a *App) sessionForThread(t *pocket.DeskThread) *pocket.Session {
	title := strings.ToLower(strings.TrimSpace(t.Title))
	if title == "" {
		return nil
	}
	var best *pocket.Session
	for i := range a.sessions {
		s := &a.sessions[i]
		st := strings.ToLower(s.Title)
		if !strings.Contains(st, title) && !strings.Contains(title, strings.TrimSpace(afterColon(st))) {
			continue
		}
		if p := a.agentOf(s); p != nil && len(t.Agents) > 0 && !slices.Contains(t.Agents, p.ID) {
			continue
		}
		if best == nil || s.UpdatedAt > best.UpdatedAt {
			best = s
		}
	}
	return best
}

// afterColon drops the "Agent: " a delegated session's title starts with.
func afterColon(s string) string {
	if _, rest, ok := strings.Cut(s, ": "); ok && len(rest) > 12 {
		return rest
	}
	return "\x00" // matches nothing
}

// agentName names a roster id, or returns it.
func (a *App) agentName(id string) string {
	if p := a.team.agent(id); p != nil {
		return p.Name
	}
	if id == "brian" && a.hello != nil {
		return a.hello.User.Name
	}
	return id
}

// boardKey makes a key for one board act: "<ms>-<random>".
func boardKey(now time.Time) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%d-%s", now.UnixMilli(), hex.EncodeToString(b))
}

// loadBoard reads the shift board.
func (a *App) loadBoard() {
	if a.desk.boardLoading {
		return
	}
	a.desk.boardLoading = true
	var b *pocket.Board
	a.request("Reading the board", func(ctx context.Context, c *pocket.Client) error {
		var err error
		b, err = c.DeskBoard(ctx, 0)
		return err
	}, func(err error) {
		a.desk.boardLoading = false
		if err == nil {
			a.desk.board = b
		}
	})
}

// boardOp picks, unpicks or launches.
func (a *App) boardOp(op string, row *pocket.BoardRow, launch []pocket.BoardRow) {
	b := a.desk.board
	if b == nil || b.Supervisor == nil {
		a.toast("The board has no supervisor yet")
		return
	}
	sup := *b.Supervisor
	key := boardKey(a.opts.Now())
	a.desk.boardBusy = true
	a.console.Add(frostui.LevelCommand, "board", op)
	a.request("Board: "+op, func(ctx context.Context, c *pocket.Client) error {
		return c.BoardOp(ctx, sup, op, key, row, launch)
	}, func(err error) {
		a.desk.boardBusy = false
		if err == nil {
			a.loadBoard()
			if op == "launch" {
				a.toast(plural(len(launch), "shift launched", "shifts launched"))
			}
		}
	})
}

// loadDashboard reads the numbers for the last days days.
func (a *App) loadDashboard(days int) {
	if a.desk.dashLoading {
		return
	}
	a.desk.dashLoading = true
	a.desk.dashDays = days
	now := a.opts.Now().UTC()
	from := now.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	to := now.Format("2006-01-02")
	var d *pocket.Dashboard
	a.request("Reading the numbers", func(ctx context.Context, c *pocket.Client) error {
		var err error
		d, err = c.DeskDashboard(ctx, from, to)
		return err
	}, func(err error) {
		a.desk.dashLoading = false
		if err == nil {
			a.desk.dash = d
		}
	})
}

// watchPeeks asks the server for live summaries of the sessions that
// matter now: those that wait or work, newest first, at most 20.
func (a *App) watchPeeks() {
	if a.link.streamID == "" || a.hello == nil || !a.hello.Server.Peeks {
		return
	}
	var ids []int
	for i := range a.sessions {
		switch a.stateOf(&a.sessions[i]) {
		case stateWaiting, stateWorking:
			ids = append(ids, a.sessions[i].ID)
		}
		if len(ids) == 20 {
			break
		}
	}
	slices.Sort(ids)
	if slices.Equal(ids, a.peekIDs) {
		return
	}
	a.peekIDs = ids
	a.peekSeq++
	seq := a.peekSeq
	conn := a.link.streamID
	a.request("Watching sessions", func(ctx context.Context, c *pocket.Client) error {
		return c.Peeks(ctx, conn, ids, seq)
	}, func(error) {})
}

// lastStep says what a session did last, from its peek.
func (a *App) lastStep(id int) string {
	p := a.peeks[id]
	if p == nil {
		return ""
	}
	for i := len(p.Lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(firstLine(p.Lines[i].Line())); l != "" {
			return l
		}
	}
	return ""
}

// notifyNewAsks tells the person of decisions the Desk did not ask for
// before, when the window is not in front.
func (a *App) notifyNewAsks(v *pocket.DeskView) {
	first := a.asksSeen == nil
	if first {
		a.asksSeen = map[string]bool{}
	}
	for i := range v.Threads {
		t := &v.Threads[i]
		for _, ask := range t.Asks {
			if a.asksSeen[ask.ID] {
				continue
			}
			a.asksSeen[ask.ID] = true
			if first || a.focused {
				continue
			}
			a.deliver(note{kind: noteDecision, title: "Decision needed · " + t.ID + " " + t.Title,
				body: truncate(firstLine(ask.Text), 200), inbox: "a:" + ask.ID})
		}
	}
}
