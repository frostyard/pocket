package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The tests of the sign-in lock: the test binary runs itself as a child to
// take the lock, as a second Pocket does. The child's mode comes from
// POCKET_TEST_SIGNIN_LOCK, and it prints 1 word on stdout.

const (
	lockHelperEnv = "POCKET_TEST_SIGNIN_LOCK"
	lockDirEnv    = "POCKET_TEST_SIGNIN_LOCK_DIR"
)

// TestHelperSignInLock is the child: "try" takes the lock and reports
// "held" or "free", and "hold" takes it and ends without a release.
func TestHelperSignInLock(t *testing.T) {
	mode := os.Getenv(lockHelperEnv)
	if mode == "" {
		t.Skip("the sign-in lock's helper, run by TestSignInLock")
	}
	dir := os.Getenv(lockDirEnv)
	release, err := LockSignIn(dir)
	switch {
	case mode == "hold" && err == nil:
		fmt.Println("held")
		// No release: the process ends, and the operating system frees the
		// lock.
		return
	case mode == "hold":
		fmt.Println("error:", err)
		return
	case errors.Is(err, ErrSignInHeld):
		fmt.Println("held")
	case err != nil:
		fmt.Println("error:", err)
	default:
		release()
		fmt.Println("free")
	}
	os.Exit(0)
}

// child runs the test binary in mode over dir, and gives its word.
func child(t *testing.T, mode, dir string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperSignInLock")
	cmd.Env = append(os.Environ(), lockHelperEnv+"="+mode, lockDirEnv+"="+dir)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the child failed: %v: %s", err, out)
	}
	// The first word of stdout: the test binary prints its own lines too.
	for _, line := range strings.Split(string(out), "\n") {
		switch line {
		case "held", "free":
			return line
		}
	}
	t.Fatalf("the child printed %q", out)
	return ""
}

// TestSignInLock: 1 Pocket at a time, and the lock is free again when the
// process that held it ends, also without a release.
func TestSignInLock(t *testing.T) {
	dir := t.TempDir()
	release, err := LockSignIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := child(t, "try", dir); got != "held" {
		t.Errorf("the child got the lock: %s", got)
	}
	release()
	if got := child(t, "try", dir); got != "free" {
		t.Errorf("the child after the release: %s", got)
	}

	// A child that takes the lock and ends without a release: the operating
	// system frees it, so the parent gets it.
	if got := child(t, "hold", dir); got != "held" {
		t.Fatalf("the child did not hold the lock: %s", got)
	}
	again, err := LockSignIn(dir)
	if err != nil {
		t.Fatalf("the lock after the child ended without a release: %v", err)
	}
	again()
}

// TestSignInLockPath: the lock is in the user's configuration folder, never
// under POCKET_CONFIG_DIR.
func TestSignInLockPath(t *testing.T) {
	cfg := t.TempDir()
	journal := t.TempDir()
	t.Setenv("POCKET_CONFIG_DIR", journal)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("APPDATA", cfg)
	release, err := LockSignIn("")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, AppID, "github-signin.lock")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the lock file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("the lock file's mode is %o", perm)
	}
	if names, _ := os.ReadDir(journal); len(names) != 0 {
		t.Errorf("POCKET_CONFIG_DIR holds %v", names)
	}
	// The folder is the keyring's service: mode 0700.
	if fi, err := os.Stat(filepath.Dir(path)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("the folder: %v, %v", fi, err)
	}
}

// TestSignInLockTwice: 1 process takes the lock once.
func TestSignInLockTwice(t *testing.T) {
	dir := t.TempDir()
	release, err := LockSignIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockSignIn(dir); !errors.Is(err, ErrSignInHeld) {
		t.Fatalf("a second lock: %v", err)
	}
	release()
	again, err := LockSignIn(dir)
	if err != nil {
		t.Fatalf("the lock after the release: %v", err)
	}
	again()
}
