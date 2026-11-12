package pocket

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"
)

// fakeTerminal is the terminal module's routes for one shell, in memory: a
// pocket.Client transport, so the tests use no network and no clock.
type fakeTerminal struct {
	mu     sync.Mutex
	output string
	exited bool
	gone   bool
	// inputs is what the shell took: an input whose seq it has seen is
	// taken once, as terminal.ts does.
	inputs  []string
	seqs    map[string]int64
	posts   int
	resizes [][2]int
	// drop is how many input POSTs the shell takes and whose answers are
	// then lost, as a dropped connection loses them.
	drop int
	// refuse answers input POSTs with 404, as for a shell that went.
	refuse bool
	more   chan struct{}
}

func newFakeTerminal() *fakeTerminal {
	return &fakeTerminal{seqs: map[string]int64{}, more: make(chan struct{}, 1)}
}

var errDropped = errors.New("connection reset by peer")

func (f *fakeTerminal) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("X-Pocket") != "1" || r.Header.Get("Authorization") != "Bearer tok" {
		return answer(r, http.StatusUnauthorized, `{"error":"no"}`), nil
	}
	switch r.URL.Path {
	case "/api/x/terminal/poll":
		// t is "id.from.ack", from in UTF-16 code units, as the server
		// counts a JavaScript string.
		parts := strings.Split(r.URL.Query().Get("t"), ".")
		from, _ := strconv.Atoi(parts[1])
		for {
			f.mu.Lock()
			units := utf16.Encode([]rune(f.output))
			exited, gone := f.exited, f.gone
			f.mu.Unlock()
			if gone {
				return answer(r, http.StatusNotFound, `{"error":"No such terminal"}`), nil
			}
			if len(units) > from || exited {
				from = min(from, len(units))
				ans := map[string]any{"data": string(utf16.Decode(units[from:])), "from": from, "to": len(units)}
				if exited {
					ans["exited"], ans["exitCode"] = true, 3
				}
				body, _ := json.Marshal(map[string]any{"terminals": map[string]any{"s1": ans}})
				return answer(r, http.StatusOK, string(body)), nil
			}
			select {
			case <-f.more:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
	case "/api/x/terminal/terminals/s1/input":
		var body struct {
			Data string `json:"data"`
			Tab  string `json:"tab"`
			Seq  int64  `json:"seq"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return answer(r, http.StatusBadRequest, `{"error":"bad body"}`), nil
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.posts++
		if f.refuse {
			return answer(r, http.StatusNotFound, `{"error":"No such terminal"}`), nil
		}
		if body.Seq > f.seqs[body.Tab] {
			f.seqs[body.Tab] = body.Seq
			f.inputs = append(f.inputs, body.Data)
		}
		if f.drop > 0 {
			// The shell took the keys; the answer is lost on the way.
			f.drop--
			return nil, errDropped
		}
		return answer(r, http.StatusOK, `{}`), nil
	case "/api/x/terminal/terminals/s1/resize":
		var body struct{ Rows, Cols int }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.resizes = append(f.resizes, [2]int{body.Cols, body.Rows})
		f.mu.Unlock()
		return answer(r, http.StatusOK, `{}`), nil
	}
	return answer(r, http.StatusNotFound, `{"error":"No such terminal"}`), nil
}

func answer(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func (f *fakeTerminal) update(change func()) {
	f.mu.Lock()
	change()
	f.mu.Unlock()
	select {
	case f.more <- struct{}{}:
	default:
	}
}

func (f *fakeTerminal) print(s string) { f.update(func() { f.output += s }) }

// open connects to the fake's shell, trying again at once after a failure.
func (f *fakeTerminal) open(t *testing.T) (*TermConn, <-chan string) {
	t.Helper()
	c, err := NewClient("http://pocket.example.org", "tok")
	if err != nil {
		t.Fatal(err)
	}
	c.HTTP = &http.Client{Transport: f}
	conn := c.OpenTerminal("s1")
	conn.retry = 0
	t.Cleanup(func() { _ = conn.Close() })
	got := make(chan string, 8)
	go func() {
		defer close(got)
		buf := make([]byte, 64)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				got <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	return conn, got
}

func TestTermConnReadsWritesAndEnds(t *testing.T) {
	f := newFakeTerminal()
	f.drop = 1
	conn, got := f.open(t)

	// é is one UTF-16 unit and 🙂 two, in two and four bytes: the next poll
	// starts where the server's "to" says, not at a byte count.
	f.print("é🙂 $ ")
	if s := <-got; s != "é🙂 $ " {
		t.Fatalf("first read %q", s)
	}
	// The shell takes the keys and the answer is lost: they are sent
	// again, with the same seq, and the shell writes them once.
	if _, err := conn.Write([]byte("ls\r")); err != nil {
		t.Fatal(err)
	}
	if err := conn.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	inputs, posts, resizes := f.inputs, f.posts, f.resizes
	f.mu.Unlock()
	if posts != 2 {
		t.Fatalf("the keys went %d times, want 2 (sent again after the lost answer)", posts)
	}
	if len(inputs) != 1 || inputs[0] != "ls\r" || len(resizes) != 1 || resizes[0] != [2]int{100, 30} {
		t.Fatalf("inputs %q resizes %v", inputs, resizes)
	}
	f.print("file.txt\r\n")
	if s := <-got; s != "file.txt\r\n" {
		t.Fatalf("second read %q", s)
	}
	f.update(func() { f.exited = true })
	for s := range got {
		t.Errorf("read %q after the shell exited", s)
	}
	if code := conn.ExitCode(); code != 3 {
		t.Fatalf("exit code %d", code)
	}
}

// TestTermConnEndsWhenTheShellGoes: a shell the server no longer has, and
// keys it refuses, end the connection with no exit code, so the view says
// it is disconnected rather than taking keys that go nowhere.
func TestTermConnEndsWhenTheShellGoes(t *testing.T) {
	t.Run("gone", func(t *testing.T) {
		f := newFakeTerminal()
		conn, got := f.open(t)
		f.update(func() { f.gone = true })
		for s := range got {
			t.Errorf("read %q from a shell that went", s)
		}
		if code := conn.ExitCode(); code != -1 {
			t.Fatalf("exit code %d, want -1", code)
		}
	})
	t.Run("refused keys", func(t *testing.T) {
		f := newFakeTerminal()
		f.refuse = true
		conn, got := f.open(t)
		if _, err := conn.Write([]byte("x")); err == nil {
			t.Fatal("refused keys were written")
		}
		if conn.ctx.Err() == nil {
			t.Fatal("the connection stays open after its keys were refused")
		}
		for s := range got {
			t.Errorf("read %q after the keys were refused", s)
		}
		f.mu.Lock()
		posts := f.posts
		f.mu.Unlock()
		if posts != 1 || conn.ExitCode() != -1 {
			t.Fatalf("posts %d exit code %d: a refusal is not sent again, and ends with no code", posts, conn.ExitCode())
		}
	})
}
