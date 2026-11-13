package pocket

import (
	"encoding/json"
	"errors"
)

// View is one session as the client holds it, kept up to date with
// "view" events by Apply. It belongs to whoever applies them (the UI
// thread).
type View struct {
	Turns        Turns
	Conversation Conversation
	Entries      map[int]*Entry
	Order        []int
	Live         Live
	Agent        Agent
	Stats        Stats
	Clients      int
	Viewers      []string
	Approvals    []Approval
	Subagents    []Subagent
	Schedules    []Schedule
	Held         []HeldMessage
	Authors      map[string]string
	Decisions    map[string]Decision
	Plan         Plan
	Inbox        []QueueRow
	Branch       *Branch
	// Rev counts changes to Entries or Order, so views built from them
	// know when to build again.
	Rev int
	// LiveRev counts changes to Live.
	LiveRev int
}

// ErrResync means an update does not fit what the view holds: open the
// session again for a whole view.
var ErrResync = errors.New("the update does not fit the view: open the session again")

// NewView returns an empty view.
func NewView() *View {
	return &View{Entries: map[int]*Entry{}}
}

// Apply merges an update into v, as the web app's mergeView does.
func (v *View) Apply(u *ViewUpdate) error {
	same := v.Conversation.ID == u.Conversation.ID
	if u.Full || !same {
		live := v.Live
		*v = View{Entries: map[int]*Entry{}, Rev: v.Rev + 1, LiveRev: v.LiveRev + 1}
		if same {
			v.Live = live // the delta below may go on from it
		}
	}
	v.Conversation = u.Conversation
	if u.Turns != nil {
		v.Turns = *u.Turns
	}
	if len(u.Entries) > 0 {
		v.Rev++
	}
	for i := range u.Entries {
		e := u.Entries[i]
		if had, ok := v.Entries[e.ID]; ok && e.Partial && !had.Partial {
			continue // a light one does not replace a whole one
		}
		v.Entries[e.ID] = &e
	}
	if u.Order != nil {
		if u.OrderFrom != nil {
			from := *u.OrderFrom
			if from > len(v.Order) {
				return ErrResync
			}
			v.Order = append(append([]int(nil), v.Order[:from]...), u.Order...)
		} else {
			v.Order = append([]int(nil), u.Order...)
		}
		keep := make(map[int]bool, len(v.Order))
		for _, id := range v.Order {
			keep[id] = true
		}
		for id := range v.Entries {
			if !keep[id] {
				delete(v.Entries, id)
			}
		}
		v.Rev++
	}
	live, err := mergeLive(v.Live, u.Live)
	v.Live = live
	v.LiveRev++
	if u.Agent != nil {
		v.Agent = *u.Agent
	}
	if u.Stats != nil {
		v.Stats = *u.Stats
	}
	v.Clients, v.Viewers, v.Approvals = u.Clients, u.Viewers, u.Approvals
	if u.Schedules != nil {
		v.Schedules = *u.Schedules
	}
	if u.Held != nil {
		v.Held = *u.Held
	}
	if u.Subagents != nil {
		v.Subagents = *u.Subagents
	}
	if u.Authors != nil {
		v.Authors = *u.Authors
	}
	if u.Decisions != nil {
		v.Decisions = *u.Decisions
	}
	if u.Plan != nil {
		v.Plan = *u.Plan
	}
	if u.Inbox != nil {
		v.Inbox = *u.Inbox
	}
	if u.Branch != nil {
		var b *Branch
		if string(u.Branch) != "null" {
			b = &Branch{}
			if err := json.Unmarshal(u.Branch, b); err != nil {
				b = nil
			}
		}
		v.Branch = b
	}
	return err
}

// mergeLive takes the live run of an update, whose message may come as
// what changed since the last one (web/live.js).
func mergeLive(current, next Live) (Live, error) {
	if next.Generation == nil || next.Generation.Message == nil || next.Generation.Message.Keep == nil {
		return next, nil
	}
	var had []Block
	if current.Generation != nil && current.Generation.Message != nil {
		had = current.Generation.Message.Blocks
	}
	blocks, ok := applyDelta(had, next.Generation.Message)
	if !ok {
		// Keep what was shown; the caller opens the session again.
		next.Generation.Message = &LiveMessage{Blocks: had}
		return next, ErrResync
	}
	next.Generation.Message = &LiveMessage{Blocks: blocks}
	return next, nil
}

// applyDelta puts a live message delta onto the blocks held.
func applyDelta(had []Block, d *LiveMessage) ([]Block, bool) {
	keep := *d.Keep
	if keep > len(had) {
		return nil, false
	}
	out := make([]Block, 0, keep+len(d.Blocks))
	out = append(out, had[:keep]...)
	rest := d.Blocks
	if len(rest) > 0 && rest[0].From != nil {
		if keep >= len(had) {
			return nil, false
		}
		base := had[keep]
		first := rest[0]
		if base.Type != first.Type || len(base.Text) != *first.From {
			return nil, false
		}
		base.Text += first.Text
		out = append(out, base)
		rest = rest[1:]
	}
	for _, b := range rest {
		b.From = nil
		out = append(out, b)
	}
	return out, true
}

// Rows are the entries a transcript shows, in order: a tool's result is
// shown in its call's card, not as a row.
func (v *View) Rows() []*Entry {
	rows := make([]*Entry, 0, len(v.Order))
	for _, id := range v.Order {
		e := v.Entries[id]
		if e == nil {
			continue
		}
		switch e.Kind {
		case "user", "assistant", "compaction", "reset", "shell", "command", "note", "fromPi":
			rows = append(rows, e)
		}
	}
	return rows
}

// Results maps tool call IDs to their results.
func (v *View) Results() map[string]*Entry {
	out := map[string]*Entry{}
	for _, e := range v.Entries {
		if e.Kind == "toolResult" && e.CallID != "" {
			out[e.CallID] = e
		}
	}
	return out
}

// Slot returns the live slot of a tool call at work, or nil.
func (v *View) Slot(callID string) *ToolSlot {
	for i := range v.Live.Tools {
		if v.Live.Tools[i].CallID == callID {
			return &v.Live.Tools[i]
		}
	}
	return nil
}

// ApprovalFor returns the approval waiting for a tool call, or nil.
func (v *View) ApprovalFor(callID string) *Approval {
	for i := range v.Approvals {
		if v.Approvals[i].CallID == callID && callID != "" {
			return &v.Approvals[i]
		}
	}
	return nil
}

// Earliest returns the ID in the order before which the view holds no
// entry, and whether any entry before it is missing: what Context loads.
func (v *View) Earliest() (before int, missing bool) {
	for i, id := range v.Order {
		if _, ok := v.Entries[id]; ok {
			return id, i > 0
		}
	}
	return 0, false
}

// AddEarlier puts entries loaded with Context into the view.
func (v *View) AddEarlier(entries []Entry) {
	inOrder := make(map[int]bool, len(v.Order))
	for _, id := range v.Order {
		inOrder[id] = true
	}
	for i := range entries {
		e := entries[i]
		if !inOrder[e.ID] {
			continue
		}
		if _, ok := v.Entries[e.ID]; ok {
			continue
		}
		v.Entries[e.ID] = &e
	}
	v.Rev++
}
