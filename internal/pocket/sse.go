package pocket

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"time"
)

// Event is one server-sent event.
type Event struct {
	Name string
	Data []byte
	ID   string
}

// sseReader reads text/event-stream framing.
type sseReader struct {
	r     *bufio.Reader
	retry time.Duration
}

func newSSEReader(r io.Reader) *sseReader {
	return &sseReader{r: bufio.NewReaderSize(r, 64<<10)}
}

// Next returns the next event, skipping comments and empty events.
func (s *sseReader) Next() (Event, error) {
	var ev Event
	var data strings.Builder
	hasData := false
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			return Event{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if !hasData {
				ev = Event{}
				continue
			}
			ev.Data = []byte(data.String())
			if ev.Name == "" {
				ev.Name = "message"
			}
			return ev, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			ev.Name = value
		case "data":
			if hasData {
				data.WriteByte('\n')
			}
			data.WriteString(value)
			hasData = true
		case "id":
			ev.ID = value
		case "retry":
			if ms, err := strconv.Atoi(value); err == nil {
				s.retry = time.Duration(ms) * time.Millisecond
			}
		}
	}
}
