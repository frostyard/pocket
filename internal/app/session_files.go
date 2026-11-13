package app

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/png"
	"path"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type sessionFiles struct {
	session                                      int
	open, loading, previewing, busy              bool
	tab, filter, address, selected, selectedFile string
	err, previewErr                              string
	changes                                      *pocket.Changes
	files                                        *pocket.FileList
	preview                                      *pocket.FileView
	image                                        ui.ImageSource
	diff                                         frostui.DiffView
	isDiff                                       bool
	seq                                          int
	list                                         ui.ListState
}

func (a *App) openSessionFiles() {
	if a.current == 0 || !a.canSteer() {
		return
	}
	a.files = &sessionFiles{session: a.current, open: true, tab: "Changes"}
	a.loadSessionFiles()
}
func (a *App) loadSessionFiles() {
	s := a.files
	if s == nil || !s.open || s.loading || !a.canSteer() {
		return
	}
	s.loading = true
	tab := s.tab
	since := ""
	if s.files != nil {
		since = s.files.Version
	}
	var changes *pocket.Changes
	var files *pocket.FileList
	a.request("Reading session files", func(ctx context.Context, c *pocket.Client) error {
		var err error
		if tab == "Changes" {
			changes, err = c.Changes(ctx, s.session)
		} else {
			files, err = c.Files(ctx, s.session, since)
		}
		return err
	}, func(err error) {
		if a.files != s {
			return
		}
		s.loading = false
		if tab != s.tab {
			a.loadSessionFiles()
			return
		}
		s.err = ""
		if err != nil {
			s.err = err.Error()
			return
		}
		if tab == "Changes" {
			s.changes = changes
		} else if !files.Same {
			s.files = files
		}
	})
}
func (a *App) previewSessionFile(selected, file string, diff bool) {
	s := a.files
	if s == nil || !s.open || !a.canSteer() {
		return
	}
	s.seq++
	seq := s.seq
	s.selected, s.selectedFile, s.isDiff = selected, file, diff
	s.preview, s.image, s.previewErr = nil, nil, ""
	s.diff.SetPatch("")
	s.previewing = true
	var patch string
	var view *pocket.FileView
	var img ui.ImageSource
	a.request("Reading file", func(ctx context.Context, c *pocket.Client) error {
		var err error
		if diff {
			patch, err = c.ChangeDiff(ctx, s.session, selected)
			return err
		}
		view, err = c.ViewFile(ctx, s.session, file)
		if err == nil && view.Kind == "image" {
			var data []byte
			data, err = c.FileImage(ctx, s.session, view.Path)
			if err == nil {
				img, err = decodeFileImage(data)
			}
		}
		return err
	}, func(err error) {
		if a.files != s || s.seq != seq {
			return
		}
		s.previewing = false
		s.preview = view
		s.image = img
		if err != nil {
			s.previewErr = err.Error()
			return
		}
		if diff {
			s.diff.SetPatch(patch)
		}
	})
}
func decodeFileImage(data []byte) (ui.ImageSource, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("image preview unavailable: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
		return nil, fmt.Errorf("image preview exceeds 16 million pixels")
	}
	return ui.DecodeImage(data)
}
func (a *App) confirmRevert(file pocket.ChangedFile) {
	s, client := a.files, a.link.client
	if s == nil || s.busy || !a.canSteer() || file.Kind == "renamed" {
		return
	}
	body := "Discard all uncommitted edits to " + file.Path + " and restore its last committed content? This cannot be undone."
	if file.Kind == "new" || file.Kind == "added" {
		body = "Delete the new file " + file.Path + "? This cannot be undone."
	}
	a.dialogs.Ask(frostui.Confirm{Title: "Revert file?", Body: body, Action: "Revert file", Destructive: true, OnConfirm: func() {
		if a.files != s || !s.open || a.current != s.session || a.link.client != client || !a.canSteer() || s.busy {
			return
		}
		s.busy = true
		a.request("Reverting file", func(ctx context.Context, c *pocket.Client) error { return c.RevertChange(ctx, s.session, file.Path) }, func(err error) {
			if a.files != s {
				return
			}
			s.busy = false
			if err != nil {
				s.err = err.Error()
				return
			}
			s.seq++
			s.selected, s.selectedFile = "", ""
			s.preview, s.image = nil, nil
			s.previewing = false
			s.previewErr = ""
			s.diff.SetPatch("")
			a.loadSessionFiles()
		})
	}})
}
func (a *App) downloadSessionFile() {
	s := a.files
	if s == nil || s.busy || !a.canSteer() || s.selectedFile == "" {
		return
	}
	selected, save := s.selectedFile, a.opts.SaveFile
	if save == nil {
		s.err = "File saving is unavailable"
		return
	}
	s.busy = true
	var saved bool
	a.requestWithin(2*time.Minute, "Downloading file", func(ctx context.Context, c *pocket.Client) error {
		data, err := c.Download(ctx, s.session, selected)
		if err != nil {
			return err
		}
		saved, err = save(ctx, path.Base(strings.ReplaceAll(selected, "\\", "/")), data)
		return err
	}, func(err error) {
		if a.files != s {
			return
		}
		s.busy = false
		if err != nil {
			s.err = err.Error()
		} else if saved {
			a.toast("File saved")
		}
	})
}
func (s *sessionFiles) changedPath(file string) string {
	if s.changes != nil && s.changes.Repo != nil {
		return strings.TrimRight(s.changes.Repo.Root, "/\\") + "/" + file
	}
	return file
}
func (a *App) sessionFilesDialog(c *ui.Context) {
	s := a.files
	if s == nil || !s.open {
		return
	}
	ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(900).Height(640).Gap(frostui.SpaceS).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Gap(frostui.SpaceS).Children(func() {
				frostui.Strong(c, "Changes and files").Grow(1)
				for _, tab := range []string{"Changes", "Files"} {
					if ui.Button(c, tab).Selected(s.tab == tab).Clicked() {
						s.tab = tab
						s.filter = ""
						s.list.ScrollTo(0, ui.Start)
						a.loadSessionFiles()
					}
				}
				if ui.Button(c, "Refresh").Disabled(s.loading || s.busy).Clicked() {
					a.loadSessionFiles()
					if s.selected != "" {
						a.previewSessionFile(s.selected, s.selectedFile, s.isDiff)
					}
				}
				if ui.Button(c, "Close").Clicked() {
					s.open = false
				}
			})
			if s.err != "" {
				frostui.Problem(c, s.err).Wrap()
			}
			if s.loading {
				frostui.Caption(c, "Refreshing…")
			}
			ui.Row(c).Gap(frostui.SpaceM).Grow(1).Basis(0).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Width(260).Gap(frostui.SpaceS).AlignItems(ui.Stretch).Children(func() {
					ui.TextInput(c, &s.filter).Placeholder("Filter paths").Label("Filter files")
					if s.tab == "Changes" {
						a.changedFilesList(c, s)
					} else {
						a.sessionFileList(c, s)
					}
				})
				ui.Column(c).Grow(1).Basis(0).Gap(frostui.SpaceS).AlignItems(ui.Stretch).Children(func() {
					ui.Row(c).Gap(frostui.SpaceS).Children(func() {
						ui.TextInput(c, &s.address).Placeholder("Path in this session").Label("File path").Grow(1).Basis(0)
						if ui.Button(c, "Open path").Disabled(strings.TrimSpace(s.address) == "").Clicked() {
							file := strings.TrimSpace(s.address)
							a.previewSessionFile(file, file, false)
						}
					})
					a.sessionFilePreview(c, s)
				})
			})
		})
	})
}
func (a *App) changedFilesList(c *ui.Context, s *sessionFiles) {
	if s.changes == nil {
		return
	}
	if s.changes.Repo != nil {
		frostui.Caption(c, firstNonEmpty(s.changes.Repo.Branch, "Repository changes")).Wrap()
	} else {
		frostui.Caption(c, "No Git repository")
	}
	// One bounded ListState covers both tabs; switching resets its scroll position.
	type row struct {
		file   pocket.ChangedFile
		piOnly bool
	}
	var rows []row
	for _, file := range s.changes.Files {
		if strings.Contains(strings.ToLower(file.Path), strings.ToLower(s.filter)) {
			rows = append(rows, row{file: file})
		}
	}
	for _, file := range s.changes.PiOnly {
		if strings.Contains(strings.ToLower(file.Path), strings.ToLower(s.filter)) {
			rows = append(rows, row{file: pocket.ChangedFile{Path: file.Path, ByPi: true}, piOnly: true})
		}
	}
	if len(rows) == 0 {
		frostui.Muted(c, "No matching changes.").Wrap()
	}
	ui.List(c, &s.list, len(rows), func(i int) {
		row := rows[i]
		file := row.file
		ui.Column(c).Padding(frostui.SpaceS).Gap(2).AlignItems(ui.Stretch).Children(func() {
			if ui.Button(c, file.Path).Label("Review " + file.Path).Clicked() {
				target := s.changedPath(file.Path)
				if row.piOnly {
					target = file.Path
				}
				a.previewSessionFile(file.Path, target, !row.piOnly)
			}
			detail := file.Kind
			if row.piOnly {
				detail = "Edited in this session"
			} else if file.ByPi {
				detail += " · edited here"
			}
			if file.Added != nil {
				detail += fmt.Sprintf(" · +%d", *file.Added)
			}
			if file.Removed != nil {
				detail += fmt.Sprintf(" −%d", *file.Removed)
			}
			frostui.Caption(c, detail).Wrap()
		})
	}).Grow(1).Basis(0).Label("Changed files")
	if s.changes.More > 0 {
		frostui.Caption(c, fmt.Sprintf("%d more changes omitted", s.changes.More)).Wrap()
	}
}
func (a *App) sessionFileList(c *ui.Context, s *sessionFiles) {
	if s.files == nil {
		return
	}
	var files []string
	for _, file := range s.files.Files {
		if strings.Contains(strings.ToLower(file), strings.ToLower(s.filter)) {
			files = append(files, file)
		}
	}
	ui.List(c, &s.list, len(files), func(i int) {
		file := files[i]
		if ui.Button(c, file).Label("View " + file).Clicked() {
			a.previewSessionFile(file, file, false)
		}
	}).Grow(1).Basis(0).Label("Session files")
	if len(files) == 0 {
		frostui.Muted(c, "No matching files.")
	}
	if s.files.Truncated {
		frostui.Caption(c, "More files exist; open a path to read one.").Wrap()
	}
}
func (a *App) sessionFilePreview(c *ui.Context, s *sessionFiles) {
	if s.selected == "" {
		frostui.Muted(c, "Choose a change or a file to read.").Wrap()
		return
	}
	frostui.Strong(c, s.selected).Wrap()
	ui.Row(c).Gap(frostui.SpaceS).Children(func() {
		if s.isDiff && ui.Button(c, "View file").Clicked() {
			a.previewSessionFile(s.selected, s.selectedFile, false)
		}
		if (s.preview == nil || (s.preview.Kind != "folder" && s.preview.Kind != "other")) && ui.Button(c, "Save a copy…").Disabled(s.busy || a.opts.SaveFile == nil).Clicked() {
			a.downloadSessionFile()
		}
		if s.changes != nil {
			for _, file := range s.changes.Files {
				if file.Path == s.selected && s.selectedFile == s.changedPath(file.Path) && file.Kind != "renamed" {
					if ui.Button(c, "Revert…").Disabled(s.busy || s.loading || !a.canSteer()).Clicked() {
						a.confirmRevert(file)
					}
				}
			}
		}
	})
	if s.previewing {
		frostui.Loading(c, "Reading…")
		return
	}
	if s.previewErr != "" {
		frostui.Problem(c, s.previewErr).Wrap()
		return
	}
	if s.isDiff {
		s.diff.View(c).Grow(1).Basis(0)
		return
	}
	view := s.preview
	if view == nil {
		return
	}
	if view.Truncated {
		frostui.Caption(c, "Preview truncated by the server.").Wrap()
	}
	switch view.Kind {
	case "text":
		ui.Scroll(c).Grow(1).Basis(0).Children(func() { frostui.Mono(c, view.Text).Wrap().Selectable() })
	case "image":
		if s.image != nil {
			ui.Image(c, s.image).Fit(ui.ScaleDown).Grow(1)
		}
	case "folder":
		ui.Scroll(c).Grow(1).Basis(0).Children(func() {
			ui.Column(c).AlignItems(ui.Stretch).Children(func() {
				for _, entry := range view.Entries {
					name := entry.Name
					if entry.Dir {
						name += "/"
					}
					if ui.Button(c, name).Clicked() {
						target := strings.TrimRight(view.Path, "/\\") + "/" + entry.Name
						a.previewSessionFile(target, target, false)
					}
				}
			})
		})
	case "binary":
		frostui.Muted(c, fmt.Sprintf("Binary file · %d bytes. Save a copy to open it locally.", view.Size)).Wrap()
	default:
		frostui.Muted(c, "Preview unavailable for this file type.").Wrap()
	}
}
