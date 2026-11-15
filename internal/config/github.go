package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/frostyard/pocket/internal/github"
)

// GitHubApp keeps the owner's GitHub App sign-in, 1 entry in the desktop
// keyring and nothing else: unlike Credentials it has no file fallback, so a
// sign-in that can merge never lands in a file without notice, and a missing
// or failing keyring is an error
// (docs/adr/0010-act-as-the-owner-through-a-github-app-user-token.md).
//
// The entry's value is JSON: {"v":1,"client_id":…,"login":…,"access":…,
// "access_expires":"<RFC 3339 UTC>","refresh":…,"refresh_expires":…}. 1
// entry keeps the pair together: a refresh writes both at once.
type GitHubApp struct {
	Ring     Keyring
	NotFound error // what Ring.Get and Ring.Delete fail with for a missing key
}

// githubAppKey is the 1 key of the sign-in.
const githubAppKey = "github:app"

// githubLegacyKeys are the keys of the 2 old personal tokens of ADR-0008,
// which the App sign-in (ADR-0010) replaces.
var githubLegacyKeys = []string{"github:bketelsen", "github:frostyard"}

// signInVersion is the only version of the entry Pocket writes and reads.
const signInVersion = 1

// ErrNoSignIn is what Load says when the keyring holds no sign-in: Settings
// shows "Sign in with GitHub". It is github.ErrNoSignIn, so a fake store in
// internal/github can give it too.
var ErrNoSignIn = github.ErrNoSignIn

// BadSignIn is a stored value that is not a sign-in; Reason says why.
// Settings shows "the stored GitHub sign-in is not usable: <reason>: sign in
// again", and a new sign-in overwrites it. It is github.BadSignIn.
type BadSignIn = github.BadSignIn

// storedSignIn is the entry's JSON.
type storedSignIn struct {
	V              int    `json:"v"`
	ClientID       string `json:"client_id"`
	Login          string `json:"login"`
	Access         string `json:"access"`
	AccessExpires  string `json:"access_expires"`
	Refresh        string `json:"refresh"`
	RefreshExpires string `json:"refresh_expires"`
}

// errNoKeyring says approve + merge needs a working desktop keyring.
var errNoKeyring = errors.New("approve + merge needs the desktop keyring")

// ring is the keyring, or the error that there is none.
func (g GitHubApp) ring() (Keyring, error) {
	if g.Ring == nil {
		return nil, errNoKeyring
	}
	return g.Ring, nil
}

// fail says why the keyring refuses.
func (g GitHubApp) fail(err error) error {
	if errors.Is(err, errors.ErrUnsupported) {
		return fmt.Errorf("%w: %w", errNoKeyring, err)
	}
	return fmt.Errorf("the desktop keyring failed: %w", err)
}

// missing says Ring reported no such key.
func (g GitHubApp) missing(err error) bool {
	return g.NotFound != nil && errors.Is(err, g.NotFound)
}

// Load returns the stored sign-in: ErrNoSignIn when there is none,
// *BadSignIn when the value is not a sign-in, and any other keyring error as
// it is (Approve + merge is then off).
func (g GitHubApp) Load() (github.AppToken, error) {
	ring, err := g.ring()
	if err != nil {
		return github.AppToken{}, err
	}
	b, err := ring.Get(githubAppKey)
	if err != nil {
		if g.missing(err) {
			return github.AppToken{}, ErrNoSignIn
		}
		return github.AppToken{}, g.fail(err)
	}
	return parseSignIn(b)
}

// parseSignIn reads the entry's value, and says why it is not a sign-in.
func parseSignIn(b []byte) (github.AppToken, error) {
	var s storedSignIn
	if err := json.Unmarshal(b, &s); err != nil {
		return github.AppToken{}, &BadSignIn{Reason: "the value does not parse"}
	}
	bad := func(reason string) (github.AppToken, error) {
		return github.AppToken{}, &BadSignIn{Reason: reason}
	}
	if s.V != signInVersion {
		return bad(fmt.Sprintf("the version is %d, not %d", s.V, signInVersion))
	}
	for _, f := range []struct {
		name, value string
	}{
		{"client_id", s.ClientID},
		{"login", s.Login},
		{"access", s.Access},
		{"refresh", s.Refresh},
	} {
		if f.value == "" {
			return bad("the " + f.name + " is empty")
		}
	}
	access, err := parseSignInTime("access_expires", s.AccessExpires)
	if err != nil {
		return bad(err.Error())
	}
	refresh, err := parseSignInTime("refresh_expires", s.RefreshExpires)
	if err != nil {
		return bad(err.Error())
	}
	if s.ClientID != github.AppClientID {
		return bad("this sign-in is for another build of Pocket")
	}
	if s.Login != github.Login {
		return bad("this sign-in is for " + s.Login + ", not " + github.Login)
	}
	return github.AppToken{
		ClientID:       s.ClientID,
		Login:          s.Login,
		Access:         s.Access,
		Refresh:        s.Refresh,
		AccessExpires:  access,
		RefreshExpires: refresh,
	}, nil
}

// parseSignInTime reads 1 of the entry's times.
func parseSignInTime(name, value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("the %s time does not parse", name)
	}
	if t.IsZero() {
		return time.Time{}, fmt.Errorf("the %s time is zero", name)
	}
	return t.UTC(), nil
}

// Save writes the sign-in, replacing what was kept. It refuses a token with
// an empty field or a zero time, and writes nothing then.
func (g GitHubApp) Save(tok github.AppToken) error {
	ring, err := g.ring()
	if err != nil {
		return err
	}
	if tok.ClientID == "" || tok.Login == "" || tok.Access == "" || tok.Refresh == "" ||
		tok.AccessExpires.IsZero() || tok.RefreshExpires.IsZero() {
		return errors.New("the GitHub sign-in is incomplete")
	}
	b, err := json.Marshal(storedSignIn{
		V:              signInVersion,
		ClientID:       tok.ClientID,
		Login:          tok.Login,
		Access:         tok.Access,
		AccessExpires:  tok.AccessExpires.UTC().Format(time.RFC3339),
		Refresh:        tok.Refresh,
		RefreshExpires: tok.RefreshExpires.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	if err := ring.Set(githubAppKey, b); err != nil {
		return g.fail(err)
	}
	return nil
}

// Forget removes the sign-in; none kept is no error.
func (g GitHubApp) Forget() error {
	ring, err := g.ring()
	if err != nil {
		return err
	}
	if err := ring.Delete(githubAppKey); err != nil && !g.missing(err) {
		return g.fail(err)
	}
	return nil
}

// Legacy says whether 1 of the 2 old token keys that ADR-0010 replaces
// (ADR-0008) is still kept. Settings > GitHub then asks the person to sign
// in and to delete them on GitHub.
func (g GitHubApp) Legacy() (bool, error) {
	ring, err := g.ring()
	if err != nil {
		return false, err
	}
	for _, key := range githubLegacyKeys {
		_, err := ring.Get(key)
		switch {
		case err == nil:
			return true, nil
		case g.missing(err):
			continue
		default:
			return false, g.fail(err)
		}
	}
	return false, nil
}

// ForgetLegacy removes the 2 old token keys that ADR-0010 replaces
// (ADR-0008), after a sign-in: none kept is no error.
func (g GitHubApp) ForgetLegacy() error {
	ring, err := g.ring()
	if err != nil {
		return err
	}
	for _, key := range githubLegacyKeys {
		if err := ring.Delete(key); err != nil && !g.missing(err) {
			return g.fail(err)
		}
	}
	return nil
}
