package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The sign-in lock is 1 Pocket at a time on GitHub: the keyring entry is 1
// per desktop user, whatever POCKET_CONFIG_DIR says, so 2 Pockets would spend
// each other's refresh tokens. The lock is an operating-system lock on
// <os.UserConfigDir()>/org.frostyard.Pocket/github-signin.lock, never under
// POCKET_CONFIG_DIR, and it is separate from the journal's lock. Pocket takes
// it at start and holds it until the process ends: the operating system
// releases it then, also after a crash
// (docs/adr/0010-act-as-the-owner-through-a-github-app-user-token.md).

// ErrSignInHeld is another Pocket that holds the sign-in lock. Settings shows
// "another Pocket holds the GitHub sign-in", and Pocket loads no sign-in,
// refreshes nothing and reads nothing on GitHub.
var ErrSignInHeld = errors.New("another Pocket holds the GitHub sign-in")

// signInLockName is the lock file inside the folder.
const signInLockName = "github-signin.lock"

// LockSignIn takes the sign-in lock and gives the release for this process;
// dir "" is <os.UserConfigDir()>/org.frostyard.Pocket, the folder of the
// keyring's service. It creates the folder (mode 0700) and the file (mode
// 0600). A lock another Pocket holds is ErrSignInHeld at once: it never
// waits.
func LockSignIn(dir string) (release func(), err error) {
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("no folder for the GitHub sign-in lock: %w", err)
		}
		dir = filepath.Join(base, AppID)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("the GitHub sign-in lock's folder: %w", err)
	}
	path := filepath.Join(dir, signInLockName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("the GitHub sign-in lock file: %w", err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		unlockFile(f)
		_ = f.Close()
	}, nil
}
