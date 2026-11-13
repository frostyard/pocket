package app

import (
	"fmt"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

func TestDailyBarsShowQuantitiesAndHoveredDay(t *testing.T) {
	a, _ := teamApp(t)
	var d pocket.Dashboard
	if err := pocket.Decode([]byte(`{"daily":[{"day":"2026-11-10","successes":0},{"day":"2026-11-11","successes":1},{"day":"2026-11-12","successes":5}]}`), &d); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.dailyBars(c, &d) }, 700, 280, ui.TesterOptions{})
	tt.Frame()
	for _, label := range []string{"Threads closed well", "0", "3", "6", "6 closed well across 3 days"} {
		if !tt.HasText(label) {
			t.Errorf("missing quantity indicator %q", label)
		}
	}
	for _, day := range d.Daily {
		label := fmt.Sprintf("%s: %d closed well", day.Day, day.Successes)
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("missing bar %q", label)
		}
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Frame()
		if !tt.HasText(label) {
			t.Fatalf("hover did not show daily count %q", label)
		}
	}
	writePNG(t, tt, "daily-counts")
	tt.Move(699, 279)
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if !tt.HasText("2026-11-10: 0 closed well") {
		t.Fatal("keyboard focus did not expose daily count")
	}
	tt.Key(0, ui.KeyTab)
	tt.Key(0, ui.KeyTab)
	r, _ := tt.Find("2026-11-10: 0 closed well")
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if !tt.HasText("2026-11-10: 0 closed well") {
		t.Fatal("focused day hid a different hovered day's count")
	}
}

func TestDailyBarsZeroAndDenseRanges(t *testing.T) {
	a, _ := teamApp(t)
	for _, count := range []int{0, 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var d pocket.Dashboard
			for i := range 90 {
				d.Daily = append(d.Daily, struct {
					Day       string `json:"day"`
					Successes int    `json:"successes"`
				}{Day: a.opts.Now().AddDate(0, 0, i-89).Format("2006-01-02"), Successes: count})
			}
			tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.dailyBars(c, &d) }, 700, 280, ui.TesterOptions{})
			tt.Frame()
			for _, label := range []string{"0", "1", "2", fmt.Sprintf("%d closed well across 90 days", count*90)} {
				if !tt.HasText(label) {
					t.Errorf("missing quantity indicator %q", label)
				}
			}
			if count == 1 {
				tick, _ := tt.Find("1")
				bar, ok := tt.Find("2026-11-12: 1 closed well")
				if !ok || math.Abs(float64(tick.Y+tick.H/2-bar.Y)) > 1 {
					t.Fatal("one-closure bar does not align with the count axis")
				}
			}
			writePNG(t, tt, fmt.Sprintf("daily-counts-90-days-%d", count))
		})
	}
}
