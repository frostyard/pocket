package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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

	details                           map[string]*pocket.DeskThreadDetail
	answers                           map[string]string
	busy                              map[string]bool
	editors                           map[string]*threadEditor
	seenPending, seenReady            bool
	seenBefore                        pocket.Millis
	completed                         []pocket.CompletedPlan
	completedLoading, completedLoaded bool
	completedErr                      string

	dash                         *pocket.Dashboard
	dashDays                     int
	dashLoading                  bool
	dashSeq                      int
	dashOwner, dashRepo, dashErr string
	dashShown                    pocket.DashboardQuery
	records                      *numberRecords
	recordThread                 *numberThread

	board        *pocket.Board
	boardLoading bool
	boardBusy    bool
	boardChosen  int
	boardDirty   bool
	boardErr     string
	boardLoaded  bool
	boardEditors map[int]*boardEditor
	boardOpen    map[int]bool
	boardKeys    map[string]string
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
				if a.desk.seenPending {
					a.stampDeskSeen()
				}
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
	a.loadPlan()
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

// askKey follows the Desk's draftKey: a reopened plan ask is a new decision.
func askKey(ask *pocket.DeskAsk) string {
	gen := 0
	if ask.Gen != nil {
		gen = *ask.Gen
	}
	return fmt.Sprintf("%s:%d", ask.ID, gen)
}

// answerAsk answers a Desk ask with words.
func (a *App) answerAsk(ask *pocket.DeskAsk, words string) {
	if a.desk.view != nil {
		for _, thread := range a.desk.view.Threads {
			for _, each := range thread.Asks {
				if each.ID == ask.ID && (thread.Origin == "plan" || ask.Gen != nil) {
					a.openPlan(thread.ID)
					return
				}
			}
		}
	}

	words = strings.TrimSpace(words)
	if words == "" {
		return
	}
	if a.desk.busy == nil {
		a.desk.busy = map[string]bool{}
	}
	key := askKey(ask)
	a.desk.busy[key] = true
	id, gen := ask.ID, ask.Gen
	a.console.Add(frostui.LevelCommand, "desk", "Answered "+id+": "+truncate(words, 80))
	a.request("Answering "+id, func(ctx context.Context, c *pocket.Client) error {
		return c.DeskAnswer(ctx, id, words, gen)
	}, func(err error) {
		delete(a.desk.busy, key)
		if err != nil {
			a.toast("Not answered: " + err.Error())
			return
		}
		delete(a.desk.answers, key)
		a.toast(id + " answered")
		a.loadDesk()
	})
}

// threadAct runs an owner action and refreshes even after a conflict.
func (a *App) threadAct(t *pocket.DeskThread, action string, body map[string]any) {
	id := t.ID
	key := "act:" + id
	if a.desk.busy == nil {
		a.desk.busy = map[string]bool{}
	}
	if a.desk.busy[key] {
		return
	}
	a.desk.busy[key] = true
	client := a.link.client
	a.console.Add(frostui.LevelCommand, "desk", action+" "+id)
	a.request(strings.ToUpper(action[:1])+action[1:]+" "+id, func(ctx context.Context, c *pocket.Client) error {
		return c.DeskAct(ctx, id, action, body)
	}, func(err error) {
		if a.link.client != client {
			return
		}
		delete(a.desk.busy, key)
		if err != nil {
			a.toast("Thread unchanged: " + err.Error())
		} else {
			delete(a.desk.editors, id)
		}
		a.loadDesk()
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

// matchThreadSession is the title fallback for older Desk modules without
// a session binding. Titles are normalized once per source snapshot.
func (a *App) matchThreadSession(t *pocket.DeskThread, titles []string) *pocket.Session {
	title := strings.ToLower(strings.TrimSpace(t.Title))
	if title == "" {
		return nil
	}
	var best *pocket.Session
	for i := range a.sessions {
		s := &a.sessions[i]
		st := titles[i]
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
		a.desk.boardDirty = true
		return
	}
	a.desk.boardLoading = true
	chosen := a.desk.boardChosen
	client := a.link.client
	var b *pocket.Board
	a.request("Reading the board", func(ctx context.Context, c *pocket.Client) error {
		var err error
		b, err = c.DeskBoard(ctx, chosen)
		return err
	}, func(err error) {
		if a.link.client != client || a.desk.boardChosen != chosen {
			return
		}
		a.desk.boardLoading = false
		a.desk.boardLoaded = true
		if err == nil {
			oldSup, newSup := 0, 0
			if a.desk.board != nil && a.desk.board.Supervisor != nil {
				oldSup = *a.desk.board.Supervisor
			}
			if b.Supervisor != nil {
				newSup = *b.Supervisor
			}
			if oldSup != newSup {
				a.desk.boardEditors = nil
				a.desk.boardOpen = nil
			}
			if a.desk.boardChosen != 0 {
				a.desk.boardChosen = newSup
			}
			a.desk.board = b
			a.desk.boardErr = ""
		} else {
			a.desk.boardErr = err.Error()
			if a.desk.board != nil && a.desk.board.Supervisor != nil {
				a.desk.boardChosen = *a.desk.board.Supervisor
			}
		}
		if a.desk.boardDirty {
			a.desk.boardDirty = false
			a.loadBoard()
		}
	})
}

// boardOp picks, unpicks or launches.
func (a *App) boardOp(op string, row *pocket.BoardRow, launch []pocket.BoardRow) {
	a.boardChange(op, row, launch, nil, "")
}

func (a *App) boardChange(op string, row *pocket.BoardRow, launch []pocket.BoardRow, fields map[string]any, thread string) {
	if a.desk.boardBusy || a.desk.boardLoading {
		return
	}
	b := a.desk.board
	if b == nil || b.Supervisor == nil {
		a.toast("The board has no supervisor yet")
		return
	}
	sup := *b.Supervisor
	if a.desk.boardChosen != 0 && a.desk.boardChosen != sup {
		a.toast("Wait for the chosen supervisor's board to load")
		return
	}
	fingerprint, _ := json.Marshal([]any{sup, pocket.BoardBody(op, row, launch, fields, thread)})
	signature := string(fingerprint)
	if a.desk.boardKeys == nil {
		a.desk.boardKeys = map[string]string{}
	}
	key := a.desk.boardKeys[signature]
	if key == "" {
		key = boardKey(a.opts.Now())
		a.desk.boardKeys[signature] = key
	}
	client := a.link.client
	a.desk.boardBusy = true
	a.console.Add(frostui.LevelCommand, "board", op)
	var result *pocket.BoardResult
	a.request("Board: "+op, func(ctx context.Context, c *pocket.Client) error {
		var err error
		result, err = c.BoardOp(ctx, sup, op, key, row, launch, fields, thread)
		return err
	}, func(err error) {
		if a.link.client != client {
			return
		}
		a.desk.boardBusy = false
		var api *pocket.APIError
		if err == nil || (errors.As(err, &api) && api.Status == 409) {
			delete(a.desk.boardKeys, signature)
		}
		if err != nil {
			if errors.As(err, &api) {
				a.toast("Board unchanged: " + err.Error())
			} else {
				a.toast("Board request failed; it may have been applied: " + err.Error())
			}
		}
		a.loadBoard()
		if err == nil {
			if row != nil {
				delete(a.desk.boardEditors, row.ID)
			}
			if result != nil {
				if result.Text != "" {
					a.toast(result.Text)
				}
				if len(result.Pass) > 0 {
					a.toast("Launch pass: " + strings.Join(result.Pass, "; "))
				}
			}
		}
	})
}

// loadDashboard reads the numbers for the last days days.
func (a *App) loadDashboard(days int) {
	if days < 1 {
		days = 30
	}
	a.desk.dashSeq++
	seq := a.desk.dashSeq
	a.desk.dashLoading = true
	a.desk.dashDays = days
	now := a.opts.Now().UTC()
	from := now.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	to := now.Format("2006-01-02")
	query := pocket.DashboardQuery{From: from, To: to, Owner: a.desk.dashOwner, Repo: a.desk.dashRepo}
	var d *pocket.Dashboard
	a.request("Reading the numbers", func(ctx context.Context, c *pocket.Client) error {
		var err error
		d, err = c.DeskDashboard(ctx, from, to, query.Owner, query.Repo)
		return err
	}, func(err error) {
		if seq != a.desk.dashSeq {
			return
		}
		a.desk.dashLoading = false
		if err != nil {
			a.desk.dashErr = err.Error()
		} else {
			a.desk.dash = d
			a.desk.dashShown = query
			a.desk.dashErr = ""
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
			if a.asksSeen[askKey(&ask)] {
				continue
			}
			a.asksSeen[askKey(&ask)] = true
			if first || a.focused {
				continue
			}
			a.deliver(note{kind: noteDecision, title: "Decision needed · " + t.ID + " " + t.Title,
				body: truncate(firstLine(ask.Text), 200), inbox: "a:" + ask.ID})
		}
	}
}
