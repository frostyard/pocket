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

type numberRecords struct {
	open, loading bool
	query         pocket.DashboardQuery
	metric, err   string
	data          *pocket.DashboardRecords
}

func (a *App) numberFilters(c *ui.Context, d *pocket.Dashboard) {
	ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
		owners := append([]string{"All owners"}, d.Choices.Owners...)
		repos := append([]string{"All repositories"}, d.Choices.Repos...)
		owner := firstNonEmpty(a.desk.dashOwner, owners[0])
		repo := firstNonEmpty(a.desk.dashRepo, repos[0])
		if !slices.Contains(owners, owner) {
			owners = append(owners, owner)
		}
		if !slices.Contains(repos, repo) {
			repos = append(repos, repo)
		}
		oldOwner, oldRepo := owner, repo
		ui.Select(c, &owner, owners).Label("GitHub owner")
		ui.Select(c, &repo, repos).Label("Repository")
		changed := false
		if owner != oldOwner {
			a.desk.dashOwner = owner
			if owner == owners[0] {
				a.desk.dashOwner = ""
			}
			a.desk.dashRepo = ""
			changed = true
		}
		if repo != oldRepo {
			a.desk.dashRepo = repo
			if repo == repos[0] {
				a.desk.dashRepo = ""
			}
			a.desk.dashOwner = ""
			changed = true
		}
		if changed {
			a.desk.records = nil
			a.loadDashboard(a.desk.dashDays)
		}
		if ui.Button(c, "Refresh numbers").Disabled(a.desk.dashLoading).Clicked() {
			a.loadDashboard(a.desk.dashDays)
		}
		if ui.Button(c, "All records").Clicked() {
			a.openNumberRecords(a.desk.dashShown, "All records")
		}
		if ui.Button(c, "Unattributed records").Clicked() {
			q := a.desk.dashShown
			q.Owner, q.Repo = "", ""
			q.Unattributed = true
			a.openNumberRecords(q, "All records")
		}
	})
	frostui.Caption(c, "Showing "+firstNonEmpty(a.desk.dashShown.Repo, a.desk.dashShown.Owner, "all Desk work")+" · UTC days").Wrap()
	frostui.Caption(c, "Coverage: "+firstNonEmpty(d.Coverage.Status, "not reported")+" · from "+firstNonEmpty(d.Coverage.BoundaryDay, "unknown")).Wrap()
	for _, limitation := range d.Coverage.Limitations {
		frostui.Muted(c, limitation.Text).Wrap()
	}
	a.disclosureRow(c.Key("number-definitions"), "number-definitions", "dialog-information", "How these numbers are counted", "", func() {
		for _, line := range []string{
			"Completion: successful latest closures divided by successful, abandoned and failed closures. Dropped, frozen and unknown outcomes are excluded.",
			"Closed well and cycle time: successful closures in this range; cycle time is the median from thread start to close. In motion is the state now.",
			"PRs: Desk-tracked PRs counted by their GitHub lifecycle times. Time to solve runs from issue creation to its resolving merge.",
			"Tokens and cost: dated assistant usage of Desk-linked sessions and subagents. Cost is recorded USD, not an invoice; missing cost remains unknown. Shared supervisor and Clerk sessions are excluded.",
		} {
			frostui.Body(c, line).Wrap()
		}
	})
}

func (a *App) openNumberRecords(query pocket.DashboardQuery, metric string) {
	r := &numberRecords{open: true, loading: true, query: query, metric: metric}
	a.desk.records = r
	var data *pocket.DashboardRecords
	a.request("Reading dashboard records", func(ctx context.Context, c *pocket.Client) error {
		var err error
		data, err = c.DashboardRecords(ctx, query)
		return err
	}, func(err error) {
		if a.desk.records != r {
			return
		}
		r.loading = false
		if err != nil {
			r.err = err.Error()
		} else {
			r.data = data
		}
	})
}

func recordThreadMatches(metric, kind string) bool {
	switch metric {
	case "Completion":
		return kind == "success" || kind == "abandoned" || kind == "failed"
	case "Closed well", "Cycle time":
		return kind == "success"
	case "In motion now":
		return kind == "now"
	case "PRs merged":
		return false
	}
	return true
}

func (a *App) numberRecordsDialog(c *ui.Context) {
	r := a.desk.records
	if r == nil || !r.open {
		return
	}
	ui.Modal(c, &r.open, func() {
		ui.Column(c).Width(700).Gap(frostui.SpaceM).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Gap(frostui.SpaceS).Children(func() {
				frostui.Strong(c, r.metric).Grow(1)
				if ui.Button(c, "Close").Clicked() {
					r.open = false
				}
			})
			scope := firstNonEmpty(r.query.Repo, r.query.Owner, "all Desk work")
			if r.query.Unattributed {
				scope = "unattributed work"
			}
			frostui.Caption(c, r.query.From+" to "+r.query.To+" · "+scope).Wrap()
			ui.Scroll(c).Height(460).Children(func() {
				ui.Column(c).Gap(frostui.SpaceS).AlignItems(ui.Stretch).Children(func() {
					if r.loading {
						frostui.Loading(c, "Reading records…")
						return
					}
					if r.err != "" {
						frostui.Problem(c, r.err).Wrap()
						return
					}
					if r.data == nil {
						return
					}
					shown := 0
					for _, th := range r.data.Threads {
						if !recordThreadMatches(r.metric, th.Kind) {
							continue
						}
						shown++
						ui.Row(c.Key(th.ID)).Gap(frostui.SpaceS).Wrap().Children(func() {
							if th.Gone {
								frostui.Mono(c, th.ID+" (no longer on the Desk)")
							} else if ui.Button(c, th.ID).Clicked() {
								r.open = false
								a.openNumberThread(th.ID)
							}
							frostui.Caption(c, th.Kind+" · "+firstNonEmpty(th.Repo, "unattributed")+" · "+th.State).Wrap()
						})
					}
					if r.metric == "All records" || r.metric == "PRs merged" {
						for _, pr := range r.data.PRs {
							if r.metric == "PRs merged" && !slices.Contains(pr.Events, "merged") {
								continue
							}
							shown++
							frostui.Strong(c, pr.Ref).Wrap()
							frostui.Caption(c, pr.State+" · "+strings.Join(pr.Events, ", ")).Wrap()
							ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
								for _, id := range pr.Threads {
									if ui.Button(c.Key(pr.Ref+id), id).Clicked() {
										r.open = false
										a.openNumberThread(id)
									}
								}
							})
						}
					}
					if shown == 0 {
						frostui.Muted(c, "No matching records.")
					}
					if r.data.Truncated.Threads+r.data.Truncated.PRs > 0 {
						frostui.Muted(c, fmt.Sprintf("The server omitted %d threads and %d PRs at its record limit.", r.data.Truncated.Threads, r.data.Truncated.PRs)).Wrap()
					}
				})
			})
		})
	})
}

// A historical metric can name a thread outside the Desk's seven-day window.
type numberThread struct {
	id      string
	loading bool
	detail  *pocket.DeskThreadDetail
	err     string
}

func (a *App) openNumberThread(id string) {
	a.goPlace("x:motion")
	a.threadSel = id
	if !a.shell.Panes.Shown("inspector") {
		a.shell.Panes.Toggle("inspector")
	}
	if a.desk.thread(id) != nil {
		a.desk.recordThread = nil
		return
	}
	n := &numberThread{id: id, loading: true}
	a.desk.recordThread = n
	var detail *pocket.DeskThreadDetail
	a.request("Reading historical thread "+id, func(ctx context.Context, c *pocket.Client) error {
		var err error
		detail, err = c.DeskThread(ctx, id)
		return err
	}, func(err error) {
		if a.desk.recordThread != n {
			return
		}
		n.loading = false
		if err != nil {
			n.err = err.Error()
		} else {
			n.detail = detail
		}
	})
}

func (a *App) numberThreadContext(c *ui.Context) bool {
	n := a.desk.recordThread
	if n == nil || n.id != a.threadSel {
		return false
	}
	switch {
	case n.loading:
		frostui.Loading(c, "Reading "+n.id+"…")
	case n.err != "":
		frostui.Problem(c, "Could not read "+n.id+": "+n.err).Wrap()
		if ui.Button(c, "Read thread again").Clicked() {
			a.openNumberThread(n.id)
		}
	case n.detail != nil:
		a.threadContext(c, &n.detail.Thread, n.detail)
	}
	return true
}
