package app

import (
	"context"
	"path"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// wiki is the homewiki module's wiki as Pocket shows it.
type wiki struct {
	on      bool
	data    *pocket.Wiki
	page    *pocket.WikiPage
	sel     string
	loading bool
	reading bool
	md      markdown.Options
	ready   bool
	// err is a failed read of the pages, and pageErr of the page shown:
	// they keep a redraw from reading again, until Try again, a refresh or
	// an event clears them.
	err     string
	pageErr string
}

// loadWiki reads the wiki's pages, and the page shown again.
func (a *App) loadWiki() {
	if a.wiki.loading {
		return
	}
	a.wiki.loading, a.wiki.err = true, ""
	var w *pocket.Wiki
	var on bool
	a.request("Reading the wiki", func(ctx context.Context, c *pocket.Client) error {
		var err error
		w, on, err = c.WikiPages(ctx)
		return err
	}, func(err error) {
		a.wiki.loading = false
		if err != nil {
			a.wiki.err = err.Error()
			return
		}
		a.wiki.on, a.wiki.data = on, w
		if !on || w == nil || len(w.Pages) == 0 {
			return
		}
		if a.wiki.sel == "" {
			a.wiki.sel = w.Pages[0].Path
		}
		if a.selected == "x:wiki" {
			a.readWikiPage(a.wiki.sel)
		}
	})
}

// readWikiPage reads one page and shows it.
func (a *App) readWikiPage(p string) {
	a.wiki.sel, a.wiki.pageErr = p, ""
	a.wiki.reading = true
	var page *pocket.WikiPage
	a.request("Reading "+p, func(ctx context.Context, c *pocket.Client) error {
		var err error
		page, err = c.WikiRead(ctx, p)
		return err
	}, func(err error) {
		a.wiki.reading = false
		if err != nil {
			if a.wiki.sel == p {
				a.wiki.pageErr = err.Error()
			}
			return
		}
		if a.wiki.sel == p {
			a.wiki.page = page
		}
	})
}

// wikiLink follows a link in a wiki page: another page of the wiki by its
// relative path, a heading, or a web address in the browser.
func (a *App) wikiLink(dest string) {
	switch {
	case strings.HasPrefix(dest, "#"):
		a.wiki.md.Anchor = dest
		a.wiki.md.AnchorVersion++
	case strings.Contains(dest, "://"), strings.HasPrefix(dest, "mailto:"):
		a.openURL(dest)
	default:
		target, anchor, _ := strings.Cut(dest, "#")
		base := ""
		if a.wiki.page != nil {
			base = path.Dir(a.wiki.page.Path)
		}
		target = path.Clean(path.Join(base, target))
		if a.wiki.data != nil {
			for _, pg := range a.wiki.data.Pages {
				if pg.Path == target {
					a.readWikiPage(target)
					if anchor != "" {
						a.wiki.md.Anchor = "#" + anchor
						a.wiki.md.AnchorVersion++
					}
					return
				}
			}
		}
		a.toast("Not a page of the wiki: " + dest)
	}
}

// wikiPane is the Wiki place: the pages at the left, the page at the right.
func (a *App) wikiPane(c *ui.Context) {
	t := c.Theme()
	if !a.wiki.ready {
		a.wiki.ready = true
		a.wiki.md = markdown.Options{OnLink: a.wikiLink}
	}
	if a.wiki.data == nil && !a.wiki.loading && a.wiki.err == "" {
		a.loadWiki()
	}
	if a.wiki.page == nil && !a.wiki.reading && a.wiki.sel != "" && a.wiki.pageErr == "" {
		a.readWikiPage(a.wiki.sel)
	}
	ui.Row(c).Grow(1).AlignItems(ui.Stretch).Background(t.Background).Children(func() {
		ui.Scroll(c).Width(250).Background(frostui.Chrome(t)).Children(func() {
			ui.Column(c).Padding(frostui.SpaceM, frostui.SpaceS).Gap(2).AlignItems(ui.Stretch).Children(func() {
				if a.wiki.data == nil {
					if a.wiki.err == "" {
						frostui.Loading(c, "Reading the wiki…")
					}
					return
				}
				section := ""
				for _, pg := range a.wiki.data.Pages {
					if pg.Section != section {
						section = pg.Section
						frostui.Caption(c.Key("s"+section), strings.ToUpper(section)).Padding(frostui.SpaceM, frostui.SpaceS, frostui.SpaceXS, frostui.SpaceS)
					}
					a.wikiRow(c.Key(pg.Path), pg)
				}
			})
		})
		ui.Box(c).Width(1).Background(t.Border)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).AlignItems(ui.Center).Padding(frostui.SpaceXL).Children(func() {
				ui.Column(c).MaxWidth(860).FillWidth().Gap(frostui.SpaceM).AlignItems(ui.Stretch).Children(func() {
					if a.wiki.err != "" {
						frostui.Problem(c, "Reading the wiki: "+a.wiki.err).Wrap()
						if ui.Button(c, "Try again").Clicked() {
							a.loadWiki()
						}
						// A failed refresh keeps the pages read before it.
						if a.wiki.data == nil {
							return
						}
					}
					if a.wiki.pageErr != "" {
						frostui.Problem(c, "Reading "+a.wiki.sel+": "+a.wiki.pageErr).Wrap()
						if ui.Button(c, "Try again").Clicked() {
							a.readWikiPage(a.wiki.sel)
						}
						return
					}
					pg := a.wiki.page
					if pg == nil {
						frostui.Loading(c, "Opening the page…")
						return
					}
					ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
						var meta []string
						if pg.Section != "" {
							meta = append(meta, pg.Section)
						}
						if pg.Updated != "" {
							meta = append(meta, "updated "+pg.Updated)
						}
						meta = append(meta, pg.Path)
						frostui.Caption(c, strings.Join(meta, " · ")).Grow(1)
						if a.wiki.reading {
							ui.Spinner(c).Size(14, 14)
						}
					})
					markdown.View(c.Key(pg.Path), pg.Body, &a.wiki.md)
				})
			})
		})
	})
}

func (a *App) wikiRow(c *ui.Context, pg pocket.WikiPage) {
	t := c.Theme()
	sel := pg.Path == a.wiki.sel
	row := ui.ButtonBase(c).Row().Padding(5, frostui.SpaceS).Radius(6).Cursor(ui.CursorPointer).Label(pg.Title).AccessSelected(sel)
	switch {
	case sel:
		row.Background(t.Accent.Alpha(0.16))
	case row.Hovered():
		row.Background(t.SurfaceHover)
	}
	if row.Clicked() {
		a.readWikiPage(pg.Path)
	}
	row.Children(func() {
		if sel {
			frostui.Strong(c, pg.Title).Wrap().Grow(1)
		} else {
			frostui.Body(c, pg.Title).Wrap().Grow(1)
		}
	})
}

// wikiContext is the inspector beside the wiki: where the pages come from.
func (a *App) wikiContext(c *ui.Context) {
	frostui.SectionHeader(c, frostui.SectionHead{Title: "Wiki", Quiet: true})
	if w := a.wiki.data; w != nil {
		facts := []frostui.KeyValue{{Key: "Pages", Value: plural(len(w.Pages), "page", "pages")}}
		if w.Head.Commit != "" {
			commit := w.Head.Commit + " · " + w.Head.Date
			if w.Head.Dirty {
				commit += " · uncommitted edits"
			}
			facts = append(facts, frostui.KeyValue{Key: "Checkout", Value: commit, Mono: true})
		}
		frostui.KeyValues(c, facts, frostui.KeyValuesStyle{KeyWidth: 72})
	}
	frostui.Caption(c, "Read-only, live from the wiki's checkout on the server. Pages change here as they are edited.").Wrap()
	if ui.Button(c, "Read again").Clicked() {
		a.loadWiki()
		if a.wiki.sel != "" {
			a.readWikiPage(a.wiki.sel)
		}
	}
}
