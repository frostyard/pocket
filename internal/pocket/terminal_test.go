package pocket

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTerminal is the terminal module's routes for one shell, in memory.
type fakeTerminal struct {
	mu      sync.Mutex
	output  string
	exited  bool
	inputs  []string
	seqs    map[string]int64
	resizes [][2]int
	fail    int // input POSTs that fail with a dropped connection first
	more    chan struct{}
}

func (f *fakeTerminal) serve(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Pocket") != "1" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, `{"error":"no"}`, http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/x/terminal/poll":
			// t is "id.from.ack": the answer starts at from.
			parts := strings.Split(r.URL.Query().Get("t"), ".")
			from, _ := strconv.Atoi(parts[1])
			deadline := time.After(2 * time.Second)
			for {
				f.mu.Lock()
				out, exited := f.output, f.exited
				f.mu.Unlock()
				if len(out) > from || exited {
					ans := map[string]any{"data": out[from:], "from": from, "to": len(out)}
					if exited {
						ans["exited"], ans["exitCode"] = true, 3
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"terminals": map[string]any{"s1": ans}})
					return
				}
				select {
				case <-f.more:
				case <-deadline:
					_ = json.NewEncoder(w).Encode(map[string]any{"terminals": map[string]any{"s1": map[string]any{"data": "", "from": from, "to": from}}})
					return
				}
			}
		case "/api/x/terminal/terminals/s1/input":
			f.mu.Lock()
			if f.fail > 0 {
				f.fail--
				f.mu.Unlock()
				hj, _ := w.(http.Hijacker)
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
				return
			}
			var body struct {
				Data string `json:"data"`
				Tab  string `json:"tab"`
				Seq  int64  `json:"seq"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Seq > f.seqs[body.Tab] {
				f.seqs[body.Tab] = body.Seq
				f.inputs = append(f.inputs, body.Data)
			}
			f.mu.Unlock()
			_, _ = io.WriteString(w, "{}")
		case "/api/x/terminal/terminals/s1/resize":
			var body struct{ Rows, Cols int }
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.resizes = append(f.resizes, [2]int{body.Cols, body.Rows})
			f.mu.Unlock()
			_, _ = io.WriteString(w, "{}")
		default:
			http.Error(w, `{"error":"No such terminal"}`, http.StatusNotFound)
		}
	}))
}

func (f *fakeTerminal) print(s string) {
	f.mu.Lock()
	f.output += s
	f.mu.Unlock()
	select {
	case f.more <- struct{}{}:
	default:
	}
}

func TestTermConnReadsWritesAndEnds(t *testing.T) {
	f := &fakeTerminal{seqs: map[string]int64{}, more: make(chan struct{}, 1), fail: 1}
	srv := f.serve(t)
	defer srv.Close()
	c, _ := NewClient(srv.URL, "tok")
	conn := c.OpenTerminal("s1")
	conn.retry = 10 * time.Millisecond
	defer conn.Close()

	f.print("$ ")
	got := make(chan string, 8)
	go func() {
		buf := make([]byte, 64)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				got <- string(buf[:n])
			}
			if err != nil {
				close(got)
				return
			}
		}
	}()
	if s := <-got; s != "$ " {
		t.Fatalf("first read %q", s)
	}
	// A key whose first POST is dropped is sent again, and written once.
	if _, err := conn.Write([]byte("ls\r")); err != nil {
		t.Fatal(err)
	}
	if err := conn.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	inputs, resizes := f.inputs, f.resizes
	f.mu.Unlock()
	if len(inputs) != 1 || inputs[0] != "ls\r" || len(resizes) != 1 || resizes[0] != [2]int{100, 30} {
		t.Fatalf("inputs %q resizes %v", inputs, resizes)
	}
	f.print("file.txt\r\n")
	if s := <-got; s != "file.txt\r\n" {
		t.Fatalf("second read %q", s)
	}
	f.mu.Lock()
	f.exited = true
	f.mu.Unlock()
	f.print("")
	for range got {
	}
	if code := conn.ExitCode(); code != 3 {
		t.Fatalf("exit code %d", code)
	}
}
