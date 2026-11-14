package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// The tests of the sign-in: the device flow against a fake github.com, the
// step 5 validation, and the hosts and secrets of ADR-0010.

// testDevice and testRefresh are clearly fake secrets; the fake server can
// echo them to test Redact.
const (
	testDevice  = "DEVICECODE_test_0000"
	testRefresh = "REFRESHTOKEN_test_0000"
)

// clock is a clock a test moves; the poll goroutine may read it while the
// test moves it, so it has a mutex.
type clock struct {
	mu sync.Mutex
	at time.Time
}

func newClock() *clock {
	return &clock{at: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

// signIn is a fake github.com: the 2 sign-in endpoints, with a script for
// each and every form body recorded.
type signIn struct {
	t   *testing.T
	srv *httptest.Server

	mu      sync.Mutex
	paths   []string
	forms   []url.Values
	queries []string
	tokens  int // token requests served

	// device and token answer 1 request; nil is the good answer, and n is
	// the count of requests to that endpoint, from 1.
	device func(w http.ResponseWriter, n int)
	token  func(w http.ResponseWriter, n int)
}

func newSignIn(t *testing.T) *signIn {
	t.Helper()
	f := &signIn{t: t}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *signIn) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	f.queries = append(f.queries, r.URL.RawQuery)
	if r.Method != http.MethodPost {
		f.t.Errorf("%s %s: the sign-in sends no GET", r.Method, r.URL.Path)
	}
	if err := r.ParseForm(); err != nil {
		f.t.Errorf("form: %v", err)
	}
	f.forms = append(f.forms, r.Form)
	if got := r.Header.Get("Accept"); got != "application/json" {
		f.t.Errorf("Accept %q", got)
	}
	// The sign-in never sends the API's bearer token, and no secret ever goes
	// in a URL.
	if got := r.Header.Get("Authorization"); got != "" {
		f.t.Errorf("the sign-in sends Authorization %q", got)
	}
	if r.URL.RawQuery != "" {
		f.t.Errorf("the sign-in puts %q in a URL", r.URL.RawQuery)
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		f.t.Errorf("Content-Type %q", r.Header.Get("Content-Type"))
	}
	path, device, token := r.URL.Path, f.device, f.token
	n := 0
	if path == tokenPath {
		f.tokens++
		n = f.tokens
	}
	f.mu.Unlock()
	switch path {
	case devicePath:
		if device != nil {
			device(w, 1)
			return
		}
		writeAnswer(w, 200, goodDeviceAnswer())
	case tokenPath:
		if token != nil {
			token(w, n)
			return
		}
		writeAnswer(w, 200, goodPairAnswer())
	default:
		f.t.Errorf("sign-in path %s", path)
		w.WriteHeader(http.StatusNotFound)
	}
}

// oauth is the OAuth on f, with the injected clock and a sleep that moves
// the clock by the interval and never waits.
func (f *signIn) oauth(c *clock) *OAuth {
	o := NewOAuth()
	o.BaseURL = f.srv.URL
	o.now = c.now
	o.sleep = func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.add(d)
		return nil
	}
	return o
}

// answer sets what the device code endpoint and the token endpoint answer.
func (f *signIn) answer(device, token func(w http.ResponseWriter, n int)) {
	f.mu.Lock()
	f.device, f.token = device, token
	f.mu.Unlock()
}

// tokenRequests is how many requests the token endpoint answered.
func (f *signIn) tokenRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokens
}

func (f *signIn) seen() (paths []string, forms []url.Values, queries []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...), append([]url.Values(nil), f.forms...), append([]string(nil), f.queries...)
}

func writeAnswer(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func goodDeviceAnswer() string {
	return `{"device_code":"` + testDevice + `","user_code":"ABCD-1234",` +
		`"verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`
}

// pairAnswer is a pair with an 8-hour access token and a 6-month refresh
// token.
func pairAnswer() string {
	return `"access_token":"ghu_ACCESS0000","refresh_token":"` + testRefresh + `",` +
		`"expires_in":28800,"refresh_token_expires_in":15897600,"token_type":"bearer"`
}

func goodPairAnswer() string { return `{` + pairAnswer() + `}` }

func TestStart(t *testing.T) {
	f := newSignIn(t)
	c := newClock()
	dc, err := f.oauth(c).Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if dc.DeviceCode != testDevice || dc.UserCode != "ABCD-1234" || dc.Interval != 5*time.Second {
		t.Errorf("device code %+v", dc)
	}
	if want := c.now().Add(15 * time.Minute); !dc.Expires.Equal(want) {
		t.Errorf("expires %v, want %v", dc.Expires, want)
	}
	paths, forms, queries := f.seen()
	if len(paths) != 1 || paths[0] != devicePath || queries[0] != "" {
		t.Fatalf("paths %v queries %v", paths, queries)
	}
	form := forms[0]
	if form.Get("client_id") != AppClientID || form.Get("scope") != "offline_access" {
		t.Errorf("form %v", form)
	}
	for _, name := range []string{"device_code", "refresh_token", "access_token", "client_secret"} {
		if _, ok := form[name]; ok {
			t.Errorf("Start sent %s", name)
		}
	}
}

func TestStartRefusesAnUnexpectedAnswer(t *testing.T) {
	for name, answer := range map[string]func(w http.ResponseWriter, n int){
		"another verification_uri": func(w http.ResponseWriter, _ int) {
			writeAnswer(w, 200, `{"device_code":"D","user_code":"U","verification_uri":"https://example.com/login/device","expires_in":900,"interval":5}`)
		},
		"no device_code": func(w http.ResponseWriter, _ int) {
			writeAnswer(w, 200, `{"user_code":"U","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`)
		},
		"no user_code": func(w http.ResponseWriter, _ int) {
			writeAnswer(w, 200, `{"device_code":"D","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`)
		},
		"no expires_in": func(w http.ResponseWriter, _ int) {
			writeAnswer(w, 200, `{"device_code":"D","user_code":"U","verification_uri":"https://github.com/login/device","interval":5}`)
		},
		"interval 0": func(w http.ResponseWriter, _ int) {
			writeAnswer(w, 200, `{"device_code":"D","user_code":"U","verification_uri":"https://github.com/login/device","expires_in":900,"interval":0}`)
		},
		"4xx with an error": func(w http.ResponseWriter, _ int) {
			writeAnswer(w, 422, `{"error":"invalid_client_id"}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSignIn(t)
			f.answer(answer, nil)
			_, err := f.oauth(newClock()).Start(context.Background())
			if !errors.Is(err, ErrUnexpected) {
				t.Fatalf("err %v", err)
			}
			if paths, _, _ := f.seen(); len(paths) != 1 {
				t.Errorf("paths %v", paths)
			}
		})
	}
}

func TestStartNoAnswer(t *testing.T) {
	for name, status := range map[string]int{"5xx": 502, "429": http.StatusTooManyRequests} {
		t.Run(name, func(t *testing.T) {
			f := newSignIn(t)
			f.answer(func(w http.ResponseWriter, n int) { writeAnswer(w, status, `{"message":"later"}`) }, nil)
			if _, err := f.oauth(newClock()).Start(context.Background()); !errors.Is(err, ErrNoAnswer) {
				t.Fatalf("err %v", err)
			}
		})
	}
	t.Run("not JSON", func(t *testing.T) {
		f := newSignIn(t)
		f.answer(func(w http.ResponseWriter, n int) { writeAnswer(w, 200, `device_code=D`) }, nil)
		if _, err := f.oauth(newClock()).Start(context.Background()); !errors.Is(err, ErrNoAnswer) {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("refused redirect", func(t *testing.T) {
		f := newSignIn(t)
		f.answer(func(w http.ResponseWriter, n int) {
			w.Header().Set("Location", "/login/device/elsewhere")
			w.WriteHeader(http.StatusTemporaryRedirect)
		}, nil)
		_, err := f.oauth(newClock()).Start(context.Background())
		if !errors.Is(err, ErrNoAnswer) {
			t.Fatalf("err %v", err)
		}
		paths, _, _ := f.seen()
		if len(paths) != 1 {
			t.Errorf("the redirect was followed: %v", paths)
		}
	})
}

// TestPoll: the poll goes on until a pair, and step 5 holds.
func TestPoll(t *testing.T) {
	f := newSignIn(t)
	c := newClock()
	o := f.oauth(c)
	dc, err := o.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.token = func(w http.ResponseWriter, n int) {
		switch n {
		case 1, 2:
			writeAnswer(w, 200, `{"error":"authorization_pending"}`)
		default:
			writeAnswer(w, 200, goodPairAnswer())
		}
	}
	start := c.now()
	tok, err := o.Poll(context.Background(), dc)
	if err != nil {
		t.Fatal(err)
	}
	if tok.ClientID != AppClientID || tok.Login != "" || tok.Access != "ghu_ACCESS0000" || tok.Refresh != testRefresh {
		t.Errorf("token %+v", tok)
	}
	// 3 polls, 5 seconds apart: the expiries count from the send time.
	sent := start.Add(15 * time.Second)
	if !tok.AccessExpires.Equal(sent.Add(8*time.Hour)) || !tok.RefreshExpires.Equal(sent.Add(184*24*time.Hour)) {
		t.Errorf("expiries %v, %v", tok.AccessExpires, tok.RefreshExpires)
	}
	_, forms, _ := f.seen()
	last := forms[len(forms)-1]
	if last.Get("client_id") != AppClientID || last.Get("device_code") != testDevice ||
		last.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
		t.Errorf("poll form %v", last)
	}
}

func TestPollSlowDownAndBadAnswers(t *testing.T) {
	f := newSignIn(t)
	c := newClock()
	var intervals []time.Duration
	o := f.oauth(c)
	o.sleep = func(ctx context.Context, d time.Duration) error {
		intervals = append(intervals, d)
		c.add(d)
		return nil
	}
	dc := DeviceCode{DeviceCode: testDevice, UserCode: "U", Expires: c.now().Add(15 * time.Minute), Interval: 5 * time.Second}
	f.token = func(w http.ResponseWriter, n int) {
		switch n {
		case 1:
			writeAnswer(w, 200, `{"error":"slow_down"}`)
		case 2:
			writeAnswer(w, 502, `{"message":"Server Error"}`)
		case 3:
			writeAnswer(w, 200, `not json`)
		case 4:
			writeAnswer(w, 429, `{"message":"slow down"}`)
		default:
			writeAnswer(w, 200, goodPairAnswer())
		}
	}
	if _, err := o.Poll(context.Background(), dc); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{5 * time.Second, 10 * time.Second, 10 * time.Second, 10 * time.Second, 10 * time.Second}
	if len(intervals) != len(want) {
		t.Fatalf("intervals %v", intervals)
	}
	for i := range want {
		if intervals[i] != want[i] {
			t.Errorf("interval %d is %v, want %v", i, intervals[i], want[i])
		}
	}
}

func TestPollEnds(t *testing.T) {
	for name, tc := range map[string]struct {
		answer string
		want   error
	}{
		"access_denied":  {`{"error":"access_denied"}`, ErrDenied},
		"expired_token":  {`{"error":"expired_token"}`, ErrCodeExpired},
		"another error":  {`{"error":"unsupported_grant_type"}`, ErrUnexpected},
		"4xx with none":  {``, ErrUnexpected},
		"no refresh":     {`{"access_token":"ghu_A","expires_in":28800,"refresh_token_expires_in":15897600}`, ErrUnexpected},
		"no expires_in":  {`{"access_token":"ghu_A","refresh_token":"` + testRefresh + `","refresh_token_expires_in":15897600}`, ErrUnexpected},
		"expires_in 600": {`{"access_token":"ghu_A","refresh_token":"` + testRefresh + `","expires_in":600,"refresh_token_expires_in":15897600}`, ErrUnexpected},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSignIn(t)
			c := newClock()
			dc := DeviceCode{DeviceCode: testDevice, Expires: c.now().Add(15 * time.Minute), Interval: 5 * time.Second}
			f.token = func(w http.ResponseWriter, n int) {
				if tc.answer == "" {
					writeAnswer(w, 403, `{"message":"no"}`)
					return
				}
				writeAnswer(w, 200, tc.answer)
			}
			_, err := f.oauth(c).Poll(context.Background(), dc)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err %v, want %v", err, tc.want)
			}
			if strings.Contains(err.Error(), testDevice) || strings.Contains(err.Error(), testRefresh) {
				t.Errorf("the error holds a secret: %v", err)
			}
		})
	}
}

// TestPollToTheDeadline: the poll ends at the code's expiry, and a success
// late in its life is taken.
func TestPollToTheDeadline(t *testing.T) {
	t.Run("deadline", func(t *testing.T) {
		f := newSignIn(t)
		c := newClock()
		dc := DeviceCode{DeviceCode: testDevice, Expires: c.now().Add(time.Minute), Interval: 5 * time.Second}
		f.token = func(w http.ResponseWriter, n int) { writeAnswer(w, 200, `{"error":"authorization_pending"}`) }
		if _, err := f.oauth(c).Poll(context.Background(), dc); !errors.Is(err, ErrCodeExpired) {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("at minute 7", func(t *testing.T) {
		f := newSignIn(t)
		c := newClock()
		dc := DeviceCode{DeviceCode: testDevice, Expires: c.now().Add(15 * time.Minute), Interval: 5 * time.Second}
		f.token = func(w http.ResponseWriter, n int) {
			if n < 84 {
				writeAnswer(w, 200, `{"error":"authorization_pending"}`)
				return
			}
			writeAnswer(w, 200, goodPairAnswer())
		}
		if _, err := f.oauth(c).Poll(context.Background(), dc); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPollCancel(t *testing.T) {
	f := newSignIn(t)
	c := newClock()
	f.token = func(w http.ResponseWriter, n int) { writeAnswer(w, 200, `{"error":"authorization_pending"}`) }
	ctx, cancel := context.WithCancel(context.Background())
	polls := 0
	o := f.oauth(c)
	o.sleep = func(ctx context.Context, d time.Duration) error {
		polls++
		if polls == 3 {
			cancel()
		}
		return ctx.Err()
	}
	dc := DeviceCode{DeviceCode: testDevice, Expires: c.now().Add(15 * time.Minute), Interval: 5 * time.Second}
	_, err := o.Poll(ctx, dc)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if got := f.tokenRequests(); got != 2 {
		t.Errorf("token requests %d", got)
	}
}

func TestRefresh(t *testing.T) {
	f := newSignIn(t)
	c := newClock()
	o := f.oauth(c)
	c.add(time.Minute)
	tok, err := o.Refresh(context.Background(), testRefresh)
	if err != nil {
		t.Fatal(err)
	}
	if tok.ClientID != AppClientID || tok.Login != "" || tok.Access != "ghu_ACCESS0000" {
		t.Errorf("token %+v", tok)
	}
	if want := c.now().Add(8 * time.Hour); !tok.AccessExpires.Equal(want) {
		t.Errorf("access expires %v, want %v", tok.AccessExpires, want)
	}
	paths, forms, queries := f.seen()
	if len(paths) != 1 || paths[0] != tokenPath || queries[0] != "" {
		t.Fatalf("paths %v queries %v", paths, queries)
	}
	if forms[0].Get("grant_type") != "refresh_token" || forms[0].Get("refresh_token") != testRefresh ||
		forms[0].Get("client_id") != AppClientID {
		t.Errorf("form %v", forms[0])
	}
}

func TestRefreshEndsTheSignIn(t *testing.T) {
	for name, answer := range map[string]func(w http.ResponseWriter, n int){
		"bad_refresh_token": func(w http.ResponseWriter, n int) {
			writeAnswer(w, 200, `{"error":"bad_refresh_token"}`)
		},
		"another error": func(w http.ResponseWriter, n int) {
			writeAnswer(w, 200, `{"error":"unsupported_grant_type"}`)
		},
		"4xx with no error": func(w http.ResponseWriter, n int) {
			writeAnswer(w, 401, `{"message":"Bad credentials"}`)
		},
		"malformed success": func(w http.ResponseWriter, n int) {
			writeAnswer(w, 200, `{"access_token":""}`)
		},
		"short expiry": func(w http.ResponseWriter, n int) {
			writeAnswer(w, 200, `{"access_token":"ghu_A","refresh_token":"R","expires_in":600,"refresh_token_expires_in":15897600}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSignIn(t)
			f.token = answer
			_, err := f.oauth(newClock()).Refresh(context.Background(), testRefresh)
			if !errors.Is(err, ErrSignIn) {
				t.Fatalf("err %v", err)
			}
			if strings.Contains(err.Error(), testRefresh) {
				t.Errorf("the error holds the refresh token: %v", err)
			}
		})
	}
}

func TestRefreshNoAnswer(t *testing.T) {
	for name, answer := range map[string]func(w http.ResponseWriter, n int){
		"5xx":      func(w http.ResponseWriter, n int) { writeAnswer(w, 503, `{"message":"later"}`) },
		"429":      func(w http.ResponseWriter, n int) { writeAnswer(w, 429, `{"message":"later"}`) },
		"not JSON": func(w http.ResponseWriter, n int) { writeAnswer(w, 200, `error=bad`) },
		"redirect": func(w http.ResponseWriter, n int) {
			w.Header().Set("Location", "/elsewhere")
			w.WriteHeader(http.StatusTemporaryRedirect)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSignIn(t)
			f.token = answer
			if _, err := f.oauth(newClock()).Refresh(context.Background(), testRefresh); !errors.Is(err, ErrNoAnswer) {
				t.Fatalf("err %v", err)
			}
		})
	}
}

// TestSignInRedactsSecrets: no error text holds a token, a refresh token or
// a device code, and no secret goes in a URL.
func TestSignInRedactsSecrets(t *testing.T) {
	f := newSignIn(t)
	c := newClock()
	f.token = func(w http.ResponseWriter, n int) {
		if n == 1 {
			writeAnswer(w, 200, `{"error":"bad_refresh_token","error_description":"`+testRefresh+` and `+testDevice+` are gone"}`)
			return
		}
		writeAnswer(w, 200, `{"error":"nope","error_description":"`+testDevice+`"}`)
	}
	_, err := f.oauth(c).Refresh(context.Background(), testRefresh)
	if err == nil {
		t.Fatal("no error")
	}
	if strings.Contains(err.Error(), testRefresh) || strings.Contains(err.Error(), testDevice) {
		t.Errorf("the error holds a secret: %v", err)
	}
	dc := DeviceCode{DeviceCode: testDevice, Expires: c.now().Add(15 * time.Minute), Interval: 5 * time.Second}
	if _, err := f.oauth(c).Poll(context.Background(), dc); err == nil || strings.Contains(err.Error(), testDevice) {
		t.Errorf("poll error %v", err)
	}
	_, _, queries := f.seen()
	for _, q := range queries {
		if q != "" {
			t.Errorf("a sign-in request has a query string: %q", q)
		}
	}
}

// TestStartNamesTheError: an error field refuses the sign-in whatever the
// status is, with the error's own name.
func TestStartNamesTheError(t *testing.T) {
	for name, status := range map[string]int{"200": 200, "422": 422} {
		t.Run(name, func(t *testing.T) {
			f := newSignIn(t)
			f.answer(func(w http.ResponseWriter, n int) {
				writeAnswer(w, status, `{"error":"device_flow_disabled"}`)
			}, nil)
			_, err := f.oauth(newClock()).Start(context.Background())
			if !errors.Is(err, ErrUnexpected) || !strings.Contains(err.Error(), "device_flow_disabled") {
				t.Fatalf("err %v", err)
			}
		})
	}
}

// blockToken makes the token endpoint wait, and answers status and body when
// the release is called. entered closes when its first request is in.
func (f *signIn) blockToken(t *testing.T, status int, body string) (release func(), entered chan struct{}) {
	t.Helper()
	gate, entered := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	var mu sync.Mutex
	n := 0
	f.answer(nil, func(w http.ResponseWriter, _ int) {
		mu.Lock()
		first := n == 0
		n++
		mu.Unlock()
		if first {
			close(entered)
		}
		<-gate
		writeAnswer(w, status, body)
	})
	return release, entered
}

// holdToken makes the token endpoint wait for release, and gives the
// channel that closes when its first request is in.
func (f *signIn) holdToken(t *testing.T) (release func(), entered chan struct{}) {
	t.Helper()
	gate, entered := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	var mu sync.Mutex
	n := 0
	f.answer(nil, func(w http.ResponseWriter, _ int) {
		mu.Lock()
		first := n == 0
		n++
		mu.Unlock()
		if first {
			close(entered)
		}
		<-gate
		writeAnswer(w, 200, goodPairAnswer())
	})
	return release, entered
}
