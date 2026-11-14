package config

import (
	"errors"
	"os"
	"testing"
)

var errMissing = errors.New("no such secret")

// fakeRing is a keyring in memory; fail makes every call fail with it.
type fakeRing struct {
	m    map[string][]byte
	fail error
}

func (f *fakeRing) Set(key string, value []byte) error {
	if f.fail != nil {
		return f.fail
	}
	f.m[key] = value
	return nil
}

func (f *fakeRing) Get(key string) ([]byte, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	v, ok := f.m[key]
	if !ok {
		return nil, errMissing
	}
	return v, nil
}

func (f *fakeRing) Delete(key string) error {
	if f.fail != nil {
		return f.fail
	}
	if _, ok := f.m[key]; !ok {
		return errMissing
	}
	delete(f.m, key)
	return nil
}

// noFiles runs the test in empty folders and fails if any file appears.
func noFiles(t *testing.T) {
	t.Helper()
	dir, home := t.TempDir(), t.TempDir()
	t.Chdir(dir)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("APPDATA", home)
	t.Cleanup(func() {
		for _, d := range []string{dir, home} {
			if names, _ := os.ReadDir(d); len(names) > 0 {
				t.Errorf("a file was written in %s: %v", d, names[0].Name())
			}
		}
	})
}

func TestGitHubTokens(t *testing.T) {
	noFiles(t)
	ring := &fakeRing{m: map[string][]byte{}}
	g := GitHubTokens{Ring: ring, NotFound: errMissing}

	if tok, err := g.Get("bketelsen"); tok != "" || err != nil {
		t.Fatalf("missing key: %q, %v", tok, err)
	}
	if err := g.Set("bketelsen", " ghp_TESTTOKEN0000\n"); err != nil {
		t.Fatal(err)
	}
	if err := g.Set("frostyard", "github_pat_TEST0000"); err != nil {
		t.Fatal(err)
	}
	if string(ring.m["github:bketelsen"]) != "ghp_TESTTOKEN0000" || string(ring.m["github:frostyard"]) != "github_pat_TEST0000" || len(ring.m) != 2 {
		t.Fatalf("keyring %v", ring.m)
	}
	if tok, err := g.Get("bketelsen"); tok != "ghp_TESTTOKEN0000" || err != nil {
		t.Fatalf("get: %q, %v", tok, err)
	}
	if err := g.Forget("bketelsen"); err != nil {
		t.Fatal(err)
	}
	if err := g.Forget("bketelsen"); err != nil {
		t.Fatalf("forget a missing key: %v", err)
	}
	if tok, err := g.Get("bketelsen"); tok != "" || err != nil {
		t.Fatalf("after forget: %q, %v", tok, err)
	}
	if err := g.Set("bketelsen", "  "); err == nil {
		t.Fatal("an empty token was kept")
	}
	for _, owner := range []string{"frostyard-machines", "", "BKETELSEN"} {
		if _, err := g.Get(owner); err == nil {
			t.Errorf("get %q: no error", owner)
		}
		if err := g.Set(owner, "ghp_TESTTOKEN0000"); err == nil {
			t.Errorf("set %q: no error", owner)
		}
		if err := g.Forget(owner); err == nil {
			t.Errorf("forget %q: no error", owner)
		}
	}
	if len(ring.m) != 1 {
		t.Fatalf("keyring %v", ring.m)
	}
}

func TestGitHubTokensNeedAKeyring(t *testing.T) {
	for name, g := range map[string]GitHubTokens{
		"no keyring":  {NotFound: errMissing},
		"unsupported": {Ring: &fakeRing{m: map[string][]byte{}, fail: errors.ErrUnsupported}, NotFound: errMissing},
		"locked":      {Ring: &fakeRing{m: map[string][]byte{}, fail: errors.New("the keyring is locked")}, NotFound: errMissing},
	} {
		t.Run(name, func(t *testing.T) {
			noFiles(t)
			if _, err := g.Get("bketelsen"); err == nil {
				t.Error("get: no error")
			}
			if err := g.Set("bketelsen", "ghp_TESTTOKEN0000"); err == nil {
				t.Error("set: no error")
			}
			if err := g.Forget("frostyard"); err == nil {
				t.Error("forget: no error")
			}
		})
	}
}
