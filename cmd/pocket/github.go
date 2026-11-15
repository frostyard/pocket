package main

import (
	"context"

	"github.com/egoist/mygo"

	"github.com/frostyard/pocket/internal/config"
	"github.com/frostyard/pocket/internal/github"
)

// githubAuth joins the App's sign-in for the window
// (docs/adr/0010-act-as-the-owner-through-a-github-app-user-token.md): the
// sign-in lock, 1 keyring entry, the device flow and the leased token. It
// is the seam internal/app calls; the lease and the refresh live in
// github.TokenSource.
type githubAuth struct {
	oauth   *github.OAuth
	src     *github.TokenSource
	store   config.GitHubApp
	release func()
}

// newGitHubAuth builds the sign-in over the desktop keyring.
func newGitHubAuth() *githubAuth {
	store := config.GitHubApp{Ring: secrets{}, NotFound: mygo.ErrSecretNotFound}
	oauth := github.NewOAuth()
	return &githubAuth{oauth: oauth, src: github.NewTokenSource(oauth, store), store: store}
}

// Open takes the sign-in lock and loads the stored sign-in: 1 Pocket at a
// time, and without the lock nothing is loaded and nothing is read on
// GitHub. The lock stays until the process ends.
func (g *githubAuth) Open() error {
	release, err := config.LockSignIn("")
	if err != nil {
		return err // ErrSignInHeld: another Pocket holds it
	}
	g.release = release
	return g.src.Load()
}

// Start asks GitHub for a device code.
func (g *githubAuth) Start(ctx context.Context) (github.DeviceCode, error) {
	return g.oauth.Start(ctx)
}

// Poll trades the code for a pair.
func (g *githubAuth) Poll(ctx context.Context, dc github.DeviceCode) (github.AppToken, error) {
	return g.oauth.Poll(ctx, dc)
}

// BeginSignIn starts a sign-in and gives its generation.
func (g *githubAuth) BeginSignIn() int { return g.src.BeginSignIn() }

// CancelSignIn cancels sign-in gen.
func (g *githubAuth) CancelSignIn(gen int) { g.src.CancelSignIn(gen) }

// Adopt keeps the pair as the sign-in of generation gen.
func (g *githubAuth) Adopt(tok github.AppToken, gen int) error { return g.src.Adopt(tok, gen) }

// SaveAgain saves the pair the keyring did not keep.
func (g *githubAuth) SaveAgain() error { return g.src.SaveAgain() }

// DeleteAgain deletes the entry a Sign out could not delete.
func (g *githubAuth) DeleteAgain() error { return g.src.DeleteAgain() }

// Invalidate marks the pair of tokenGen spent, after a 401.
func (g *githubAuth) Invalidate(tokenGen int) { g.src.Invalidate(tokenGen) }

// Forget is Sign out.
func (g *githubAuth) Forget() error { return g.src.Forget() }

// Lease gives an access token, its token generation and its release: 1
// per GitHub job, released with defer.
func (g *githubAuth) Lease(ctx context.Context) (string, int, func(), error) {
	return g.src.Lease(ctx)
}

// Status is the sign-in's state: no token, so it never leaks 1.
func (g *githubAuth) Status() github.AppStatus { return g.src.Status() }

// Legacy says whether 1 of the 2 fine-grained token keys is still kept.
func (g *githubAuth) Legacy() (bool, error) { return g.store.Legacy() }

// ForgetLegacy deletes the 2 fine-grained token keys, after a sign-in.
func (g *githubAuth) ForgetLegacy() error { return g.store.ForgetLegacy() }

// Close stops new leases and waits at most 35 seconds for a refresh or a
// write in progress. The sign-in lock goes only with the process.
func (g *githubAuth) Close() { g.src.Close() }
