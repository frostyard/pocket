package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type boardHTTP func(*http.Request) (*http.Response, error)

func (f boardHTTP) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBoardEditUsesOpenedRevisionAndRefreshesConflict(t *testing.T) {
	a, loop := teamApp(t)
	sup := 7
	row := pocket.BoardRow{ID: 2, Rev: 3, State: "proposed", Max: 4, Until: "23:00"}
	a.desk.board = &pocket.Board{Supervisor: &sup, Rows: []pocket.BoardRow{row}}
	a.desk.boardChosen = 7
	editor := &boardEditor{row: row, action: "edit", max: "8", until: "22:00"}
	a.desk.boardEditors = map[int]*boardEditor{2: editor}
	a.desk.board.Rows[0].Rev = 4 // a refresh must not change the form's expected revision
	a.link.client.HTTP = &http.Client{Transport: boardHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			if r.URL.Path != "/api/x/desk/board/7" {
				t.Errorf("path %s", r.URL.Path)
			}
			var body struct {
				Op       string
				Row, Rev int
				Fields   map[string]any
				Key      string
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Op != "edit" || body.Row != 2 || body.Rev != 3 || body.Fields["max"] != float64(8) || body.Key == "" {
				t.Errorf("wrong edit: %+v", body)
			}
			resp := reply(r, `{"error":"The row changed"}`)
			resp.StatusCode = 409
			return resp, nil
		}
		if r.URL.Query().Get("supervisor") != "7" {
			t.Errorf("refresh lost supervisor: %s", r.URL)
		}
		return reply(r, `{"supervisor":7,"rows":[{"id":2,"rev":5,"state":"proposed"}]}`), nil
	})}
	a.boardChange("edit", &editor.row, nil, map[string]any{"max": 8, "until": "22:00"}, "")
	loop.RunUntil(t, func() bool { return !a.desk.boardBusy && !a.desk.boardLoading && a.desk.board.Rows[0].Rev == 5 })
	if a.desk.boardEditors[2] != editor || len(a.desk.boardKeys) != 0 {
		t.Fatal("conflict lost form or retained rejected key")
	}
}

func TestBoardFailedSupervisorReadRestoresDisplayedBoard(t *testing.T) {
	a, loop := teamApp(t)
	sup := 7
	a.desk.board = &pocket.Board{Supervisor: &sup}
	a.desk.boardChosen = 8
	a.link.client.HTTP = &http.Client{Transport: boardHTTP(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("supervisor") != "8" {
			t.Errorf("wrong choice %s", r.URL)
		}
		resp := reply(r, `{"error":"Unavailable"}`)
		resp.StatusCode = 503
		return resp, nil
	})}
	a.loadBoard()
	loop.RunUntil(t, func() bool { return !a.desk.boardLoading })
	if a.desk.boardChosen != 7 || a.desk.board.Supervisor != &sup || a.desk.boardErr == "" {
		t.Fatal("failed read left a mismatched board choice")
	}
}

func TestBoardDetailsAndActionsSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	sup := 7
	row := pocket.BoardRow{ID: 2, Rev: 3, Title: "Improve build reliability", State: "proposed", Agent: "duncan", Task: "Investigate intermittent build failures", Objective: "Repeatable builds", StopWhen: "All target builds pass", Worker: "default", Note: "Keep the reproduction", Max: 4, Until: "23:00", Kind: "fix"}
	a.desk.board = &pocket.Board{Supervisor: &sup, Supervisors: []int{7, 8}, Titles: map[string]any{"7": "First supervisor", "8": "Second supervisor"}, Rows: []pocket.BoardRow{row}}
	a.desk.boardOpen = map[int]bool{2: true}
	a.desk.boardLoaded = true
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.boardPane(c) }, 1000, 1000, ui.TesterOptions{})
	tt.Frame()
	for _, label := range []string{row.Task, row.Objective, row.StopWhen, "Edit limits", "Drop…", "First supervisor (7)"} {
		if !tt.HasText(label) {
			t.Errorf("missing %s", label)
		}
	}
	if err := tt.Click("Edit limits"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.desk.boardEditors[2].row.Rev != 3 {
		t.Fatal("editor did not capture displayed revision")
	}
	writePNG(t, tt, "board-row-details")
}

func TestBoardFallbackFollowsReturnedSupervisor(t *testing.T) {
	a, loop := teamApp(t)
	sup := 8
	a.desk.board = &pocket.Board{Supervisor: &sup}
	a.desk.boardChosen = 8
	a.desk.boardEditors = map[int]*boardEditor{2: {action: "drop"}}
	posts := make(chan string, 1)
	a.link.client.HTTP = &http.Client{Transport: boardHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			posts <- r.URL.Path
			return reply(r, `{"text":"Picked"}`), nil
		}
		return reply(r, `{"supervisor":7,"supervisors":[7],"rows":[{"id":2,"rev":1,"state":"proposed"}]}`), nil
	})}
	a.loadBoard()
	loop.RunUntil(t, func() bool { return !a.desk.boardLoading })
	if a.desk.boardChosen != 7 || len(a.desk.boardEditors) != 0 {
		t.Fatal("fallback did not follow supervisor and clear old forms")
	}
	a.boardOp("pick", &a.desk.board.Rows[0], nil)
	loop.RunUntil(t, func() bool { return !a.desk.boardBusy && !a.desk.boardLoading })
	select {
	case path := <-posts:
		if path != "/api/x/desk/board/7" {
			t.Fatalf("posted to %s", path)
		}
	default:
		t.Fatal("fallback silently dropped the operation")
	}
}

func TestBoardRetryKeyIgnoresDisplayFactsAndShowsLaunchPass(t *testing.T) {
	a, loop := teamApp(t)
	sup := 7
	a.desk.board = &pocket.Board{Supervisor: &sup, Rows: []pocket.BoardRow{{ID: 2, Rev: 3, State: "picked"}}}
	keys := make(chan string, 2)
	calls := 0
	a.link.client.HTTP = &http.Client{Transport: boardHTTP(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			var body struct{ Key string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			keys <- body.Key
			calls++
			if calls == 1 {
				return nil, fmt.Errorf("connection closed")
			}
			return reply(r, `{"text":"Rows queued","pass":["Check failed; rows wait for the next sweep"]}`), nil
		}
		return reply(r, `{"supervisor":7,"rows":[{"id":2,"rev":3,"state":"picked","changedAt":123,"next":"agent","threadState":"open"}]}`), nil
	})}
	a.boardOp("launch", nil, a.desk.board.Rows)
	loop.RunUntil(t, func() bool { return !a.desk.boardBusy && !a.desk.boardLoading })
	a.boardOp("launch", nil, a.desk.board.Rows)
	loop.RunUntil(t, func() bool { return !a.desk.boardBusy && !a.desk.boardLoading })
	if first, second := <-keys, <-keys; first != second || first == "" {
		t.Fatal("display refresh changed the uncertain retry key")
	}
	notices := strings.Join(a.pendingToasts, "\n")
	if !strings.Contains(notices, "may have been applied") || !strings.Contains(notices, "Check failed") {
		t.Fatalf("missing operation notices: %s", notices)
	}
}

func TestBoardControlsFollowRowState(t *testing.T) {
	for state, want := range map[string][]string{"proposed": {"Edit limits", "Drop…"}, "picked": {"Edit limits", "Drop…"}, "waiting": {"Drop…"}, "failed": {"Reopen"}, "unknown": {"Resolve…"}, "starting": {}, "launched": {}, "dropped": {}} {
		t.Run(state, func(t *testing.T) {
			a, _ := teamApp(t)
			tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.boardRowDetails(c, pocket.BoardRow{ID: 2, State: state}) }, 600, 500, ui.TesterOptions{})
			tt.Frame()
			for _, label := range []string{"Edit limits", "Drop…", "Reopen", "Resolve…"} {
				if tt.HasText(label) != slices.Contains(want, label) {
					t.Errorf("%s availability in %s", label, state)
				}
			}
		})
	}
}
