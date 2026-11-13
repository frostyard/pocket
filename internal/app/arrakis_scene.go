package app

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// The desert is painted, not laid out: layoutSand places the eclipse and
// the figures in a box of any size, and paintSand draws them every frame
// from the scene the view built and the frame clock. Nothing here keeps
// state: what moves is a function of the time.
//
// Shapes that stay (the dunes, the figures, the ring's arcs) are paths,
// whose masks MyGo keeps between frames. What moves is discs, rectangles
// and transforms of them, which need no mask, so a frame costs little.

// sandEdge keeps text off the scene's edges.
const sandEdge = 14

// sandSlowFrame is the scene's frame where no GPU draws the window.
const sandSlowFrame = 125 * time.Millisecond

// sandBlow is how long spice blows where a run ended.
const sandBlow = 2600 * time.Millisecond

// The ridges, from the horizon to the foreground.
const (
	ridgeFar = iota
	ridgeMid
	ridgeNear
)

// sandSpot is where a figure stands.
type sandSpot struct {
	x, y  float32 // its feet
	h     float32 // its height
	ridge int
	box   ui.Rect // what a click on it takes
	width float32 // the widest its label may be
}

// sandLayout places the scene in a box.
type sandLayout struct {
	r           ui.Rect
	unit        float32 // the height of a figure
	cx, cy, rad float32 // the eclipse's disc
	dial        ui.Rect // what a click on the eclipse takes
	spots       []sandSpot
}

func clampf(v, lo, hi float32) float32 { return max(lo, min(v, hi)) }

// crestAt is how far down the box a ridge's crest is at u across it, both
// from 0 to 1. The middle ridge rises at its center, where the supervisor
// stands under the eclipse.
func crestAt(ridge int, u float32) float32 {
	wave := func(freq, phase float32) float32 { return float32(math.Sin(float64(u*freq + phase))) }
	switch ridge {
	case ridgeFar:
		return 0.47 + 0.016*wave(5.1, 0.4) + 0.008*wave(11.3, 2.1)
	case ridgeMid:
		d := float64(u-0.5) / 0.2
		return 0.59 + 0.022*wave(3.4, 1.2) + 0.009*wave(8.7, 0.3) - 0.03*float32(math.Exp(-d*d))
	}
	return 0.83 + 0.024*wave(2.6, 3.0) + 0.009*wave(7.1, 1.0)
}

// crestY is the crest of a ridge at x, in the box's coordinates.
func (l *sandLayout) crestY(ridge int, x float32) float32 {
	return l.r.Y + l.r.H*crestAt(ridge, (x-l.r.X)/l.r.W)
}

// layoutSand places the scene's eclipse and figures in r. The first named
// figure stands at the center of the middle ridge; the others alternate
// left and right of it, a half step apart, on the near and middle ridges
// in turn, so that neighbors on one ridge are a whole step apart and each
// label has that room. Sessions of no named agent stand small on the far
// ridge, clear of the center.
func layoutSand(sc *sandScene, r ui.Rect) sandLayout {
	l := sandLayout{r: r}
	l.unit = clampf(min(r.H*0.135, r.W*0.10), 26, 124)
	l.rad = clampf(min(r.H*0.105, r.W*0.11), 20, 110)
	l.cx, l.cy = r.X+r.W/2, r.Y+r.H*0.205
	reach := l.rad * 1.3
	l.dial = ui.Rect{X: l.cx - reach, Y: l.cy - reach, W: 2 * reach, H: 2 * reach}

	spot := func(ridge int, u, h, width float32) sandSpot {
		x := r.X + r.W*u
		s := sandSpot{x: x, y: l.crestY(ridge, x) + h*0.02, h: h, ridge: ridge, width: width}
		below := float32(36)
		if ridge == ridgeFar {
			below = 6
		}
		s.box = ui.Rect{X: x - h*0.45, Y: s.y - h*1.2, W: h * 0.9, H: h*1.2 + below}
		if over := s.box.Y + s.box.H - (r.Y + r.H); over > 0 {
			s.box.H -= over
		}
		return s
	}
	n := len(sc.figures)
	if n > 0 {
		pairs := n / 2 // of the others, rounded up
		step := float32(0.36)
		if pairs > 0 {
			step = min(step, 0.88/float32(pairs))
		}
		// Figures shrink in a crowd, and where the ridges are close: a label
		// on the middle ridge then clears the heads on the near one.
		if n > 1 {
			l.unit = min(l.unit, step*r.W*0.62)
		}
		l.unit = max(min(l.unit, (r.H*0.24-46)/1.08), 24)
		width := max(step*r.W-10, 36)
		if n == 1 {
			width = r.W * 0.6
		}
		for i := range sc.figures {
			if i == 0 {
				h := l.unit * 1.1
				if sc.figures[0].supervisor {
					h = l.unit * 1.22
				}
				l.spots = append(l.spots, spot(ridgeMid, 0.5, h, width))
				continue
			}
			ring := (i - 1) / 2 // how many half steps out, less one
			off := float32(ring+1) * 0.5 * step
			if (i-1)%2 == 0 {
				off = -off
			}
			if ring%2 == 0 {
				l.spots = append(l.spots, spot(ridgeNear, 0.5+off, l.unit*1.08, width))
			} else {
				l.spots = append(l.spots, spot(ridgeMid, 0.5+off, l.unit, width))
			}
		}
	}
	// Each on the far ridge takes the place farthest from those who stand
	// in front of it and from the others there, clear of the eclipse.
	taken := []float32{0.5}
	for _, s := range l.spots {
		if s.ridge == ridgeMid {
			taken = append(taken, (s.x-r.X)/r.W)
		}
	}
	for range sc.loose {
		best, room := float32(0.5), float32(-1)
		for u := float32(0.06); u <= 0.941; u += 0.04 {
			if u > 0.38 && u < 0.62 {
				continue
			}
			near := float32(1)
			for _, t := range taken {
				near = min(near, max(u-t, t-u))
			}
			if near > room+0.001 {
				best, room = u, near
			}
		}
		taken = append(taken, best)
		l.spots = append(l.spots, spot(ridgeFar, best, l.unit*0.55, 0))
	}
	return l
}

// sandPalette is the scene's colors at an hour.
type sandPalette struct {
	skyTop, skyLow ui.Color
	ridge          [3][2]ui.Color // each ridge's crest and foot
	crest          ui.Color       // the lit edge of a crest
	figure         ui.Color
	corona         ui.Color
	night          float32 // how much of the night shows: stars and moons
}

var (
	sandNight = sandPalette{
		skyTop: ui.RGB(0x0a, 0x0e, 0x22), skyLow: ui.RGB(0x2c, 0x25, 0x54),
		ridge: [3][2]ui.Color{
			{ui.RGB(0x3d, 0x34, 0x5e), ui.RGB(0x2a, 0x24, 0x46)},
			{ui.RGB(0x2e, 0x27, 0x49), ui.RGB(0x1c, 0x19, 0x32)},
			{ui.RGB(0x1f, 0x1b, 0x35), ui.RGB(0x0e, 0x0c, 0x1b)},
		},
		crest: ui.RGB(0x8a, 0x7c, 0xc0), figure: ui.RGB(0x06, 0x05, 0x0c), corona: ui.RGB(0xd9, 0xd4, 0xff), night: 1,
	}
	sandDawn = sandPalette{
		skyTop: ui.RGB(0x34, 0x2c, 0x5c), skyLow: ui.RGB(0xea, 0x9c, 0x6c),
		ridge: [3][2]ui.Color{
			{ui.RGB(0xbd, 0x7c, 0x6c), ui.RGB(0x93, 0x5d, 0x5c)},
			{ui.RGB(0x9b, 0x5f, 0x53), ui.RGB(0x6c, 0x41, 0x43)},
			{ui.RGB(0x6c, 0x40, 0x3d), ui.RGB(0x3a, 0x22, 0x27)},
		},
		crest: ui.RGB(0xf6, 0xc4, 0x92), figure: ui.RGB(0x18, 0x0e, 0x14), corona: ui.RGB(0xff, 0xdc, 0xb0), night: 0.2,
	}
	sandDay = sandPalette{
		skyTop: ui.RGB(0xa9, 0x95, 0x76), skyLow: ui.RGB(0xee, 0xdb, 0xb0),
		ridge: [3][2]ui.Color{
			{ui.RGB(0xe4, 0xc3, 0x8e), ui.RGB(0xcf, 0xa6, 0x6e)},
			{ui.RGB(0xd8, 0xa9, 0x68), ui.RGB(0xb6, 0x82, 0x47)},
			{ui.RGB(0xbb, 0x85, 0x48), ui.RGB(0x86, 0x58, 0x2a)},
		},
		crest: ui.RGB(0xfb, 0xea, 0xc0), figure: ui.RGB(0x1c, 0x12, 0x0b), corona: ui.RGB(0xff, 0xf0, 0xcc), night: 0,
	}
	sandDusk = sandPalette{
		skyTop: ui.RGB(0x36, 0x1f, 0x45), skyLow: ui.RGB(0xe4, 0x72, 0x3c),
		ridge: [3][2]ui.Color{
			{ui.RGB(0xab, 0x57, 0x3d), ui.RGB(0x80, 0x3d, 0x34)},
			{ui.RGB(0x7d, 0x3b, 0x31), ui.RGB(0x52, 0x25, 0x26)},
			{ui.RGB(0x4b, 0x22, 0x23), ui.RGB(0x25, 0x12, 0x17)},
		},
		crest: ui.RGB(0xf4, 0xa4, 0x64), figure: ui.RGB(0x10, 0x09, 0x0d), corona: ui.RGB(0xff, 0xd0, 0x9a), night: 0.25,
	}
	// sandHours are the palettes around the clock; between two the colors
	// blend.
	sandHours = []struct {
		hour float64
		pal  *sandPalette
	}{{0, &sandNight}, {4.5, &sandNight}, {6.5, &sandDawn}, {9, &sandDay}, {16.5, &sandDay}, {19, &sandDusk}, {21.5, &sandNight}, {24, &sandNight}}
)

// paletteAt blends the palettes around hour.
func paletteAt(hour float64) sandPalette {
	hour = math.Mod(math.Mod(hour, 24)+24, 24)
	for i := 1; i < len(sandHours); i++ {
		from, to := sandHours[i-1], sandHours[i]
		if hour > to.hour {
			continue
		}
		t := float32((hour - from.hour) / (to.hour - from.hour))
		a, b := from.pal, to.pal
		out := sandPalette{
			skyTop: a.skyTop.Mix(b.skyTop, t), skyLow: a.skyLow.Mix(b.skyLow, t),
			crest: a.crest.Mix(b.crest, t), figure: a.figure.Mix(b.figure, t), corona: a.corona.Mix(b.corona, t),
			night: a.night + (b.night-a.night)*t,
		}
		for r := range out.ridge {
			out.ridge[r] = [2]ui.Color{a.ridge[r][0].Mix(b.ridge[r][0], t), a.ridge[r][1].Mix(b.ridge[r][1], t)}
		}
		return out
	}
	return sandNight
}

// ink is the color of text on a ridge: dark on light sand, light on dark.
func (pal *sandPalette) ink(ridge int) ui.Color {
	c := pal.ridge[ridge][0] // a label sits under the crest
	if 0.299*float32(c.R)+0.587*float32(c.G)+0.114*float32(c.B) > 132 {
		return ui.RGB(0x2a, 0x17, 0x07)
	}
	return ui.RGB(0xf3, 0xe6, 0xcc)
}

// toneColor is the color a tone shows in, on the dark of the eclipse and
// of a caption.
func toneColor(tone sandTone) ui.Color {
	switch tone {
	case toneYou:
		return ui.RGB(0xff, 0xb6, 0x48)
	case toneSupervisor:
		return ui.RGB(0xb7, 0x9c, 0xff)
	case toneWork:
		return ui.RGB(0xff, 0x8f, 0x45)
	case toneReview:
		return ui.RGB(0x6f, 0xc3, 0xff)
	case toneFailed:
		return ui.RGB(0xff, 0x6a, 0x55)
	case toneFresh:
		return ui.RGB(0x86, 0xd8, 0xff)
	}
	return ui.RGB(0x9a, 0xa0, 0xb6)
}

// sandHash is a number from 0 to 1 that n always gives: where a star is,
// how long a ray.
func sandHash(n int) float32 {
	x := uint32(n)*2654435761 + 0x9e3779b9
	x ^= x >> 15
	x *= 2246822519
	x ^= x >> 13
	x *= 3266489917
	x ^= x >> 16
	return float32(x%100003) / 100003
}

func frac(v float64) float64 { return v - math.Floor(v) }

// sandPainter paints one frame of the scene.
type sandPainter struct {
	p   *ui.Painter
	r   ui.Rect
	sc  *sandScene
	lay *sandLayout
	pal sandPalette
	t   float64 // seconds since the place first drew; 0 when still
	now time.Time
}

// paintSand draws the scene in r.
func paintSand(p *ui.Painter, r ui.Rect, sc *sandScene) {
	if r.W < 60 || r.H < 60 {
		return
	}
	lay := layoutSand(sc, r)
	s := &sandPainter{p: p, r: r, sc: sc, lay: &lay, pal: paletteAt(sc.hour), now: p.Now()}
	if !sc.still {
		s.t = s.now.Sub(sc.epoch).Seconds()
		if p.Accelerated() {
			p.AnimationFrameRate(30)
		} else {
			// Drawn on the CPU, the sky's gradients cost: move seldom.
			p.After(sandSlowFrame)
		}
	}
	s.sky()
	s.eclipse()
	onRidge := func(ridge int) {
		s.ridge(ridge)
		s.wind(ridge)
		sc.each(func(i int, f *sandFigure) {
			if lay.spots[i].ridge == ridge {
				s.figure(i, f, lay.spots[i])
			}
		})
	}
	onRidge(ridgeFar)
	s.worm()
	onRidge(ridgeMid)
	onRidge(ridgeNear)
	// The label of the figure under the pointer or the choice goes over the
	// others.
	marked := func(f *sandFigure) bool { return sc.sel == f.key || sc.hover == f.key }
	sc.each(func(i int, f *sandFigure) {
		if !marked(f) {
			s.label(f, lay.spots[i], false)
		}
	})
	sc.each(func(i int, f *sandFigure) {
		if marked(f) {
			s.label(f, lay.spots[i], true)
		}
	})
	s.foot()
}

func (s *sandPainter) disc(x, y, rad float32, c ui.Color) {
	s.p.Fill(ui.Rect{X: x - rad, Y: y - rad, W: 2 * rad, H: 2 * rad}, c, rad)
}

func (s *sandPainter) circle(x, y, rad, width float32, c ui.Color) {
	s.p.Stroke(ui.Rect{X: x - rad, Y: y - rad, W: 2 * rad, H: 2 * rad}, c, rad, width)
}

// glow is light fading out from a point.
func (s *sandPainter) glow(x, y, rad float32, c ui.Color) {
	s.p.FillRadialGradient(ui.Rect{X: x - rad, Y: y - rad, W: 2 * rad, H: 2 * rad},
		ui.RadialGradient{Stops: []ui.GradientStop{{Color: c}, {Color: c.Alpha(0.35), At: 0.45}, {Color: c.Alpha(0), At: 1}}, Radius: rad}, rad)
}

// groundRing is a circle lying on the sand around a point.
func (s *sandPainter) groundRing(x, y, rad, width float32, c ui.Color) {
	s.p.Transform(ui.Scaling(1, 0.28), x, y, func() { s.circle(x, y, rad, width, c) })
}

// centered draws a span centered on x, its top at y, and returns its size.
// Near an edge of the scene it moves in, to stay whole.
func (s *sandPainter) centered(x, y float32, span ui.Span) (w, h float32) {
	w, h = s.p.MeasureText(0, span)
	left := x - w/2
	if room := s.r.W - w - 2*sandEdge; room > 0 {
		left = clampf(left, s.r.X+sandEdge, s.r.X+sandEdge+room)
	}
	s.p.RichText(left, y, 0, span)
	return w, h
}

// sky paints the sky, with its stars and two moons at night, and the rock
// on the horizon.
func (s *sandPainter) sky() {
	r, pal := s.r, &s.pal
	s.p.FillGradient(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H * 0.6}, ui.LinearGradient{From: pal.skyTop, To: pal.skyLow, Angle: 180}, 0)
	if pal.night > 0.02 {
		s.nightSky()
	}
	// The Shield Wall stands on the horizon, clear of the eclipse.
	rock := pal.skyLow.Mix(pal.ridge[ridgeFar][1], 0.6)
	for _, wall := range [][][2]float32{
		{{0, 0.43}, {0.03, 0.43}, {0.045, 0.405}, {0.09, 0.4}, {0.1, 0.415}, {0.14, 0.412}, {0.155, 0.39}, {0.21, 0.385}, {0.225, 0.42}, {0.26, 0.425}, {0.29, 0.455}, {0.34, 0.475}, {0.34, 0.52}, {0, 0.52}},
		{{0.65, 0.475}, {0.7, 0.452}, {0.73, 0.447}, {0.745, 0.41}, {0.8, 0.405}, {0.815, 0.425}, {0.86, 0.42}, {0.875, 0.395}, {0.93, 0.39}, {0.945, 0.42}, {1, 0.425}, {1, 0.52}, {0.65, 0.52}},
	} {
		path := &ui.Path{}
		for i, pt := range wall {
			x, y := r.X+r.W*pt[0], r.Y+r.H*pt[1]
			if i == 0 {
				path.MoveTo(x, y)
			} else {
				path.LineTo(x, y)
			}
		}
		s.p.FillPath(path.Close(), rock)
	}
}

// nightSky paints the stars and the two moons, as much as the night shows.
func (s *sandPainter) nightSky() {
	r, pal := s.r, &s.pal
	for i := range 80 {
		x, y := r.X+r.W*sandHash(i*3+1), r.Y+r.H*0.44*sandHash(i*3+2)
		if math.Hypot(float64(x-s.lay.cx), float64(y-s.lay.cy)) < float64(s.lay.rad)*1.6 {
			continue
		}
		twinkle := 0.55 + 0.45*math.Sin(s.t*float64(0.5+sandHash(i))+float64(i))
		size := 0.7 + 0.9*sandHash(i*3)
		s.disc(x, y, size, ui.RGB(0xf2, 0xee, 0xff).Alpha(pal.night*float32(twinkle)*(0.35+0.65*sandHash(i*5))))
	}
	moon := func(u, v, size float32, c ui.Color) {
		x, y, rad := r.X+r.W*u, r.Y+r.H*v, s.lay.rad*size
		s.glow(x, y, rad*3, c.Alpha(0.22*pal.night))
		s.disc(x, y, rad, c.Alpha(pal.night))
	}
	moon(0.15, 0.13, 0.2, ui.RGB(0xec, 0xe4, 0xd0))
	moon(0.87, 0.08, 0.12, ui.RGB(0xc8, 0xd6, 0xf4))
}

// arcPath is the arc of a circle from a0 to a1, in degrees clockwise from
// the top.
func arcPath(cx, cy, rad float32, a0, a1 float64) *ui.Path {
	path := &ui.Path{}
	steps := max(2, int(math.Ceil(math.Abs(a1-a0)/4)))
	for i := 0; i <= steps; i++ {
		a := (a0 + (a1-a0)*float64(i)/float64(steps)) * math.Pi / 180
		x, y := cx+rad*float32(math.Sin(a)), cy-rad*float32(math.Cos(a))
		if i == 0 {
			path.MoveTo(x, y)
		} else {
			path.LineTo(x, y)
		}
	}
	return path.Cap(ui.ButtCap)
}

// eclipse paints the dark sun: its corona, the ring that counts the open
// threads by who moves them next, and in its disc what waits on the owner.
func (s *sandPainter) eclipse() {
	p, sc, pal := s.p, s.sc, &s.pal
	cx, cy, rad := s.lay.cx, s.lay.cy, s.lay.rad
	s.glow(cx, cy, rad*3.4, pal.corona.Alpha(0.34))
	s.glow(cx, cy, rad*2.0, pal.corona.Alpha(0.5))
	// The corona's rays keep their shapes and shimmer in their light.
	const rays = 84
	for i := range rays {
		a := (float64(i) + float64(sandHash(i*11))*0.6) / rays * 2 * math.Pi
		dx, dy := float32(math.Sin(a)), -float32(math.Cos(a))
		from, to := rad*1.36, rad*(1.44+0.42*sandHash(i*7+3)*sandHash(i*7+5))
		shimmer := 0.55 + 0.45*math.Sin(s.t*float64(0.7+sandHash(i*13))+float64(i)*1.7)
		p.StrokePolyline([][2]float32{{cx + dx*from, cy + dy*from}, {cx + dx*to, cy + dy*to}}, 1.1,
			pal.corona.Alpha(float32(0.18+0.5*shimmer*float64(sandHash(i*17)))))
	}
	bezel := ui.RGBA(0x0a, 0x08, 0x12, 0.7)
	if sc.hover == "dial" {
		bezel = ui.RGBA(0x1c, 0x17, 0x2c, 0.8)
	}
	s.disc(cx, cy, rad*1.3, bezel)
	s.disc(cx, cy, rad, ui.RGB(0x05, 0x04, 0x0a))
	s.circle(cx, cy, rad, 1, pal.corona.Alpha(0.5))

	// The ring: an arc for each part that holds something.
	total, held := 0, 0
	for _, part := range sc.parts {
		total += part.n
		if part.n > 0 {
			held++
		}
	}
	ringAt, ringWide := rad*1.15, max(rad*0.075, 2.5)
	if total == 0 {
		s.circle(cx, cy, ringAt, 1, ui.RGB(0x9a, 0xa0, 0xb6).Alpha(0.35))
	} else {
		gap := 5.0
		if held == 1 {
			gap = 0
		}
		at, span := 0.0, 360-gap*float64(held)
		for _, part := range sc.parts {
			if part.n == 0 {
				continue
			}
			sweep := span * float64(part.n) / float64(total)
			c := toneColor(part.tone)
			if part.tone == toneYou {
				c = c.Alpha(float32(0.72 + 0.28*math.Sin(s.t*2.4)))
			}
			p.StrokePath(arcPath(cx, cy, ringAt, at+gap/2, at+gap/2+sweep), ringWide, c)
			at += sweep + gap
		}
	}
	// A turning ring of marks inside the disc, as the Sisterhood writes.
	p.Transform(ui.Rotation(float32(math.Mod(s.t*2.5, 360))), cx, cy, func() {
		const marks = 48
		for i := range marks {
			if sandHash(i*23+1) < 0.22 {
				continue
			}
			a := float64(i) / marks * 2 * math.Pi
			dx, dy := float32(math.Sin(a)), -float32(math.Cos(a))
			from, to := rad*0.9, rad*(0.87-0.09*sandHash(i*29))
			p.StrokePolyline([][2]float32{{cx + dx*from, cy + dy*from}, {cx + dx*to, cy + dy*to}}, 1, pal.corona.Alpha(0.34))
		}
	})
	s.circle(cx, cy, rad*0.93, 1, pal.corona.Alpha(0.18))

	cream, quiet := ui.RGB(0xf8, 0xea, 0xd0), ui.RGB(0xc4, 0xb5, 0x94)
	small := clampf(rad*0.125, 7, 11)
	if sc.waits == 0 {
		w, h := p.MeasureText(0, ui.Span{Text: "ALL IS QUIET", Size: small, Weight: 600, Color: quiet, LetterSpacing: 2})
		p.RichText(cx-w/2, cy-h/2, 0, ui.Span{Text: "ALL IS QUIET", Size: small, Weight: 600, Color: quiet, LetterSpacing: 2})
		return
	}
	number := ui.Span{Text: fmt.Sprint(sc.waits), Size: rad * 0.78, Weight: 300, Color: cream}
	_, nh := p.MeasureText(0, number)
	s.centered(cx, cy-nh*0.62, number)
	word := "AWAIT YOU"
	if sc.waits == 1 {
		word = "AWAITS YOU"
	}
	s.centered(cx, cy+nh*0.3, ui.Span{Text: word, Size: small, Weight: 600, Color: quiet, LetterSpacing: 2})
}

// ridge paints a ridge of dunes: its body, the lit edge of its crest, and
// the ripples the wind leaves below it.
func (s *sandPainter) ridge(ridge int) {
	r, lay := s.r, s.lay
	foot := [3]float32{0.70, 0.94, 1}[ridge]
	body, crest := &ui.Path{}, &ui.Path{}
	const step = 6
	for x := r.X; ; x += step {
		x = min(x, r.X+r.W)
		y := lay.crestY(ridge, x)
		if x == r.X {
			body.MoveTo(x, y)
			crest.MoveTo(x, y)
		} else {
			body.LineTo(x, y)
			crest.LineTo(x, y)
		}
		if x >= r.X+r.W {
			break
		}
	}
	body.LineTo(r.X+r.W, r.Y+r.H*foot+1).LineTo(r.X, r.Y+r.H*foot+1).Close()
	s.p.FillPathGradient(body, ui.LinearGradient{From: s.pal.ridge[ridge][0], To: s.pal.ridge[ridge][1], Angle: 180})
	s.p.StrokePath(crest, 1.2, s.pal.crest.Alpha(0.6))
	if ridge == ridgeFar {
		return
	}
	for i, below := range []float32{0.045, 0.085} {
		ripple := &ui.Path{}
		for x := r.X; x <= r.X+r.W; x += step * 3 {
			y := lay.crestY(ridge, x+r.W*0.03*float32(i+1)) + r.H*below
			if x == r.X {
				ripple.MoveTo(x, y)
			} else {
				ripple.LineTo(x, y)
			}
		}
		s.p.StrokePath(ripple, 1, s.pal.crest.Alpha(0.10-0.03*float32(i)))
	}
}

// wind blows streaks of sand off a ridge's crest; harder while runs fail.
func (s *sandPainter) wind(ridge int) {
	if s.sc.still {
		return
	}
	r := s.r
	speed := 1.0
	if s.sc.failed > 0 {
		speed = 2.2
	}
	for i := range 12 {
		seed := ridge*40 + i
		u := frac(float64(sandHash(seed*3)) + s.t*speed*float64(0.018+0.03*sandHash(seed*3+1)))
		x := r.X + r.W*float32(u)
		y := s.lay.crestY(ridge, x) - 2 - 9*sandHash(seed*3+2)
		length := 8 + 16*sandHash(seed*5)
		fade := float32(math.Sin(math.Pi * u))
		s.p.Fill(ui.Rect{X: x, Y: y, W: length, H: 1}, s.pal.crest.Alpha((0.12+0.3*sandHash(seed*7))*fade), 0.5)
	}
}

// worm sends Shai-Hulud along behind the middle ridge while runs are at
// work: the more of them, the sooner it comes again.
func (s *sandPainter) worm() {
	sc, r := s.sc, s.r
	if sc.still || sc.working == 0 {
		return
	}
	const cross = 13.0
	period := math.Max(17, 36-5*float64(sc.working))
	pass := math.Floor(s.t / period)
	at := s.t - pass*period
	if at > cross {
		return
	}
	dir := float32(1)
	if int(pass)%2 == 1 {
		dir = -1
	}
	rad := clampf(s.lay.unit*0.25, 6, 26)
	head := r.X + r.W*float32(-0.25+1.5*at/cross)
	if dir < 0 {
		head = r.X + r.W*float32(1.25-1.5*at/cross)
	}
	// It arcs out of the sand: the head dives ahead, the back follows over
	// the top and down under the crest again, each ring of it lit above.
	body, lit := s.pal.ridge[ridgeNear][1].Mix(s.pal.ridge[ridgeMid][1], 0.4), s.pal.crest
	const segments = 24
	breach := 1.7 + 0.5*math.Sin(s.t*0.8)
	segment := func(i int) (x, y, size float32) {
		along := float64(i) / segments
		x = head - dir*float32(i)*rad*0.78
		lift := breach*math.Sin(math.Pi*(along+0.22)/1.22) + 0.12*math.Sin(float64(i)*0.9-s.t*3)
		y = s.lay.crestY(ridgeMid, x) + rad*0.9 - rad*1.25*float32(lift)
		return x, y, rad * (1 - 0.5*float32(along*along))
	}
	for i := segments - 1; i >= 0; i-- {
		x, y, size := segment(i)
		s.disc(x, y, size, body)
		s.circle(x, y, size, 1.2, lit.Alpha(0.42))
	}
	x, y, size := segment(0)
	s.disc(x+dir*size*0.3, y+size*0.12, size*0.62, ui.RGB(0x09, 0x05, 0x08))
	s.circle(x+dir*size*0.3, y+size*0.12, size*0.62, 1, lit.Alpha(0.7))
	// Sand flies where it breaks the crest.
	for grain := range 10 {
		up := frac(s.t*0.9 + float64(sandHash(grain*13)))
		gx := head + dir*rad*(0.6+1.6*sandHash(grain*17)) - dir*rad*2*float32(up)
		gy := s.lay.crestY(ridgeMid, gx) - rad*1.6*float32(math.Sin(math.Pi*up))*sandHash(grain*19)
		s.disc(gx, gy, 1.2+sandHash(grain*23), lit.Alpha(0.5*float32(1-up)))
	}
}

// figurePath is a figure's body with its feet at (x, y) and h tall: hooded
// in a robe, or caped with the head bare (which the caller draws).
func figurePath(x, y, h float32, hooded bool) *ui.Path {
	pt := func(u, v float32) (float32, float32) { return x + u*h, y + v*h }
	path := &ui.Path{}
	move := func(u, v float32) { path.MoveTo(pt(u, v)) }
	cube := func(u1, v1, u2, v2, u, v float32) {
		x1, y1 := pt(u1, v1)
		x2, y2 := pt(u2, v2)
		ex, ey := pt(u, v)
		path.CubeTo(x1, y1, x2, y2, ex, ey)
	}
	if hooded {
		move(-0.26, 0)
		cube(-0.25, -0.30, -0.20, -0.52, -0.155, -0.66)
		cube(-0.13, -0.72, -0.10, -0.74, -0.085, -0.78)
		cube(-0.125, -0.84, -0.11, -0.97, 0, -1)
		cube(0.11, -0.97, 0.125, -0.84, 0.085, -0.78)
		cube(0.10, -0.74, 0.13, -0.72, 0.155, -0.66)
		cube(0.20, -0.52, 0.25, -0.30, 0.26, 0)
		cube(0.12, 0.03, -0.12, 0.03, -0.26, 0)
		return path.Close()
	}
	move(-0.23, 0)
	cube(-0.25, -0.28, -0.25, -0.50, -0.225, -0.68)
	cube(-0.215, -0.755, -0.13, -0.785, -0.055, -0.795)
	path.LineTo(pt(-0.05, -0.83))
	path.LineTo(pt(0.05, -0.83))
	path.LineTo(pt(0.055, -0.795))
	cube(0.13, -0.785, 0.215, -0.755, 0.225, -0.68)
	cube(0.25, -0.50, 0.25, -0.28, 0.23, 0)
	cube(0.11, 0.03, -0.11, 0.03, -0.23, 0)
	return path.Close()
}

// figure paints someone standing at a spot, with what its state shows: a
// thumper that calls, a glowglobe at work, embers, water, a spice blow.
func (s *sandPainter) figure(i int, f *sandFigure, spot sandSpot) {
	p, sc, pal := s.p, s.sc, &s.pal
	x, y, h := spot.x, spot.y, spot.h
	look := factionLooks[f.faction]
	chosen, hovered := sc.sel == f.key, sc.hover == f.key
	seed := float64(i) * 1.91

	// On the sand, under the figure.
	if chosen {
		s.groundRing(x, y, h*0.5, 3.2, ui.RGB(0xff, 0xdf, 0x9a))
	} else if hovered {
		s.groundRing(x, y, h*0.5, 2.4, ui.RGB(0xff, 0xdf, 0x9a).Alpha(0.5))
	}
	thumper := x - h*0.46
	if f.calling > 0 {
		for ring := range 3 {
			out := frac(s.t/2.4 + float64(ring)/3)
			amber := toneColor(toneYou).Alpha(float32(math.Pow(1-out, 1.4)) * 0.95)
			s.groundRing(thumper, y+h*0.02, h*float32(0.12+0.85*out), 2.6, amber)
		}
	}
	if f.failed > 0 {
		s.glow(x, y, h*0.7, toneColor(toneFailed).Alpha(0.3))
	}
	p.Fill(ui.Rect{X: x - h*0.3, Y: y - h*0.025, W: h * 0.6, H: h * 0.07}, ui.RGBA(0, 0, 0, 0.28), h*0.035)

	// The light behind it.
	globeX, globeY := x+h*0.4, y-h*1.03+h*0.028*float32(math.Sin(s.t*1.5+seed))
	if f.working > 0 {
		s.glow(globeX, globeY, h*0.75, ui.RGB(0xff, 0xc4, 0x66).Alpha(0.42))
	} else if f.limited > 0 {
		s.glow(globeX, globeY, h*0.5, toneColor(toneFresh).Alpha(0.22))
	}
	if f.supervisor {
		s.circle(x, y-h*0.875, h*0.175, 1.2, ui.RGB(0xf1, 0xd0, 0x88).Alpha(0.75))
	}

	// The figure: a lit edge toward the eclipse, then the body.
	toward := float32(1.2)
	if x > s.lay.cx {
		toward = -1.2
	}
	rim := pal.crest.Alpha(0.5)
	if hovered || chosen {
		rim = ui.RGB(0xff, 0xe9, 0xc0).Alpha(0.9)
	}
	p.FillPath(figurePath(x+toward, y-1, h, look.hooded), rim)
	p.FillPath(figurePath(x, y, h, look.hooded), pal.figure)
	eyes := y - h*0.875
	if !look.hooded {
		s.disc(x+toward, y-h*0.905-1, h*0.092, rim)
		s.disc(x, y-h*0.905, h*0.092, pal.figure)
		eyes = y - h*0.91
	}
	if h >= 36 {
		trim := look.trim
		if f.failed > 0 {
			trim = toneColor(toneFailed)
		}
		p.Fill(ui.Rect{X: x - 0.5, Y: y - h*0.7, W: 1.2, H: h * 0.62}, trim.Alpha(0.55), 0.6)
		dot := max(h*0.013, 0.8)
		s.disc(x-h*0.03, eyes, dot, look.eyes)
		s.disc(x+h*0.03, eyes, dot, look.eyes)
		if look.armed {
			// A blade at the hip.
			p.StrokePolyline([][2]float32{{x + h*0.08, y - h*0.42}, {x + h*0.31, y - h*0.05}}, 1.3, ui.RGB(0xc9, 0xd2, 0xdc).Alpha(0.8))
		}
	}

	// Beside and over it.
	if f.calling > 0 {
		wide := max(h*0.035, 2)
		beat := frac(s.t / 1.2)
		lift := h * 0.15 * float32(math.Min(beat/0.8, 1))
		if beat > 0.8 {
			lift = h * 0.15 * float32(1-(beat-0.8)/0.2)
		}
		p.Fill(ui.Rect{X: thumper - wide/2, Y: y - h*0.36, W: wide, H: h*0.36 + 2}, ui.RGB(0x2b, 0x25, 0x22), wide/2)
		p.Fill(ui.Rect{X: thumper - h*0.055, Y: y - h*0.44 - lift, W: h * 0.11, H: h * 0.09}, toneColor(toneYou), 2)
	}
	if f.working > 0 || f.limited > 0 {
		core := ui.RGB(0xff, 0xf2, 0xcc)
		if f.working == 0 {
			core = toneColor(toneFresh)
		}
		s.disc(globeX, globeY, max(h*0.05, 2.2), core)
		for orb := range min(f.subagents, 5) {
			a := s.t*1.3 + seed + float64(orb)*2*math.Pi/float64(min(f.subagents, 5))
			s.disc(globeX+h*0.16*float32(math.Cos(a)), globeY+h*0.09*float32(math.Sin(a)), max(h*0.022, 1.3), ui.RGB(0xff, 0xe2, 0xa6))
		}
	}
	if f.working > 0 && !sc.still {
		// Spice rises from the sand where work goes on.
		for mote := range 7 {
			up := frac(s.t*0.21 + float64(sandHash(i*31+mote)))
			mx := x + h*(sandHash(i*37+mote)-0.5)*0.8 + h*0.04*float32(math.Sin(s.t*1.2+float64(mote)))
			s.disc(mx, y-h*1.15*float32(up), 1.1+0.9*sandHash(mote*3+i), toneColor(toneWork).Alpha(0.85*float32(math.Sin(math.Pi*up))))
		}
	}
	if f.failed > 0 {
		for ember := range 7 {
			a := s.t*1.7 + seed + float64(ember)*0.9
			up := frac(float64(sandHash(i*41+ember)) + s.t*0.06)
			s.disc(x+h*0.34*float32(math.Cos(a)), y-h*(0.1+0.8*float32(up))+h*0.04*float32(math.Sin(a)), 1.6,
				toneColor(toneFailed).Alpha(0.9*float32(math.Sin(math.Pi*up))))
		}
	}
	if f.fresh > 0 {
		wx, wy := x+h*0.4, y-h*0.05
		s.glow(wx, wy, h*0.24, toneColor(toneFresh).Alpha(float32(0.4+0.2*math.Sin(s.t*2+seed))))
		s.disc(wx, wy, max(h*0.04, 2), toneColor(toneFresh))
	}
	if at, ok := sc.blows[f.key]; ok && !sc.still {
		age := s.now.Sub(at).Seconds()
		if age >= 0 && age < sandBlow.Seconds() {
			fade := float32(1 - age/sandBlow.Seconds())
			s.glow(x, y-h*0.2, h*(0.5+0.5*float32(age)), toneColor(toneWork).Alpha(0.5*fade*fade))
			for grain := range 26 {
				lean := (float64(sandHash(grain*5+i)) - 0.5) * 1.3
				speed := float64(h) * (1.3 + 1.5*float64(sandHash(grain*7+i)))
				gx := x + float32(math.Sin(lean)*speed*age*0.7)
				gy := y - float32(math.Cos(lean)*speed*age) + float32(0.5*float64(h)*1.1*age*age)
				s.disc(gx, gy, 1.4+1.4*sandHash(grain*11), toneColor(toneWork).Alpha(fade))
			}
		}
	}
}

// fit cuts text to about width DIPs at a font size, without measuring.
func fit(text string, size, width, per float32) string {
	return truncate(text, max(int(width/(size*per)), 4))
}

// label names a figure under its feet, and under the name says what
// presses. In a crowd, and on the far ridge, only the figure under the
// pointer or the choice says it, and what it did last when nothing
// presses; its label then takes the room it needs, over its neighbors'.
func (s *sandPainter) label(f *sandFigure, spot sandSpot, marked bool) {
	if spot.ridge == ridgeFar && !marked {
		return // the far ridge is too far to read, until the pointer asks
	}
	ink := s.pal.ink(spot.ridge)
	if !f.active() && !marked {
		ink = ink.Alpha(0.62)
	}
	width := spot.width
	if marked {
		width = max(width, 300)
	}
	size, spacing := clampf(s.lay.unit*0.15, 9, 12.5), float32(1.6)
	if width < 90 {
		spacing = 0.8
	}
	y := spot.y + max(spot.h*0.07, 5)
	_, h := s.centered(spot.x, y, ui.Span{Text: fit(strings.ToUpper(f.name), size, width, 0.62+spacing/size), Size: size, Weight: 700, Color: ink, LetterSpacing: spacing})
	text, c := f.caption, toneColor(f.tone)
	if text == "" && marked {
		text, c = firstNonEmpty(f.last, f.title), ui.RGB(0xd8, 0xd2, 0xe4)
	}
	if text == "" || width < 120 || y+h+16 > s.r.Y+s.r.H {
		return
	}
	s.centered(spot.x, y+h+3, ui.Span{Text: fit(text, 10.5, width-12, 0.52), Size: 10.5, Weight: 500, Color: c,
		Background: ui.RGBA(0x0c, 0x09, 0x14, 0.66), Radius: 7, PaddingX: 6, PaddingY: 2})
}

// foot writes the scene's tallies along its bottom edge, and the hour.
func (s *sandPainter) foot() {
	sc, r := s.sc, s.r
	ink := s.pal.ink(ridgeNear).Alpha(0.78)
	span := func(text string) ui.Span {
		return ui.Span{Text: text, Size: 10, Weight: 600, Color: ink, LetterSpacing: 1.3}
	}
	if len(sc.figures) == 0 {
		s.centered(r.X+r.W/2, r.Y+r.H*0.86, span("NO ONE WALKS THE SAND"))
	}
	var tally []string
	if sc.working > 0 {
		tally = append(tally, fmt.Sprintf("%d AT WORK", sc.working))
	}
	if sc.waits > 0 {
		tally = append(tally, fmt.Sprintf("%d FOR YOU", sc.waits))
	}
	if sc.failed > 0 {
		tally = append(tally, fmt.Sprintf("%d FAILED", sc.failed))
	}
	if sc.ready > 0 {
		tally = append(tally, fmt.Sprintf("%d TO MERGE", sc.ready))
	}
	if sc.more > 0 {
		tally = append(tally, fmt.Sprintf("%d MORE IN THE DEEP DESERT", sc.more))
	}
	if len(tally) == 0 {
		tally = []string{"THE DESERT IS STILL"}
	}
	left := span(strings.Join(tally, "  ·  "))
	_, h := s.p.MeasureText(0, left)
	y := r.Y + r.H - h - 10
	lw, _ := s.p.RichText(r.X+sandEdge, y, 0, left)
	right := span(skyName(sc.hour) + "  ·  " + sc.clock)
	rw, _ := s.p.MeasureText(0, right)
	if lw+rw+60 < r.W {
		s.p.RichText(r.X+r.W-sandEdge-rw, y, 0, right)
	}
}

// skyName names the hour of the desert's sky.
func skyName(hour float64) string {
	switch {
	case hour < 4.5 || hour >= 21.5:
		return "NIGHT OF TWO MOONS"
	case hour < 9:
		return "FIRST LIGHT"
	case hour < 16.5:
		return "HIGH SUN"
	}
	return "THE COOL OF EVENING"
}
