package app

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/pocket"
)

// WebPane shows a web page inside the window: an artifact's sandboxed page,
// signed in as the person. The program makes it where the system has a web
// view; without one, artifacts open in the browser.
type WebPane interface {
	// View is the web view to place with ui.WebView.
	View() ui.NativeWebView
	// Show loads url, signed in to server with token.
	Show(url, server, token string) error
	// SignOut deletes every sign-in cookie Show set and loads about:blank.
	// It is safe when nothing is signed in.
	SignOut()
}

// artifacts is the Artifacts place's state.
type artifacts struct {
	list           []pocket.Artifact
	dirty          bool
	refreshPending bool
	loading        bool
	loaded         bool
	err            string
	sel            string
	filter         string
	// svgs are the SVG artifacts read, by key and version.
	svgs     map[string]*ui.SVG
	fetching map[string]bool
	// shown is the page the web view shows.
	shown string
	// then runs once the list under way is read: what waits for it.
	then []func()
}

func artifactKey(x *pocket.Artifact) string {
	return strconv.Itoa(x.Session) + "/" + x.ID
}

// signOutWeb signs the web view out: it deletes the sign-in cookie it set
// and blanks the page, so that no artifact page stays open, or stays signed
// in, after the person signs out or changes server. The next preview shows
// its page again.
func (a *App) signOutWeb() {
	if a.opts.Web != nil {
		a.opts.Web.SignOut()
	}
	a.arts.shown = ""
}

// loadArtifacts reads the artifacts of every session.
func (a *App) loadArtifacts() {
	if a.arts.loading {
		a.arts.dirty = true
		return
	}
	a.arts.loading = true
	var list []pocket.Artifact
	a.request("Reading the artifacts", func(ctx context.Context, c *pocket.Client) error {
		var err error
		list, err = c.Artifacts(ctx)
		return err
	}, func(err error) {
		a.arts.loading, a.arts.loaded = false, true
		defer func() {
			if a.arts.dirty {
				a.arts.dirty = false
				a.loadArtifacts()
			}
			a.scheduleArtifactRefresh()
		}()
		then := a.arts.then
		a.arts.then = nil
		defer func() {
			for _, fn := range then {
				fn()
			}
		}()
		if err != nil {
			a.arts.err = err.Error()
			return
		}
		a.arts.err = ""
		slices.SortStableFunc(list, func(x, y pocket.Artifact) int { return int(y.UpdatedAt - x.UpdatedAt) })
		a.arts.list = list
		if a.arts.sel == "" && len(list) > 0 {
			a.arts.sel = artifactKey(&list[0])
		}
	})
}

// scheduleArtifactRefresh keeps an open list current without polling hidden pages.
func (a *App) scheduleArtifactRefresh() {
	if a.selected != "x:artifacts" || a.arts.refreshPending || a.link.client == nil {
		return
	}
	client := a.link.client
	a.arts.refreshPending = true
	a.after(time.Minute, func() {
		if a.link.client != client {
			return
		}
		a.arts.refreshPending = false
		if a.selected == "x:artifacts" {
			a.loadArtifacts()
		}
	})
}

// followLink follows a link in Markdown Pocket shows: an artifact
// reference opens the artifact, a web or mail address the browser.
func (a *App) followLink(dest string) {
	switch {
	case strings.HasPrefix(dest, "deskfile:"):
		a.openDeskFile(strings.TrimPrefix(dest, "deskfile:"))
	case strings.HasPrefix(dest, "artifact:"):
		id, hint, _ := strings.Cut(strings.TrimPrefix(dest, "artifact:"), "#")
		var prefer []int
		if n, err := strconv.Atoi(hint); err == nil {
			prefer = append(prefer, n)
		}
		a.openArtifactRef(id, prefer)
	case strings.HasPrefix(dest, "https://"), strings.HasPrefix(dest, "http://"), strings.HasPrefix(dest, "mailto:"):
		a.openURL(dest)
	}
}

// findArtifact returns the artifact of an id: in the first session of
// prefer that has one, else the newest.
func (a *App) findArtifact(id string, prefer []int) *pocket.Artifact {
	var newest *pocket.Artifact
	for _, session := range prefer {
		for i := range a.arts.list {
			if x := &a.arts.list[i]; x.ID == id && x.Session == session {
				return x
			}
		}
	}
	for i := range a.arts.list {
		if x := &a.arts.list[i]; x.ID == id && newest == nil {
			newest = x
		}
	}
	return newest
}

// openArtifactRef opens the artifact a reference names in the Artifacts
// place, reading the list again when the artifact is not in it yet.
func (a *App) openArtifactRef(id string, prefer []int) {
	if !a.arts.loaded {
		a.afterArtifacts(func() { a.showArtifactRef(id, prefer, false) })
		return
	}
	a.showArtifactRef(id, prefer, true)
}

func (a *App) showArtifactRef(id string, prefer []int, retry bool) {
	if x := a.findArtifact(id, prefer); x != nil {
		a.goPlace("x:artifacts")
		a.arts.sel = artifactKey(x)
		return
	}
	if retry {
		a.afterArtifacts(func() { a.showArtifactRef(id, prefer, false) })
		return
	}
	a.toast("No artifact " + id + " in the sessions you can see: its session may be archived")
}

// afterArtifacts reads the artifacts, then runs fn.
func (a *App) afterArtifacts(fn func()) {
	a.arts.then = append(a.arts.then, fn)
	a.loadArtifacts()
}

// artifactPrefer lists the sessions a thread's references are looked for
// in first: its own session, then its supervisor's.
func (a *App) artifactPrefer(t *pocket.DeskThread) []int {
	var prefer []int
	if t == nil {
		return nil
	}
	if s := a.sessionForThread(t); s != nil {
		prefer = append(prefer, s.ID)
	}
	if t.Supervisor != 0 {
		prefer = append(prefer, t.Supervisor)
	}
	return prefer
}

// artifactHint is the session a link in a thread's text names first.
func (a *App) artifactHint(t *pocket.DeskThread) string {
	if p := a.artifactPrefer(t); len(p) > 0 {
		return strconv.Itoa(p[0])
	}
	return ""
}

// artifactButtons gives each artifact a text references a button that
// opens it, titled as the artifact is once the list is read.
func (a *App) artifactButtons(c *ui.Context, ids []string, prefer []int) {
	if len(ids) == 0 {
		return
	}
	if !a.arts.loaded && !a.arts.loading {
		a.loadArtifacts()
	}
	ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
		for _, id := range ids {
			label := "artifact:" + id
			tip := "Open artifact:" + id
			if x := a.findArtifact(id, prefer); x != nil {
				label = x.Title
				tip = fmt.Sprintf("Open %s (%s, from %s)", x.Title, strings.ToUpper(x.Type), firstNonEmpty(x.SessionTitle, "a session"))
			}
			ref, want := id, prefer
			if frostui.LabeledButton(c.Key("art-"+id), artifactIcon(a.artifactType(id, prefer)), label, false).Tooltip(tip).Clicked() {
				a.openArtifactRef(ref, want)
			}
		}
	})
}

// artifactType is a referenced artifact's type, once known.
func (a *App) artifactType(id string, prefer []int) string {
	if x := a.findArtifact(id, prefer); x != nil {
		return x.Type
	}
	return ""
}

func (a *App) chosenArtifact() *pocket.Artifact {
	for i := range a.arts.list {
		if artifactKey(&a.arts.list[i]) == a.arts.sel {
			return &a.arts.list[i]
		}
	}
	return nil
}

// artifactsPane is the Artifacts place: every session's artifacts, newest
// first, and the one chosen.
func (a *App) artifactsPane(c *ui.Context) {
	t := c.Theme()
	if !a.arts.loaded && !a.arts.loading {
		a.loadArtifacts()
	}
	ui.Row(c).Grow(1).AlignItems(ui.Stretch).Background(t.Background).Children(func() {
		ui.Column(c).Width(340).AlignItems(ui.Stretch).Background(frostui.Chrome(t)).Children(func() {
			ui.Row(c).Padding(frostui.SpaceS).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
				ui.SearchField(c, &a.arts.filter).Placeholder("Filter artifacts").Label("Filter artifacts").Grow(1)
				if a.arts.loading {
					ui.Spinner(c).Size(14, 14)
				} else if frostui.IconButton(c, "view-refresh", "Read the artifacts again").Clicked() {
					a.loadArtifacts()
				}
			})
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).Padding(0, frostui.SpaceS, frostui.SpaceS, frostui.SpaceS).Gap(2).AlignItems(ui.Stretch).Children(func() {
					if a.arts.err != "" {
						frostui.Problem(c, a.arts.err)
					}
					q := strings.ToLower(strings.TrimSpace(a.arts.filter))
					shown := 0
					for i := range a.arts.list {
						x := &a.arts.list[i]
						if q != "" && !strings.Contains(strings.ToLower(x.Title+" "+x.SessionTitle+" "+x.Group), q) {
							continue
						}
						shown++
						a.artifactRow(c.Key(artifactKey(x)), x)
					}
					if a.arts.loaded && shown == 0 && a.arts.err == "" {
						frostui.Muted(c, "No artifacts yet. Pi makes them when you ask for a page, a document or a picture.").Wrap().Padding(frostui.SpaceM)
					}
				})
			})
		})
		ui.Box(c).Width(1).Background(t.Border)
		a.artifactPreview(c)
	})
}

func artifactIcon(kind string) string {
	switch kind {
	case "markdown":
		return "text-x-generic"
	case "html":
		return "pocket-diff"
	}
	return "pocket-artifact"
}

func (a *App) artifactRow(c *ui.Context, x *pocket.Artifact) {
	t := c.Theme()
	key := artifactKey(x)
	sel := key == a.arts.sel
	row := ui.ButtonBase(c).Row().Gap(frostui.SpaceS).Padding(6, frostui.SpaceS).Radius(6).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label(x.Title).AccessSelected(sel)
	switch {
	case sel:
		row.Background(t.Accent.Alpha(0.16))
	case row.Hovered():
		row.Background(t.SurfaceHover)
	}
	if row.Clicked() {
		a.arts.sel = key
	}
	row.Children(func() {
		frostui.Icon(c, artifactIcon(x.Type))
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			frostui.Strong(c, x.Title).SingleLine()
			frostui.Caption(c, firstNonEmpty(x.SessionTitle, "Session "+strconv.Itoa(x.Session))).SingleLine()
		})
		frostui.Caption(c, ago(a.serverNow(), x.UpdatedAt.Time()))
	})
}

// artifactPreview shows the chosen artifact: an SVG drawn here, a page in
// the web view, or a way to the browser.
func (a *App) artifactPreview(c *ui.Context) {
	x := a.chosenArtifact()
	ui.Column(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
		if x == nil {
			frostui.Empty(c, "pocket-artifact", "No artifact chosen", "Choose one at the left.").Grow(1)
			return
		}
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Padding(frostui.SpaceM, frostui.SpaceL).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				frostui.Strong(c, x.Title).SingleLine()
				meta := []string{strings.ToUpper(x.Type)}
				if x.Versions > 1 {
					meta = append(meta, fmt.Sprintf("version %d of %d", x.Current, x.Versions))
				}
				meta = append(meta, humanBytes(x.Size), clock(x.UpdatedAt.Time()))
				frostui.Caption(c, strings.Join(meta, " · ")).SingleLine()
			})
			if ui.Button(c, "Open the session").Clicked() {
				a.open(x.Session)
			}
			if ui.Button(c, "Open in browser").Clicked() && a.link.client != nil {
				a.openURL(a.link.client.ArtifactURL(x, 0))
			}
		})
		frostui.RowDivider(c)
		switch {
		case x.Type == "svg":
			a.svgPreview(c, x)
		case a.opts.Web != nil:
			a.webPreview(c, x)
		default:
			ui.Column(c).Grow(1).Children(func() {
				frostui.Empty(c, artifactIcon(x.Type), "Opens in your browser",
					"This system has no web view for Pocket to show the page in. Open in browser uses that browser's sign-in; you may need to sign in there.").Grow(1)
			})
		}
	})
}

func (a *App) svgPreview(c *ui.Context, x *pocket.Artifact) {
	key := artifactKey(x) + "@" + strconv.Itoa(x.Current)
	if a.arts.svgs == nil {
		a.arts.svgs, a.arts.fetching = map[string]*ui.SVG{}, map[string]bool{}
	}
	img := a.arts.svgs[key]
	if img == nil && !a.arts.fetching[key] {
		a.arts.fetching[key] = true
		art := *x
		var data []byte
		a.request("Reading "+x.Title, func(ctx context.Context, cl *pocket.Client) error {
			var err error
			data, err = cl.ArtifactBody(ctx, &art)
			return err
		}, func(err error) {
			delete(a.arts.fetching, key)
			if err != nil {
				return
			}
			if svg, perr := ui.ParseSVG(data); perr == nil {
				a.arts.svgs[key] = svg
			} else {
				a.toast("Drawing " + art.Title + ": " + perr.Error())
			}
		})
	}
	ui.Column(c).Grow(1).Padding(frostui.SpaceXL).AlignItems(ui.Center).Children(func() {
		if img == nil {
			frostui.Loading(c, "Drawing…")
			return
		}
		ui.Image(c, img).Fit(ui.ScaleDown).Grow(1).AlignSelf(ui.Stretch)
	})
}

func (a *App) webPreview(c *ui.Context, x *pocket.Artifact) {
	if a.link.client == nil {
		return
	}
	u := a.link.client.ArtifactURL(x, 0)
	if a.arts.shown != u {
		a.arts.shown = u
		if err := a.opts.Web.Show(u, a.link.client.Base.String(), a.link.client.Auth); err != nil {
			a.console.Add(frostui.LevelWarn, "artifacts", "Showing "+x.Title+": "+err.Error())
		}
	}
	if v := a.opts.Web.View(); v != nil {
		ui.WebView(c, v).Grow(1)
	}
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
