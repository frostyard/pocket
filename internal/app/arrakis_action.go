package app

import (
	"math"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
)

// What happens in the desert: a run at work is a harvester on the sand
// beside its figure, and what changes between two views of the scene is an
// event the painter plays for a while. A carryall sets the harvester down
// when a run begins and lifts it when the run ends; the worm takes it when
// the run fails; an ornithopter carries a task to an agent and a call to
// the owner. All of it is discs and rectangles under transforms, which
// need no mask (arrakis_scene.go).

// sandEventKind is what happened.
type sandEventKind int

const (
	evSetDown sandEventKind = iota // a run began: a carryall sets a harvester down
	evLift                         // a run ended: a carryall lifts it, and spice blows
	evTaken                        // a run failed: the worm takes the harvester
	evPuff                         // a run took a step: its harvester strikes spice
	evMessage                      // an ornithopter flies from one to another
	evVoice                        // more waits on the owner: the eclipse rings
)

// lasts is how long the painter plays an event.
func (k sandEventKind) lasts() time.Duration {
	switch k {
	case evSetDown:
		return 3400 * time.Millisecond
	case evLift:
		return 4200 * time.Millisecond
	case evTaken:
		return 3600 * time.Millisecond
	case evPuff:
		return 1400 * time.Millisecond
	case evMessage:
		return 3800 * time.Millisecond
	}
	return 2600 * time.Millisecond
}

// sandDial names the eclipse as the end of an ornithopter's flight.
const sandDial = "dial"

// sandEventsMax is how many events the scene plays at once.
const sandEventsMax = 32

// sandEvent is something that happened at a figure, on the frame clock.
// A message flies from from to key; each is a figure's key or sandDial.
type sandEvent struct {
	kind sandEventKind
	key  string
	from string
	at   time.Time
}

// figureSeen is a figure as a view saw it.
type figureSeen struct {
	working, calling, failed int
	step                     string
}

// note compares the scene with the one the view saw last, makes events of
// what changed, and gives the scene the events still playing at now, the
// frame clock. After a view that did not show the place (away), nothing
// counts as changed: the desert does not replay what it missed.
func (ar *arrakis) note(sc *sandScene, now time.Time, away bool) {
	first := ar.seen == nil || away
	seen := map[string]figureSeen{}
	supervisor := ""
	if len(sc.figures) > 0 && sc.figures[0].supervisor {
		supervisor = sc.figures[0].key
	}
	add := func(kind sandEventKind, key, from string) {
		ar.events = append(ar.events, sandEvent{kind: kind, key: key, from: from, at: now})
	}
	sc.each(func(_ int, f *sandFigure) {
		cur := figureSeen{f.working, f.calling, f.failed, f.step}
		seen[f.key] = cur
		if first {
			return
		}
		prev, known := ar.seen[f.key]
		switch {
		case cur.working > 0 && prev.working == 0:
			add(evSetDown, f.key, "")
			// The task comes from the supervisor, or from the owner.
			from := sandDial
			if f.linked && supervisor != "" && supervisor != f.key {
				from = supervisor
			}
			add(evMessage, f.key, from)
		case cur.working == 0 && prev.working > 0:
			if cur.failed > prev.failed {
				add(evTaken, f.key, "")
			} else {
				add(evLift, f.key, "")
			}
		case cur.working > 0 && cur.step != prev.step && cur.step != "":
			add(evPuff, f.key, "")
		}
		if known && cur.calling > prev.calling {
			add(evMessage, sandDial, f.key)
		}
	})
	if !first && sc.waits > ar.waits {
		add(evVoice, sandDial, "")
	}
	ar.seen, ar.waits = seen, sc.waits
	ar.events = slices.DeleteFunc(ar.events, func(ev sandEvent) bool { return now.Sub(ev.at) >= ev.kind.lasts() })
	if over := len(ar.events) - sandEventsMax; over > 0 {
		ar.events = slices.Delete(ar.events, 0, over)
	}
	sc.events = slices.Clone(ar.events)
}

// event finds the newest event of a kind at a figure, and its age.
func (s *sandPainter) event(kind sandEventKind, key string) (age float64, ok bool) {
	for i := len(s.sc.events) - 1; i >= 0; i-- {
		ev := &s.sc.events[i]
		if ev.kind != kind || ev.key != key {
			continue
		}
		age = s.now.Sub(ev.at).Seconds()
		return age, age >= 0 && age < kind.lasts().Seconds()
	}
	return 0, false
}

func ease(k float64) float64 { k = math.Max(0, math.Min(1, k)); return k * k * (3 - 2*k) }

// machineColors are a machine's hull and its dark parts, at the hour.
func (s *sandPainter) machineColors() (hull, dark ui.Color) {
	return s.pal.figure.Mix(s.pal.crest, 0.3), s.pal.figure
}

// harvester draws a spice harvester with its treads on (x, y), sized for a
// figure h tall: a hull on treads, a lit cab, and a stack that throws a
// plume of sand and spice down the wind while it works. burst, from 0 to
// 1, is spice just struck.
func (s *sandPainter) harvester(x, y, h float32, seed int, working bool, burst float32) {
	p := s.p
	hull, dark := s.machineColors()
	p.Fill(ui.Rect{X: x - h*0.26, Y: y - h*0.065, W: h * 0.52, H: h * 0.075}, dark, h*0.037)
	p.Fill(ui.Rect{X: x - h*0.24, Y: y - h*0.17, W: h * 0.46, H: h * 0.12}, hull, h*0.03)
	p.Fill(ui.Rect{X: x - h*0.24, Y: y - h*0.17, W: h * 0.46, H: 1}, s.pal.crest.Alpha(0.55), 0)
	p.Fill(ui.Rect{X: x + h*0.09, Y: y - h*0.245, W: h * 0.13, H: h * 0.085}, hull, h*0.02)
	lamp := ui.RGB(0xff, 0xcf, 0x7a)
	p.Fill(ui.Rect{X: x + h*0.125, Y: y - h*0.225, W: h * 0.075, H: h * 0.035}, lamp.Alpha(0.75+0.25*burst), 1)
	p.Fill(ui.Rect{X: x + h*0.2, Y: y - h*0.105, W: h * 0.12, H: h * 0.035}, dark, 1)
	p.Fill(ui.Rect{X: x - h*0.2, Y: y - h*0.28, W: h * 0.05, H: h * 0.12}, hull, 1)
	// The wheels in its treads turn.
	for wheel := range 4 {
		wx := x - h*0.19 + h*0.127*float32(wheel)
		s.disc(wx, y-h*0.027, h*0.022, s.pal.crest.Alpha(0.35))
	}
	if s.pal.night > 0.3 {
		s.glow(x+h*0.34, y-h*0.1, h*0.28, lamp.Alpha(0.28*s.pal.night))
	}
	if !working || s.sc.still {
		return
	}
	stackX, stackY := x-h*0.175, y-h*0.28
	spice, sand := toneColor(toneWork), s.pal.crest
	for mote := range 11 {
		up := frac(s.t*0.55 + float64(sandHash(seed*53+mote)))
		px := stackX + h*0.5*float32(up) + h*0.03*float32(math.Sin(s.t*2+float64(mote)))
		py := stackY - h*0.42*float32(math.Sqrt(up)) + h*0.1*float32(up*up)
		c := sand
		if mote%3 == 0 {
			c = spice
		}
		s.disc(px, py, 1.1+h*0.03*float32(up), c.Alpha(0.6*float32(1-up)))
	}
	// Spice glints where the intake bites.
	for glint := range 3 {
		on := math.Sin(s.t*7 + float64(seed+glint)*2.3)
		if on > 0.2 {
			s.disc(x+h*(0.31+0.03*float32(glint)), y-h*0.07-h*0.03*sandHash(seed+glint*7), 1.3, spice.Alpha(float32(on)))
		}
	}
	if burst > 0 {
		s.glow(stackX, stackY, h*0.4*burst+h*0.15, spice.Alpha(0.55*burst))
		for grain := range 14 {
			age := float64(1 - burst)
			lean := (float64(sandHash(grain*5+seed)) - 0.3) * 1.4
			speed := float64(h) * (0.5 + 0.7*float64(sandHash(grain*7+seed)))
			s.disc(stackX+float32(math.Sin(lean)*speed*age), stackY-float32(math.Cos(lean)*speed*age)+float32(float64(h)*0.5*age*age),
				1.3+1.3*sandHash(grain*11), spice.Alpha(burst))
		}
	}
}

// carryall draws the lifter at (x, y): a long flat hull on four thrusters,
// with cables down to what it carries, drop below it (0 for none).
func (s *sandPainter) carryall(x, y, h, drop float32) {
	p := s.p
	hull, dark := s.machineColors()
	if drop > 0 {
		for _, side := range []float32{-0.15, 0.15} {
			p.Fill(ui.Rect{X: x + h*side - 0.5, Y: y, W: 1, H: drop}, dark.Alpha(0.8), 0)
		}
	}
	for _, pod := range []float32{-0.36, -0.2, 0.2, 0.36} {
		flicker := 0.6 + 0.4*math.Sin(s.t*23+float64(pod)*9)
		s.glow(x+h*pod, y+h*0.06, h*0.13, ui.RGB(0x9f, 0xd4, 0xff).Alpha(float32(0.55*flicker)))
		s.disc(x+h*pod, y+h*0.01, h*0.048, dark)
	}
	p.Fill(ui.Rect{X: x - h*0.4, Y: y - h*0.04, W: h * 0.8, H: h * 0.075}, hull, h*0.037)
	p.Fill(ui.Rect{X: x - h*0.17, Y: y - h*0.085, W: h * 0.34, H: h * 0.06}, hull, h*0.02)
	p.Fill(ui.Rect{X: x - h*0.4, Y: y - h*0.04, W: h * 0.8, H: 1}, s.pal.crest.Alpha(0.6), 0)
	if math.Sin(s.t*6) > 0 {
		s.disc(x-h*0.4, y, 1.4, ui.RGB(0xff, 0x5a, 0x4a))
		s.disc(x+h*0.4, y, 1.4, ui.RGB(0xff, 0x5a, 0x4a))
	}
}

// machines draws what a figure's runs do on the sand beside it: the
// harvester while a run works, the carryall that brings and takes it, and
// the worm that takes it when the run fails.
func (s *sandPainter) machines(i int, f *sandFigure, spot sandSpot) {
	h := spot.h
	x := spot.x + h*0.8
	if x+h*0.34 > s.r.X+s.r.W {
		x = spot.x - h*0.8 // no room at the scene's edge: on the other side
	}
	if !s.sc.still {
		x += h * 0.05 * float32(math.Sin(s.t*0.5+float64(i)))
	}
	y := s.lay.crestY(spot.ridge, x) + h*0.02
	r := s.r
	// The carryall comes down from the scene's nearer top corner, and
	// leaves for the eclipse.
	inX := r.X - h
	if spot.x > s.lay.cx {
		inX = r.X + r.W + h
	}
	hover := y - h*0.72
	hang := func(cx, cy float32, k float64) { // the harvester under the carryall, k of the way down
		drop := h * 0.72 * float32(0.72+0.28*k)
		s.harvester(cx, cy+drop, h, i, false, 0)
		s.carryall(cx, cy, h, drop-h*0.26)
	}
	if age, ok := s.event(evSetDown, f.key); ok && !s.sc.still {
		switch {
		case age < 2:
			k := float32(ease(age / 2))
			hang(inX+(x-inX)*k, r.Y-h+(hover-(r.Y-h))*k, 0)
		case age < 2.5:
			hang(x, hover, (age-2)/0.5)
		default:
			s.harvester(x, y, h, i, f.working > 0, 0)
			k := float32(ease((age - 2.5) / 0.9))
			s.carryall(x+(s.lay.cx-x)*k*0.5, hover-(hover-(r.Y-h))*k, h, 0)
		}
		return
	}
	if age, ok := s.event(evLift, f.key); ok && !s.sc.still {
		switch {
		case age < 1.2:
			s.harvester(x, y, h, i, false, 0)
			k := float32(ease(age / 1.2))
			s.carryall(inX+(x-inX)*k, r.Y-h+(hover-(r.Y-h))*k, h, 0)
		case age < 1.7:
			hang(x, hover, 1)
		default:
			k := float32(ease((age - 1.7) / 2.5))
			hang(x+(s.lay.cx-x)*k, hover-(hover-(r.Y-h*1.2))*k*k, float64(1-k))
			s.blow(x, y, h, i, age-1.7)
		}
		return
	}
	if age, ok := s.event(evTaken, f.key); ok && !s.sc.still {
		s.taken(x, y, h, i, age)
		return
	}
	if f.working > 0 {
		burst := float32(0)
		if age, ok := s.event(evPuff, f.key); ok {
			burst = float32(1 - age/evPuff.lasts().Seconds())
		}
		s.harvester(x, y, h, i, true, burst)
	}
}

// blow is spice thrown up from (x, y), age seconds ago.
func (s *sandPainter) blow(x, y, h float32, seed int, age float64) {
	const lasts = 2.4
	if age < 0 || age >= lasts {
		return
	}
	fade := float32(1 - age/lasts)
	spice := toneColor(toneWork)
	s.glow(x, y-h*0.2, h*(0.5+0.5*float32(age)), spice.Alpha(0.5*fade*fade))
	for grain := range 26 {
		lean := (float64(sandHash(grain*5+seed)) - 0.5) * 1.3
		speed := float64(h) * (1.3 + 1.5*float64(sandHash(grain*7+seed)))
		gx := x + float32(math.Sin(lean)*speed*age*0.7)
		gy := y - float32(math.Cos(lean)*speed*age) + float32(0.5*float64(h)*1.1*age*age)
		s.disc(gx, gy, 1.4+1.4*sandHash(grain*11), spice.Alpha(fade))
	}
}

// taken plays the worm taking a harvester at (x, y): the sand shakes, the
// maw comes up under the machine and closes over it, and goes down again.
func (s *sandPainter) taken(x, y, h float32, seed int, age float64) {
	p := s.p
	lasts := evTaken.lasts().Seconds()
	rise := float32(math.Pow(math.Sin(math.Pi*math.Min(age/lasts, 1)), 0.7))
	rad := h * 0.34
	top := y - rise*h*1.05
	body, lit := s.pal.ridge[ridgeNear][1].Mix(s.pal.ridge[ridgeMid][1], 0.4), s.pal.crest
	// The machine shakes, tips, and rides the maw up until it closes.
	if age < 1.7 {
		shake := float32(math.Sin(age*40)) * 3 * float32(math.Min(age/0.6, 1))
		p.Transform(ui.Rotation(shake+float32(age)*14), x, min(top+rad*0.3, y), func() {
			s.harvester(x, min(top+rad*0.3, y), h, seed, false, 0)
		})
	}
	// Its column stands in the sand: what is under the crest does not show.
	p.Clip(ui.Rect{X: x - rad*3, Y: s.r.Y, W: rad * 6, H: y + 2 - s.r.Y}, 0, func() {
		for ring := 8; ring >= 0; ring-- {
			ry := top + rad*0.5 + float32(ring)*rad*0.42
			if ry > y+rad {
				continue
			}
			sway := rad * 0.12 * float32(math.Sin(age*3+float64(ring)*0.6))
			s.disc(x+sway, ry, rad, body)
			s.circle(x+sway, ry, rad, 1.2, lit.Alpha(0.4))
		}
	})
	if rise > 0.05 {
		// The maw, seen from a little above: teeth around the dark.
		open := float32(math.Max(0.25, 1-math.Max(0, age-1.5)/0.8))
		p.Transform(ui.Scaling(1, 0.42), x, top+rad*0.45, func() {
			s.disc(x, top+rad*0.45, rad*1.02, body)
			s.circle(x, top+rad*0.45, rad*1.02, 1.6, lit.Alpha(0.75))
			s.disc(x, top+rad*0.45, rad*0.8*open, ui.RGB(0x08, 0x04, 0x06))
			for tooth := range 14 {
				a := float64(tooth) / 14 * 2 * math.Pi
				s.disc(x+rad*0.84*open*float32(math.Cos(a)), top+rad*0.45+rad*0.84*open*float32(math.Sin(a)), rad*0.06, lit.Alpha(0.9))
			}
			s.glow(x, top+rad*0.45, rad*0.7*open, toneColor(toneFailed).Alpha(0.5))
		})
	}
	// Sand flies where it breaks the crest.
	for grain := range 22 {
		up := frac(age*1.1 + float64(sandHash(grain*13+seed)))
		lean := (sandHash(grain*17+seed) - 0.5) * 2
		c := lit
		if grain%4 == 0 {
			c = toneColor(toneFailed)
		}
		s.disc(x+rad*1.6*lean*float32(0.4+up), y-h*0.7*rise*float32(math.Sin(math.Pi*up))*sandHash(grain*19), 1.2+1.4*sandHash(grain*23), c.Alpha(0.75*rise*float32(1-up)))
	}
}

// thopter draws an ornithopter at (x, y), heading (dx, dy), sized for a
// figure h tall: a pod with a tail boom, and wings that beat.
func (s *sandPainter) thopter(x, y, dx, dy, h float32, seed float64) {
	p := s.p
	hull, dark := s.machineColors()
	l := h * 0.34
	deg := float32(math.Atan2(float64(dy), float64(dx)) * 180 / math.Pi)
	flip := float32(1)
	if dx < 0 {
		// Mirrored, so that it flies left the right way up.
		flip, deg = -1, deg-180
	}
	p.Transform(ui.Scaling(flip, 1).Then(ui.Rotation(deg)), x, y, func() {
		p.Fill(ui.Rect{X: x - l*0.66, Y: y - l*0.02, W: l * 0.5, H: l * 0.045}, hull, 1)
		p.Fill(ui.Rect{X: x - l*0.68, Y: y - l*0.1, W: l * 0.05, H: l * 0.13}, hull, 1)
		beat := math.Sin(s.t*42 + seed)
		for pair, at := range []float32{-0.04, 0.12} {
			// Each pair beats a little after the one behind it.
			swing := float32(40 * math.Sin(s.t*42+seed+float64(pair)*0.7))
			for _, up := range []float32{-1, 1} {
				p.Transform(ui.Rotation(up*(105-swing)), x+l*at, y, func() {
					p.Fill(ui.Rect{X: x + l*at, Y: y - l*0.02, W: l * 0.7, H: l * 0.04}, dark.Alpha(0.8), 1)
				})
			}
		}
		p.Fill(ui.Rect{X: x - l*0.22, Y: y - l*0.08, W: l * 0.46, H: l * 0.16}, hull, l*0.08)
		s.disc(x+l*0.19, y-l*0.01, l*0.07, ui.RGB(0xff, 0xd9, 0x9a).Alpha(0.85))
		if beat > 0 {
			s.disc(x-l*0.66, y, 1.3, ui.RGB(0xff, 0x6a, 0x4a))
		}
	})
}

// anchor is where an ornithopter leaves from or arrives: above a figure's
// head, or under the eclipse.
func (s *sandPainter) anchor(key string) (x, y float32, ok bool) {
	if key == sandDial {
		return s.lay.cx, s.lay.cy + s.lay.rad*1.45, true
	}
	found := false
	s.sc.each(func(i int, f *sandFigure) {
		if f.key == key {
			spot := s.lay.spots[i]
			x, y, found = spot.x, spot.y-spot.h*1.2, true
		}
	})
	return x, y, found
}

// flights draws the ornithopters under way: one for each message, along
// an arc from its sender, with a wake of dust; and, with none, a patrol
// that crosses the sky now and then.
func (s *sandPainter) flights() {
	if s.sc.still {
		return
	}
	h := s.lay.unit
	flying := false
	for n := range s.sc.events {
		ev := &s.sc.events[n]
		age := s.now.Sub(ev.at).Seconds()
		if ev.kind != evMessage || age < 0 || age >= evMessage.lasts().Seconds() {
			continue
		}
		ax, ay, okA := s.anchor(ev.from)
		bx, by, okB := s.anchor(ev.key)
		if !okA || !okB {
			continue
		}
		flying = true
		const flight = 3.3
		// The arc's high point, between the two and above them.
		mx, my := (ax+bx)/2, min(ay, by)-s.r.H*0.13
		at := func(k float32) (x, y float32) {
			u := 1 - k
			return u*u*ax + 2*u*k*mx + k*k*bx, u*u*ay + 2*u*k*my + k*k*by
		}
		k := float32(ease(age / flight))
		x, y := at(k)
		if age >= flight {
			// It lands in a flash.
			s.glow(bx, by, h*0.5*float32(1-(age-flight)/(evMessage.lasts().Seconds()-flight)), toneColor(toneYou).Alpha(0.6))
			continue
		}
		for wake := 1; wake <= 7; wake++ {
			wx, wy := at(max(k-0.035*float32(wake), 0))
			s.disc(wx, wy, 1.6-0.15*float32(wake), s.pal.crest.Alpha(0.5-0.06*float32(wake)))
		}
		nx, ny := at(min(k+0.02, 1))
		s.thopter(x, y, nx-x, ny-y, h, float64(n))
	}
	if flying {
		return
	}
	const every, cross = 31.0, 10.0
	pass := math.Floor(s.t / every)
	at := s.t - pass*every
	if at > cross {
		return
	}
	k, dir := float32(at/cross), float32(1)
	if int(pass)%2 == 1 {
		k, dir = 1-k, -1
	}
	x := s.r.X + s.r.W*(-0.1+1.2*k)
	y := s.r.Y + s.r.H*(0.36+0.025*float32(math.Sin(s.t*0.9)))
	s.thopter(x, y, dir, 0.04*float32(math.Cos(s.t*0.9)), h*0.8, pass)
}

// voice rings out from the eclipse when more waits on the owner.
func (s *sandPainter) voice() {
	age, ok := s.event(evVoice, sandDial)
	if !ok || s.sc.still {
		return
	}
	for ring := range 3 {
		out := (age - 0.3*float64(ring)) / (evVoice.lasts().Seconds() - 0.6)
		if out <= 0 || out >= 1 {
			continue
		}
		s.circle(s.lay.cx, s.lay.cy, s.lay.rad*float32(1.35+3.4*out), float32(0.8+2.6*(1-out)),
			toneColor(toneYou).Alpha(float32(math.Pow(1-out, 1.5))*0.85))
	}
}

// sandBolts are the shapes lightning takes, in a box of 1 by 1.
var sandBolts = [][][2]float32{
	{{0.5, 0}, {0.42, 0.25}, {0.55, 0.4}, {0.4, 0.7}, {0.5, 1}},
	{{0.3, 0}, {0.45, 0.3}, {0.35, 0.5}, {0.6, 0.75}, {0.55, 1}},
	{{0.7, 0}, {0.55, 0.2}, {0.65, 0.45}, {0.45, 0.65}, {0.5, 1}},
}

// storm raises a Coriolis storm on the horizon while failed runs stand
// unread: a wall of dust that churns, taller the more of them, and
// lightning in it.
func (s *sandPainter) storm() {
	sc, r := s.sc, s.r
	if sc.failed == 0 {
		return
	}
	force := float32(min(sc.failed, 3))
	dust := s.pal.ridge[ridgeFar][1].Mix(ui.RGB(0x7a, 0x30, 0x1c), 0.5)
	base := r.Y + r.H*0.5
	// Many faint billows make one wall: none shows its edge.
	for i := range 18 + 6*int(force) {
		u := 0.58 + 0.48*sandHash(i*3+7)
		tall := (0.01 + 0.085*sandHash(i*3+8)*sandHash(i*3+10)) * (1 + 0.6*force) * (0.35 + 1.3*(u-0.58))
		rad := r.H * (0.05 + 0.06*sandHash(i*3+9))
		x := r.X + r.W*u + r.W*0.012*float32(math.Sin(s.t*0.3+float64(i)))
		y := base - r.H*tall + rad*0.25*float32(math.Sin(s.t*0.5+float64(i)*1.7))
		s.glow(x, y, rad*1.6, dust.Alpha(0.6))
	}
	s.p.FillGradient(ui.Rect{X: r.X + r.W*0.56, Y: base - r.H*0.12, W: r.W * 0.44, H: r.H * 0.12},
		ui.LinearGradient{From: dust.Alpha(0), To: dust.Alpha(0.75), Angle: 180}, 0)
	if sc.still {
		return
	}
	every := 4.2 - 0.8*float64(force)
	strike := math.Floor(s.t / every)
	at := s.t - strike*every
	if at > 0.09 && (at < 0.16 || at > 0.24) {
		return
	}
	bolt := sandBolts[int(strike)%len(sandBolts)]
	bx := r.X + r.W*(0.66+0.26*sandHash(int(strike)*31))
	top, tall := base-r.H*(0.1+0.05*force), r.H*(0.1+0.05*force)
	pts := make([][2]float32, len(bolt))
	for i, pt := range bolt {
		pts[i] = [2]float32{bx + (pt[0]-0.5)*tall*0.6, top + pt[1]*tall}
	}
	s.glow(bx, top+tall*0.4, tall*1.3, ui.RGB(0xe6, 0xdc, 0xff).Alpha(0.35))
	s.p.StrokePolyline(pts, 1.6, ui.RGB(0xf4, 0xee, 0xff))
}

// heighliners hang in the sky, one for each pull request ready to merge,
// three at most: the Guild waits on the owner's word.
func (s *sandPainter) heighliners() {
	r, rad := s.r, s.lay.rad
	for i := range min(s.sc.ready, 3) {
		x := r.X + r.W*(0.25+0.075*float32(i)) + 3*float32(math.Sin(s.t*0.12+float64(i)))
		y := r.Y + r.H*(0.07+0.04*float32(i%2))
		long, thick := rad*0.7, rad*0.085
		s.p.Fill(ui.Rect{X: x - long/2, Y: y - thick/2, W: long, H: thick}, ui.RGB(0x24, 0x22, 0x33), thick/2)
		s.p.Fill(ui.Rect{X: x - long*0.42, Y: y - thick/2, W: long * 0.84, H: 1}, s.pal.corona.Alpha(0.6), 0)
		for lamp := range 4 {
			on := 0.5 + 0.5*math.Sin(s.t*1.6+float64(lamp+i)*1.9)
			s.disc(x-long*0.3+long*0.2*float32(lamp), y+thick*0.1, 0.9, toneColor(toneFresh).Alpha(float32(0.35+0.65*on)))
		}
	}
}

// sandLights are the lit doors of the sietch in the rock on the left, and
// the windows of the keep on the right, across and down the box.
var sandLights = [][2]float32{
	{0.058, 0.458}, {0.108, 0.45}, {0.168, 0.44}, {0.192, 0.455}, {0.245, 0.462},
	{0.768, 0.44}, {0.786, 0.452}, {0.832, 0.445}, {0.888, 0.43}, {0.906, 0.446}, {0.912, 0.418}, {0.958, 0.452},
}

// lights lights the doors and windows in the rock, brighter by night, and
// a beacon on the keep.
func (s *sandPainter) lights() {
	r := s.r
	wide := max(r.H*0.0055, 1.6)
	for i, at := range sandLights {
		flicker := 0.75 + 0.25*math.Sin(s.t*(1.1+float64(sandHash(i*7)))+float64(i)*2.2)
		c := ui.RGB(0xff, 0xc0, 0x6a).Alpha((0.3 + 0.7*s.pal.night) * float32(flicker))
		x, y := r.X+r.W*at[0], r.Y+r.H*at[1]
		s.p.Fill(ui.Rect{X: x - wide/2, Y: y - wide*1.7, W: wide, H: wide * 1.7}, c, wide/2)
		if s.pal.night > 0.4 {
			s.glow(x, y-wide, wide*4, c.Alpha(0.3))
		}
	}
	if math.Sin(s.t*2.2) > 0.2 || s.sc.still {
		s.disc(r.X+r.W*0.902, r.Y+r.H*0.386, max(wide*0.6, 1.3), ui.RGB(0xff, 0x5a, 0x4a))
	}
}

// glitter is spice in the sand of a ridge, which catches the light now
// and then.
func (s *sandPainter) glitter(ridge int) {
	if s.sc.still || ridge == ridgeFar {
		return
	}
	r := s.r
	for i := range 26 {
		seed := ridge*97 + i
		on := math.Sin(s.t*(0.9+float64(sandHash(seed*3)))+float64(seed)*2.1) - 0.72
		if on <= 0 {
			continue
		}
		x := r.X + r.W*sandHash(seed*5+1)
		y := s.lay.crestY(ridge, x) + r.H*(0.02+0.09*sandHash(seed*5+2))
		s.disc(x, y, 0.9+0.8*sandHash(seed*5+3), ui.RGB(0xff, 0xb0, 0x5a).Alpha(float32(on/0.28)))
	}
}

// mouse sends Muad'Dib, the desert mouse, hopping along the near ridge now
// and then.
func (s *sandPainter) mouse() {
	if s.sc.still || s.r.H < 260 {
		return
	}
	const every, run = 27.0, 6.5
	pass := math.Floor((s.t + 9) / every)
	at := s.t + 9 - pass*every
	if at > run {
		return
	}
	dir := float32(1)
	from := 0.08 + 0.6*sandHash(int(pass)*41)
	if int(pass)%2 == 1 {
		dir, from = -1, from+0.24
	}
	x := s.r.X + s.r.W*(from+dir*0.24*float32(at/run))
	hop := float32(math.Abs(math.Sin(at * 6.5)))
	y := s.lay.crestY(ridgeNear, x) - 2 - 7*hop
	c := s.pal.figure
	s.p.Fill(ui.Rect{X: x - dir*8 - 3, Y: y - 0.5, W: 6, H: 1}, c, 0.5)
	s.disc(x, y, 2.8, c)
	s.disc(x+dir*2.6, y-1.6, 1.9, c)
	s.disc(x+dir*2.2, y-3.6, 1.2, c)
}

// star lets a star fall through the night sky now and then.
func (s *sandPainter) star() {
	if s.sc.still || s.pal.night < 0.5 {
		return
	}
	const every, falls = 11.0, 0.8
	pass := math.Floor(s.t / every)
	at := (s.t - pass*every) / falls
	if at > 1 {
		return
	}
	x := s.r.X + s.r.W*(0.1+0.7*sandHash(int(pass)*59)+0.12*float32(at))
	y := s.r.Y + s.r.H*(0.04+0.12*sandHash(int(pass)*61)+0.06*float32(at))
	fade := float32(math.Sin(math.Pi*at)) * s.pal.night
	s.p.Transform(ui.Rotation(27), x, y, func() {
		s.p.Fill(ui.Rect{X: x - 44, Y: y - 0.6, W: 44, H: 1.2}, ui.RGB(0xf2, 0xee, 0xff).Alpha(0.5*fade), 0.6)
	})
	s.disc(x, y, 1.5, ui.RGB(0xff, 0xff, 0xff).Alpha(fade))
}
