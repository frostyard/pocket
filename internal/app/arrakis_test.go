package app

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/frostyard/pocket/internal/pocket"
)

// arrakisScene is the desert of the made-up team.
func arrakisScene(t *testing.T) (*App, *sandScene) {
	t.Helper()
	a, _ := teamApp(t)
	return a, a.sandScene()
}

func TestArrakisReadsTheTeam(t *testing.T) {
	_, sc := arrakisScene(t)
	if len(sc.figures) != 4 || !sc.figures[0].supervisor || sc.figures[0].name != "Odrade" {
		t.Fatalf("figures = %+v, want the 4 agents with the supervisor first", sc.figures)
	}
	if len(sc.loose) != 0 {
		t.Errorf("a session already seen stands on the far ridge: %+v", sc.loose)
	}
	odrade, duncan, irulan, sheeana := sc.figure("a:odrade"), sc.figure("a:duncan"), sc.figure("a:irulan"), sc.figure("a:sheeana")
	if odrade.title != "Mother Superior" || odrade.faction != beneGesserit {
		t.Errorf("Odrade is %q of %v", odrade.title, odrade.faction)
	}
	if odrade.working != 1 || odrade.subagents != 2 || odrade.tone != toneWork || !strings.Contains(odrade.caption, "team_delegate") {
		t.Errorf("Odrade: %+v, want 1 run at work with 2 subagents and its last step", odrade)
	}
	// A call to allow comes before a failed run, and leads to its session.
	if duncan.calling != 1 || duncan.failed != 1 || duncan.tone != toneYou || duncan.session != 2 || !strings.Contains(duncan.caption, "git push") {
		t.Errorf("Duncan: %+v, want the call to allow in session 2", duncan)
	}
	// The Desk's asks call too, and lead to the Inbox.
	if irulan.calling != 3 || irulan.fresh != 1 || irulan.inbox != "a:T1439b" || strings.Contains(irulan.caption, "**") {
		t.Errorf("Irulan: %+v, want 3 asks, the first in the Inbox, without Markdown marks", irulan)
	}
	if sheeana.working != 1 || !strings.Contains(sheeana.caption, "go test") {
		t.Errorf("Sheeana: %+v, want her run's last step", sheeana)
	}
	if sc.waits != 5 || sc.working != 2 || sc.failed != 1 || sc.ready != 1 {
		t.Errorf("tallies: waits %d, working %d, failed %d, ready %d", sc.waits, sc.working, sc.failed, sc.ready)
	}
	got := map[string]int{}
	for _, part := range sc.parts {
		got[part.name] = part.n
	}
	want := map[string]int{"With you": 2, "With the supervisor": 1, "With agents": 3, "In review": 1, "Watching": 1}
	for name, n := range want {
		if got[name] != n {
			t.Errorf("the ring's %q holds %d, want %d (%v)", name, got[name], n, got)
		}
	}
}

func TestArrakisWithoutATeamShowsTheSessionsThatMove(t *testing.T) {
	a, _ := teamApp(t)
	a.team = team{}
	a.desk = desk{}
	a.invalidateDerived()
	sc := a.sandScene()
	// Sessions 1 to 5 move; 6 was seen. The one that calls stands first.
	if len(sc.figures) != 5 || len(sc.loose) != 0 || sc.figures[0].key != "s:2" {
		t.Fatalf("figures = %+v", sc.figures)
	}
	if sc.parts[0].name != "Waiting for you" || sc.parts[0].n != 1 {
		t.Errorf("the ring without a Desk: %+v", sc.parts)
	}
}

func TestArrakisFarRidgeHoldsSoMany(t *testing.T) {
	a, _ := teamApp(t)
	list := append([]pocket.Session(nil), a.sessions...)
	for i := range looseMax + 3 {
		list = append(list, pocket.Session{ID: 100 + i, Title: fmt.Sprintf("Loose %d", i), Cwd: "/srv/elsewhere", Busy: true})
	}
	list = append(list, pocket.Session{ID: 200, Title: "Duncan Idaho: CI fixes", Cwd: "/srv/elsewhere", Waiting: true})
	a.setSessions(list)
	sc := a.sandScene()
	if len(sc.loose) != looseMax || sc.more != 4 {
		t.Fatalf("far ridge holds %d and leaves %d out, want %d and 4", len(sc.loose), sc.more, looseMax)
	}
	// The one that calls comes first, and is the person its title names.
	if first := sc.loose[0]; first.key != "s:200" || first.name != "Duncan Idaho" || first.title != "Swordmaster" {
		t.Errorf("first on the far ridge: %+v", first)
	}
}

func TestArrakisBlowsSpiceWhereARunEnds(t *testing.T) {
	a, _ := teamApp(t)
	at := time.Date(2026, 11, 12, 15, 30, 0, 0, time.UTC)
	scene := func(at time.Time) *sandScene {
		sc := a.sandScene()
		a.arrakis.noteRuns(sc, at)
		return sc
	}
	if sc := scene(at); len(sc.blows) != 0 {
		t.Fatalf("spice blows before any run ended: %v", sc.blows)
	}
	a.byID[3].Busy = false
	a.invalidateDerived()
	sc := scene(at.Add(time.Second))
	if _, ok := sc.blows["a:sheeana"]; !ok {
		t.Fatalf("no spice blows where Sheeana's run ended: %v", sc.blows)
	}
	if _, ok := sc.blows["a:odrade"]; ok {
		t.Errorf("spice blows where a run still goes on")
	}
	sc = scene(at.Add(time.Second + 2*sandBlow))
	if len(sc.blows) != 0 {
		t.Errorf("the blow never ends: %v", sc.blows)
	}
}

// TestArrakisFitsItsBox places every figure and the eclipse inside the box,
// with neighbors apart, whatever the box and however many stand in it.
func TestArrakisFitsItsBox(t *testing.T) {
	for _, size := range [][2]float32{{420, 300}, {860, 820}, {1700, 1000}, {600, 1200}} {
		for n := 0; n <= 14; n++ {
			sc := &sandScene{}
			for i := range n {
				sc.figures = append(sc.figures, sandFigure{key: fmt.Sprint("a:", i), supervisor: i == 0})
			}
			for i := range min(n, looseMax) {
				sc.loose = append(sc.loose, sandFigure{key: fmt.Sprint("s:", i)})
			}
			r := ui.Rect{X: 30, Y: 20, W: size[0], H: size[1]}
			lay := layoutSand(sc, r)
			if len(lay.spots) != len(sc.figures)+len(sc.loose) {
				t.Fatalf("%v, %d figures: %d spots", size, n, len(lay.spots))
			}
			inside := func(b ui.Rect) bool {
				return b.X >= r.X && b.Y >= r.Y && b.X+b.W <= r.X+r.W && b.Y+b.H <= r.Y+r.H+0.01
			}
			if !inside(lay.dial) {
				t.Errorf("%v: the eclipse %v leaves the box", size, lay.dial)
			}
			for i, s := range lay.spots {
				if !inside(s.box) {
					t.Errorf("%v, %d figures: spot %d %v leaves the box", size, n, i, s.box)
				}
				if i == 0 && n > 0 && s.box.Y < lay.cy+lay.rad {
					t.Errorf("%v: the first figure reaches into the eclipse", size)
				}
				for j := range i {
					o := lay.spots[j]
					if o.ridge == s.ridge && max(s.x-o.x, o.x-s.x) < (s.h+o.h)*0.3 {
						t.Errorf("%v, %d figures: spots %d and %d stand on each other", size, n, j, i)
					}
				}
			}
		}
	}
}

func TestArrakisSkyFollowsTheHour(t *testing.T) {
	if night, day := paletteAt(1), paletteAt(12); night.night != 1 || day.night != 0 {
		t.Errorf("night shows %v at 01:00 and %v at noon", night.night, day.night)
	}
	// Between two palettes the colors blend, and the day wraps around.
	if got := paletteAt(5.5); got.night <= 0.2 || got.night >= 1 {
		t.Errorf("05:30 shows %v of the night, want between dawn's and the night's", got.night)
	}
	if paletteAt(24) != paletteAt(0) || paletteAt(-1) != paletteAt(23) {
		t.Errorf("the sky does not wrap around midnight")
	}
	// Text on a ridge stands out from it at every hour.
	for hour := 0.0; hour < 24; hour += 0.5 {
		pal := paletteAt(hour)
		for ridge := ridgeMid; ridge <= ridgeNear; ridge++ {
			sand, ink := pal.ridge[ridge][0], pal.ink(ridge)
			lum := func(c ui.Color) float32 { return 0.299*float32(c.R) + 0.587*float32(c.G) + 0.114*float32(c.B) }
			if d := lum(sand) - lum(ink); d < 70 && d > -70 {
				t.Errorf("%.1f h, ridge %d: ink %v on sand %v", hour, ridge, ink, sand)
			}
		}
	}
}

func TestArrakisSnapshot(t *testing.T) {
	a, loop := teamApp(t)
	tt := ui.NewTesterWith(a.View, 1440, 900, ui.TesterOptions{Dark: os.Getenv("POCKET_DARK") != ""})
	settle := func() {
		for range 5 {
			tt.Advance(100 * time.Millisecond)
			loop.Flush()
			tt.Frame()
		}
	}
	loop.Post(func() { a.goPlace("x:arrakis") })
	settle()
	if !tt.HasText("Reading the desert") {
		t.Errorf("the desert lacks its legend: %v", tt.Texts())
	}
	for _, label := range []string{"Arrakis: 5 things wait for you, 2 runs at work, 1 run failed", "Open the Inbox: 5 things wait for you",
		"Odrade: 1 run at work", "Duncan: 1 call for you, 1 run failed", "Irulan: 3 calls for you, 1 run finished", "Sheeana: 1 run at work"} {
		if _, ok := tt.Access(label); !ok {
			t.Errorf("no %q in the desert", label)
		}
	}
	writePNG(t, tt, "arrakis-day")

	// A click chooses a figure, whose sessions the right pane then lists.
	if err := tt.Click("Duncan: 1 call for you, 1 run failed"); err != nil {
		t.Fatal(err)
	}
	settle()
	if a.arrakis.sel != "a:duncan" || !tt.HasText("Swordmaster · House Atreides") || !tt.HasText("Fix the flaky CI in nsl") {
		t.Errorf("chose %q; the right pane shows %v", a.arrakis.sel, tt.Texts())
	}
	writePNG(t, tt, "arrakis-chosen")

	// J and K walk the figures.
	loop.Post(func() { a.moveArrakis(1) })
	settle()
	if a.arrakis.sel != "a:irulan" {
		t.Errorf("after Duncan comes %q", a.arrakis.sel)
	}
	for sky, name := range map[sandSky]string{skyNight: "arrakis-night", skyDusk: "arrakis-dusk"} {
		loop.Post(func() { a.arrakis.sky, a.arrakis.sel = sky, "" })
		settle()
		tt.Advance(4 * time.Second)
		tt.Frame()
		writePNG(t, tt, name)
	}

	// The eclipse leads to the Inbox.
	if err := tt.Click("Open the Inbox: 5 things wait for you"); err != nil {
		t.Fatal(err)
	}
	settle()
	if a.selected != "x:inbox" {
		t.Errorf("the eclipse led to %q", a.selected)
	}
}

func TestArrakisDoubleClickGoesWhereItPresses(t *testing.T) {
	a, loop := teamApp(t)
	a.goPlace("x:arrakis")
	tt := ui.NewTesterWith(a.View, 1440, 900, ui.TesterOptions{})
	frames := func() {
		for range 3 {
			tt.Advance(100 * time.Millisecond)
			loop.Flush()
			tt.Frame()
		}
	}
	frames()
	twice := func(label string) {
		t.Helper()
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("no %q in the desert", label)
		}
		x, y := r.X+r.W/2, r.Y+r.H/2
		tt.Move(x, y)
		tt.Frame()
		if tt.Cursor() != ui.CursorPointer {
			t.Errorf("%q does not take the pointer", label)
		}
		tt.ClickAt(x, y)
		tt.ClickAt(x, y)
		frames()
	}
	// A Desk ask leads to the Inbox at that ask.
	twice("Irulan: 3 calls for you, 1 run finished")
	if a.selected != "x:inbox" || a.inboxSel != "a:T1439b" {
		t.Errorf("Irulan's ask led to %q at %q", a.selected, a.inboxSel)
	}
	// A call to allow leads to its session.
	loop.Post(func() { a.goPlace("x:arrakis") })
	frames()
	twice("Duncan: 1 call for you, 1 run failed")
	if a.current != 2 {
		t.Errorf("Duncan's call led to session %d", a.current)
	}
}

// TestArrakisPaintsInAnyBox draws the moving scene in boxes down to almost
// nothing, with every state on show, without failing.
func TestArrakisPaintsInAnyBox(t *testing.T) {
	a, loop := teamApp(t)
	a.byID[4].Limited = pocket.Millis(a.serverNow().Add(time.Hour).UnixMilli())
	a.invalidateDerived()
	a.goPlace("x:arrakis")
	for _, size := range [][2]int{{1440, 900}, {760, 420}, {700, 260}} {
		tt := ui.NewTesterWith(a.View, size[0], size[1], ui.TesterOptions{})
		for range 12 {
			tt.Advance(400 * time.Millisecond)
			loop.Flush()
			tt.Frame()
		}
		if _, ok := tt.Access("Open the Inbox: 5 things wait for you"); !ok {
			t.Errorf("%v: no eclipse", size)
		}
	}
}
