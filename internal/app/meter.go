package app

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

// meterNote says what the Subscriptions card counts.
const meterNote = "Account-wide: the filters do not apply. The percent and the reset come from the provider. List price is what the answers would cost at the API's prices, not money spent on a subscription."

// meterGauge is a fresh reading's bar: its percent and its label.
type meterGauge struct {
	Percent float64
	Label   string
}

// meterRow is 1 provider's row of the Subscriptions card, as the web card's
// meterRows (pi-pocket-team desk-web/dashboard.js) makes it. State is the
// main window's reading state, or "none" with no provider report. A fresh
// reading has a gauge; the other states have Status in its place. Line is
// the server's line, empty when Status already holds it.
type meterRow struct {
	Provider, Label, State string
	Gauge                  *meterGauge
	Status                 string
	Line, Grants, Ledger   string
}

// meterRows makes the card's rows from the dashboard's meter, with no
// projection of its own: the texts are the server's.
func meterRows(m *pocket.MeterView) []meterRow {
	if m == nil {
		return nil
	}
	rows := make([]meterRow, 0, len(m.Providers))
	for _, p := range m.Providers {
		row := meterRow{
			Provider: p.Provider,
			Label:    p.Label,
			Line:     p.Line,
			Grants:   meterGrantsText(p, m.At),
			Ledger:   meterLedgerText(p.Ledger),
		}
		var main *pocket.MeterWindow
		for i := range p.Windows {
			if p.Windows[i].ID == p.MainWindowID {
				main = &p.Windows[i]
				break
			}
		}
		switch {
		case main == nil:
			row.State = "none"
			row.Status = strings.TrimPrefix(p.Line, p.Label+": ")
			row.Line = ""
		case main.State == "awaiting":
			row.State = "awaiting"
			row.Status = "reset passed, awaiting provider report"
		case main.State == "stale":
			row.State = "stale"
			minutes := int64(0)
			if p.ReadAt != nil {
				minutes = max(0, int64(math.Floor(float64(m.At-*p.ReadAt)/60000)))
			}
			row.Status = fmt.Sprintf("%s %s%% used, read %d min ago", main.Label, meterPercent(main.Percent), minutes)
		default:
			row.State = "fresh"
			pct := 0.0
			if main.Percent != nil {
				pct = *main.Percent
			}
			row.Gauge = &meterGauge{Percent: pct, Label: fmt.Sprintf("%s%% of the %s used", meterPercent(main.Percent), main.Label)}
		}
		rows = append(rows, row)
	}
	return rows
}

// meterPercent writes a percent as the server sends it: "95", "12.5"; "?"
// when it is unknown.
func meterPercent(p *float64) string {
	if p == nil {
		return "?"
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

// meterGrantsText groups the granted resets by label: "resets granted:
// Weekly ×2 (first expires in 26d) · Fable ×2"; or "grants: <note>"; or
// empty. Times count from the view's at.
func meterGrantsText(p pocket.MeterProvider, at pocket.Millis) string {
	if len(p.Grants) == 0 {
		if p.GrantsNote != "" {
			return "grants: " + p.GrantsNote
		}
		return ""
	}
	type group struct {
		label string
		left  int
		first *pocket.Millis
	}
	var groups []*group
	for _, g := range p.Grants {
		var cur *group
		for _, each := range groups {
			if each.label == g.Label {
				cur = each
				break
			}
		}
		if cur == nil {
			cur = &group{label: g.Label}
			groups = append(groups, cur)
		}
		cur.left += g.ResetsLeft
		if g.EndsAt != nil && (cur.first == nil || *g.EndsAt < *cur.first) {
			end := *g.EndsAt
			cur.first = &end
		}
	}
	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		part := fmt.Sprintf("%s ×%d", g.label, g.left)
		if g.first != nil {
			part += " (first expires in " + meterLeft(int64(*g.first-at)) + ")"
		}
		parts = append(parts, part)
	}
	return "resets granted: " + strings.Join(parts, " · ")
}

// meterLeft writes a time left as "26d" or "5h", floored.
func meterLeft(ms int64) string {
	const hour, day = 3_600_000, 86_400_000
	if ms >= day {
		return fmt.Sprintf("%dd", ms/day)
	}
	return fmt.Sprintf("%dh", max(0, int64(math.Floor(float64(ms)/hour))))
}

// meterLedgerText writes the ledger at list price: "list price: $70 last
// hour, $228 today, $2,263 in 7 days (partial); top: model $2,263".
func meterLedgerText(l pocket.MeterLedger) string {
	text := fmt.Sprintf("list price: %s last hour, %s today, %s in 7 days", wholeDollars(l.LastHour), wholeDollars(l.Today), wholeDollars(l.Days7))
	if l.Partial {
		text += " (partial)"
	}
	if len(l.Top) > 0 {
		top := make([]string, 0, len(l.Top))
		for _, item := range l.Top {
			top = append(top, item.Model+" "+wholeDollars(item.Cost))
		}
		text += "; top: " + strings.Join(top, ", ")
	}
	if l.UnknownCost != nil && *l.UnknownCost > 0 {
		text += fmt.Sprintf("; %d answers with no cost", *l.UnknownCost)
	}
	return text
}

// wholeDollars writes US dollars rounded to whole dollars, with thousands
// commas: "$2,263".
func wholeDollars(usd float64) string {
	n := int64(math.Round(usd))
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	digits := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return sign + "$" + b.String()
}

// meterCard draws the Subscriptions card: 1 row for each provider, then
// the note that the card is account-wide.
func (a *App) meterCard(c *ui.Context, m *pocket.MeterView) {
	t := c.Theme()
	rows := meterRows(m)
	frostui.SectionHeader(c, frostui.SectionHead{Title: "Subscriptions"})
	ui.Column(c).Gap(frostui.SpaceM).Padding(frostui.SpaceL).Radius(10).AlignItems(ui.Stretch).
		Background(frostui.Panel(t)).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
		for i, r := range rows {
			if i > 0 {
				frostui.RowDivider(c.Key(fmt.Sprint("md", i, r.Provider)))
			}
			ui.Column(c.Key(fmt.Sprint("m", i, r.Provider))).Gap(frostui.SpaceXS).AlignItems(ui.Stretch).Children(func() {
				frostui.Strong(c, r.Label)
				if r.Gauge != nil {
					ui.Row(c).Gap(frostui.SpaceM).AlignItems(ui.Center).Children(func() {
						ui.Meter(c, r.Gauge.Percent, 0, 100, &ui.MeterLevels{Warning: 75, Critical: 90}).
							Label(r.Gauge.Label).Width(200)
						frostui.Body(c, r.Gauge.Label).Wrap().Grow(1)
					})
				} else {
					frostui.Body(c, r.Status).Wrap()
				}
				if r.Line != "" {
					frostui.Body(c, r.Line).Wrap()
				}
				if r.Grants != "" {
					frostui.Body(c, r.Grants).Wrap()
				}
				if r.Ledger != "" {
					frostui.Caption(c, r.Ledger).Wrap()
				}
			})
		}
		frostui.Caption(c, meterNote).Wrap()
	})
}
