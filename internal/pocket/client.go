// Package pocket is a client of a Pi Pocket server: its HTTP API and its
// event stream, as the web app uses them. It holds no UI state; callers
// hand what it decodes to their UI thread.
package pocket

import (
	"bytes"
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

// CookieName is the cookie Pi Pocket signs a device in with.
const CookieName = "pocket_auth"

// ErrSignedOut is returned when the server no longer accepts the
// credential: the token was rotated or the person removed.
var ErrSignedOut = errors.New("signed out: the server does not accept this device's sign-in")

// Client talks to one server as one signed-in person. It is safe for
// concurrent use.
type Client struct {
	Base *url.URL
	// Auth is the person's token, sent as a bearer token. (The server
	// prefers a pocket_auth cookie over it, so the client sends none.)
	Auth string
	HTTP *http.Client
	// Drift hears a field the server sent in a shape this client does not
	// expect. The rest of the answer is still decoded and used: a module
	// that changes one field does not blank a whole view. Nil ignores it.
	Drift func(error)
}

// NewClient returns a client of the server at base, signed in with auth.
func NewClient(base, auth string) (*Client, error) {
	u, err := ParseServer(base)
	if err != nil {
		return nil, err
	}
	return &Client{Base: u, Auth: auth, HTTP: &http.Client{Timeout: 0}}, nil
}

// ParseServer reads a server's address: a URL, or a bare host name, which
// means https.
func ParseServer(s string) (*url.URL, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("no server address")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("reading the server address: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("the server address must be http or https, not %s", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("the server address has no host")
	}
	return &url.URL{Scheme: u.Scheme, Host: u.Host}, nil
}

// SignIn reads a sign-in link and returns the server's address and the
// token to sign in with. An owner link (/login?token=…) carries the token;
// an invite (/join/<code>) is redeemed for one under name. It checks the
// token with the server before returning it.
func SignIn(ctx context.Context, link, name string) (server string, token string, err error) {
	link = strings.TrimSpace(link)
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", "", errors.New("paste the whole sign-in link, starting with https://")
	}
	server = (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
	switch {
	case u.Query().Get("token") != "":
		token = u.Query().Get("token")
	case strings.HasPrefix(u.Path, "/join/"):
		token, err = redeemInvite(ctx, server+u.Path, name)
		if err != nil {
			return "", "", err
		}
	default:
		return "", "", errors.New("this is not a sign-in link: it has no token and is not an invite (/join/…)")
	}
	c := &Client{Base: &url.URL{Scheme: u.Scheme, Host: u.Host}, Auth: token, HTTP: &http.Client{Timeout: 20 * time.Second}}
	if _, err := c.Me(ctx); err != nil {
		if errors.Is(err, ErrSignedOut) {
			return "", "", errors.New("the server did not accept this link: it may have been rotated")
		}
		return "", "", fmt.Errorf("reaching %s: %w", u.Host, err)
	}
	return server, token, nil
}

// redeemInvite posts the invite's form and reads the token from the
// cookie it sets.
func redeemInvite(ctx context.Context, joinURL, name string) (string, error) {
	hc := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if name == "" {
		name = "Pocket"
	}
	form := url.Values{"name": {name}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	for _, c := range resp.Cookies() {
		if c.Name == CookieName && c.Value != "" {
			v, err := url.QueryUnescape(c.Value)
			if err != nil {
				v = c.Value
			}
			return v, nil
		}
	}
	if resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound {
		return "", errors.New("this invite has expired or was already used")
	}
	return "", fmt.Errorf("the server answered %s to the invite", resp.Status)
}

// APIError is a non-2xx answer from the API.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("the server answered %d", e.Status)
}

func (c *Client) url(path string) string {
	return c.Base.String() + path
}

func (c *Client) request(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Auth)
	// The API asks every request for this header: a browser cannot send
	// it across sites without asking, so it stands in for a CSRF check.
	req.Header.Set("X-Pocket", "1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// Do sends a request to the API and decodes its JSON answer into out,
// when out is not nil.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	req, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrSignedOut
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		return &APIError{Status: resp.StatusCode, Message: e.Error}
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := Decode(data, out); err != nil {
		var drift *json.UnmarshalTypeError
		if errors.As(err, &drift) {
			if c.Drift != nil {
				c.Drift(fmt.Errorf("%s %s: %w", method, path, err))
			}
			return nil
		}
		return fmt.Errorf("reading %s %s: %w", method, path, err)
	}
	return nil
}

// Decode is json.Unmarshal: a value of another type than the field's
// leaves that field empty, fills the rest, and reports the first such
// field as a *json.UnmarshalTypeError.
func Decode(data []byte, out any) error { return json.Unmarshal(data, out) }

// Get is Do with GET.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.Do(ctx, http.MethodGet, path, nil, out)
}

// Post is Do with POST.
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, http.MethodPost, path, body, out)
}
