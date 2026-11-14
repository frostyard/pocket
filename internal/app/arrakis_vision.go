package app

import (
	"math"
	"time"
)

// The vision is a made-up order at work, to see the desert move without
// waiting for the team: each of its people goes around from rest to a run
// at work, step by step, and on to a call, a failure or an end, each at a
// pace of its own. It is a function of the time since it began, so the
// events it plays are the ones arrakis.note finds between two looks, as
// for the team.

// sandVisionLook is how often the view looks at the vision again.
const sandVisionLook = 400 * time.Millisecond

// visionPerson is one of the vision's people: how long its round takes,
// where in it the vision finds it, how its run ends, and what it does.
type visionPerson struct {
	name          string
	round, shift  float64
	calls, fails  bool
	subagents     int
	steps         []string
	asks, failure string
}

var visionOrder = []visionPerson{
	{name: "Odrade", round: 27, shift: 4, subagents: 2,
		steps: []string{"read · desk/threads.md", "team_delegate · sheeana", "team_delegate · duncan", "read · reports/night-shift.md"}},
	{name: "Duncan Idaho", round: 19, shift: 9, fails: true,
		steps: []string{"bash · make ci", "edit · .github/workflows/ci.yml", "bash · go test -race ./..."}, failure: "exit status 1 · TestLoad/empty"},
	{name: "Sheeana", round: 23, shift: 1, subagents: 1,
		steps: []string{"read · internal/app/arrakis.go", "edit · internal/app/arrakis_scene.go", "bash · make verify", "gh pr create"}},
	{name: "Irulan", round: 31, shift: 14, calls: true,
		steps: []string{"read · CHANGELOG.md", "write · notes/v0.6.0.md", "artifact · release notes"}, asks: "asks: tag v0.6.0 now, or hold for the docs pass?"},
	{name: "Bellonda", round: 17, shift: 6,
		steps: []string{"grep · archive/", "read · archive/1147.md", "memory · save"}},
	{name: "Miles Teg", round: 29, shift: 20, calls: true, subagents: 3,
		steps: []string{"bash · ansible-playbook fleet.yml", "bash · ssh proxy-2 systemctl status", "edit · hosts.ini"}, asks: "bash · reboot proxy-2"},
	{name: "Murbella", round: 21, shift: 11, fails: true,
		steps: []string{"web_search · flatpak review", "fetch · flathub.org", "edit · manifest.json"}, failure: "Hit the spend limit ($5.00)"},
}

var visionLoose = []visionPerson{
	{name: "Stilgar: scout the deep desert", round: 22, shift: 3, steps: []string{"bash · rsync", "read · map.md"}},
	{name: "Chani: water count", round: 34, shift: 18, calls: true, steps: []string{"read · cistern.csv"}, asks: "asks: open the reserve?"},
}

// visionScene is the vision at seconds since it began.
func visionScene(seconds float64) *sandScene {
	sc := &sandScene{vision: true}
	you, work, done, failed := 0, 0, 0, 0
	person := func(p *visionPerson, key string, supervisor bool) sandFigure {
		f := sandFigure{key: key, name: p.name, title: "Of the deep desert", supervisor: supervisor, linked: !supervisor}
		if who, ok := castOf(p.name); ok {
			f.title, f.faction = who.title, who.faction
		}
		at := seconds + p.shift
		turn := math.Floor(at / p.round)
		part := at/p.round - turn
		switch {
		case part < 0.1:
			f.last = p.steps[len(p.steps)-1] + " · now"
		case part < 0.68:
			f.working, f.subagents, f.tone = 1, p.subagents, toneWork
			f.step = p.steps[int(at/2.6)%len(p.steps)]
			f.caption = f.step
			work++
		case p.calls && part < 0.93:
			f.calling, f.tone, f.caption = 1, toneYou, p.asks
			you++
		case p.fails && int(turn)%2 == 0:
			f.failed, f.tone, f.caption = 1, toneFailed, p.failure
			failed++
		case part < 0.88:
			f.fresh, f.tone, f.caption = 1, toneFresh, "finished · "+p.steps[len(p.steps)-1]
			done++
		default:
			f.last = p.steps[len(p.steps)-1] + " · now"
		}
		if f.working > 0 && f.title == "Rider of worms" {
			sc.rider = true
		}
		return f
	}
	for i := range visionOrder {
		sc.figures = append(sc.figures, person(&visionOrder[i], "v:"+visionOrder[i].name, i == 0))
	}
	for i := range visionLoose {
		p := &visionLoose[i]
		f := person(p, "v:"+p.name, false)
		f.linked = false
		if f.active() {
			sc.loose = append(sc.loose, f)
		}
	}
	// A pull request comes ready now and then, and waits a while.
	if math.Mod(seconds, 40) > 14 {
		sc.ready = 1 + int(seconds/40)%2
	}
	sc.waits, sc.working, sc.failed = you+sc.ready, work, failed
	sc.parts = []dialPart{{"With you", you + sc.ready, toneYou}, {"With the supervisor", 1, toneSupervisor},
		{"With agents", work, toneWork}, {"In review", done, toneReview}, {"Watching", 1, toneWatch}}
	return sc
}
