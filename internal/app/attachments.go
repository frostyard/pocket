package app

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type composerFile struct {
	name      string
	file      pocket.Attachment
	uploading bool
	err       string
}

func (cp *composer) filesFor(id int) []*composerFile {
	if cp.files == nil {
		cp.files = map[int][]*composerFile{}
	}
	return cp.files[id]
}
func (cp *composer) filesReady() bool {
	for _, f := range cp.filesFor(cp.session) {
		if f.uploading || f.err != "" {
			return false
		}
	}
	return true
}
func (cp *composer) restoreDraft(id int, text string, files []*composerFile) {
	if cp.drafts == nil {
		cp.drafts = map[int]string{}
	}
	draft := cp.drafts[id]
	if cp.session == id {
		draft = cp.draft
	}
	if text != "" {
		if draft != "" {
			draft = text + "\n\n" + draft
		} else {
			draft = text
		}
	}
	cp.drafts[id] = draft
	if cp.session == id {
		cp.draft = draft
	}
	cp.filesFor(id)
	cp.files[id] = append(files, cp.files[id]...)
}

func (a *App) attachFiles(id int, paths []string) {
	for _, path := range paths {
		a.uploadAttachment(id, filepath.Base(path), func() ([]byte, error) {
			info, err := os.Stat(path)
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("choose a regular file")
			}
			if info.Size() > pocket.MaxUpload {
				return nil, fmt.Errorf("files can be up to 50 MB")
			}
			f, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			defer func() { _ = f.Close() }()
			return io.ReadAll(io.LimitReader(f, pocket.MaxUpload+1))
		})
	}
}

func (a *App) uploadAttachment(id int, name string, read func() ([]byte, error)) {
	if id == 0 || !a.canSteer() {
		return
	}
	cp := &a.composer
	cp.filesFor(id)
	f := &composerFile{name: name, uploading: true}
	cp.files[id] = append(cp.files[id], f)
	var file *pocket.Attachment
	a.request("Uploading "+name, func(ctx context.Context, c *pocket.Client) error {
		data, err := read()
		if err != nil {
			return err
		}
		typ := mime.TypeByExtension(filepath.Ext(name))
		if typ == "" {
			typ = http.DetectContentType(data)
		}
		file, err = c.Upload(ctx, id, name, typ, data)
		return err
	}, func(err error) {
		if !slices.Contains(cp.files[id], f) {
			return
		}
		f.uploading = false
		if err != nil {
			f.err = err.Error()
			return
		}
		f.file = *file
	})
}

func (a *App) pasteAttachments(id int, data transfer.Data) bool {
	if paths, err := data.Files(); err == nil && len(paths) > 0 {
		a.attachFiles(id, paths)
		return true
	}
	if png, err := data.Read(transfer.PNG); err == nil {
		a.uploadAttachment(id, "pasted-image.png", func() ([]byte, error) { return png, nil })
		return true
	}
	return false
}

func (a *App) pickAttachments() {
	if a.opts.PickFiles == nil || a.composer.picking || !a.canSteer() {
		return
	}
	id := a.current
	pick := a.opts.PickFiles
	a.composer.picking = true
	var paths []string
	a.request("Choosing attachments", func(ctx context.Context, _ *pocket.Client) error {
		var err error
		paths, err = pick(ctx)
		return err
	}, func(err error) {
		a.composer.picking = false
		if err != nil {
			a.toast("Choosing attachments: " + err.Error())
			return
		}
		a.attachFiles(id, paths)
	})
}

func (a *App) attachmentChips(c *ui.Context) {
	cp := &a.composer
	ui.Row(c).Wrap().Gap(frostui.SpaceS).Children(func() {
		for _, f := range slices.Clone(cp.filesFor(a.current)) {
			ui.Row(c.Key(f)).Gap(frostui.SpaceXS).Padding(4, 8).Radius(6).Background(c.Theme().Surface).Children(func() {
				label := f.name
				if f.uploading {
					label += " · uploading…"
				}
				if f.err != "" {
					label += " · upload failed"
				}
				frostui.Caption(c, label).Tooltip(f.err)
				if ui.Button(c, "Remove").Label("Remove attachment " + f.name).Clicked() {
					cp.files[a.current] = slices.DeleteFunc(cp.files[a.current], func(each *composerFile) bool { return each == f })
				}
			})
		}
	})
}

func (a *App) queueAction(submission int, held, action string) {
	cp := &a.composer
	if cp.queueBusy || !a.canSteer() {
		return
	}
	id := a.current
	cp.queueBusy = true
	var back *pocket.MessageDraft
	a.request("Changing queued message", func(ctx context.Context, c *pocket.Client) error {
		var err error
		if held != "" || action == "send" {
			back, err = c.Held(ctx, id, action, held)
		} else if action == "edit" {
			back, err = c.EditQueued(ctx, id, submission)
		} else {
			var result string
			result, err = c.Withdraw(ctx, id, submission)
			if err == nil && result != "aborted" {
				err = fmt.Errorf("that message has already gone to Pi")
			}
		}
		return err
	}, func(err error) {
		cp.queueBusy = false
		if err != nil {
			a.toast("Queue action failed: " + err.Error())
			return
		}
		if action == "edit" && back != nil {
			var files []*composerFile
			for _, f := range back.Attachments {
				files = append(files, &composerFile{name: f.Name, file: f})
			}
			cp.restoreDraft(id, back.Text, files)
			a.toast(fmt.Sprintf("Message returned to session %d's draft", id))
		}
	})
}

func (a *App) queueSection(c *ui.Context) {
	cp := &a.composer
	if len(a.view.Inbox) > 0 {
		frostui.SectionHeader(c, frostui.SectionHead{Title: "Queued", Quiet: true})
		for _, q := range a.view.Inbox {
			ui.Column(c.Key(q.ID)).Gap(frostui.SpaceXS).AlignItems(ui.Stretch).Children(func() {
				mode := firstNonEmpty(map[string]string{"steer": "Steer", "followUp": "Queued", "write": "Sending", "interrupt": "Interrupt"}[q.Mode], "Queued")
				frostui.Muted(c, mode+" · "+firstNonEmpty(q.Label, q.Text)).Wrap()
				if !a.canSteer() {
					return
				}
				ui.Row(c).Gap(frostui.SpaceS).Children(func() {
					if q.By == a.hello.User.ID && q.Mode != "write" {
						if ui.Button(c, "Edit").Disabled(cp.queueBusy).Label(fmt.Sprintf("Edit queued %d", q.ID)).Clicked() {
							a.queueAction(q.ID, "", "edit")
						}
					}
					if ui.Button(c, "Withdraw").Disabled(cp.queueBusy).Label(fmt.Sprintf("Withdraw queued %d", q.ID)).Clicked() {
						a.queueAction(q.ID, "", "drop")
					}
				})
			})
		}
	}
	if len(a.view.Held) > 0 {
		frostui.SectionHeader(c, frostui.SectionHead{Title: "Held messages", Quiet: true})
		if a.canSteer() && ui.Button(c, "Send held messages").Disabled(cp.queueBusy).Clicked() {
			a.queueAction(0, "", "send")
		}
		for _, q := range a.view.Held {
			ui.Column(c.Key(q.Key)).Gap(frostui.SpaceXS).Children(func() {
				frostui.Muted(c, strings.TrimSpace(q.Text)).Wrap()
				if !a.canSteer() {
					return
				}
				ui.Row(c).Gap(frostui.SpaceS).Children(func() {
					if q.By == a.hello.User.ID && ui.Button(c, "Edit").Disabled(cp.queueBusy).Clicked() {
						a.queueAction(0, q.Key, "edit")
					}
					if ui.Button(c, "Discard").Disabled(cp.queueBusy).Clicked() {
						a.queueAction(0, q.Key, "drop")
					}
				})
			})
		}
	}
}
