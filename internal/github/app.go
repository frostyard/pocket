package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The Pocket App: Pocket signs in to GitHub as the owner through a GitHub
// App user access token, made by the device flow and kept in the desktop
// keyring (docs/adr/0010-act-as-the-owner-through-a-github-app-user-token.md).
// This file holds the App's identity, its sign-in against github.com, and
// the token it gives; tokensource.go holds the leases and the refresh.

const (
	// AppName is the App's name on GitHub. Every text that names the App
	// uses it.
	AppName = "Pocket for bketelsen"
	// AppSlug is AppName's slug on GitHub.
	AppSlug = "pocket-for-bketelsen"
	// AppClientID is the App's client ID: public, and no secret, because
	// the device flow needs none.
	// TODO(T1800): the client ID from gh api apps/pocket-for-bketelsen
	AppClientID = "Iv23liPENDING0000000"
)

// githubURL is the site whose sign-in endpoints Pocket posts to. The
// access token goes only to apiURL.
const githubURL = "https://github.com"

// The 2 sign-in endpoints, on githubURL.
const (
	devicePath = "/login/device/code"
	tokenPath  = "/login/oauth/access_token"
)

// deviceURL is where a person enters the code: Pocket's own constant, never
// the answer's.
const deviceURL = githubURL + "/login/device"

// minTokenSeconds is the shortest validity of either token of a pair: 15
// minutes (step 5 of the sign-in).
const minTokenSeconds = 900

// oauthTimeout is how long 1 sign-in request may take.
const oauthTimeout = 30 * time.Second

// slowDownExtra is what `slow_down` adds to the poll interval (RFC 8628).
const slowDownExtra = 5 * time.Second

var (
	// ErrSignIn is a sign-in that ended: its refresh token is spent,
	// refused or expired. Pocket deletes the entry and asks for a new
	// sign-in.
	ErrSignIn = errors.New("the GitHub sign-in ended: sign in to GitHub again")
	// ErrDenied is the person refusing the sign-in on GitHub.
	ErrDenied = errors.New("the sign-in was denied on GitHub")
	// ErrCodeExpired is a device code that expired before the sign-in.
	ErrCodeExpired = errors.New("the sign-in code expired: sign in again")
	// ErrUnexpected is a sign-in answer that is refused or malformed; its
	// text names the field or the HTTP status.
	ErrUnexpected = errors.New("GitHub gave an unexpected sign-in answer")
	// ErrStale is a credential write of an old sign-in generation.
	ErrStale = errors.New("this sign-in was cancelled")
	// ErrUnsaved is no lease: the keyring did not keep the pair.
	ErrUnsaved = errors.New("the keyring did not keep the GitHub sign-in")
	// ErrNotSignedIn is no sign-in to lease a token from.
	ErrNotSignedIn = errors.New("no GitHub sign-in")

	// ErrNoSignIn is what an AppStore says when it holds no sign-in. It
	// lives here, not in internal/config, so this package imports no
	// Pocket package; config.ErrNoSignIn is this error.
	ErrNoSignIn = errors.New("no GitHub sign-in is stored")
)

// BadSignIn is a stored value that is not a sign-in; Reason says why. Its
// text is what Settings shows. config.BadSignIn is this type.
type BadSignIn struct{ Reason string }

// Error is the reason in words.
func (e *BadSignIn) Error() string {
	if e.Reason == "" {
		return "the stored GitHub sign-in is not usable"
	}
	return "the stored GitHub sign-in is not usable: " + e.Reason
}

// AppToken is 1 sign-in of the App: who it acts as, the pair of tokens and
// when each expires.
type AppToken struct {
	ClientID, Login, Access, Refresh string
	AccessExpires, RefreshExpires    time.Time
}

// valid says every field is set: the client ID, the login and both tokens
// with their expiries.
func (t AppToken) valid() bool {
	return t.ClientID != "" && t.Login != "" && t.Access != "" && t.Refresh != "" &&
		!t.AccessExpires.IsZero() && !t.RefreshExpires.IsZero()
}

// secrets are the token texts every error text redacts.
func (t AppToken) secrets() []string { return []string{t.Access, t.Refresh} }

// DeviceCode is what Start gives: the code to show, the code GitHub waits
// for, and when it ends.
type DeviceCode struct {
	DeviceCode, UserCode string
	Expires              time.Time
	Interval             time.Duration
}

// OAuth is the sign-in against github.com: the device flow and the
// refresh. It needs no client secret.
type OAuth struct {
	BaseURL, ClientID string
	HTTP              *http.Client

	// sleep waits d or until ctx ends; nil is a real timer. Tests inject it.
	sleep func(ctx context.Context, d time.Duration) error
	// now is the clock; nil is time.Now.
	now func() time.Time
}

// NewOAuth is the sign-in to github.com for the Pocket App. It waits 30
// seconds at most for an answer and follows no redirect.
func NewOAuth() *OAuth {
	return &OAuth{
		BaseURL:  githubURL,
		ClientID: AppClientID,
		HTTP:     &http.Client{Timeout: oauthTimeout, CheckRedirect: refuseRedirect},
	}
}

func (o *OAuth) clock() time.Time {
	if o.now != nil {
		return o.now()
	}
	return time.Now()
}

func (o *OAuth) wait(ctx context.Context, d time.Duration) error {
	if o.sleep != nil {
		return o.sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// deviceAnswer is the answer of the device code endpoint.
type deviceAnswer struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// Start asks GitHub for a device code: the code to show, and the device
// code the poll trades for a token. It sends the form body only, never a
// URL.
func (o *OAuth) Start(ctx context.Context) (DeviceCode, error) {
	if o.ClientID == "" {
		return DeviceCode{}, notSent("no GitHub App client ID")
	}
	form := url.Values{"client_id": {o.ClientID}, "scope": {"offline_access"}}
	sent := o.clock()
	data, status, err := o.post(ctx, devicePath, form)
	if err != nil {
		return DeviceCode{}, err
	}
	// An error field refuses the sign-in whatever the status is: GitHub can
	// answer `{"error":"device_flow_disabled"}` with a 200.
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(data, &e)
	if e.Error != "" {
		return DeviceCode{}, fmt.Errorf("%w: GitHub refused the sign-in: %s", ErrUnexpected, e.Error)
	}
	if status/100 != 2 {
		return DeviceCode{}, fmt.Errorf("%w: GitHub answered %d", ErrUnexpected, status)
	}
	var a deviceAnswer
	if err := json.Unmarshal(data, &a); err != nil {
		return DeviceCode{}, fmt.Errorf("%w: the answer does not parse", ErrUnexpected)
	}
	switch {
	case a.DeviceCode == "":
		return DeviceCode{}, fmt.Errorf("%w: the answer has no device_code", ErrUnexpected)
	case a.UserCode == "":
		return DeviceCode{}, fmt.Errorf("%w: the answer has no user_code", ErrUnexpected)
	case a.VerificationURI != deviceURL:
		return DeviceCode{}, fmt.Errorf("%w: the answer's verification_uri is %q", ErrUnexpected, a.VerificationURI)
	case a.ExpiresIn <= 0:
		return DeviceCode{}, fmt.Errorf("%w: the answer's expires_in is %d", ErrUnexpected, a.ExpiresIn)
	case a.Interval <= 0:
		return DeviceCode{}, fmt.Errorf("%w: the answer's interval is %d", ErrUnexpected, a.Interval)
	}
	return DeviceCode{
		DeviceCode: a.DeviceCode,
		UserCode:   a.UserCode,
		Expires:    sent.Add(time.Duration(a.ExpiresIn) * time.Second),
		Interval:   time.Duration(a.Interval) * time.Second,
	}, nil
}

// tokenAnswer is the answer of the token endpoint: a pair, or an error.
type tokenAnswer struct {
	AccessToken           string `json:"access_token"`
	RefreshToken          string `json:"refresh_token"`
	ExpiresIn             int    `json:"expires_in"`
	RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
	Error                 string `json:"error"`
	ErrorDescription      string `json:"error_description"`
}

// Poll trades dc for a pair, every dc.Interval until it ends at dc.Expires.
// It gives the token on its own goroutine in the app: it waits, it never
// blocks a read.
func (o *OAuth) Poll(ctx context.Context, dc DeviceCode) (AppToken, error) {
	if dc.DeviceCode == "" {
		return AppToken{}, fmt.Errorf("%w: no device code", ErrUnexpected)
	}
	if dc.Interval <= 0 {
		return AppToken{}, fmt.Errorf("%w: no poll interval", ErrUnexpected)
	}
	interval := dc.Interval
	for {
		if err := o.wait(ctx, interval); err != nil {
			return AppToken{}, err
		}
		if !o.clock().Before(dc.Expires) {
			return AppToken{}, fmt.Errorf("%w: the code expired", ErrCodeExpired)
		}
		sent := o.clock()
		data, status, err := o.post(ctx, tokenPath, url.Values{
			"client_id":   {o.ClientID},
			"device_code": {dc.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		})
		if err != nil {
			// No answer, a timeout, a 5xx, a 429 or a non-JSON answer: go
			// on until the code ends.
			if errors.Is(err, ErrNoAnswer) {
				continue
			}
			return AppToken{}, Redact(err, dc.DeviceCode)
		}
		var a tokenAnswer
		_ = json.Unmarshal(data, &a)
		switch a.Error {
		case "":
			// A pair, or a 4xx with no error.
		case "authorization_pending":
			continue
		case "slow_down":
			interval += slowDownExtra
			continue
		case "access_denied":
			return AppToken{}, fmt.Errorf("%w: you cancelled on GitHub", ErrDenied)
		case "expired_token":
			return AppToken{}, fmt.Errorf("%w: the code expired", ErrCodeExpired)
		default:
			return AppToken{}, Redact(fmt.Errorf("%w: GitHub gave %s", ErrUnexpected, a.Error),
				dc.DeviceCode, a.AccessToken, a.RefreshToken)
		}
		if status/100 != 2 {
			return AppToken{}, fmt.Errorf("%w: GitHub refused the sign-in (HTTP %d)", ErrUnexpected, status)
		}
		tok, err := o.pair(a, sent, ErrUnexpected)
		return tok, Redact(err, dc.DeviceCode, a.AccessToken, a.RefreshToken)
	}
}

// Refresh trades the pair's refresh token for a new pair. The answer must
// pass step 5, and every refusal means the sign-in ended (ErrSignIn).
func (o *OAuth) Refresh(ctx context.Context, refresh string) (AppToken, error) {
	if refresh == "" {
		return AppToken{}, fmt.Errorf("%w: no refresh token", ErrSignIn)
	}
	sent := o.clock()
	data, status, err := o.post(ctx, tokenPath, url.Values{
		"client_id":     {o.ClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
	})
	if err != nil {
		return AppToken{}, Redact(err, refresh)
	}
	var a tokenAnswer
	_ = json.Unmarshal(data, &a)
	if a.Error != "" {
		return AppToken{}, Redact(fmt.Errorf("%w: GitHub gave %s", ErrSignIn, a.Error),
			refresh, a.AccessToken, a.RefreshToken)
	}
	if status/100 != 2 {
		return AppToken{}, fmt.Errorf("%w: GitHub refused the sign-in (HTTP %d)", ErrSignIn, status)
	}
	tok, err := o.pair(a, sent, ErrSignIn)
	return tok, Redact(err, refresh, a.AccessToken, a.RefreshToken)
}

// pair is the pair of step 5, with both expiries counted from the time the
// request was sent. An error is bad, naming the field that fails.
func (o *OAuth) pair(a tokenAnswer, sent time.Time, bad error) (AppToken, error) {
	switch {
	case a.AccessToken == "":
		return AppToken{}, fmt.Errorf("%w: the answer has no access_token", bad)
	case a.RefreshToken == "":
		return AppToken{}, fmt.Errorf("%w: the answer has no refresh_token", bad)
	case a.ExpiresIn < minTokenSeconds:
		return AppToken{}, fmt.Errorf("%w: expires_in is %d, less than %d", bad, a.ExpiresIn, minTokenSeconds)
	case a.RefreshTokenExpiresIn < minTokenSeconds:
		return AppToken{}, fmt.Errorf("%w: refresh_token_expires_in is %d, less than %d", bad, a.RefreshTokenExpiresIn, minTokenSeconds)
	}
	return AppToken{
		ClientID:       o.ClientID,
		Access:         a.AccessToken,
		Refresh:        a.RefreshToken,
		AccessExpires:  sent.Add(time.Duration(a.ExpiresIn) * time.Second),
		RefreshExpires: sent.Add(time.Duration(a.RefreshTokenExpiresIn) * time.Second),
	}, nil
}

// post sends form to a sign-in endpoint of BaseURL. The form body carries
// every secret: no value goes in a URL. A transport error, a 5xx, a 429 or
// an answer that is not JSON is ErrNoAnswer.
func (o *OAuth) post(ctx context.Context, path string, form url.Values) ([]byte, int, error) {
	if o.BaseURL == "" {
		return nil, 0, notSent("no GitHub base URL")
	}
	target := strings.TrimRight(o.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, notSent("%s", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "frostyard-pocket")
	// A copy, so no redirect is ever followed, whatever HTTP the caller set.
	hc := http.Client{Timeout: oauthTimeout}
	if o.HTTP != nil {
		hc = *o.HTTP
	}
	hc.CheckRedirect = refuseRedirect
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %s", ErrNoAnswer, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %s", ErrNoAnswer, err)
	}
	if len(data) > maxBody {
		return nil, 0, fmt.Errorf("%w: the answer is too large", ErrNoAnswer)
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return nil, resp.StatusCode, fmt.Errorf("%w: GitHub answered %d", ErrNoAnswer, resp.StatusCode)
	}
	if !json.Valid(data) {
		return nil, resp.StatusCode, fmt.Errorf("%w: the answer is not JSON", ErrNoAnswer)
	}
	return data, resp.StatusCode, nil
}
