package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

func reviewOrder(level string) int {
	switch level {
	case "L4":
		return 0
	case "L3":
		return 1
	case "L2":
		return 2
	case "L1/L2":
		return 3
	case "L1":
		return 4
	}
	return 5
}

// Capture the last look before stamping this visit; subsequent refreshes keep
// the same baseline, so fresh marks do not vanish as soon as seen succeeds.
func (a *App) stampDeskSeen() {
	if a.desk.view == nil {
		return
	}
	a.desk.seenPending = false
	a.desk.seenBefore = a.desk.view.Seen
	a.desk.seenReady = true
	a.request("Marking the Desk seen", func(ctx context.Context, c *pocket.Client) error { return c.DeskSeen(ctx) }, func(err error) {
		if err != nil {
			a.toast("Could not mark the Desk seen: " + err.Error())
		}
	})
}

func (a *App) loadCompletedPlans() {
	if a.desk.completedLoading {
		return
	}
	a.desk.completedLoading = true
	client := a.link.client
	var plans []pocket.CompletedPlan
	a.request("Reading completed plans", func(ctx context.Context, c *pocket.Client) error {
		var err error
		plans, err = c.CompletedPlans(ctx)
		return err
	}, func(err error) {
		if a.link.client != client {
			return
		}
		a.desk.completedLoading = false
		a.desk.completedLoaded = true
		if err != nil {
			a.desk.completedErr = err.Error()
		} else {
			a.desk.completed = plans
			a.desk.completedErr = ""
		}
	})
}

func (a *App) deskExtras(c *ui.Context, v *pocket.DeskView) {
	a.disclosureRow(c.Key("completed-plans"), "completed-plans", "object-select", "Completed plans", fmt.Sprint(v.PlansDone), func() {
		if !a.desk.completedLoaded && !a.desk.completedLoading {
			a.loadCompletedPlans()
		}
		if a.desk.completedLoading {
			frostui.Loading(c, "Reading completed plans…")
		}
		if a.desk.completedErr != "" {
			frostui.Problem(c, a.desk.completedErr).Wrap()
		}
		if ui.Button(c, "Refresh completed plans").Disabled(a.desk.completedLoading).Clicked() {
			a.loadCompletedPlans()
		}
		for _, p := range a.desk.completed {
			ui.Column(c.Key(p.ID)).Gap(frostui.SpaceXS).Children(func() {
				frostui.Strong(c, p.ID+" · "+p.Title).Wrap()
				line := fmt.Sprintf("%s · %s · %d rounds, %d failed", p.Outcome, a.agentName(p.Lead), p.Rounds, p.Failed)
				if p.Tasks != nil {
					line += fmt.Sprintf(" · %d/%d tasks", p.Tasks.Done, p.Tasks.Total)
				}
				frostui.Caption(c, line).Wrap()
				if p.Session != 0 && ui.Button(c, "Open planning session").Clicked() {
					a.open(p.Session)
				}
			})
		}
	})
	total := 0
	var repos []string
	for repo, n := range v.Others {
		if n > 0 {
			total += n
			repos = append(repos, repo)
		}
	}
	slices.Sort(repos)
	if total > 0 {
		a.disclosureRow(c.Key("other-prs"), "other-prs", "pocket-git-pr", "Other PRs", fmt.Sprint(total), func() {
			frostui.Caption(c, "Open PRs by other authors, from the Desk's last read.").Wrap()
			for _, repo := range repos {
				frostui.Strong(c, fmt.Sprintf("%s (%d)", repo, v.Others[repo])).Wrap()
				for _, pr := range v.OtherPRs[repo] {
					a.prRow(c.Key(pr.Ref), pocket.DeskPR{Ref: pr.Ref, Title: pr.Title, URL: pr.URL, State: "OPEN"})
					frostui.Caption(c, "by "+pr.Author).Wrap()
				}
				if n := v.Others[repo] - len(v.OtherPRs[repo]); n > 0 {
					frostui.Caption(c, fmt.Sprintf("%d more; the full list comes with the next Desk sweep.", n)).Wrap()
				}
			}
		})
	}
	if q := v.Issues; q != nil {
		a.disclosureRow(c.Key("queued-issues"), "queued-issues", "view-list", "Queued issues", fmt.Sprint(len(q.Items)), func() {
			if q.Off {
				frostui.Muted(c, "The issue queue read is off.").Wrap()
			} else if !q.OK {
				frostui.Problem(c, "Issue queue: "+q.Error).Wrap()
			}
			for _, it := range q.Items {
				ui.Column(c.Key(it.Ref)).Gap(frostui.SpaceXS).Children(func() {
					frostui.Strong(c, it.Ref+" · "+it.Title).Wrap()
					if it.Urgent {
						frostui.Pill(c, "urgent", frostui.Warning)
					}
					if it.Unverified {
						frostui.Pill(c, "unverified", frostui.Warning)
					}
					owners := it.Owners
					if len(owners) == 0 && it.Agent != "" {
						owners = []string{it.Agent}
					}
					frostui.Caption(c, strings.Join(owners, ", ")+" · "+strings.Join(it.Names, ", ")+" · "+it.By).Wrap()
					if len(it.FixedBy) > 0 {
						frostui.Caption(c, "Fixed by "+strings.Join(it.FixedBy, ", ")).Wrap()
					}
					ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
						if it.URL != "" && ui.Button(c, "Open issue").Clicked() {
							a.openURL(it.URL)
						}
						for _, th := range it.Threads {
							if ui.Button(c.Key(th.ID), th.ID+" · "+th.Word).Clicked() {
								a.threadSel = th.ID
							}
						}
					})
				})
			}
		})
	}
}
