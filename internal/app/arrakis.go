package app

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// Arrakis is the team as one desert scene: an eclipse that counts what
// waits on the owner, and a figure on the dunes for each named agent, with
// a glowglobe while it works and a thumper while it calls. It draws what
// the window already knows (sessions, peeks, the team and the Desk) and
// asks the server for nothing. arrakis_scene.go lays it out and paints it.

// sandSky is which sky the desert shows.
type sandSky int

const (
	skyClock sandSky = iota // the device's hour
	skyDay
	skyDusk
	skyNight
)

// arrakis is the place's state, on the UI thread.
type arrakis struct {
	// sel is the figure chosen, as its key, or "" for none.
	sel string
	sky sandSky
	// epoch is when the place first drew, on the frame clock: the scene's
	// motion counts from it.
	epoch time.Time
	// atWork are the figures at work when the view last ran, and blows when
	// each one's run last ended, on the frame clock: spice blows there.
	atWork map[string]bool
	blows  map[string]time.Time
	// size is the scene's box in the last frame that drew it.
	size ui.Rect
}

// sandTone is what a figure's caption, or a part of the dial, says.
type sandTone int

const (
	toneQuiet sandTone = iota
	toneWork
	toneYou
	toneSupervisor
	toneReview
	toneWatch
	toneFailed
	toneFresh
)

// sandFigure is someone in the desert: a named agent, or a session of no
// named agent.
type sandFigure struct {
	key        string // agentKey or sessionKey
	name       string
	title      string // who they are: "Mother Superior"
	faction    faction
	supervisor bool
	agent      string // the named agent's ID, or ""

	// How many of its sessions are in each state; calling counts the Desk's
	// asks from it too.
	working, calling, failed, fresh, limited int
	subagents                                int

	// caption says the most pressing thing about it, in tone; last what it
	// did last when nothing presses.
	caption string
	tone    sandTone
	last    string

	// A double click opens session, or the Inbox at the item inbox.
	session int
	inbox   string

	sessions []*pocket.Session // not yet seen, the most pressing first
	asks     []*pocket.DeskAsk
}

// active reports whether anything about the figure moves or presses.
func (f *sandFigure) active() bool {
	return f.working+f.calling+f.failed+f.fresh+f.limited > 0
}

// spoken says the figure and its state, for a screen reader and the tooltip.
func (f *sandFigure) spoken() string {
	var parts []string
	if f.calling > 0 {
		parts = append(parts, plural(f.calling, "call for you", "calls for you"))
	}
	if f.working > 0 {
		parts = append(parts, plural(f.working, "run at work", "runs at work"))
	}
	if f.failed > 0 {
		parts = append(parts, plural(f.failed, "run failed", "runs failed"))
	}
	if f.limited > 0 {
		parts = append(parts, "waiting out a limit")
	}
	if f.fresh > 0 {
		parts = append(parts, plural(f.fresh, "run finished", "runs finished"))
	}
	if len(parts) == 0 {
		parts = append(parts, "at rest")
	}
	return f.name + ": " + strings.Join(parts, ", ")
}

// who says the figure's title and faction.
func (f *sandFigure) who() string {
	return f.title + " · " + factionLooks[f.faction].name
}

// dialPart is an arc of the eclipse's ring: how many open threads (or
// sessions, without a Desk) are with whom.
type dialPart struct {
	name string
	n    int
	tone sandTone
}

// sandScene is what the desert shows, built by the view and read by the
// painter. It does not change once the view that built it ends.
type sandScene struct {
	figures []sandFigure // the named agents, the supervisor first
	loose   []sandFigure // sessions of no named agent, on the far ridge
	more    int          // sessions of no named agent that did not fit
	waits   int          // what the Inbox counts
	parts   []dialPart
	// Tallies for the line at the foot of the scene.
	working, failed, ready int
	hour                   float64 // the hour of the sky, 0 to 24
	clock                  string  // the device's time, "15:04"
	sel, hover             string
	epoch                  time.Time
	blows                  map[string]time.Time
	still                  bool // the desktop asks for less motion
}

// figure finds a figure by its key.
func (sc *sandScene) figure(key string) *sandFigure {
	for i := range sc.figures {
		if sc.figures[i].key == key {
			return &sc.figures[i]
		}
	}
	for i := range sc.loose {
		if sc.loose[i].key == key {
			return &sc.loose[i]
		}
	}
	return nil
}

// each calls fn with every figure, the named agents first.
func (sc *sandScene) each(fn func(i int, f *sandFigure)) {
	for i := range sc.figures {
		fn(i, &sc.figures[i])
	}
	for i := range sc.loose {
		fn(len(sc.figures)+i, &sc.loose[i])
	}
}

// summary says the scene in a line.
func (sc *sandScene) summary() string {
	parts := []string{plural(sc.waits, "thing waits for you", "things wait for you"), plural(sc.working, "run at work", "runs at work")}
	if sc.failed > 0 {
		parts = append(parts, plural(sc.failed, "run failed", "runs failed"))
	}
	return strings.Join(parts, ", ")
}

// looseMax is how many sessions of no named agent the far ridge holds, and
// how many stand in for the team on a server without one.
const looseMax = 7

// sandScene gathers the desert from what the window knows. It changes
// nothing: noteRuns adds the spice blows, which take the frame clock.
func (a *App) sandScene() *sandScene {
	ar := &a.arrakis
	sc := &sandScene{waits: a.inboxCount(), epoch: ar.epoch, sel: ar.sel}
	local := a.opts.Now()
	sc.clock = local.Format("15:04")
	switch ar.sky {
	case skyDay:
		sc.hour = 12
	case skyDusk:
		sc.hour = 19
	case skyNight:
		sc.hour = 0
	default:
		sc.hour = float64(local.Hour()) + float64(local.Minute())/60
	}
	// The supervisor stands first, under the eclipse.
	agents := make([]*pocket.Persona, 0, len(a.team.agents))
	for i := range a.team.agents {
		agents = append(agents, &a.team.agents[i])
	}
	slices.SortStableFunc(agents, func(x, y *pocket.Persona) int {
		return cmp.Compare(boolRank(y.Role == "supervisor"), boolRank(x.Role == "supervisor"))
	})
	asks := a.asksByAgent()
	// Each session once: whose it is, or nobody's.
	owned := map[string][]*pocket.Session{}
	var unowned []*pocket.Session
	for i := range a.sessions {
		s := &a.sessions[i]
		if s.Archived {
			continue
		}
		if p := a.agentOf(s); p != nil {
			owned[p.ID] = append(owned[p.ID], s)
		} else {
			unowned = append(unowned, s)
		}
		switch a.stateOf(s) {
		case stateWorking:
			sc.working++
		case stateFailed:
			sc.failed++
		}
	}
	for _, p := range agents {
		f := sandFigure{key: agentKey(p.ID), name: p.Name, agent: p.ID, supervisor: p.Role == "supervisor", title: roleTitle(p.Role)}
		if who, ok := castOf(p.Name, p.ID); ok {
			f.title, f.faction = who.title, who.faction
		}
		var latest *pocket.Session
		for _, s := range owned[p.ID] {
			a.figureTakes(&f, s)
			if latest == nil || s.UpdatedAt > latest.UpdatedAt {
				latest = s
			}
		}
		f.asks = asks[p.ID]
		f.calling += len(f.asks)
		a.settleFigure(&f, latest)
		sc.figures = append(sc.figures, f)
	}
	// Sessions of no named agent show while something about them moves.
	var loose []sandFigure
	for _, s := range unowned {
		if a.stateOf(s) == stateSeen {
			continue
		}
		f := sandFigure{key: sessionKey(s.ID), name: s.DisplayTitle(), title: "Of the deep desert"}
		if before, _, named := strings.Cut(s.Title, ":"); named {
			if who, ok := castOf(before); ok {
				f.name, f.title, f.faction = strings.TrimSpace(before), who.title, who.faction
			}
		}
		a.figureTakes(&f, s)
		a.settleFigure(&f, s)
		loose = append(loose, f)
	}
	slices.SortStableFunc(loose, func(x, y sandFigure) int { return cmp.Compare(figureRank(&x), figureRank(&y)) })
	if len(loose) > looseMax {
		sc.more = len(loose) - looseMax
		loose = loose[:looseMax]
	}
	if len(sc.figures) == 0 {
		// No team: the sessions that move are the desert's people.
		sc.figures = loose
	} else {
		sc.loose = loose
	}
	sc.parts = a.dialParts()
	if v := a.desk.view; v != nil {
		sc.ready = len(v.Ready)
	}
	if sc.figure(sc.sel) == nil {
		sc.sel = ""
	}
	return sc
}

// noteRuns keeps which figures are at work, and gives the scene the spice
// blows: where a run ended since the view last ran, for a while from now,
// the frame clock.
func (ar *arrakis) noteRuns(sc *sandScene, now time.Time) {
	atWork := map[string]bool{}
	sc.each(func(_ int, f *sandFigure) {
		if f.working > 0 {
			atWork[f.key] = true
		} else if ar.atWork[f.key] {
			if ar.blows == nil {
				ar.blows = map[string]time.Time{}
			}
			ar.blows[f.key] = now
		}
	})
	ar.atWork = atWork
	sc.blows = make(map[string]time.Time, len(ar.blows))
	for key, at := range ar.blows {
		if now.Sub(at) > sandBlow {
			delete(ar.blows, key)
			continue
		}
		sc.blows[key] = at
	}
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// figureRank orders figures, the most pressing first.
func figureRank(f *sandFigure) int {
	switch {
	case f.calling > 0:
		return 0
	case f.working > 0:
		return 1
	case f.failed > 0:
		return 2
	case f.limited > 0:
		return 3
	case f.fresh > 0:
		return 4
	}
	return 5
}

// asksByAgent groups the Desk's asks by the agent that asks: the one the
// ask names, or the thread's first.
func (a *App) asksByAgent() map[string][]*pocket.DeskAsk {
	out := map[string][]*pocket.DeskAsk{}
	v := a.desk.view
	if v == nil {
		return out
	}
	for i := range v.Threads {
		t := &v.Threads[i]
		for j := range t.Asks {
			from := t.Asks[j].From
			if from == "" && len(t.Agents) > 0 {
				from = t.Agents[0]
			}
			if from != "" {
				out[from] = append(out[from], &t.Asks[j])
			}
		}
	}
	return out
}

// figureTakes counts session s for figure f.
func (a *App) figureTakes(f *sandFigure, s *pocket.Session) {
	st := a.stateOf(s)
	switch st {
	case stateWaiting:
		f.calling++
	case stateWorking:
		f.working++
		if s.Subagents != nil {
			f.subagents += s.Subagents.Working
		}
	case stateLimited:
		f.limited++
	case stateFailed:
		f.failed++
	case stateFresh:
		f.fresh++
	}
	if st != stateSeen {
		f.sessions = append(f.sessions, s)
	}
}

// sessionRank orders a figure's sessions, the most pressing first.
func (a *App) sessionRank(s *pocket.Session) int {
	switch a.stateOf(s) {
	case stateWaiting:
		return 0
	case stateWorking:
		return 1
	case stateFailed:
		return 2
	case stateLimited:
		return 3
	case stateFresh:
		return 4
	}
	return 5
}

// settleFigure writes f's caption and where a double click leads, from the
// most pressing thing about it; latest is its newest session, if any.
func (a *App) settleFigure(f *sandFigure, latest *pocket.Session) {
	slices.SortStableFunc(f.sessions, func(x, y *pocket.Session) int {
		if d := a.sessionRank(x) - a.sessionRank(y); d != 0 {
			return d
		}
		return cmp.Compare(y.UpdatedAt, x.UpdatedAt)
	})
	var first *pocket.Session
	if len(f.sessions) > 0 {
		first = f.sessions[0]
		f.session = first.ID
	} else if latest != nil {
		f.session = latest.ID
	}
	count := func(n int) string {
		if n > 1 {
			return fmt.Sprintf("%d · ", n)
		}
		return ""
	}
	switch {
	case first != nil && a.stateOf(first) == stateWaiting:
		f.tone = toneYou
		f.caption = "asks you"
		if ap := first.Approval; ap != nil {
			f.caption = ap.Tool + " · " + ap.Subject
			if ap.Form {
				f.caption = "asks: " + ap.Subject
			}
		}
		f.caption = count(f.calling) + firstLine(f.caption)
	case len(f.asks) > 0:
		f.tone = toneYou
		f.caption = count(f.calling) + "asks: " + plainLine(f.asks[0].Text)
		f.inbox = "a:" + f.asks[0].ID
	case f.working > 0:
		f.tone = toneWork
		f.caption = count(f.working) + firstNonEmpty(a.lastStep(first.ID), first.DisplayTitle())
	case f.failed > 0:
		f.tone = toneFailed
		f.caption = firstNonEmpty(firstLine(first.Failure), "failed · "+first.DisplayTitle())
	case f.limited > 0:
		f.tone = toneWatch
		f.caption = "waits out a limit"
	case f.fresh > 0:
		f.tone = toneFresh
		f.caption = count(f.fresh) + "finished · " + first.DisplayTitle()
	}
	if latest != nil {
		f.last = latest.DisplayTitle()
		if at := ago(a.serverNow(), latest.UpdatedAt.Time()); at != "" {
			f.last += " · " + at
		}
	}
}

// plainLine is the first line of Markdown text without its emphasis marks.
func plainLine(text string) string {
	return strings.NewReplacer("**", "", "__", "", "`", "").Replace(firstLine(text))
}

// dialParts are the arcs of the eclipse's ring: every open thread of the
// Desk by who moves it next, or, without a Desk, the sessions that move.
func (a *App) dialParts() []dialPart {
	if v := a.desk.view; v != nil {
		var you, supervisor, agents, review, watching int
		for i := range v.Threads {
			t := &v.Threads[i]
			switch t.Group() {
			case "need", "found", "stale":
				you++
			case "review":
				review++
			case "watching":
				watching++
			case "plans":
				agents++
			case "working":
				if t.Next == "odrade" {
					supervisor++
				} else {
					agents++
				}
			}
		}
		return []dialPart{{"With you", you, toneYou}, {"With the supervisor", supervisor, toneSupervisor},
			{"With agents", agents, toneWork}, {"In review", review, toneReview}, {"Watching", watching, toneWatch}}
	}
	var you, work, failed, fresh int
	for i := range a.sessions {
		s := &a.sessions[i]
		if s.Archived {
			continue
		}
		switch a.stateOf(s) {
		case stateWaiting:
			you++
		case stateWorking:
			work++
		case stateFailed:
			failed++
		case stateFresh:
			fresh++
		}
	}
	return []dialPart{{"Waiting for you", you, toneYou}, {"At work", work, toneWork},
		{"Finished", fresh, toneFresh}, {"Failed", failed, toneFailed}}
}

// arrakisPane is the Arrakis place: one drawn scene, with a button over
// each figure and over the eclipse.
func (a *App) arrakisPane(c *ui.Context) {
	ar := &a.arrakis
	if ar.epoch.IsZero() {
		ar.epoch = c.Now()
	}
	sc := a.sandScene()
	sc.still = c.Preferences().ReduceMotion
	ar.sel = sc.sel // a figure that left takes the choice with it
	ar.noteRuns(sc, c.Now())
	box := ui.Box(c).Grow(1).Clip().Label("Arrakis: " + sc.summary())
	if b := box.Bounds(); b.W > 0 && b.H > 0 {
		ar.size = ui.Rect{W: b.W, H: b.H}
	} else {
		// The first frame has no size yet: the buttons come with the next.
		c.AnimationFrame()
	}
	box.Draw(func(p *ui.Painter, r ui.Rect) { paintSand(p, r, sc) })
	box.Children(func() {
		if ar.size.W <= 0 {
			return
		}
		lay := layoutSand(sc, ar.size)
		dial := ui.ButtonBase(c.Key("dial")).Absolute().Left(lay.dial.X).Top(lay.dial.Y).Size(lay.dial.W, lay.dial.H).
			Radius(lay.dial.W / 2).Cursor(ui.CursorPointer).
			Label("Open the Inbox: " + plural(sc.waits, "thing waits for you", "things wait for you")).Tooltip(dialTip(sc))
		if dial.Hovered() {
			sc.hover = "dial"
		}
		if dial.Clicked() {
			a.goPlace("x:inbox")
		}
		sc.each(func(i int, f *sandFigure) {
			spot := lay.spots[i]
			btn := ui.ButtonBase(c.Key(f.key)).Absolute().Left(spot.box.X).Top(spot.box.Y).Size(spot.box.W, spot.box.H).
				Radius(8).Cursor(ui.CursorPointer).Label(f.spoken())
			if btn.Hovered() {
				sc.hover = f.key
			}
			switch {
			case btn.DoubleClicked():
				ar.sel = f.key
				a.openFigure(f)
			case btn.Clicked():
				if ar.sel == f.key {
					ar.sel = ""
				} else {
					ar.sel = f.key
				}
			}
		})
		sc.sel = ar.sel
	})
}

// dialTip says what the eclipse's ring counts.
func dialTip(sc *sandScene) string {
	var parts []string
	for _, part := range sc.parts {
		if part.n > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d", part.name, part.n))
		}
	}
	if len(parts) == 0 {
		return "Nothing is open. Click for the Inbox."
	}
	return strings.Join(parts, " · ") + ". Click for the Inbox."
}

// openFigure leads where a figure's most pressing thing is: the Inbox at
// its ask, its session, or its agent's page.
func (a *App) openFigure(f *sandFigure) {
	switch {
	case f.inbox != "":
		a.goPlace("x:inbox")
		a.inboxSel = f.inbox
	case f.session != 0:
		a.open(f.session)
	case f.agent != "":
		a.openAgent(f.agent)
	}
}

// moveArrakis chooses the next figure (or the previous one) of the desert.
func (a *App) moveArrakis(by int) {
	sc := a.sandScene()
	var keys []string
	sc.each(func(_ int, f *sandFigure) { keys = append(keys, f.key) })
	if len(keys) == 0 {
		return
	}
	i := slices.Index(keys, a.arrakis.sel)
	switch {
	case i < 0 && by < 0:
		i = len(keys) - 1
	case i < 0:
		i = 0
	default:
		i = (i + by + len(keys)) % len(keys)
	}
	a.arrakis.sel = keys[i]
}

// arrakisBar holds the place's own control: which sky it shows.
func (a *App) arrakisBar(c *ui.Context) {
	ui.Spacer(c)
	names := []string{"Sky follows the clock", "Day", "Dusk", "Night"}
	ui.MenuButton(c, names[a.arrakis.sky], func(m *ui.Menu) {
		for i, name := range names {
			if m.Item(name).Checked(a.arrakis.sky == sandSky(i)).Chosen() {
				a.arrakis.sky = sandSky(i)
			}
		}
	}).Label("Sky").Tooltip("Choose the desert's sky")
}

// arrakisContext is the right pane beside the desert: the chosen figure,
// or how to read the scene.
func (a *App) arrakisContext(c *ui.Context) {
	sc := a.sandScene()
	f := sc.figure(a.arrakis.sel)
	if f == nil {
		a.arrakisLegend(c)
		return
	}
	ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
		if f.agent != "" {
			a.agentAvatar(c, f.agent, 28)
		}
		ui.Column(c).Grow(1).Children(func() {
			frostui.Strong(c, f.name).Wrap()
			frostui.Caption(c, f.who()).Wrap()
		})
	})
	switch {
	case f.caption != "":
		frostui.Body(c, f.caption).Wrap()
	case f.last != "":
		frostui.Muted(c, "At rest. Last: "+f.last).Wrap()
	default:
		frostui.Muted(c, "At rest.").Wrap()
	}
	if len(f.asks) > 0 {
		frostui.SectionHeader(c, frostui.SectionHead{Title: "Asks you", Quiet: true})
		for _, ask := range f.asks {
			row := ui.Column(c.Key("ask:" + ask.ID)).Gap(frostui.SpaceXS).AlignItems(ui.Start)
			row.Children(func() {
				frostui.Body(c, truncate(plainLine(ask.Text), 220)).Wrap()
				if ui.Button(c, "Answer in the Inbox").Clicked() {
					a.goPlace("x:inbox")
					a.inboxSel = "a:" + ask.ID
				}
			})
		}
	}
	if len(f.sessions) > 0 {
		frostui.SectionHeader(c, frostui.SectionHead{Title: "Sessions", Quiet: true})
		for i, s := range f.sessions {
			if i == 6 {
				frostui.Caption(c, fmt.Sprintf("and %d more", len(f.sessions)-i))
				break
			}
			a.sessionCard(c.Key(s.ID), s)
		}
	}
	if f.agent != "" {
		if ui.Button(c, "Open "+f.name+"'s page").Clicked() {
			a.openAgent(f.agent)
		}
	}
}

// arrakisLegend says how to read the desert, and its keys.
func (a *App) arrakisLegend(c *ui.Context) {
	frostui.SectionHeader(c, frostui.SectionHead{Title: "Reading the desert", Quiet: true})
	for _, row := range [][2]string{
		{"The eclipse", "What waits on you. Its ring is every open thread, by who moves it next. Click it for the Inbox."},
		{"A figure", "A named agent, the supervisor under the eclipse. Click for its sessions here; double-click to go where it presses most."},
		{"A thumper", "It calls for you: a call to allow, a question, or a decision."},
		{"A glowglobe", "A run at work. The small lights around it are its subagents."},
		{"Embers", "A run failed."},
		{"Water", "A run finished that you have not read."},
		{"Wormsign", "Work is under way somewhere; a run that ends blows spice."},
		{"The far ridge", "Sessions of no named agent, while something about them moves."},
	} {
		ui.Column(c.Key(row[0])).Gap(2).AlignItems(ui.Start).Children(func() {
			frostui.Strong(c, row[0])
			frostui.Caption(c, row[1]).Wrap()
		})
	}
	frostui.SectionHeader(c, frostui.SectionHead{Title: "Keys", Quiet: true})
	for _, k := range [][2]string{
		{"Cmd 9", "Arrakis"},
		{"J / K", "Next / previous figure"},
		{"Enter", "Choose the figure with the focus"},
	} {
		ui.Column(c.Key(k[0])).Gap(2).AlignItems(ui.Start).Children(func() {
			frostui.Shortcut(c, strings.Fields(keys(k[0]))...)
			frostui.Caption(c, k[1]).Wrap()
		})
	}
}
