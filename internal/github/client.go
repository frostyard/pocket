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
	// ErrChecksUnreadable is a token that GitHub refuses the check runs to:
	// a fine-grained token has no Checks permission, so GitHub refuses every
	// CheckRun node of the PR query's statusCheckRollup, with partial data and
	// an `errors` list. It is a refusal, never a retry: Pocket cannot check
	// the required checks, so it sends no mutation
	// (docs/adr/0009-read-check-runs-only-with-a-token-that-can.md).
	ErrChecksUnreadable = errors.New("this token cannot read the PR's check runs (a fine-grained token has no Checks permission)")

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

// callError is an error with the name of the call that failed: its text is
// "<call>: <err>", and errors.Is and errors.As reach the error under it.
type callError struct {
	call string
	err  error
}

func (e *callError) Error() string { return e.call + ": " + e.err.Error() }
func (e *callError) Unwrap() error { return e.err }

// onCall is err with the name of the call that failed; nil stays nil.
func onCall(call string, err error) error {
	if err == nil {
		return nil
	}
	return &callError{call: call, err: err}
}

// gqlErrors is a 200 answer with an `errors` list: ErrIncomplete, with
// GitHub's messages. To a mutation it proves nothing: GitHub can make the
// change, then null the result on a later error.
type gqlErrors struct {
	msg string
	// list is every entry of `errors`, with the token redacted: a caller
	// reads its type and path, as pr does for the check runs.
	list []gqlError
}

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
	msg := fmt.Sprintf("GitHub answered %d", e.Status)
	if e.Message != "" {
		msg += ": " + e.Message
	}
	if e.Accepted != "" {
		msg += " (accepted permissions: " + e.Accepted + ")"
	}
	return msg
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

// redactPath redacts the token in each segment of a GraphQL path.
func (c *Client) redactPath(p []json.RawMessage) []json.RawMessage {
	if len(p) == 0 || c.Token == "" {
		return p
	}
	out := make([]json.RawMessage, 0, len(p))
	for _, seg := range p {
		out = append(out, json.RawMessage(c.redact(string(seg))))
	}
	return out
}

// REST reads path (a GET) into out. Every error names the call: "GET" and
// the path, without its query string, which holds no fact.
func (c *Client) REST(ctx context.Context, path string, out any) error {
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err == nil && out != nil {
		if jerr := json.Unmarshal(data, out); jerr != nil {
			err = fmt.Errorf("%w: %s", ErrIncomplete, c.redact(jerr.Error()))
		}
	}
	return Redact(onCall("GET "+noQuery(path), err), c.Token)
}

// noQuery is path without its query string.
func noQuery(path string) string {
	if i := strings.Index(path, "?"); i >= 0 {
		return path[:i]
	}
	return path
}

type gqlError struct {
	Message string            `json:"message"`
	Type    string            `json:"type"`
	Path    []json.RawMessage `json:"path"`
}

// segments is the path, each JSON string segment unquoted and each other
// segment as written.
func (e gqlError) segments() []string {
	if len(e.Path) == 0 {
		return nil
	}
	out := make([]string, 0, len(e.Path))
	for _, p := range e.Path {
		var s string
		if json.Unmarshal(p, &s) == nil {
			out = append(out, s)
			continue
		}
		out = append(out, string(p))
	}
	return out
}

// where is the path as segments joined by "."; "" when there is none.
func (e gqlError) where() string { return strings.Join(e.segments(), ".") }

// text is the entry in words: "TYPE at a.b.0.c: message", with the type or
// the path left out when GitHub gives none.
func (e gqlError) text() string {
	where := e.where()
	switch {
	case e.Type != "" && where != "":
		return e.Type + " at " + where + ": " + e.Message
	case where != "":
		return where + ": " + e.Message
	case e.Type != "":
		return e.Type + ": " + e.Message
	}
	return e.Message
}

// maxGQLErrors is how many entries gqlErrors.msg shows.
const maxGQLErrors = 3

// gqlMsg is the entries in words, 3 at most, then the count of the rest.
func gqlMsg(list []gqlError) string {
	shown := list
	more := 0
	if len(list) > maxGQLErrors {
		shown, more = list[:maxGQLErrors], len(list)-maxGQLErrors
	}
	parts := make([]string, 0, len(shown)+1)
	for _, e := range shown {
		parts = append(parts, e.text())
	}
	msg := strings.Join(parts, "; ")
	if more > 0 {
		msg += fmt.Sprintf("; and %d more", more)
	}
	return msg
}

// opName is the operation name of a GraphQL text: its 2nd field, cut at the
// first "(" or "{" ("query PocketPR($owner..." is PocketPR); "query" when
// the text names none.
func opName(query string) string {
	fields := strings.Fields(query)
	if len(fields) < 2 {
		return "query"
	}
	name := fields[1]
	if i := strings.IndexAny(name, "({"); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return "query"
	}
	return name
}

// GraphQL sends query with vars and decodes the answer's `data` into out.
// A 200 with an `errors` list is ErrIncomplete with GitHub's messages. A
// mutation must be 1 of Pocket's 3, with exactly its inputs. Every error
// names the call: "GraphQL" and the operation's name.
func (c *Client) GraphQL(ctx context.Context, query string, vars map[string]any, out any) error {
	return Redact(onCall("GraphQL "+opName(query), c.graphql(ctx, query, vars, out)), c.Token)
}

// graphql is GraphQL's work, without the name of the call.
func (c *Client) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
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
		return err
	}
	// An answer that does not parse says nothing of what GitHub did.
	if err := json.Unmarshal(data, &answer); err != nil {
		return fmt.Errorf("%w: the answer does not parse", ErrNoAnswer)
	}
	if len(answer.Errors) > 0 {
		list := make([]gqlError, 0, len(answer.Errors))
		for _, e := range answer.Errors {
			list = append(list, gqlError{Message: c.redact(e.Message), Type: c.redact(e.Type), Path: c.redactPath(e.Path)})
		}
		return &gqlErrors{msg: gqlMsg(list), list: list}
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
