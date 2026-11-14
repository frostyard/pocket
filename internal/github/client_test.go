package github

import (
	"context"
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
