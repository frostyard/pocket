package app

import (
	"context"
	"regexp"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

var deskSegment = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

// deskPath matches desk-web/ask-text.js and the server's file route.
func deskPath(path string) bool {
	if len(path) == 0 || len(path) > 200 || !strings.HasSuffix(path, ".md") {
		return false
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return false
	}
	switch parts[0] {
	case "reports", "desk", "plans", "decisions":
	default:
		return false
	}
	for _, part := range parts {
		if !deskSegment.MatchString(part) {
			return false
		}
	}
	return true
}

func deskPathRefs(text string) []artifactRef {
	return textRefs(text, func(word string) string {
		if deskPath(word) {
			return word
		}
		return ""
	})
}

func linkDeskPaths(text string) string {
	var out strings.Builder
	last := 0
	for _, ref := range deskPathRefs(text) {
		out.WriteString(text[last:ref.start])
		if ref.linked {
			out.WriteString("deskfile:" + ref.id)
		} else {
			out.WriteString("[" + text[ref.start:ref.end] + "](deskfile:" + ref.id + ")")
		}
		last = ref.end
	}
	out.WriteString(text[last:])
	return out.String()
}

func (a *App) deskFileButtons(c *ui.Context, text string) {
	seen := map[string]bool{}
	for _, ref := range deskPathRefs(text) {
		if seen[ref.id] {
			continue
		}
		seen[ref.id] = true
		if ui.Button(c.Key(ref.id), truncate(ref.id, 34)).Tooltip("Read " + ref.id).Clicked() {
			a.openDeskFile(ref.id)
		}
	}
}

type deskFile struct {
	path, text    string
	open, loading bool
	md            markdown.Options
}

func (a *App) openDeskFile(path string) {
	if !deskPath(path) {
		a.toast("Not a Desk Markdown file path")
		return
	}
	f := &deskFile{path: path, open: true, loading: true, md: markdown.Options{OnLink: a.followLink}}
	a.deskFile = f
	client := a.link.client
	var text string
	a.request("Reading Desk file", func(ctx context.Context, c *pocket.Client) error {
		var err error
		text, err = c.DeskFile(ctx, path)
		return err
	}, func(err error) {
		if a.deskFile != f || a.link.client != client {
			return
		}
		f.loading = false
		if err != nil {
			f.open = false
			a.toast("Could not read " + path + ": " + err.Error())
			return
		}
		f.text = text
	})
}

func (a *App) deskFileDialog(c *ui.Context) {
	f := a.deskFile
	if f == nil || !f.open {
		return
	}
	ui.Modal(c, &f.open, func() {
		ui.Column(c).Width(680).Gap(frostui.SpaceM).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
				frostui.Strong(c, f.path).SingleLine().Tooltip(f.path).Grow(1)
				if ui.Button(c, "Close").Clicked() {
					f.open = false
				}
			})
			ui.Scroll(c).Height(460).Children(func() {
				if f.loading {
					frostui.Muted(c, "Reading…")
					return
				}
				// Only .md is currently accepted by the server. Keep image loading nil.
				markdown.View(c, linkDeskPaths(f.text), &f.md)
			})
		})
	})
}
