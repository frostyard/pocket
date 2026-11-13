package app

import (
	"strings"
	"time"

	"github.com/frostyard/pocket/internal/pocket"
)

// derived is rebuilt after a source changes, not for spinner/hover redraws.
// Its pointers belong to the same immutable server snapshots as App's sources.
type derived struct {
	key              derivedKey
	expires          time.Time
	states           map[*pocket.Session]state
	waiting, working int
	inbox            []inboxItem
	inboxReady       bool
	threads          map[*pocket.DeskThread]*pocket.Session
	titles           []string
}

type derivedKey struct {
	first                              *pocket.Session
	count, current, loading, rev, live int
	view                               *pocket.View
	desk                               *pocket.DeskView
	memory                             *pocket.Memory
	core                               *pocket.CoreMemory
	hello                              *pocket.Hello
}

func (a *App) invalidateDerived() { a.derived = nil }

func (a *App) derivedData() *derived {
	key := derivedKey{count: len(a.sessions), current: a.current, loading: a.loading, view: a.view, desk: a.desk.view, memory: a.lessons.memory, hello: a.hello}
	if len(a.sessions) > 0 {
		key.first = &a.sessions[0]
	}
	if a.view != nil {
		key.rev, key.live = a.view.Rev, a.view.LiveRev
	}
	if a.core != nil {
		key.core = a.core.data
	}
	now := a.serverNow()
	d := a.derived
	if d != nil && d.key == key && (d.expires.IsZero() || now.Before(d.expires)) {
		return d
	}
	d = &derived{key: key, states: make(map[*pocket.Session]state, len(a.sessions)), threads: make(map[*pocket.DeskThread]*pocket.Session), titles: make([]string, len(a.sessions))}
	a.derived = d
	for i := range a.sessions {
		s := &a.sessions[i]
		st := a.computeState(s)
		d.states[s] = st
		if st == stateWaiting {
			d.waiting++
		}
		if st == stateWorking {
			d.working++
		}
		d.titles[i] = strings.ToLower(s.Title)
		if s.EndedAt != 0 {
			at := s.EndedAt.Time().Add(30 * 24 * time.Hour)
			if at.After(now) && (d.expires.IsZero() || at.Before(d.expires)) {
				d.expires = at
			}
		}
	}
	return d
}

func (a *App) stateOf(s *pocket.Session) state {
	if st, ok := a.derivedData().states[s]; ok {
		return st
	}
	return a.computeState(s)
}

func (a *App) inboxItems() []inboxItem {
	d := a.derivedData()
	if !d.inboxReady {
		d.inbox = a.computeInbox()
		d.inboxReady = true
	}
	return d.inbox
}

func (a *App) sessionForThread(t *pocket.DeskThread) *pocket.Session {
	d := a.derivedData()
	if s, ok := d.threads[t]; ok {
		return s
	}
	s := a.matchThreadSession(t, d.titles)
	d.threads[t] = s
	return s
}
