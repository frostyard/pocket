package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	c := New(testToken)
	if c.BaseURL != "https://api.github.com" || c.Token != testToken || c.HTTP.Timeout != 30*time.Second || c.HTTP.CheckRedirect == nil {
		t.Fatalf("%+v", c)
	}
	if err := c.HTTP.CheckRedirect(nil, nil); err == nil {
		t.Fatal("a redirect is followed")
	}
}

// TestRedirectIsRefused: a redirect from the base URL reaches no other
// host, so the token never leaves it.
func TestRedirectIsRefused(t *testing.T) {
	var other atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		other.Add(1)
	}))
	defer elsewhere.Close()
	var base atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base.Add(1)
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	for _, hc := range []*http.Client{nil, {}, {CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}} {
		c := New(testToken)
		c.BaseURL = srv.URL
		if hc != nil {
			c.HTTP = hc
		}
		_, err := c.Preview(context.Background(), Ref{"frostyard", "core", 1})
		var api *APIError
		if !errors.As(err, &api) || api.Status != http.StatusTemporaryRedirect {
			t.Fatalf("err %v", err)
		}
		p := Preview{Ref: Ref{"frostyard", "core", 1}, NodeID: "PR_x", Head: strings.Repeat("a", 40), Eligible: true, Method: "merge"}
		res := c.Act(context.Background(), Intent{Preview: p, Mode: "single"}, "op", nil)
		if res.Kind != "failed" || res.Step != "read" {
			t.Fatalf("%+v", res)
		}
	}
	if other.Load() != 0 || base.Load() != 6 {
		t.Fatalf("elsewhere got %d requests, the base %d", other.Load(), base.Load())
	}
}

// TestTokenOnlyToTheBaseURL: each request goes to the base URL, with the
// token, and a path cannot name another host.
func TestTokenOnlyToTheBaseURL(t *testing.T) {
	var hosts []string
	c := New(testToken)
	c.BaseURL = "https://api.example.org"
	c.HTTP.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Scheme+"://"+r.URL.Host)
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			t.Errorf("authorization %q", r.Header.Get("Authorization"))
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})
	for _, path := range []string{"user", "//evil.example/x", "/x?u=https://evil.example", "https://evil.example/x", "@evil.example/x"} {
		if err := c.REST(context.Background(), path, nil); err == nil && !strings.HasPrefix(path, "//") {
			t.Errorf("path %q was sent", path)
		}
	}
	if err := c.REST(context.Background(), "/user", nil); err != nil {
		t.Fatal(err)
	}
	for _, h := range hosts {
		if h != "https://api.example.org" {
			t.Errorf("a request went to %s", h)
		}
	}
	if len(hosts) == 0 {
		t.Fatal("no request")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRedact(t *testing.T) {
	if Redact(nil, testToken) != nil {
		t.Fatal("nil is not nil")
	}
	api := &APIError{Status: 401, Message: "bad " + testToken}
	err := Redact(fmt.Errorf("read: %w", api), testToken)
	if strings.Contains(err.Error(), testToken) || !strings.Contains(err.Error(), "[token]") {
		t.Fatal(err)
	}
	var got *APIError
	if !errors.As(err, &got) || got.Status != 401 {
		t.Fatal("errors.As lost the APIError")
	}
	err = Redact(fmt.Errorf("%w: %s", ErrNoAnswer, testToken), testToken)
	if !errors.Is(err, ErrNoAnswer) || strings.Contains(err.Error(), testToken) {
		t.Fatal(err)
	}
	if plain := errors.New("no token here"); Redact(plain, testToken) != plain {
		t.Fatal("an error with no token changed")
	}
}

// TestErrorsHoldNoToken: GitHub's messages that echo the token reach no
// error text, whatever the answer.
func TestErrorsHoldNoToken(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   error
	}{
		{401, `{"message":"Bad credentials ` + testToken + `"}`, nil},
		{404, `{"message":"` + testToken + ` not found"}`, nil},
		{200, `{"errors":[{"message":"token ` + testToken + `"}]}`, ErrIncomplete},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}))
		c := New(testToken)
		c.BaseURL = srv.URL
		_, err := c.Preview(context.Background(), Ref{"frostyard", "core", 1})
		if err == nil || strings.Contains(err.Error(), testToken) || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Errorf("%d: %v", tc.status, err)
		}
		var api *APIError
		if errors.As(err, &api) && strings.Contains(api.Message, testToken) {
			t.Errorf("APIError.Message holds the token")
		}
		err = c.REST(context.Background(), "/user", nil)
		if err == nil && tc.status != 200 || err != nil && strings.Contains(err.Error(), testToken) {
			t.Errorf("REST %d: %v", tc.status, err)
		}
		srv.Close()
	}
}

// TestOnlyTheAllowedMutations: GraphQL refuses any other mutation, or 1 of
// the 3 with other inputs, before a request.
func TestOnlyTheAllowedMutations(t *testing.T) {
	var sent int
	c := New(testToken)
	c.BaseURL = "https://api.example.org"
	c.HTTP.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		sent++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":{}}`)), Header: http.Header{}}, nil
	})
	ok := map[string]any{"pullRequestId": "PR_x", "expectedHeadOid": strings.Repeat("a", 40)}
	for name, tc := range map[string]struct {
		query string
		vars  map[string]any
	}{
		"another mutation":        {`mutation { mergeBranch(input: {}) { clientMutationId } }`, nil},
		"a renamed mutation":      {strings.Replace(enqueueMutation, "PocketEnqueue", "PocketEnqueue2", 1), map[string]any{"input": ok}},
		"an extra input":          {enqueueMutation, map[string]any{"input": map[string]any{"pullRequestId": "PR_x", "expectedHeadOid": "a", "jump": "true"}}},
		"a missing input":         {mergeMutation, map[string]any{"input": ok}},
		"a body":                  {approveMutation, map[string]any{"input": map[string]any{"pullRequestId": "PR_x", "commitOID": "a", "body": "x"}}},
		"a second variable":       {enqueueMutation, map[string]any{"input": ok, "x": 1}},
		"a mutation in a query":   {prQuery + "\nmutation X { a }", nil},
		"an empty pullRequestId":  {enqueueMutation, map[string]any{"input": map[string]any{"pullRequestId": "", "expectedHeadOid": "a"}}},
		"a mutation, lower case":  {"MUTATION { x }", nil},
		"input of the wrong type": {enqueueMutation, map[string]any{"input": "x"}},
	} {
		if err := c.GraphQL(context.Background(), tc.query, tc.vars, nil); !errors.Is(err, errNotAllowed) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if sent != 0 {
		t.Fatalf("%d requests sent", sent)
	}
	if err := c.GraphQL(context.Background(), enqueueMutation, map[string]any{"input": ok}, nil); err != nil || sent != 1 {
		t.Fatal(err, sent)
	}
}

func TestNoAnswer(t *testing.T) {
	c := New(testToken)
	c.BaseURL = "https://api.example.org"
	c.HTTP.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") })
	if err := c.REST(context.Background(), "/user", nil); !errors.Is(err, ErrNoAnswer) {
		t.Fatal(err)
	}
	c.HTTP.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := c.REST(ctx, "/user", nil); !errors.Is(err, ErrNoAnswer) {
		t.Fatal(err)
	}
	c.HTTP.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("not json")), Header: http.Header{}}, nil
	})
	if err := c.GraphQL(context.Background(), prQuery, nil, nil); !errors.Is(err, ErrNoAnswer) {
		t.Fatal(err)
	}
	c.HTTP.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat(" ", maxBody+1))), Header: http.Header{}}, nil
	})
	if err := c.REST(context.Background(), "/user", nil); !errors.Is(err, ErrNoAnswer) {
		t.Fatal(err)
	}
	c.Token = ""
	if err := c.REST(context.Background(), "/user", nil); err == nil {
		t.Fatal("a request with no token")
	}
}

// gqlEntry is 1 entry of a GraphQL answer's `errors`, as JSON.
func gqlEntry(typ string, path []any, msg string) json.RawMessage {
	e := map[string]any{"message": msg}
	if typ != "" {
		e["type"] = typ
	}
	if path != nil {
		e["path"] = path
	}
	b, err := json.Marshal(e)
	if err != nil {
		panic(err)
	}
	return b
}

// gqlAnswer is a 200 answer with `data` and the given `errors`.
func gqlAnswer(data string, entries ...json.RawMessage) string {
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, string(e))
	}
	return `{"data":` + data + `,"errors":[` + strings.Join(parts, ",") + `]}`
}

// TestGraphQLNamesTheCall: an `errors` answer names the call and the
// operation, shows the type and path of 3 entries and the count of the
// rest, and holds no token.
func TestGraphQLNamesTheCall(t *testing.T) {
	rollup := func(node int) []any {
		return []any{"repository", "pullRequest", "commits", "nodes", 0, "commit", "statusCheckRollup", "contexts", "nodes", node}
	}
	body := gqlAnswer("null",
		gqlEntry("FORBIDDEN", rollup(0), "Resource not accessible by personal access token ("+testToken+")"),
		gqlEntry("FORBIDDEN", rollup(1), "Resource not accessible by personal access token"),
		gqlEntry("FORBIDDEN", rollup(2), "Resource not accessible by personal access token"),
		gqlEntry("FORBIDDEN", rollup(3), "Resource not accessible by personal access token"),
		gqlEntry("FORBIDDEN", rollup(4), "Resource not accessible by personal access token ("+testToken+")"),
	)
	c := New(testToken)
	c.BaseURL = "https://api.example.org"
	c.HTTP.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})
	var out json.RawMessage
	err := c.GraphQL(context.Background(), prQuery, nil, &out)
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("err %v", err)
	}
	text := err.Error()
	if !strings.HasPrefix(text, "GraphQL PocketPR: ") {
		t.Errorf("the call is not named: %s", text)
	}
	want := "FORBIDDEN at repository.pullRequest.commits.nodes.0.commit.statusCheckRollup.contexts.nodes.0: "
	if !strings.Contains(text, want) {
		t.Errorf("the type and path are not shown: %s", text)
	}
	if !strings.Contains(text, "and 2 more") {
		t.Errorf("the rest of the entries is not counted: %s", text)
	}
	if strings.Contains(text, testToken) {
		t.Errorf("the error holds the token: %s", text)
	}
	var g *gqlErrors
	if !errors.As(err, &g) || len(g.list) != 5 {
		t.Fatalf("the entries are lost: %v", err)
	}
	for _, e := range g.list {
		if strings.Contains(e.Message, testToken) || strings.Contains(e.where(), testToken) {
			t.Errorf("an entry holds the token: %+v", e)
		}
	}
}

// TestGQLErrorText: each entry in words, and 3 entries at most in the
// message.
func TestGQLErrorText(t *testing.T) {
	raw := func(s ...string) []json.RawMessage {
		out := make([]json.RawMessage, 0, len(s))
		for _, seg := range s {
			out = append(out, json.RawMessage(seg))
		}
		return out
	}
	for _, tc := range []struct {
		name string
		err  gqlError
		want string
	}{
		{"type and path", gqlError{Type: "FORBIDDEN", Path: raw(`"repository"`, `0`, `"labels"`), Message: "no"}, "FORBIDDEN at repository.0.labels: no"},
		{"path only", gqlError{Path: raw(`"a"`, `"b"`), Message: "no"}, "a.b: no"},
		{"type only", gqlError{Type: "NOT_FOUND", Message: "no"}, "NOT_FOUND: no"},
		{"neither", gqlError{Message: "no"}, "no"},
	} {
		if got := tc.err.text(); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	list := []gqlError{{Message: "a"}, {Message: "b"}, {Message: "c"}, {Message: "d"}, {Message: "e"}}
	if got := gqlMsg(list); got != "a; b; c; and 2 more" {
		t.Errorf("5 entries: %q", got)
	}
	if got := gqlMsg(list[:3]); got != "a; b; c" {
		t.Errorf("3 entries: %q", got)
	}
}

// TestRESTNamesTheCall: an error names the GET it answered, with the
// permissions GitHub accepted and no query string.
func TestRESTNamesTheCall(t *testing.T) {
	f := direct(t)
	f.restFail = func(path string) (int, string, http.Header) {
		if strings.Contains(path, "/rules/branches/") {
			return 403, `{"message":"Resource not accessible by personal access token"}`, http.Header{"X-Accepted-Github-Permissions": {"metadata=read"}}
		}
		return 0, "", nil
	}
	_, err := f.client().Preview(context.Background(), f.ref)
	if err == nil {
		t.Fatal("the rules read passed")
	}
	text := err.Error()
	for _, want := range []string{
		"GET /repos/bketelsen/pi-pocket-team/rules/branches/main: GitHub answered 403",
		"(accepted permissions: metadata=read)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the error holds no %q: %s", want, text)
		}
	}
	if strings.Contains(text, "?per_page") {
		t.Errorf("the error holds the query string: %s", text)
	}
	var api *APIError
	if !errors.As(err, &api) || api.Accepted != "metadata=read" {
		t.Errorf("err %v", err)
	}
}

// TestRefusedMutationKeepsItsKind: a mutation refused before any request is
// still errNotSent through the call's name.
func TestRefusedMutationKeepsItsKind(t *testing.T) {
	var sent int
	c := New(testToken)
	c.BaseURL = "https://api.example.org"
	c.HTTP.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		sent++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":{}}`)), Header: http.Header{}}, nil
	})
	err := c.GraphQL(context.Background(), "mutation PocketNope { mergeBranch }", nil, nil)
	if !errors.Is(err, errNotSent) || sent != 0 {
		t.Fatalf("err %v, %d requests sent", err, sent)
	}
	if !strings.HasPrefix(err.Error(), "GraphQL PocketNope: ") {
		t.Errorf("the call is not named: %v", err)
	}
}

// checksRefusedAnswer is a 200 answer for f's PR query: every check node null,
// and 1 FORBIDDEN entry at statusCheckRollup for each, as a fine-grained
// token without the Checks permission gets.
func checksRefusedAnswer(f *fake) string {
	data := copyJSON(f.data)
	pr := obj(data, "repository", "pullRequest")
	commit := pr["commits"].(map[string]any)["nodes"].([]any)[0].(map[string]any)["commit"].(map[string]any)
	contexts := commit["statusCheckRollup"].(map[string]any)["contexts"].(map[string]any)
	got := contexts["nodes"].([]any)
	entries := make([]json.RawMessage, 0, len(got))
	for i := range got {
		got[i] = nil
		entries = append(entries, gqlEntry("FORBIDDEN",
			[]any{"repository", "pullRequest", "commits", "nodes", 0, "commit", "statusCheckRollup", "contexts", "nodes", i},
			"Resource not accessible by personal access token"))
	}
	b, err := json.Marshal(map[string]any{"data": data, "errors": entries})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestChecksUnreadable: a token that GitHub refuses the check runs to is
// refused by name, and Pocket sends no mutation.
func TestChecksUnreadable(t *testing.T) {
	t.Run("check runs", func(t *testing.T) {
		f := direct(t)
		c := f.client()
		p, err := c.Preview(context.Background(), f.ref)
		if err != nil || !p.Eligible {
			t.Fatalf("preview %+v, %v", p, err)
		}
		f.prFail = func(int) (int, string, http.Header) { return 200, checksRefusedAnswer(f), nil }

		_, err = c.Preview(context.Background(), f.ref)
		if !errors.Is(err, ErrChecksUnreadable) || !errors.Is(err, ErrIncomplete) {
			t.Fatalf("err %v", err)
		}
		var g *gqlErrors
		if !errors.As(err, &g) || len(g.list) == 0 {
			t.Errorf("the entries are lost: %v", err)
		}
		if !strings.Contains(err.Error(), "statusCheckRollup") {
			t.Errorf("the error names no check run: %v", err)
		}
		row := c.Preflight(context.Background(), f.ref)
		if row.Readable || !strings.Contains(row.Reason, "cannot read the PR's check runs") {
			t.Errorf("row %+v", row)
		}

		f.reset()
		res := c.Act(context.Background(), Intent{Preview: p, Mode: "single"}, "op-1", nil)
		if res.Kind != "failed" || res.Step != "read" || !errors.Is(res.Err, ErrChecksUnreadable) {
			t.Errorf("result %+v", res)
		}
		if len(f.mutations) != 0 {
			t.Errorf("mutations %v", f.mutations)
		}
	})
	t.Run("another path", func(t *testing.T) {
		f := direct(t)
		f.prFail = func(int) (int, string, http.Header) {
			return 200, gqlAnswer("null", gqlEntry("FORBIDDEN",
				[]any{"repository", "pullRequest", "labels"},
				"Resource not accessible by personal access token")), nil
		}
		_, err := f.client().Preview(context.Background(), f.ref)
		if err == nil || errors.Is(err, ErrChecksUnreadable) || !errors.Is(err, ErrIncomplete) {
			t.Fatalf("err %v", err)
		}
	})
}
