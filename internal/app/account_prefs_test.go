package app

import (
	"testing"

	"github.com/frostyard/frostui"

	"github.com/frostyard/pocket/internal/config"
)

// TestAccountPrefsSurviveTheStore checks that the settings file keeps 2
// accounts apart, with the pins, seen marks and last session of each.
func TestAccountPrefsSurviveTheStore(t *testing.T) {
	dir := t.TempDir()
	a, loop := scopeApp(t, dir)
	leaveWorkInSeven(t, a, loop, serverOne, "u1")
	a.signedOut("the sign-in was rotated")
	signInTo(t, a, loop, serverTwo, "u2")
	a.open(7)
	a.togglePin(7)
	a.saveSettings()

	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Server != serverTwo {
		t.Fatalf("server %q is not the one signed in to", saved.Server)
	}
	if len(saved.Pinned) != 0 || len(saved.Seen) != 0 || saved.LastSession != "" {
		t.Fatalf("settings mixed the accounts into the file: %+v", saved)
	}
	one := saved.Account(serverOne, "u1")
	if one == nil || len(one.Pinned) != 1 || one.Pinned[0] != "7" || one.Seen["7"] == 0 || one.LastSession != "7" {
		t.Fatalf("first account's settings: %+v", one)
	}
	two := saved.Account(serverTwo, "u2")
	if two == nil || len(two.Pinned) != 1 || two.Pinned[0] != "7" || two.Seen["7"] == 0 || two.LastSession != "7" {
		t.Fatalf("second account's settings: %+v", two)
	}
}

// TestLegacySettingsMoveToTheAccountOfTheirServer checks that pins, seen marks and
// the last session kept by an older Pocket move to the account of the server
// they belong to, and never to another server.
func TestLegacySettingsMoveToTheAccountOfTheirServer(t *testing.T) {
	legacy := func(t *testing.T, dir string) {
		t.Helper()
		store, err := config.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		st, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		st.Server = serverOne
		st.Pinned = []string{"7"}
		st.Seen = map[string]int64{"7": 5}
		st.LastSession = "7"
		if err := store.Save(st); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("the server they belong to", func(t *testing.T) {
		dir := t.TempDir()
		legacy(t, dir)
		a, loop := scopeApp(t, dir)
		signInTo(t, a, loop, serverOne, "u1")
		if !a.pinned(7) {
			t.Fatal("the legacy pin did not move to the account")
		}
		if a.loading != 7 {
			t.Fatalf("last session %d was not restored, want 7", a.loading)
		}
		if st := a.stateOf(a.byID[7]); st != stateSeen {
			t.Fatalf("the legacy seen mark did not move: state %d", st)
		}
		store, err := config.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if len(saved.Pinned) != 0 || len(saved.Seen) != 0 || saved.LastSession != "" {
			t.Fatalf("the legacy settings were not cleared: %+v", saved)
		}
		p := saved.Account(serverOne, "u1")
		if p == nil || len(p.Pinned) != 1 || p.Pinned[0] != "7" || p.Seen["7"] != 5 || p.LastSession != "7" {
			t.Fatalf("account settings: %+v", p)
		}
	})

	t.Run("another server", func(t *testing.T) {
		dir := t.TempDir()
		legacy(t, dir)
		a, loop := scopeApp(t, dir)
		signInTo(t, a, loop, serverTwo, "u1")
		if a.pinned(7) {
			t.Fatal("another server took the legacy pin")
		}
		if a.loading != 0 {
			t.Fatalf("another server restored session %d", a.loading)
		}
		store, err := config.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if len(saved.Pinned) != 1 || saved.Pinned[0] != "7" || saved.Seen["7"] != 5 || saved.LastSession != "7" {
			t.Fatalf("the legacy settings were given away: %+v", saved)
		}
	})
}

// TestAccountChangeRestoresTheNewAccountsSelection checks that a hello naming
// another user on a connection that is already up (not a first connection)
// shows that user's own last session, and that Close keeps each account's
// selection apart.
func TestAccountChangeRestoresTheNewAccountsSelection(t *testing.T) {
	dir := t.TempDir()
	a, loop := scopeApp(t, dir)
	other, _ := a.settings.EnsureAccount(serverOne, "u2")
	other.LastSession = "9"
	signInTo(t, a, loop, serverOne, "u1")
	a.setSessions(scopeSessions(7, 9))
	a.open(7)

	a.conn.State = frostui.Connected // a reconnect, not a first connection
	helloAs(a, "u2")
	if a.current != 0 || a.loading != 9 {
		t.Fatalf("current %d loading %d, want the new user's last session 9", a.current, a.loading)
	}
	a.setSessions(scopeSessions(7, 9))
	loop.Flush()
	if a.current != 9 {
		t.Fatalf("session 9 was not opened: %d", a.current)
	}
	if p := a.settings.Account(serverOne, "u2"); p == nil || p.Seen["7"] != 0 {
		t.Fatalf("the new user marked the first user's session seen: %+v", p)
	}
	a.Close()

	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if one := saved.Account(serverOne, "u1"); one == nil || one.LastSession != "7" {
		t.Fatalf("first user's settings: %+v", one)
	}
	if two := saved.Account(serverOne, "u2"); two == nil || two.LastSession != "9" {
		t.Fatalf("second user's settings: %+v", two)
	}
}
