package app

import (
	"fmt"
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

// agentCostNote says what the Cost by agent card counts.
const agentCostNote = "List price of each agent's answers, from the Desk meter's ledger. Subagents and linked sessions count to the agent of the session that started them. Today: since midnight in the server's zone; 7 days: the last 7 days, to the hour."

// agentCostRow is 1 row of the Cost by agent card, its texts ready to draw.
// A row's order is the server's: by 7-day cost, highest first.
type agentCostRow struct {
	Kind, Label       string
	Today, Days7      string
	Tokens            string
	Providers, Models string
}

// agentCostRows makes the card's rows from the dashboard's agents, keeping
// the server's order. A row's tokens are the 7 days': in counts the input,
// the cache reads and the cache writes; the split is the 7-day list price.
func agentCostRows(v *pocket.AgentCost) []agentCostRow {
	if v == nil {
		return nil
	}
	rows := make([]agentCostRow, 0, len(v.Rows))
	for _, r := range v.Rows {
		rows = append(rows, agentCostRow{
			Kind:      r.Kind,
			Label:     r.Label,
			Today:     "today " + agentMoney(r.Today.Cost),
			Days7:     "7 days " + agentMoney(r.Days7.Cost),
			Tokens:    agentTokens(r.Days7),
			Providers: agentProviderText(r.Providers),
			Models:    agentModelText(r.Providers),
		})
	}
	return rows
}

// agentTokens writes a span's tokens: "1.2M in / 300k out", with the cache
// reads and writes counted with the input.
func agentTokens(t pocket.AgentCostTotals) string {
	in := humanTokens(t.Input + t.CacheRead + t.CacheWrite)
	return in + " in / " + humanTokens(t.Output) + " out"
}

// agentProviderText writes a row's 7-day split by provider:
// "anthropic $12.34 · openai $3.10"; empty with no provider.
func agentProviderText(providers []pocket.AgentCostProvider) string {
	parts := make([]string, 0, len(providers))
	for _, p := range providers {
		parts = append(parts, p.Provider+" "+agentMoney(p.Days7.Cost))
	}
	return strings.Join(parts, " · ")
}

// agentModelText writes a row's 7-day split by model, the providers' order
// kept: "claude-opus-5-5 $70.00 · gpt-6.1 $3.10"; empty with no model.
func agentModelText(providers []pocket.AgentCostProvider) string {
	var parts []string
	for _, p := range providers {
		for _, m := range p.Models {
			parts = append(parts, m.Model+" "+agentMoney(m.Days7.Cost))
		}
	}
	return strings.Join(parts, " · ")
}

// agentMoney writes US dollars at list price: "$12.34" with 2 decimals below
// $100, and whole dollars with thousands commas above: "$1,235".
func agentMoney(usd float64) string {
	if math.Abs(usd) < 100 {
		return fmt.Sprintf("$%.2f", usd)
	}
	return wholeDollars(usd)
}

// agentCostCard draws the Cost by agent card: 1 row for each agent, its
// 7-day split by provider and by model under it, then the card's notes.
// It draws only when the dashboard has the agents field.
func (a *App) agentCostCard(c *ui.Context, v *pocket.AgentCost) {
	t := c.Theme()
	rows := agentCostRows(v)
	frostui.SectionHeader(c, frostui.SectionHead{Title: "Cost by agent", Aside: fmt.Sprint(len(rows))})
	ui.Column(c).Gap(frostui.SpaceM).Padding(frostui.SpaceL).Radius(10).AlignItems(ui.Stretch).
		Background(frostui.Panel(t)).BorderWidth(1, 1, 1, 1).BorderColor(t.Border).Children(func() {
		if len(rows) == 0 {
			frostui.Muted(c, "No model use in the last 7 days.").Wrap()
		}
		for i, r := range rows {
			if i > 0 {
				frostui.RowDivider(c.Key(fmt.Sprint("ad", i, r.Kind, r.Label)))
			}
			ui.Column(c.Key(fmt.Sprint("a", i, r.Kind, r.Label))).Gap(frostui.SpaceXS).AlignItems(ui.Stretch).Children(func() {
				ui.Row(c).Gap(frostui.SpaceM).AlignItems(ui.Center).Children(func() {
					frostui.Strong(c, r.Label).Grow(1)
					frostui.Caption(c, r.Today).Width(120)
					frostui.Caption(c, r.Days7).Width(120)
					frostui.Caption(c, r.Tokens).Width(170)
				})
				if r.Providers != "" {
					frostui.Caption(c, r.Providers).Wrap()
				}
				if r.Models != "" {
					frostui.Muted(c, r.Models).Wrap()
				}
			})
		}
		if v.Partial {
			frostui.Caption(c, "7-day costs (partial): the Desk's ledger does not cover 7 days yet.").Wrap()
		}
		if v.Pending > 0 {
			frostui.Caption(c, plural(v.Pending, "conversation", "conversations")+" not attributed yet.").Wrap()
		}
		frostui.Caption(c, agentCostNote).Wrap()
	})
}
