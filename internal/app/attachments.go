package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

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
	cancel    context.CancelFunc
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
		a.uploadSource(id, filepath.Base(path), func() (io.ReadCloser, int64, error) {
			f, err := openUploadFile(path)
			if err != nil {
				return nil, 0, err
			}
			info, err := f.Stat()
			if err == nil && !info.Mode().IsRegular() {
				err = fmt.Errorf("choose a regular file")
			}
			if err == nil && info.Size() > pocket.MaxUpload {
				err = fmt.Errorf("files can be up to 50 MB")
			}
			if err != nil {
				_ = f.Close()
				return nil, 0, err
			}
			return f, info.Size(), nil
		})
	}
}

func (a *App) uploadAttachment(id int, name string, read func() ([]byte, error)) {
	a.uploadSource(id, name, func() (io.ReadCloser, int64, error) {
		data, err := read()
		return io.NopCloser(bytes.NewReader(data)), int64(len(data)), err
	})
}
func (a *App) uploadSource(id int, name string, open func() (io.ReadCloser, int64, error)) {
	if id == 0 || !a.canAttach(id) {
		return
	}
	cp := &a.composer
	cp.filesFor(id)
	life, cancel := context.WithCancel(context.Background())
	f := &composerFile{name: name, uploading: true, cancel: cancel}
	cp.files[id] = append(cp.files[id], f)
	var file *pocket.Attachment
	a.requestWithin(10*time.Minute, "Uploading "+name, func(parent context.Context, c *pocket.Client) error {
		ctx, stop := context.WithCancel(parent)
		defer stop()
		defer cancel()
		stopWatch := context.AfterFunc(life, stop)
		defer stopWatch()
		if life.Err() != nil {
			return life.Err()
		}
		reader, size, err := open()
		if err != nil {
			if reader != nil {
				_ = reader.Close()
			}
			return err
		}
		defer func() { _ = reader.Close() }()
		if size < 0 || size > pocket.MaxUpload {
			return fmt.Errorf("files can be up to 50 MB")
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		typ := mime.TypeByExtension(filepath.Ext(name))
		var body io.Reader = reader
		if typ == "" {
			head := make([]byte, min(size, 512))
			n, readErr := io.ReadFull(reader, head)
			if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
				return readErr
			}
			head = head[:n]
			typ = http.DetectContentType(head)
			body = io.MultiReader(bytes.NewReader(head), reader)
		}
		file, err = c.UploadReader(ctx, id, name, typ, body, size)
		return err
	}, func(err error) {
		if !slices.Contains(cp.files[id], f) {
			return
		}
		f.uploading = false
		f.cancel = nil
		if err != nil {
			f.err = err.Error()
			return
		}
		f.file = *file
	})
}

var remoteWorkspace = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]+://`)

func (a *App) canAttach(id int) bool {
	if !a.canSteer() {
		return false
	}
	cwd := ""
	if a.view != nil && id == a.current {
		cwd = a.view.Conversation.Cwd
	} else if session := a.byID[id]; session != nil {
		cwd = session.Cwd
	}
	return !remoteWorkspace.MatchString(cwd)
}
func (a *App) queueBlocked() bool {
	return a.hello != nil && a.hello.Server.Collab >= 2 && a.view != nil && a.view.Turns.On && a.view.Turns.Driver != a.hello.User.ID
}

func (a *App) pasteAttachments(id int, data transfer.Data) bool {
	if !a.canAttach(id) {
		a.toast("Attachments require a writable local session")
		return false
	}
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
	if a.opts.PickFiles == nil || a.composer.picking || !a.canAttach(a.current) {
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
					if f.cancel != nil {
						f.cancel()
					}
					cp.files[a.current] = slices.DeleteFunc(cp.files[a.current], func(each *composerFile) bool { return each == f })
				}
			})
		}
	})
}

// queueKey names one queued message: a submission in a session, or a held
// message's key. Identity, never text: two messages can read the same.
type queueKey struct {
	session    int
	submission int
	held       string
}

// queuedText is the queued message shown for submission, or for held key.
func (a *App) queuedText(submission int, held string) string {
	if a.view == nil {
		return ""
	}
	if held != "" {
		for _, q := range a.view.Held {
			if q.Key == held {
				return q.Text
			}
		}
		return ""
	}
	for _, q := range a.view.Inbox {
		if q.ID == submission {
			return q.Text
		}
	}
	return ""
}

func (a *App) queueAction(submission int, held, action string) {
	cp := &a.composer
	if cp.queueBusy || !a.canSteer() {
		return
	}
	id := a.current
	// Keep the displayed text before Edit can remove it on the server. Even a
	// timeout or sign-out must leave this local recovery copy available, so the
	// key holds it: a retried Edit of the same message copies it only once.
	key := queueKey{session: id, submission: submission, held: held}
	recovered := ""
	if action == "edit" {
		if text, ok := cp.queueRecovered[key]; ok {
			recovered = text
		} else if text := a.queuedText(submission, held); text != "" {
			if cp.queueRecovered == nil {
				cp.queueRecovered = map[queueKey]string{}
			}
			cp.queueRecovered[key] = text
			recovered = text
			cp.restoreDraft(id, text, nil)
		}
	}
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
		if action == "edit" {
			// The message is in the draft now: a later Edit of it starts over.
			delete(cp.queueRecovered, key)
		}
		if action == "edit" && back != nil {
			var files []*composerFile
			for _, f := range back.Attachments {
				files = append(files, &composerFile{name: f.Name, file: f})
			}
			text := back.Text
			if text == recovered {
				text = ""
			}
			cp.restoreDraft(id, text, files)
			a.toast(fmt.Sprintf("Message returned to session %d's draft", id))
		}
	})
}

func (a *App) queueSection(c *ui.Context) {
	cp := &a.composer
	if len(a.view.Inbox) > 0 {
		frostui.SectionHeader(c, frostui.SectionHead{Title: "Queued", Aside: fmt.Sprint(len(a.view.Inbox)), Quiet: true})
		for _, q := range a.view.Inbox {
			ui.Column(c.Key(q.ID)).Gap(frostui.SpaceXS).AlignItems(ui.Stretch).Children(func() {
				mode := firstNonEmpty(map[string]string{"steer": "Steer", "followUp": "Queued", "write": "Sending", "interrupt": "Interrupt"}[q.Mode], "Queued")
				frostui.Muted(c, mode+" · "+firstNonEmpty(q.Label, q.Text)).Wrap()
				if !a.canSteer() {
					return
				}
				ui.Row(c).Gap(frostui.SpaceS).Children(func() {
					if q.By == a.hello.User.ID && q.Mode != "write" && !a.queueBlocked() {
						if ui.Button(c, "Edit").Disabled(cp.queueBusy).Label(fmt.Sprintf("Edit queued %d", q.ID)).Clicked() {
							a.queueAction(q.ID, "", "edit")
						}
					}
					if (!a.queueBlocked() || q.By == a.hello.User.ID) && ui.Button(c, "Withdraw").Disabled(cp.queueBusy).Label(fmt.Sprintf("Withdraw queued %d", q.ID)).Clicked() {
						a.queueAction(q.ID, "", "drop")
					}
				})
			})
		}
	}
	if len(a.view.Held) > 0 {
		frostui.SectionHeader(c, frostui.SectionHead{Title: "Held messages", Quiet: true})
		if a.canSteer() && !a.queueBlocked() && ui.Button(c, "Send held messages").Disabled(cp.queueBusy).Clicked() {
			a.queueAction(0, "", "send")
		}
		for _, q := range a.view.Held {
			ui.Column(c.Key(q.Key)).Gap(frostui.SpaceXS).Children(func() {
				frostui.Muted(c, strings.TrimSpace(q.Text)).Wrap()
				if !a.canSteer() {
					return
				}
				ui.Row(c).Gap(frostui.SpaceS).Children(func() {
					if q.By == a.hello.User.ID && !a.queueBlocked() && ui.Button(c, "Edit").Label("Edit held "+q.Key).Disabled(cp.queueBusy).Clicked() {
						a.queueAction(0, q.Key, "edit")
					}
					if (!a.queueBlocked() || q.By == a.hello.User.ID) && ui.Button(c, "Discard").Label("Discard held "+q.Key).Disabled(cp.queueBusy).Clicked() {
						a.queueAction(0, q.Key, "drop")
					}
				})
			})
		}
	}
}
