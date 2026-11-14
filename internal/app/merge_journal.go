package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// The operation journal of Approve + merge (ADR-0008): 1 file,
// operations.json, in Pocket's config folder. Each attempt is written
// before each of its mutations, so a crash leaves a record that the next
// start reconciles by reads only. It holds no token, ever.

const (
	journalName = "operations.json"
	lockName    = "operations.lock"
	// corruptName marks a journal that did not parse: it holds the name the
	// file was kept as, and keeps the buttons off across restarts until the
	// owner starts a new journal.
	corruptName = "operations.corrupt"
	// journalVersion is the file's shape: 2 keeps 1 receipt per result; 1
	// kept 1 receipt per attempt, and is read and migrated.
	journalVersion = 2
	// journalKeep is how long an ended attempt with a settled receipt stays.
	journalKeep = 7 * 24 * time.Hour
)

// opOrigin is where the attempt's receipt goes: the server and user the
// window was signed in to, and the Desk thread of the PR.
type opOrigin struct {
	Server string `json:"server"`
	User   string `json:"user"`
	Thread string `json:"thread"`
}

// opIntent is the intent the confirm click froze, without the PR's title
// or anything secret.
type opIntent struct {
	Ref     string `json:"ref"`
	NodeID  string `json:"nodeId"`
	BaseRef string `json:"baseRef"`
	BaseOid string `json:"baseOid,omitempty"`
	Head    string `json:"head"`
	Method  string `json:"method"`
	Queue   bool   `json:"queue"`
	Mode    string `json:"mode"`
	Risk    bool   `json:"risk"`
	AckHead string `json:"ackHead,omitempty"`
}

// Receipt states.
const (
	receiptPending  = "pending"
	receiptRecorded = "recorded"
	receiptRejected = "rejected"
	receiptGaveUp   = "gave-up"
)

// opReceipt is the Desk receipt of 1 result of an attempt: the Desk keys
// it <op>:<result>, so a later result adds a receipt and never replaces
// another's.
type opReceipt struct {
	// Result, Method and Reason are the receipt's body (with the op's id,
	// ref and head).
	Result string `json:"result,omitempty"`
	Method string `json:"method,omitempty"`
	Reason string `json:"reason,omitempty"`
	State  string `json:"state,omitempty"`
	// Since is when the result came; Pocket gives up 24 hours later.
	Since time.Time `json:"since,omitzero"`
	// Failed says a post failed and the receipt waits for the next try.
	Failed bool `json:"failed,omitempty"`
}

// mergeOp is 1 attempt in the journal.
type mergeOp struct {
	ID     string   `json:"id"`
	Origin opOrigin `json:"origin"`
	Intent opIntent `json:"intent"`
	// Step is the last step written: "start", "about to approve", "about to
	// merge", or the step the attempt ended at.
	Step string `json:"step"`
	// Result is "" while the attempt runs, else merged, queued, approved,
	// refused, failed or unknown.
	Result      string `json:"result"`
	Reason      string `json:"reason,omitempty"`
	Method      string `json:"method,omitempty"`
	MergeCommit string `json:"mergeCommit,omitempty"`
	// Superseded is the attempt that Try again started after this one.
	Superseded string `json:"superseded,omitempty"`
	// Receipts are the Desk receipts, 1 per result, in order.
	Receipts []*opReceipt `json:"receipts,omitempty"`
	// Receipt is the 1 receipt of a version 1 file, read only to migrate.
	Receipt *opReceipt `json:"receipt,omitempty"`
	At      time.Time  `json:"at"`
	EndedAt time.Time  `json:"endedAt,omitzero"`

	// unsaved: the write of the result failed; the PR stays locked.
	unsaved bool
}

// receipt is the receipt of result, or nil.
func (o *mergeOp) receipt(result string) *opReceipt {
	for _, r := range o.Receipts {
		if r.Result == result {
			return r
		}
	}
	return nil
}

// settled says no receipt of the attempt waits.
func (o *mergeOp) settled() bool {
	for _, r := range o.Receipts {
		if r.State != receiptRecorded && r.State != receiptRejected && r.State != receiptGaveUp {
			return false
		}
	}
	return true
}

// migrate moves a version 1 receipt into the list, as the receipt of the
// result the attempt had then.
func (o *mergeOp) migrate() {
	if o.Receipt == nil {
		return
	}
	if len(o.Receipts) == 0 && o.Receipt.State != "" && o.Result != "" {
		r := *o.Receipt
		r.Result = o.Result
		switch o.Result {
		case "merged":
			r.Method = firstNonEmpty(o.Method, "merge")
		case "queued":
			r.Method = "queue"
		case "approved":
			r.Reason = o.Reason
		default:
			r.Reason = firstNonEmpty(o.Reason, o.Step, o.Result)
		}
		o.Receipts = []*opReceipt{&r}
	}
	o.Receipt = nil
}

// ended says the attempt's result is final: not running, not queued, not
// unknown (or replaced by a later attempt).
func (o *mergeOp) ended() bool {
	switch o.Result {
	case "merged", "approved", "refused", "failed":
		return true
	}
	return o.Superseded != ""
}

// The journal's states: only journalOK lets a mutation start.
const (
	journalClosed = iota
	journalOK
	journalLocked
	journalCorrupt
	journalBroken
)

type journalFile struct {
	Version int        `json:"version"`
	Ops     []*mergeOp `json:"ops"`
}

// journal is operations.json and its lock, on the UI thread.
type journal struct {
	dir   string
	state int
	// why says what is wrong when the state is not journalOK; corrupt is
	// the name the unreadable file was given.
	why, corrupt string
	held         bool
	ops          []*mergeOp
	now          func() time.Time
	// rename replaces the file with the written copy; tests make it fail.
	rename func(oldpath, newpath string) error
}

func newJournal(dir string, now func() time.Time) *journal {
	return &journal{dir: dir, now: now, rename: os.Rename}
}

func (j *journal) path() string   { return filepath.Join(j.dir, journalName) }
func (j *journal) lock() string   { return filepath.Join(j.dir, lockName) }
func (j *journal) marker() string { return filepath.Join(j.dir, corruptName) }

// ok says mutations may start.
func (j *journal) ok() bool { return j != nil && j.state == journalOK }

// open takes the lock and reads the file. A held lock, an unreadable file
// or a file that does not parse leave the journal closed to mutations.
func (j *journal) open() {
	j.ops, j.why, j.corrupt = nil, "", ""
	if !j.held {
		if err := os.MkdirAll(j.dir, 0o700); err != nil {
			j.state, j.why = journalBroken, "the config folder: "+err.Error()
			return
		}
		f, err := os.OpenFile(j.lock(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, fs.ErrExist) {
			j.state, j.why = journalLocked, "another Pocket holds the journal lock ("+lockName+")"
			return
		}
		if err != nil {
			j.state, j.why = journalBroken, "the journal lock: "+err.Error()
			return
		}
		_, _ = fmt.Fprintf(f, "pid %d at %s\n", os.Getpid(), j.now().UTC().Format(time.RFC3339))
		_ = f.Close()
		j.held = true
	}
	// A journal found corrupt before stays corrupt until the owner starts a
	// new one, also when operations.json is gone.
	switch kept, err := os.ReadFile(j.marker()); {
	case err == nil:
		j.state, j.corrupt = journalCorrupt, string(kept)
		j.why = "the journal did not parse; Pocket kept it as " + j.corrupt
		return
	case !errors.Is(err, fs.ErrNotExist):
		j.state, j.why = journalBroken, "reading the journal's corrupt marker: "+err.Error()
		return
	}
	data, err := os.ReadFile(j.path())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		j.state = journalOK
		return
	case err != nil:
		j.state, j.why = journalBroken, "reading the journal: "+err.Error()
		return
	}
	var file journalFile
	if err := json.Unmarshal(data, &file); err != nil || (file.Version != 1 && file.Version != journalVersion) || !validOps(file.Ops) {
		name := journalName + ".corrupt-" + j.now().UTC().Format("20060102T150405Z")
		// The marker first: without it a restart would open as new.
		if werr := j.writeAtomic(j.marker(), []byte(name)); werr != nil {
			j.state, j.why = journalBroken, "the journal does not parse, and marking it failed: "+werr.Error()
			return
		}
		if rerr := os.Rename(j.path(), filepath.Join(j.dir, name)); rerr != nil {
			j.state, j.why = journalBroken, "the journal does not parse, and keeping it aside failed: "+rerr.Error()
			return
		}
		j.state, j.corrupt = journalCorrupt, name
		j.why = "the journal did not parse; Pocket kept it as " + name
		return
	}
	for _, o := range file.Ops {
		o.migrate()
	}
	j.ops = file.Ops
	j.state = journalOK
	if j.prune() {
		_ = j.save()
	}
}

// validOps refuses an op without the fields every reader relies on.
func validOps(ops []*mergeOp) bool {
	for _, o := range ops {
		if o == nil || o.ID == "" || o.Intent.Ref == "" {
			return false
		}
	}
	return true
}

// close removes the lock this Pocket holds.
func (j *journal) close() {
	if j == nil || !j.held {
		return
	}
	_ = os.Remove(j.lock())
	j.held = false
	j.state = journalClosed
}

// releaseLock is the owner's click on a lock a crash left: it removes the
// lock file and opens the journal.
func (j *journal) releaseLock() {
	if j.state != journalLocked {
		return
	}
	_ = os.Remove(j.lock())
	j.open()
}

// startNew is the owner's click after a corrupt file: an empty journal.
func (j *journal) startNew() error {
	if j.state != journalCorrupt {
		return nil
	}
	j.ops = nil
	j.state = journalOK
	if err := j.save(); err != nil {
		j.state = journalCorrupt
		return err
	}
	// The marker goes only once the new journal is on disk.
	if err := os.Remove(j.marker()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		j.state = journalCorrupt
		return err
	}
	j.why, j.corrupt = "", ""
	return nil
}

// prune removes the ended attempts whose receipt settled more than 7 days
// after they ended. It reports whether it removed one.
func (j *journal) prune() bool {
	now := j.now()
	kept := j.ops[:0]
	for _, o := range j.ops {
		if o.ended() && o.settled() && !o.EndedAt.IsZero() && now.Sub(o.EndedAt) > journalKeep {
			continue
		}
		kept = append(kept, o)
	}
	removed := len(kept) != len(j.ops)
	clear(j.ops[len(kept):])
	j.ops = kept
	return removed
}

// save writes the journal: a copy, synced, renamed over the file. A failed
// rename leaves the old file whole.
func (j *journal) save() error {
	if j.state != journalOK {
		return errors.New("the journal is not open: " + j.why)
	}
	j.prune()
	data, err := json.MarshalIndent(journalFile{Version: journalVersion, Ops: j.ops}, "", "  ")
	if err != nil {
		return err
	}
	return j.writeAtomic(j.path(), data)
}

// writeAtomic writes data to path: a copy, synced, renamed over path, mode
// 0600. A failed rename leaves the old file whole.
func (j *journal) writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := j.rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// find is the attempt with id.
func (j *journal) find(id string) *mergeOp {
	if j == nil {
		return nil
	}
	for _, o := range j.ops {
		if o.ID == id {
			return o
		}
	}
	return nil
}

// latest is the last attempt of ref.
func (j *journal) latest(ref string) *mergeOp {
	if j == nil {
		return nil
	}
	for i := len(j.ops) - 1; i >= 0; i-- {
		if j.ops[i].Intent.Ref == ref {
			return j.ops[i]
		}
	}
	return nil
}
