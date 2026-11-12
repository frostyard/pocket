package pocket

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestSSEReaderReadsEvents(t *testing.T) {
	src := "retry: 1500\n\n: comment\nevent: hello\ndata: {\"a\":1}\n\ndata: line1\ndata: line2\nid: 7\n\n"
	r := newSSEReader(strings.NewReader(src))
	ev, err := r.Next()
	if err != nil || ev.Name != "hello" || string(ev.Data) != `{"a":1}` {
		t.Fatalf("first = %+v, %v", ev, err)
	}
	ev, err = r.Next()
	if err != nil || ev.Name != "message" || string(ev.Data) != "line1\nline2" || ev.ID != "7" {
		t.Fatalf("second = %+v, %v", ev, err)
	}
	if r.retry != 1500*time.Millisecond {
		t.Fatalf("retry = %v", r.retry)
	}
	if _, err := r.Next(); err != io.EOF {
		t.Fatalf("end = %v", err)
	}
}
