package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
// result the attempt had then. An attempt with a result must have a
// receipt with a known state: else the file is corrupt, and its bytes are
// kept, never dropped. An attempt with no result (it ran at the crash) has
// no receipt yet: an empty one goes.
func (o *mergeOp) migrate() error {
	if len(o.Receipts) > 0 {
		return errors.New("a version 1 attempt with a list of receipts")
	}
	legacy := o.Receipt
	o.Receipt = nil
	if o.Result == "" {
		if legacy != nil && (legacy.State != "" || legacy.Result != "") {
			return errors.New("a receipt on an attempt with no result")
		}
		return nil
	}
	if legacy == nil || !receiptStates[legacy.State] || (legacy.Result != "" && legacy.Result != o.Result) {
		return errors.New("an attempt with a result and no valid receipt")
	}
	r := *legacy
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
	return nil
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
	// digest is the SHA-256 (hex) of the corrupt bytes the kept copy holds.
	digest string
	held   bool
	ops    []*mergeOp
	now    func() time.Time
	// rename replaces the file with the written copy, and remove removes
	// the corrupt marker; tests make them fail.
	rename func(oldpath, newpath string) error
	remove func(path string) error
}

func newJournal(dir string, now func() time.Time) *journal {
	return &journal{dir: dir, now: now, rename: os.Rename, remove: os.Remove}
}

func (j *journal) path() string   { return filepath.Join(j.dir, journalName) }
func (j *journal) lock() string   { return filepath.Join(j.dir, lockName) }
func (j *journal) marker() string { return filepath.Join(j.dir, corruptName) }

// ok says mutations may start.
func (j *journal) ok() bool { return j != nil && j.state == journalOK }

// open takes the lock and reads the file. A held lock, an unreadable file
// or a file that does not parse leave the journal closed to mutations.
func (j *journal) open() {
	j.ops, j.why, j.corrupt, j.digest = nil, "", "", ""
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
	// new one, also when operations.json is gone or newer. Its kept copy is
	// bound to the SHA-256 of the corrupt bytes: only those bytes are ever
	// copied there.
	switch kept, err := os.ReadFile(j.marker()); {
	case err == nil:
		m, merr := parseMarker(kept)
		if merr != nil {
			j.state, j.why = journalBroken, "the journal's corrupt marker is not valid: "+merr.Error()
			return
		}
		j.corrupt, j.digest = m.Archive, m.SHA256
		if err := j.ensureCopy(); err != nil {
			j.state, j.why = journalBroken, err.Error()
			return
		}
		j.state = journalCorrupt
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
	if file, ok := parseJournal(data); ok {
		j.ops = file.Ops
		j.state = journalOK
		if j.prune() {
			_ = j.save()
		}
		return
	}
	// The file does not parse: 1. the marker, with the copy's name and the
	// digest of these bytes (without it a restart would open as new);
	// 2. a synced copy of the bytes. The original stays until the owner's
	// Start a new journal replaces it.
	name, err := j.freeArchiveName()
	if err != nil {
		j.state, j.why = journalBroken, "the journal does not parse, and no name is free for its copy: "+err.Error()
		return
	}
	j.corrupt, j.digest = name, digestOf(data)
	marker, _ := json.Marshal(corruptMarker{Archive: name, SHA256: j.digest})
	if err := j.writeAtomic(j.marker(), marker); err != nil {
		j.state, j.why = journalBroken, "the journal does not parse, and marking it failed: "+err.Error()
		return
	}
	if err := j.ensureCopy(); err != nil {
		j.state, j.why = journalBroken, err.Error()
		return
	}
	j.state = journalCorrupt
	j.why = "the journal did not parse; Pocket kept it as " + name
}

// parseJournal reads a journal file: version 1 (migrated) or 2, with every
// op and receipt valid. ok is false for anything else.
func parseJournal(data []byte) (journalFile, bool) {
	var file journalFile
	if err := json.Unmarshal(data, &file); err != nil {
		return file, false
	}
	switch file.Version {
	case 1:
		for _, o := range file.Ops {
			if o == nil || o.migrate() != nil {
				return file, false
			}
		}
	case journalVersion:
		for _, o := range file.Ops {
			if o != nil && o.Receipt != nil {
				return file, false // a version 2 file has no legacy receipt
			}
		}
	default:
		return file, false
	}
	// The ops are checked after the migration: what Pocket reads is what
	// it checks.
	return file, validOps(file.Ops)
}

// corruptMarker is operations.corrupt: the kept copy's name and the
// SHA-256 (hex) of the corrupt bytes it holds.
type corruptMarker struct {
	Archive string `json:"archive"`
	SHA256  string `json:"sha256"`
}

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// parseMarker reads a marker: exactly its 2 fields, a safe name and a
// SHA-256 digest.
func parseMarker(b []byte) (corruptMarker, error) {
	var m corruptMarker
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, err
	}
	if !validArchiveName(m.Archive) {
		return m, errors.New("no safe name of a kept copy: " + m.Archive)
	}
	if !digestPattern.MatchString(m.SHA256) {
		return m, errors.New("no SHA-256 digest")
	}
	return m, nil
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// freeArchiveName is a name for a new kept copy that no file has: an
// earlier copy is never written over.
func (j *journal) freeArchiveName() (string, error) {
	base := journalName + ".corrupt-" + j.now().UTC().Format("20060102T150405Z")
	for i := 1; i <= 100; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		_, err := os.Lstat(filepath.Join(j.dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			return name, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", errors.New("100 copies have that time")
}

// receiptResults and receiptStates are the values a receipt may hold.
var (
	receiptResults = map[string]bool{"merged": true, "queued": true, "approved": true, "refused": true, "failed": true, "unknown": true}
	receiptStates  = map[string]bool{receiptPending: true, receiptRecorded: true, receiptRejected: true, receiptGaveUp: true}
)

// validOps refuses an op without the fields every reader relies on, and a
// receipt that is null, has an empty or unknown result or state, or
// repeats a result of its op.
func validOps(ops []*mergeOp) bool {
	for _, o := range ops {
		if o == nil || o.ID == "" || o.Intent.Ref == "" {
			return false
		}
		seen := map[string]bool{}
		for _, r := range o.Receipts {
			if r == nil || !receiptResults[r.Result] || !receiptStates[r.State] || seen[r.Result] {
				return false
			}
			seen[r.Result] = true
		}
	}
	return true
}

// validArchiveName says name is a kept copy's name in the folder.
func validArchiveName(name string) bool {
	return strings.HasPrefix(name, journalName+".corrupt-") && filepath.Base(name) == name && !strings.ContainsAny(name, "/\\")
}

func (j *journal) archive() string { return filepath.Join(j.dir, j.corrupt) }

// errCopyMissing is a corrupt journal whose bytes Pocket no longer has.
var errCopyMissing = errors.New("the kept copy of the corrupt journal is missing")

// copyIntact says the kept copy holds the bytes the marker names.
func (j *journal) copyIntact() error {
	b, err := os.ReadFile(j.archive())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return errCopyMissing
		}
		return fmt.Errorf("reading the kept copy %s: %w", j.corrupt, err)
	}
	if digestOf(b) != j.digest {
		return errors.New("the kept copy " + j.corrupt + " does not hold the corrupt journal")
	}
	return nil
}

// ensureCopy makes the kept copy hold the marker's bytes. A copy that does
// is left alone. Else only operations.json with exactly those bytes is
// copied (synced, renamed, then checked again); any other file is never
// copied, and the copy is missing.
func (j *journal) ensureCopy() error {
	if j.copyIntact() == nil {
		return nil
	}
	original, err := os.ReadFile(j.path())
	if err != nil || digestOf(original) != j.digest {
		return errCopyMissing
	}
	if err := j.writeAtomic(j.archive(), original); err != nil {
		return fmt.Errorf("keeping a copy of the corrupt journal as %s failed: %w", j.corrupt, err)
	}
	return j.copyIntact()
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
// It checks the kept copy against the marker's digest, saves the empty
// journal, and only then removes the marker. A crash between the 2 leaves
// the marker: the next open finds the copy whole and stays corrupt, and a
// second click is safe, as it checks only the copy.
func (j *journal) startNew() error {
	if j.state != journalCorrupt {
		return errors.New("only a corrupt journal starts again: " + firstNonEmpty(j.why, "the journal is not corrupt"))
	}
	if err := j.copyIntact(); err != nil {
		j.why = "Pocket keeps the corrupt journal: " + err.Error()
		return errors.New(j.why)
	}
	j.ops = nil
	j.state = journalOK
	if err := j.save(); err != nil {
		j.state = journalCorrupt
		return err
	}
	if err := j.remove(j.marker()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		j.state = journalCorrupt
		j.why = "the new journal is saved, but removing the corrupt marker failed: " + err.Error() + "; Start a new journal again"
		return errors.New(j.why)
	}
	j.why, j.corrupt, j.digest = "", "", ""
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
	syncDir(filepath.Dir(path))
	return nil
}

// syncDir makes a rename in dir durable before the next file step, so a
// power loss cannot keep a later step (the new journal) and lose an earlier
// one (the marker or the copy). Best effort: some systems (Windows) cannot
// sync a folder.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
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
