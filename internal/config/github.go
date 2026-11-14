package config

import (
	"errors"
	"fmt"
	"strings"
)

// GitHubTokens keeps the owner's GitHub tokens (ADR-0008), 1 for each
// resource owner, in the desktop keyring only. Unlike Credentials it has no
// file fallback: a token that can merge must not land in a file without
// notice, so a missing or failing keyring is an error.
type GitHubTokens struct {
	Ring     Keyring
	NotFound error // what Ring.Get and Ring.Delete fail with for a missing key
}

// githubOwners are the resource owners Pocket keeps a token for.
var githubOwners = map[string]bool{"bketelsen": true, "frostyard": true}

// errNoKeyring says approve + merge needs a working desktop keyring.
var errNoKeyring = errors.New("approve + merge needs the desktop keyring")

func githubKey(owner string) (string, error) {
	if !githubOwners[owner] {
		return "", fmt.Errorf("no GitHub token is kept for %q", owner)
	}
	return "github:" + owner, nil
}

func (g GitHubTokens) ring(owner string) (string, error) {
	key, err := githubKey(owner)
	if err != nil {
		return "", err
	}
	if g.Ring == nil {
		return "", errNoKeyring
	}
	return key, nil
}

func (g GitHubTokens) fail(err error) error {
	if errors.Is(err, errors.ErrUnsupported) {
		return fmt.Errorf("%w: %w", errNoKeyring, err)
	}
	return fmt.Errorf("the desktop keyring failed: %w", err)
}

func (g GitHubTokens) missing(err error) bool {
	return g.NotFound != nil && errors.Is(err, g.NotFound)
}

// Get returns owner's token, or "" when none is kept.
func (g GitHubTokens) Get(owner string) (string, error) {
	key, err := g.ring(owner)
	if err != nil {
		return "", err
	}
	b, err := g.Ring.Get(key)
	if err == nil {
		return string(b), nil
	}
	if g.missing(err) {
		return "", nil
	}
	return "", g.fail(err)
}

// Set keeps token for owner, replacing what was kept.
func (g GitHubTokens) Set(owner, token string) error {
	key, err := g.ring(owner)
	if err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("the GitHub token is empty")
	}
	if err := g.Ring.Set(key, []byte(token)); err != nil {
		return g.fail(err)
	}
	return nil
}

// Forget removes owner's token; none kept is no error.
func (g GitHubTokens) Forget(owner string) error {
	key, err := g.ring(owner)
	if err != nil {
		return err
	}
	if err := g.Ring.Delete(key); err != nil && !g.missing(err) {
		return g.fail(err)
	}
	return nil
}
