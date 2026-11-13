package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

func TestComposerDropPasteAndRemove(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	received := make(chan string, 2)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/upload") {
			data, _ := io.ReadAll(r.Body)
			received <- r.URL.Query().Get("name") + ":" + string(data)
			return reply(r, `{"path":"/uploads/file","name":"file","mime":"text/plain","size":4}`), nil
		}
		return snapshotHTTP{}.RoundTrip(r)
	})}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.composerView(c) }, 850, 300, ui.TesterOptions{})
	tt.Frame()
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("text"), 0600); err != nil {
		t.Fatal(err)
	}
	r, ok := tt.Find("Message")
	if !ok {
		t.Fatal("composer absent")
	}
	if !tt.DropFiles(r.X+r.W/2, r.Y+r.H/2, path) {
		t.Fatal("drop not accepted")
	}
	loop.RunUntil(t, func() bool { return len(a.composer.files[7]) == 1 && !a.composer.files[7][0].uploading })
	if got := <-received; got != "notes.txt:text" {
		t.Fatalf("upload %q", got)
	}
	tt.SetClipboardData(transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, []byte("png bytes")))))
	if err := tt.Click("Message"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyV)
	loop.RunUntil(t, func() bool { return len(a.composer.files[7]) == 2 && !a.composer.files[7][1].uploading })
	if got := <-received; got != "pasted-image.png:png bytes" {
		t.Fatalf("paste upload %q", got)
	}
	tt.Frame()
	writePNG(t, tt, "composer-attachments")
	if err := tt.Click("Remove attachment notes.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.composer.files[7]) != 1 || a.composer.files[7][0].name != "pasted-image.png" {
		t.Fatal("chip removal lost wrong file")
	}
}

func TestRemovedUploadCannotReturnAndSendWaits(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	entered, release := make(chan struct{}), make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/upload") {
			t.Errorf("sent before upload finished: %s", r.URL)
		}
		close(entered)
		<-release
		return reply(r, `{"path":"/uploads/example","name":"example.txt","size":4}`), nil
	})}
	a.uploadAttachment(7, "example.txt", func() ([]byte, error) { return []byte("text"), nil })
	<-entered
	a.composer.draft = "keep this draft"
	a.send(a.composer.draft, "")
	if a.composer.sending || a.composer.draft != "keep this draft" {
		t.Fatal("send consumed a pending upload")
	}
	a.composer.files[7] = nil
	before := loop.Ran()
	close(release)
	loop.RunUntil(t, func() bool { return loop.Ran() > before })
	if len(a.composer.files[7]) != 0 {
		t.Fatal("removed upload came back")
	}
}

func TestFailedAttachmentSendRestoresOriginalSessionDraft(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	a.composer.filesFor(7)
	a.composer.files[7] = []*composerFile{{name: "example.txt", file: pocket.Attachment{Path: "/uploads/example", Name: "example.txt"}}}
	a.composer.draft = "inspect this"
	entered, release := make(chan struct{}), make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		var body struct {
			Text        string              `json:"text"`
			Attachments []pocket.Attachment `json:"attachments"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/api/c/7/submit" || body.Text != "inspect this" || len(body.Attachments) != 1 || body.Attachments[0].Path != "/uploads/example" {
			t.Errorf("wrong submitted draft %s %+v", r.URL, body)
		}
		close(entered)
		<-release
		resp := reply(r, `{"error":"Try later"}`)
		resp.StatusCode = 503
		return resp, nil
	})}
	a.send(a.composer.draft, "")
	<-entered
	a.current = 8
	a.composer.reset(8)
	a.composer.draft = "other session"
	close(release)
	loop.RunUntil(t, func() bool { return !a.composer.sending })
	if a.composer.draft != "other session" || len(a.composer.files[8]) != 0 {
		t.Fatal("failure contaminated current session")
	}
	a.composer.reset(7)
	if a.composer.draft != "inspect this" || len(a.composer.files[7]) != 1 {
		t.Fatal("failure lost original draft")
	}
	a.composer.resetRequests()
	if len(a.composer.files) != 0 {
		t.Fatal("remote file references survived sign-out")
	}
}

func TestQueueEditRestoresTextAndAttachmentsAfterNavigation(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	a.composer.draft = "existing draft"
	entered, release := make(chan struct{}), make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/c/7/edit-queued" {
			t.Errorf("wrong queue route %s", r.URL)
		}
		close(entered)
		<-release
		return reply(r, `{"text":"returned message","attachments":[{"path":"/uploads/example","name":"example.txt"}]}`), nil
	})}
	a.queueAction(42, "", "edit")
	<-entered
	a.queueAction(42, "", "edit")
	a.current = 8
	a.composer.reset(8)
	a.composer.draft = "different draft"
	close(release)
	loop.RunUntil(t, func() bool { return !a.composer.queueBusy })
	if a.composer.draft != "different draft" {
		t.Fatal("late edit overwrote another session")
	}
	a.composer.reset(7)
	if a.composer.draft != "returned message\n\nexisting draft" || len(a.composer.files[7]) != 1 {
		t.Fatal("returned draft was lost")
	}
}

func TestQueueControlsRespectSenderAndViewer(t *testing.T) {
	a, _ := teamApp(t)
	a.view.Inbox = []pocket.QueueRow{{ID: 42, By: "u1", Mode: "followUp", Text: "Check the tests"}, {ID: 43, By: "u2", Mode: "followUp", Text: "Another person's message"}}
	a.view.Held = []pocket.HeldMessage{{Key: "held1", By: "u1", Mode: "followUp", Text: "Continue the review"}}
	tt := ui.NewTesterWith(func(c *ui.Context) {
		frostui.Adapt(c)
		ui.Column(c).Padding(16).Gap(10).AlignItems(ui.Stretch).Children(func() { a.queueSection(c) })
	}, 360, 500, ui.TesterOptions{})
	tt.Frame()
	if _, ok := tt.Find("Edit queued 42"); !ok {
		t.Fatal("own message cannot be edited")
	}
	if _, ok := tt.Find("Edit queued 43"); ok {
		t.Fatal("another sender's message can be edited")
	}
	writePNG(t, tt, "queue-controls")
	a.hello.User.Role = "viewer"
	tt.Frame()
	if tt.HasText("Edit") || tt.HasText("Withdraw") || tt.HasText("Send held messages") {
		t.Fatal("viewer offered mutations")
	}
}

func TestAttachmentSizeLimitBeforeUpload(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	path := filepath.Join(t.TempDir(), "large.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(pocket.MaxUpload + 1); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		t.Errorf("oversized file reached %s", r.URL)
		return reply(r, `{}`), nil
	})}
	a.attachFiles(7, []string{path})
	loop.RunUntil(t, func() bool { return !a.composer.files[7][0].uploading })
	if a.composer.files[7][0].err == "" || a.composer.filesReady() {
		t.Fatal("oversized file became sendable")
	}
}

func TestPostedComposerSendKeepsOriginalSession(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
		t.Errorf("send moved to %s", r.URL)
		return reply(r, `{}`), nil
	})}
	a.postComposerSend("original message", "")
	a.current = 8
	a.composer.reset(8)
	a.composer.draft = "another draft"
	loop.RunUntil(t, func() bool { return a.composer.drafts[7] == "original message" })
	if a.composer.draft != "another draft" {
		t.Fatal("navigation lost active draft")
	}
}

func TestUploadCancellationByRemoveAndSignOut(t *testing.T) {
	for _, remove := range []bool{true, false} {
		t.Run(map[bool]string{true: "remove", false: "sign-out"}[remove], func(t *testing.T) {
			a, loop := teamApp(t)
			a.current = 7
			a.composer.reset(7)
			entered, cancelled := make(chan struct{}), make(chan struct{})
			a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
				if r.ContentLength != 10<<20 {
					t.Errorf("content length %d", r.ContentLength)
				}
				close(entered)
				<-r.Context().Done()
				close(cancelled)
				return nil, r.Context().Err()
			})}
			// A sparse file exercises the streaming path without allocating its body.
			path := filepath.Join(t.TempDir(), "large.bin")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.Truncate(10 << 20); err != nil {
				t.Fatal(err)
			}
			_ = f.Close()
			a.attachFiles(7, []string{path})
			<-entered
			before := loop.Ran()
			if remove {
				tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.attachmentChips(c) }, 600, 100, ui.TesterOptions{})
				tt.Frame()
				if err = tt.Click("Remove attachment large.bin"); err != nil {
					t.Fatal(err)
				}
			} else {
				a.link.client = nil
				a.reset()
			}
			<-cancelled
			loop.RunUntil(t, func() bool { return loop.Ran() > before })
			if len(a.composer.files[7]) != 0 {
				t.Fatal("cancelled upload returned")
			}
		})
	}
}

func TestQueuedEditKeepsTextAfterFailureOrSignOut(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, signedOut := range []bool{false, true} {
			t.Run(fmt.Sprintf("held=%v/signout=%v", held, signedOut), func(t *testing.T) {
				a, loop := teamApp(t)
				a.current = 7
				a.composer.reset(7)
				a.composer.draft = "existing"
				a.view.Inbox = []pocket.QueueRow{{ID: 42, By: "u1", Text: "recover this"}}
				a.view.Held = []pocket.HeldMessage{{Key: "h1", By: "u1", Text: "recover this"}}
				entered, release := make(chan struct{}), make(chan struct{})
				a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) {
					close(entered)
					<-release
					resp := reply(r, `{"error":"conflict"}`)
					resp.StatusCode = 409
					return resp, nil
				})}
				key := ""
				if held {
					key = "h1"
				}
				a.queueAction(42, key, "edit")
				<-entered
				if a.composer.draft != "recover this\n\nexisting" {
					t.Fatal("text not retained before request")
				}
				if signedOut {
					key := a.account
					a.link.client = nil
					a.reset()
					// The text waits for the account it belongs to.
					if a.composer.kept[key].drafts[7] != "recover this\n\nexisting" {
						t.Fatal("sign-out lost the recovery copy")
					}
					a.composer.adopt(key)
					a.composer.reset(7)
				}
				before := loop.Ran()
				close(release)
				loop.RunUntil(t, func() bool { return loop.Ran() > before })
				if a.composer.draft != "recover this\n\nexisting" {
					t.Fatal("failed edit lost recovery copy")
				}
			})
		}
	}
}

func TestQueueDriverAndRemoteAttachmentControls(t *testing.T) {
	a, _ := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	a.hello.Server.Collab = 2
	a.view.Turns = pocket.Turns{On: true, Driver: "u2"}
	a.view.Inbox = []pocket.QueueRow{{ID: 42, By: "u1", Mode: "followUp", Text: "mine"}, {ID: 43, By: "u2", Mode: "followUp", Text: "theirs"}}
	a.view.Held = []pocket.HeldMessage{{Key: "h1", By: "u1", Text: "mine"}, {Key: "h2", By: "u2", Text: "theirs"}}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Column(c).Children(func() { a.queueSection(c) }) }, 500, 650, ui.TesterOptions{})
	tt.Frame()
	for _, label := range []string{"Edit queued 42", "Withdraw queued 43", "Edit held h1", "Edit held h2", "Discard held h2", "Send held messages"} {
		if _, ok := tt.Find(label); ok {
			t.Errorf("blocked control %s visible", label)
		}
	}
	for _, label := range []string{"Withdraw queued 42", "Discard held h1"} {
		if _, ok := tt.Find(label); !ok {
			t.Errorf("own withdrawal %s hidden", label)
		}
	}
	a.view.Conversation.Cwd = "ssh://example.org/work"
	if a.canAttach(7) {
		t.Fatal("remote upload allowed")
	}
	a.view.Conversation.Cwd = "/work"
	a.hello.User.Role = "viewer"
	if a.pasteAttachments(7, transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, []byte("png"))))) {
		t.Fatal("viewer paste consumed")
	}
	if len(a.pendingToasts) == 0 {
		t.Fatal("viewer paste silently ignored")
	}
}

func TestWithdrawAlreadySentReportsFailure(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.link.client.HTTP = &http.Client{Transport: sessionToolsHTTP(func(r *http.Request) (*http.Response, error) { return reply(r, `{"result":"already-sent"}`), nil })}
	a.queueAction(42, "", "drop")
	loop.RunUntil(t, func() bool { return !a.composer.queueBusy })
	if len(a.pendingToasts) == 0 || !strings.Contains(a.pendingToasts[0], "already gone") {
		t.Fatal("withdraw result lost")
	}
}

func TestAttachmentDirectoryRejected(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	a.attachFiles(7, []string{t.TempDir()})
	loop.RunUntil(t, func() bool { return !a.composer.files[7][0].uploading })
	if !strings.Contains(a.composer.files[7][0].err, "regular file") {
		t.Fatal("directory accepted")
	}
}

// queueSteps answers the queue's requests in order, one step per request. A
// nil step fails the request at the transport, as a lost answer does.
type queueSteps struct {
	mu    sync.Mutex
	steps []func(*http.Request) (*http.Response, error)
}

func (s *queueSteps) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	var step func(*http.Request) (*http.Response, error)
	if len(s.steps) > 0 {
		step = s.steps[0]
		s.steps = s.steps[1:]
	}
	s.mu.Unlock()
	if step == nil {
		return nil, fmt.Errorf("connection reset by the server")
	}
	return step(r)
}

func queueConflict(r *http.Request) (*http.Response, error) {
	resp := reply(r, `{"error":"conflict"}`)
	resp.StatusCode = http.StatusConflict
	return resp, nil
}

func queueReturned(r *http.Request) (*http.Response, error) {
	return reply(r, `{"text":"queued message","attachments":[{"path":"/uploads/example","name":"example.txt"}]}`), nil
}

func TestQueuedEditRetryRecoversOneCopy(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			a, loop := teamApp(t)
			a.current = 7
			a.composer.reset(7)
			a.composer.draft = "existing draft"
			a.view.Inbox = []pocket.QueueRow{{ID: 42, By: "u1", Text: "queued message"}}
			a.view.Held = []pocket.HeldMessage{{Key: "h1", By: "u1", Text: "queued message"}}
			a.link.client.HTTP = &http.Client{Transport: &queueSteps{steps: []func(*http.Request) (*http.Response, error){
				queueConflict, queueConflict,
			}}}
			key := ""
			if held {
				key = "h1"
			}
			for range 2 {
				a.queueAction(42, key, "edit")
				loop.RunUntil(t, func() bool { return !a.composer.queueBusy })
			}
			if a.composer.draft != "queued message\n\nexisting draft" {
				t.Fatalf("a retried edit copied its message twice: %q", a.composer.draft)
			}
			a.link.client = nil
			a.reset()
			if len(a.composer.queueRecovered) != 0 {
				t.Fatal("sign-out kept a queue recovery marker")
			}
		})
	}
}

func TestQueuedEditRecoversEqualTextOfAnotherMessage(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	a.composer.draft = "existing draft"
	// Two messages read the same: identity, not text, decides what was copied.
	a.view.Inbox = []pocket.QueueRow{{ID: 42, By: "u1", Text: "same words"}, {ID: 43, By: "u1", Text: "same words"}}
	a.link.client.HTTP = &http.Client{Transport: &queueSteps{steps: []func(*http.Request) (*http.Response, error){
		queueConflict, queueConflict,
	}}}
	a.queueAction(42, "", "edit")
	loop.RunUntil(t, func() bool { return !a.composer.queueBusy })
	a.queueAction(43, "", "edit")
	loop.RunUntil(t, func() bool { return !a.composer.queueBusy })
	if a.composer.draft != "same words\n\nsame words\n\nexisting draft" {
		t.Fatalf("a message with the same text was treated as one already copied: %q", a.composer.draft)
	}
}

func TestQueuedEditAfterLostAnswerKeepsOneCopy(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	a.composer.draft = "existing draft"
	a.view.Inbox = []pocket.QueueRow{{ID: 42, By: "u1", Text: "queued message"}}
	// The first answer is lost; the server may have removed the message, so the
	// retry must copy it once and still take the attachments of its answer.
	a.link.client.HTTP = &http.Client{Transport: &queueSteps{steps: []func(*http.Request) (*http.Response, error){
		nil, queueReturned,
	}}}
	for range 2 {
		a.queueAction(42, "", "edit")
		loop.RunUntil(t, func() bool { return !a.composer.queueBusy })
	}
	if a.composer.draft != "queued message\n\nexisting draft" {
		t.Fatalf("the retried edit copied its message twice: %q", a.composer.draft)
	}
	if len(a.composer.files[7]) != 1 || a.composer.files[7][0].file.Path != "/uploads/example" {
		t.Fatal("the answer's attachments were not restored")
	}
	if len(a.composer.queueRecovered) != 0 {
		t.Fatal("the recovery marker survived the edit")
	}
}

// TestQueuedEditRetriesKeepDraftEditAndAttachmentsOnce walks one Edit through
// a conflict, an ambiguous failure and a success, with the person editing the
// recovered text between attempts: the retries add no second copy of the
// message, keep that edit and the attachment already in the composer, and add
// the answer's attachments exactly once.
func TestQueuedEditRetriesKeepDraftEditAndAttachmentsOnce(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			a, loop := teamApp(t)
			a.current = 7
			a.composer.reset(7)
			a.composer.draft = "existing draft"
			a.composer.filesFor(7)
			a.composer.files[7] = []*composerFile{{name: "earlier.txt", file: pocket.Attachment{Path: "/uploads/earlier", Name: "earlier.txt"}}}
			a.view.Inbox = []pocket.QueueRow{{ID: 42, By: "u1", Text: "queued message"}}
			a.view.Held = []pocket.HeldMessage{{Key: "h1", By: "u1", Text: "queued message"}}
			a.link.client.HTTP = &http.Client{Transport: &queueSteps{steps: []func(*http.Request) (*http.Response, error){
				queueConflict, nil, queueReturned,
			}}}
			key := ""
			if held {
				key = "h1"
			}
			a.queueAction(42, key, "edit")
			loop.RunUntil(t, func() bool { return !a.composer.queueBusy })
			// The person writes on in the recovered text between attempts.
			a.composer.draft += " more"
			a.queueAction(42, key, "edit")
			loop.RunUntil(t, func() bool { return !a.composer.queueBusy })
			a.queueAction(42, key, "edit")
			loop.RunUntil(t, func() bool { return !a.composer.queueBusy })
			if a.composer.draft != "queued message\n\nexisting draft more" {
				t.Fatalf("the retries copied the message again or lost the person's edit: %q", a.composer.draft)
			}
			files := a.composer.files[7]
			if len(files) != 2 || files[0].file.Path != "/uploads/example" || files[1].file.Path != "/uploads/earlier" {
				t.Fatalf("attachments after the retries: %+v", files)
			}
			if len(a.composer.queueRecovered) != 0 {
				t.Fatalf("the recovery marker survived the edit: %v", a.composer.queueRecovered)
			}
		})
	}
}
