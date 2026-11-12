// Package config keeps what Pocket remembers between runs: the Pi Pocket
// server it signs in to, the window's layout, and the credential, which
// goes to the desktop's credential store when there is one.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// AppID names the app for the desktop and the credential store.
const AppID = "org.frostyard.Pocket"

// Settings is the file Pocket keeps in the user's config folder.
type Settings struct {
	// Server is the base URL of the Pi Pocket server, such as
	// https://pocket.example.ts.net.
	Server string `json:"server,omitempty"`
	// Panes is the saved layout of the window's panes.
	Widths    map[string]float32 `json:"widths,omitempty"`
	Collapsed map[string]bool    `json:"collapsed,omitempty"`
	// Pinned sessions, in order, by conversation ID.
	Pinned []string `json:"pinned,omitempty"`
	// Notify turned desktop notifications off before Notifications had
	// its own switch; read once, then left alone.
	Notify *bool `json:"notify,omitempty"`
	// Notifications says which desktop notifications Pocket shows, and
	// when. Nil is the defaults (DefaultNotifications).
	Notifications *Notifications `json:"notifications,omitempty"`
	// Theme is "system" (or empty), "light" or "dark".
	Theme string `json:"theme,omitempty"`
	// LastSession is the conversation shown when Pocket last closed.
	LastSession string `json:"lastSession,omitempty"`
	// Seen is when the person last saw each session, by the server's
	// clock in milliseconds, keyed by conversation ID.
	Seen map[string]int64 `json:"seen,omitempty"`
}

// Finished says which sessions' finished runs notify.
const (
	FinishedMine = "mine" // sessions the person started or pinned
	FinishedAll  = "all"
	FinishedNone = "none"
)

// Notifications are Pocket's own notification settings. The server's
// (its Notifications menu) decide what it sends; these decide what Pocket
// shows of it, and of the Desk.
type Notifications struct {
	Enabled bool `json:"enabled"`
	// The kinds.
	Approvals bool   `json:"approvals"` // calls to allow and questions
	Decisions bool   `json:"decisions"` // the Desk's asks
	Finished  string `json:"finished"`  // FinishedMine, FinishedAll, FinishedNone
	Chat      bool   `json:"chat"`      // people's chat and mentions
	Limits    bool   `json:"limits"`    // runs waiting for a usage limit
	Watches   bool   `json:"watches"`   // news of watched PRs
	// Batch makes the notifications that come within a few seconds of
	// one another one notification.
	Batch bool `json:"batch"`
	// Do not disturb: now, until a time, or every night.
	DND         bool   `json:"dnd"`
	SnoozeUntil int64  `json:"snoozeUntil,omitempty"` // Unix milliseconds
	Quiet       bool   `json:"quiet"`
	QuietFrom   string `json:"quietFrom"` // "22:00", local time
	QuietTo     string `json:"quietTo"`   // "07:00"
	// Urgent lets calls waiting for approval through do not disturb.
	Urgent bool `json:"urgent"`
}

// DefaultNotifications tell of what waits on the person, and of finished
// runs only in the sessions they started or pinned: a team's delegated
// runs finish all day.
func DefaultNotifications() Notifications {
	return Notifications{Enabled: true, Approvals: true, Decisions: true, Finished: FinishedMine,
		Chat: true, Limits: true, Watches: true, Batch: true, QuietFrom: "22:00", QuietTo: "07:00", Urgent: true}
}

// Notes returns the notification settings, made once from the defaults
// (and the old on/off switch) when there are none.
func (s *Settings) Notes() *Notifications {
	if s.Notifications == nil {
		n := DefaultNotifications()
		if s.Notify != nil && !*s.Notify {
			n.Enabled = false
		}
		s.Notifications = &n
	}
	return s.Notifications
}

// NotifyOn reports whether desktop notifications are on.
func (s *Settings) NotifyOn() bool { return s.Notes().Enabled }

// Store reads and writes Settings in a folder.
type Store struct {
	mu  sync.Mutex
	dir string
}

// Open returns the store in the user's config folder, or in dir when it
// is not empty (POCKET_CONFIG_DIR, for development and tests).
func Open(dir string) (*Store, error) {
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("finding the config folder: %w", err)
		}
		dir = filepath.Join(base, "pocket")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Dir is the store's folder.
func (s *Store) Dir() string { return s.dir }

func (s *Store) file() string { return filepath.Join(s.dir, "settings.json") }

// Load reads the settings; a missing file is empty settings.
func (s *Store) Load() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st Settings
	b, err := os.ReadFile(s.file())
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return Settings{}, fmt.Errorf("reading %s: %w", s.file(), err)
	}
	return st, nil
}

// Save writes the settings, atomically.
func (s *Store) Save(st Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.file() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.file())
}
