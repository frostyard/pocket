package app

import (
	"errors"
	"io"
	"sync"

	"github.com/egoist/mygo/plugins/terminal"
)

// replaySink gives the temporary emulator nowhere to send historical replies.
// Its reader stays open until after the snapshot, so EOF cannot draw an exit.
type replaySink struct {
	closed chan struct{}
	once   sync.Once
}

func (s *replaySink) Read([]byte) (int, error)  { <-s.closed; return 0, io.EOF }
func (*replaySink) Write(p []byte) (int, error) { return len(p), nil }
func (s *replaySink) Close() error              { s.once.Do(func() { close(s.closed) }); return nil }

// terminalReplay renders old output off-screen, discarding all replies. Only
// Ghostty's serialized display state reaches the live emulator, never old queries.
func terminalReplay(data []byte, cols, rows int) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	sink := &replaySink{closed: make(chan struct{})}
	term, err := terminal.New(terminal.Options{Conn: sink, NoClipboardWrite: true, ClipboardRead: terminal.ClipboardDeny})
	if err != nil {
		return nil, err
	}
	defer func() { _ = term.Close() }()
	term.Resize(cols, rows)
	term.Feed(data)
	snapshot := term.Snapshot()
	if len(snapshot) == 0 {
		return nil, errors.New("could not render terminal history")
	}
	return snapshot, nil
}
