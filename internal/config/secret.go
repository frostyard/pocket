package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Keyring is the part of a credential store Pocket uses: mygo.Secrets in
// the app, a map in tests.
type Keyring interface {
	Set(key string, value []byte) error
	Get(key string) ([]byte, error)
	Delete(key string) error
}

// Credentials keeps the token of each server: in the keyring when it
// works, else in a file only the user can read.
type Credentials struct {
	Ring     Keyring
	NotFound error // what Ring.Get fails with for a missing key
	Dir      string
}

func credKey(server string) string { return "token:" + strings.TrimRight(server, "/") }

func (c Credentials) file(server string) string {
	name := strings.NewReplacer("://", "_", "/", "_", ":", "_").Replace(strings.TrimRight(server, "/"))
	return filepath.Join(c.Dir, "token-"+name)
}

// Token returns the token kept for server, or "" when there is none.
func (c Credentials) Token(server string) (string, error) {
	if c.Ring != nil {
		b, err := c.Ring.Get(credKey(server))
		if err == nil {
			return string(b), nil
		}
		if c.NotFound == nil || !errors.Is(err, c.NotFound) {
			if !errors.Is(err, errors.ErrUnsupported) {
				return "", err
			}
		}
	}
	b, err := os.ReadFile(c.file(server))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}

// SetToken keeps token for server, replacing what was kept.
func (c Credentials) SetToken(server, token string) error {
	if c.Ring != nil {
		err := c.Ring.Set(credKey(server), []byte(token))
		if err == nil {
			_ = os.Remove(c.file(server))
			return nil
		}
		if !errors.Is(err, errors.ErrUnsupported) {
			return err
		}
	}
	return os.WriteFile(c.file(server), []byte(token), 0o600)
}

// Forget removes the token of server.
func (c Credentials) Forget(server string) error {
	if c.Ring != nil {
		_ = c.Ring.Delete(credKey(server))
	}
	err := os.Remove(c.file(server))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
