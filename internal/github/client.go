// Package github approves and merges a pull request as the owner, with the
// owner's own token, under ADR-0008
// (docs/adr/0008-approve-and-merge-as-the-owner-from-the-desktop.md): the
// reads of the gates, the intent check, the 3 allowed mutations and the
// reconciliation of a mutation that got no answer. It holds no UI.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Login is the only GitHub account Pocket acts as. Another account needs a
// change of this constant.
const Login = "bketelsen"

// apiURL is GitHub's API, the only host that gets the token.
const apiURL = "https://api.github.com"

// maxBody caps the answer Pocket reads.
const maxBody = 8 << 20

var (
	// ErrIncomplete is a GitHub answer Pocket cannot act on: a 200 with an
	// `errors` list, a null where none may be, or a truncated list.
	ErrIncomplete = errors.New("GitHub gave an incomplete answer")
	// ErrNoAnswer is a request with no HTTP answer, a timeout or a 5xx: the
	// outcome of a mutation is unknown.
	ErrNoAnswer = errors.New("GitHub gave no answer")

	errRedirect = errors.New("redirect refused")
	// errNotSent is an error before any request went out: nothing reached
	// GitHub, so a mutation surely had no effect.
	errNotSent = errors.New("not sent to GitHub")
)

// notSent is an error before the request went out: errors.Is(err,
// errNotSent) holds, and its text says why.
func notSent(format string, a ...any) error {
	return &localError{msg: fmt.Sprintf(format, a...)}
}

type localError struct{ msg string }

func (e *localError) Error() string        { return e.msg }
func (e *localError) Is(target error) bool { return target == errNotSent }

// gqlErrors is a 200 answer with an `errors` list: ErrIncomplete, with
// GitHub's messages. To a mutation it proves nothing: GitHub can make the
// change, then null the result on a later error.
type gqlErrors struct{ msg string }

func (e *gqlErrors) Error() string        { return ErrIncomplete.Error() + ": " + e.msg }
func (e *gqlErrors) Is(target error) bool { return target == ErrIncomplete }

// APIError is a 4xx answer (or a refused redirect) from GitHub.
type APIError struct {
	Status  int
	Message string
	// RetryAfter is when a rate limit ends; zero when the answer names none.
	RetryAfter time.Time
	// Accepted is the header X-Accepted-GitHub-Permissions of a 403 or 404
	// answer: the fine-grained permissions the call needs; "" when absent.
	Accepted string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("GitHub answered %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("GitHub answered %d", e.Status)
}

// Client calls GitHub's API with 1 token.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client

	// sleep waits d or until ctx ends; nil is a real timer. Tests inject it.
	sleep func(ctx context.Context, d time.Duration) error
	// now is the clock; nil is time.Now.
	now func() time.Time
}

// New is a client of api.github.com with token. It waits 30 seconds at most
// for an answer and follows no redirect.
func New(token string) *Client {
	return &Client{
		BaseURL: apiURL,
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second, CheckRedirect: refuseRedirect},
	}
}

func refuseRedirect(*http.Request, []*http.Request) error { return errRedirect }

func (c *Client) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Client) wait(ctx context.Context, d time.Duration) error {
	if c.sleep != nil {
		return c.sleep(ctx, d)
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

// redacted is an error whose text no longer holds the token; it unwraps to
// the original, so errors.Is and errors.As still work.
type redacted struct {
	msg string
	err error
}

func (r *redacted) Error() string { return r.msg }
func (r *redacted) Unwrap() error { return r.err }

// Redact replaces each occurrence of token in err's text with "[token]".
func Redact(err error, token string) error {
	if err == nil || token == "" {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, token) {
		return err
	}
	return &redacted{msg: strings.ReplaceAll(msg, token, "[token]"), err: err}
}

func (c *Client) redact(s string) string {
	if c.Token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.Token, "[token]")
}

// REST reads path (a GET) into out.
func (c *Client) REST(ctx context.Context, path string, out any) error {
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err == nil && out != nil {
		if jerr := json.Unmarshal(data, out); jerr != nil {
			err = fmt.Errorf("%w: %s", ErrIncomplete, c.redact(jerr.Error()))
		}
	}
	return Redact(err, c.Token)
}

type gqlError struct {
	Message string `json:"message"`
}

// GraphQL sends query with vars and decodes the answer's `data` into out.
// A 200 with an `errors` list is ErrIncomplete with GitHub's messages. A
// mutation must be 1 of Pocket's 3, with exactly its inputs.
func (c *Client) GraphQL(ctx context.Context, query string, vars map[string]any, out any) error {
	if err := allowed(query, vars); err != nil {
		return err
	}
	var answer struct {
		Data   json.RawMessage `json:"data"`
		Errors []gqlError      `json:"errors"`
	}
	body := map[string]any{"query": query, "variables": vars}
	data, err := c.do(ctx, http.MethodPost, "/graphql", body)
	if err != nil {
		return Redact(err, c.Token)
	}
	// An answer that does not parse says nothing of what GitHub did.
	if err := json.Unmarshal(data, &answer); err != nil {
		return fmt.Errorf("%w: the answer does not parse", ErrNoAnswer)
	}
	if len(answer.Errors) > 0 {
		msgs := make([]string, 0, len(answer.Errors))
		for _, e := range answer.Errors {
			msgs = append(msgs, c.redact(e.Message))
		}
		return &gqlErrors{msg: strings.Join(msgs, "; ")}
	}
	if out == nil {
		return nil
	}
	if len(answer.Data) == 0 || string(answer.Data) == "null" {
		return fmt.Errorf("%w: no data", ErrIncomplete)
	}
	if err := json.Unmarshal(answer.Data, out); err != nil {
		return fmt.Errorf("%w: %s", ErrIncomplete, c.redact(err.Error()))
	}
	return nil
}

// do sends 1 request and gives the body of a 2xx answer.
func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	data, _, err := c.doHeaders(ctx, method, path, body)
	return data, err
}

// doHeaders is do that also gives the headers of a 2xx answer.
func (c *Client) doHeaders(ctx context.Context, method, path string, body any) ([]byte, http.Header, error) {
	if c.Token == "" {
		return nil, nil, notSent("no GitHub token")
	}
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "://") {
		return nil, nil, notSent("refused path %q", path)
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil || base.Host == "" {
		return nil, nil, notSent("bad GitHub base URL")
	}
	target := strings.TrimRight(c.BaseURL, "/") + path
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, notSent("%s", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, r)
	if err != nil {
		return nil, nil, notSent("%s", c.redact(err.Error()))
	}
	if req.URL.Host != base.Host || req.URL.Scheme != base.Scheme {
		return nil, nil, notSent("refused path %q", path)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "frostyard-pocket")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// A copy, so no redirect is ever followed, whatever HTTP the caller set.
	hc := http.Client{Timeout: 30 * time.Second}
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	hc.CheckRedirect = refuseRedirect
	resp, err := hc.Do(req)
	if errors.Is(err, errRedirect) {
		status := http.StatusFound
		if resp != nil {
			status = resp.StatusCode
		}
		return nil, nil, &APIError{Status: status, Message: "GitHub answered with a redirect, which Pocket does not follow"}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrNoAnswer, c.redact(err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrNoAnswer, c.redact(err.Error()))
	}
	if len(data) > maxBody {
		return nil, nil, fmt.Errorf("%w: the answer is too large", ErrNoAnswer)
	}
	if resp.StatusCode >= 500 {
		return nil, nil, fmt.Errorf("%w: GitHub answered %d", ErrNoAnswer, resp.StatusCode)
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		apiErr := &APIError{Status: resp.StatusCode, Message: c.redact(e.Message)}
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			apiErr.RetryAfter = c.retryAfter(resp.Header)
		}
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
			apiErr.Accepted = strings.TrimSpace(resp.Header.Get("X-Accepted-GitHub-Permissions"))
		}
		return nil, nil, apiErr
	}
	return data, resp.Header, nil
}

// retryAfter reads a rate limit's end: `retry-after` (seconds or a date),
// else `x-ratelimit-reset` when `x-ratelimit-remaining` is 0.
func (c *Client) retryAfter(h http.Header) time.Time {
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if s, err := strconv.Atoi(v); err == nil && s >= 0 {
			return c.clock().Add(time.Duration(s) * time.Second)
		}
		if t, err := http.ParseTime(v); err == nil {
			return t
		}
	}
	if strings.TrimSpace(h.Get("X-RateLimit-Remaining")) == "0" {
		if s, err := strconv.ParseInt(strings.TrimSpace(h.Get("X-RateLimit-Reset")), 10, 64); err == nil {
			return time.Unix(s, 0)
		}
	}
	return time.Time{}
}
