package app

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/frostui/frostuitest"

	"github.com/frostyard/pocket/internal/config"
	"github.com/frostyard/pocket/internal/icons"
	"github.com/frostyard/pocket/internal/pocket"
)

// teamApp returns an app with a made-up team and its sessions, shown
// without a server.
func teamApp(t *testing.T) (*App, *frostuitest.Loop) {
	t.Helper()
	t.Setenv("FROSTUI_ICON_THEME", "Adwaita")
	frostui.SetIcons(frostui.NewIcons(icons.Embedded()))
	store, _ := config.Open(t.TempDir())
	loop := frostuitest.NewLoop()
	now := time.Date(2026, 11, 12, 15, 30, 0, 0, time.Local)
	a, err := New(Options{Post: loop.Post, Store: store, Creds: config.Credentials{Dir: store.Dir()}, Version: "test",
		Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	a.link.client, _ = pocket.NewClient("http://127.0.0.1:9", "test")
	a.conn = frostui.Connection{State: frostui.Connected, Host: "pocket.example.ts.net", Daemon: "Pi Pocket"}
	a.hello = &pocket.Hello{User: pocket.User{ID: "u1", Name: "Brian", Role: "owner"},
		Server: pocket.ServerInfo{PocketVersion: "0.28.1", Home: "/home/team"}}
	a.team = team{on: true, supervisor: &pocket.Supervisor{Found: true, ConversationID: 1, Asks: 2}, agents: []pocket.Persona{
		{ID: "odrade", Name: "Odrade", Role: "supervisor", Home: "/home/team/agents/odrade", Model: "anthropic/claude-opus-5-5"},
		{ID: "duncan", Name: "Duncan", Role: "agent", Home: "/home/team/agents/duncan", Model: "anthropic/claude-sonnet-5-5",
			Repositories: map[string]string{"frostyard/nsl": "/home/team/projects/nsl", "frostyard/snow": "/home/team/projects/snow"},
			Instructions: "# Duncan\n\nA swordmaster of build systems.\n\n## Responsibilities\n\n- CI and release tooling\n- The nsl repository"},
		{ID: "irulan", Name: "Irulan", Role: "clerk", Home: "/home/team/agents/irulan"},
		{ID: "sheeana", Name: "Sheeana", Role: "agent", Home: "/home/team/agents/sheeana"},
	}}
	ms := func(d time.Duration) pocket.Millis { return pocket.Millis(now.Add(-d).UnixMilli()) }
	a.setSessions([]pocket.Session{
		{ID: 1, Title: "Plan the Q4 roadmap", Cwd: "/home/team/agents/odrade", UpdatedAt: ms(time.Minute), Busy: true, Model: "claude-opus-5-5",
			Subagents: &pocket.SubagentCounts{Working: 2}},
		{ID: 2, Title: "Fix the flaky CI in nsl", Cwd: "/home/team/projects/nsl--duncan-fix-ci", UpdatedAt: ms(2 * time.Minute), Waiting: true,
			Approval: &pocket.SessionApproval{ConversationID: 2, Tool: "bash", Subject: "git push --force-with-lease origin duncan/fix-ci", Count: 1},
			Model:    "claude-sonnet-5-5"},
		{ID: 3, Title: "Desk dashboard: weekly metrics", Cwd: "/home/team/projects/pi-pocket-team--sheeana-3b192616", UpdatedAt: ms(4 * time.Minute),
			Busy: true, Model: "claude-sonnet-5-5"},
		{ID: 4, Title: "Draft the release notes", Cwd: "/home/team/agents/irulan", UpdatedAt: ms(20 * time.Minute), EndedAt: ms(20 * time.Minute),
			Model: "claude-haiku-5-5"},
		{ID: 5, Title: "Snow image fails to boot", Cwd: "/home/team/projects/snow--duncan-boot", UpdatedAt: ms(time.Hour), EndedAt: ms(time.Hour),
			Failed: true, Failure: "Hit the spend limit ($5.00)"},
		{ID: 6, Title: "Homewiki: garden page", Cwd: "/home/team/projects/homewiki", UpdatedAt: ms(30 * time.Hour), EndedAt: ms(30 * time.Hour)},
	})
	a.markSeen(6)
	for _, id := range []int{2, 3, 5} {
		a.byID[id].LinkedFrom = &struct {
			ID int `json:"id"`
		}{ID: 1}
	}
	a.peeks[1] = &pocket.Peek{ConversationID: 1, Lines: []pocket.PeekLine{
		{Kind: "text", Text: "Delegating the dashboard work to Sheeana."},
		{Kind: "tool", Name: "team_delegate", Args: map[string]any{"agent": "sheeana"}, Status: "done"},
	}}
	a.peeks[3] = &pocket.Peek{ConversationID: 3, Lines: []pocket.PeekLine{
		{Kind: "tool", Name: "bash", Args: map[string]any{"command": "go test ./... -run TestDashboard"}, Status: "running"},
	}}
	a.desk = deskFixture(ms)
	return a, loop
}

func deskFixture(ms func(time.Duration) pocket.Millis) desk {
	gen := 0
	v := &pocket.DeskView{
		Threads: []pocket.DeskThread{
			{ID: "T1439", Title: "frostmail Gmail OAuth: what app verification needs", Origin: "delegate", Agents: []string{"irulan"},
				Repo: "frostyard/frostmail", State: "open", Next: "brian", LastEventAt: ms(30 * time.Minute),
				Asks: []pocket.DeskAsk{
					{ID: "T1439b", Text: "Gmail OAuth publish needs **branding verification** first. Tamalane is making the 2 frostyard.org pages (Frostmail home, privacy; T1459). When they are live: Google Auth Platform > Branding, then Audience > Publish app.", At: ms(40 * time.Minute), Options: []string{"Done", "Later"}, From: "irulan", Gen: &gen},
					{ID: "T1439c", Text: "Verify frostyard.org in Google Search Console now (Domain property, DNS TXT record in Cloudflare); Google then wants 24 h before the branding retry.", At: ms(35 * time.Minute), Options: []string{"Done", "Later"}, From: "irulan"},
				},
				Open: []string{"2 asks wait for Brian"}},
			{ID: "T1455", Title: "frostui: correctness for the apps (#10, B1 menu, mailto attach)", Origin: "delegate", Agents: []string{"duncan"},
				Repo: "frostyard/frostui", State: "open", Next: "agent", LastEventAt: ms(4 * time.Minute),
				PRs: []pocket.DeskPR{{Ref: "frostyard/frostui#143", State: "OPEN", Title: "correctness for the apps"}}},
			{ID: "T1460", Title: "Desk dashboard: weekly metrics", Origin: "delegate", Agents: []string{"sheeana"}, Repo: "bketelsen/pi-pocket-team",
				State: "open", Next: "agent", LastEventAt: ms(2 * time.Minute)},
			{ID: "T1456", Title: "Required checks disabled during the 11-11 GitHub outage", Origin: "ask", Agents: []string{"odrade"},
				State: "open", Next: "odrade", LastEventAt: ms(3 * time.Hour)},
			{ID: "T1450", Title: "frostmail + frostnotes: issue triage and cleanup", Origin: "delegate", Agents: []string{"irulan"}, Repo: "frostyard/frostmail",
				State: "open", Next: "reviewer", LastEventAt: ms(5 * time.Hour),
				PRs: []pocket.DeskPR{{Ref: "frostyard/frostmail#288", State: "OPEN", Ready: true, Level: "L3", Title: "triage cleanup"}}},
			{ID: "T1013", Title: "Plan: GitHub webhooks into Pi Pocket", Origin: "plan", Agents: []string{"sheeana"}, State: "open", Next: "agent", LastEventAt: ms(6 * time.Hour)},
			{ID: "T1440", Title: "snosi: GNOME System Monitor flatpak", Origin: "delegate", Agents: []string{"duncan"}, State: "open", Next: "agent", Asleep: true,
				Watch: &pocket.DeskWatch{CheckOn: "2026-11-14", Check: "flathub review answered"}, LastEventAt: ms(26 * time.Hour)},
			{ID: "T1452", Title: "frostnotes: fix #203 flake, #193 preview perf gate, #195 docs", Origin: "delegate", Agents: []string{"irulan"},
				State: "closed", Next: "none", LastEventAt: ms(6 * time.Hour), Closed: &struct {
					At      pocket.Millis `json:"at"`
					Why     string        `json:"why"`
					By      string        `json:"by"`
					Outcome string        `json:"outcome,omitempty"`
				}{At: ms(6 * time.Hour), Why: "done", By: "sweep", Outcome: "completed"}},
		},
		Ready: []pocket.ReadyPR{{Ref: "frostyard/frostmail#288", Title: "Issue triage: close 14 stale, label 9", Level: "L3", Checks: "SUCCESS",
			Head: "a1b2c3d", Thread: "T1450", Agents: []string{"irulan"}, At: ms(5 * time.Hour)}},
	}
	v.Cap.Counted, v.Cap.Cap = 3, 16
	v.Counts.Open, v.Counts.Brian, v.Counts.Agent, v.Counts.Reviewer, v.Counts.Watching = 6, 1, 3, 1, 1
	rate, week, cycle := 0.991, 269.0, float64(54*time.Minute/time.Millisecond)
	cost, tokens := 412.37, 1.92e9
	dash := &pocket.Dashboard{AsOf: ms(0)}
	dash.Range.From, dash.Range.To = "2026-10-14", "2026-11-12"
	m := &dash.Totals
	m.Completion.Rate, m.Completion.Success, m.Completion.Denominator = &rate, 1147, 1157
	m.Velocity.PerWeek, m.Velocity.Successes = &week, 1147
	m.CycleTime.MedianMs = &cycle
	m.Workload.Total, m.Workload.Open = 25, 25
	m.PRs.Merged, m.PRs.Created, m.PRs.OpenNow = 1304, 1310, 4
	m.Usage.Cost, m.Usage.TotalTokens, m.Usage.State, m.Usage.Sessions = &cost, &tokens, "partial", 1830
	for i := range 30 {
		dash.Daily = append(dash.Daily, struct {
			Day       string `json:"day"`
			Successes int    `json:"successes"`
		}{Day: fmt.Sprintf("2026-%02d-%02d", 10+(i+14)/32, (i+14)%31+1), Successes: 20 + (i*37)%40})
	}
	for _, r := range []string{"frostyard/frostmail", "frostyard/frostnotes", "frostyard/frostui", "bketelsen/pi-pocket-team"} {
		dash.Rows.Projects = append(dash.Rows.Projects, pocket.ProjectRow{Repo: r})
	}
	sup := 1
	board := &pocket.Board{Supervisor: &sup, Rows: []pocket.BoardRow{
		{ID: 144, Rev: 1, State: "picked", Agent: "duncan", Repo: "frostyard/snow", Title: "snow: ISO build hygiene", Kind: "hygiene", Max: 6, Until: "06:30", Worker: "deepseek/deepseek-flash"},
		{ID: 145, Rev: 2, State: "proposed", Agent: "sheeana", Repo: "bketelsen/pi-pocket-team", Title: "Desk: thread to session links", Kind: "feature", Max: 8, Until: "06:30", Note: "Odrade: after T1460 lands"},
		{ID: 143, Rev: 1, State: "launched", Agent: "ghanima", Repo: "frostyard/frostui", Title: "frostui: correctness for the apps (#10, B1 menu, mailto attach)", Kind: "goal", Max: 8, Until: "18:30", Thread: "T1455", ShiftEnded: true, ChangedAt: ms(5 * time.Hour)},
	}}
	board.Limits = &struct {
		Line      string `json:"line"`
		AtOnce    int    `json:"atOnce"`
		Running   int    `json:"running"`
		Cap       int    `json:"cap"`
		Counted   int    `json:"counted"`
		Reserve   int    `json:"reserve"`
		Rounds    int    `json:"rounds"`
		RoundsMax int    `json:"roundsMax"`
	}{Line: "8 at once, 0 running · cap 16, 3 count, 2 kept free · 49 of 250 rounds today", AtOnce: 8, Cap: 16, Counted: 3, Reserve: 2, Rounds: 49, RoundsMax: 250}
	return desk{on: true, view: v, dash: dash, dashDays: 30, board: board}
}

func writePNG(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("POCKET_SNAPSHOT_DIR")
	if dir == "" {
		return
	}
	if os.Getenv("POCKET_DARK") != "" {
		name += "-dark"
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestTeamSnapshots(t *testing.T) {
	a, loop := teamApp(t)
	tt := ui.NewTesterWith(a.View, 1440, 900, ui.TesterOptions{Dark: os.Getenv("POCKET_DARK") != ""})
	settle := func() {
		for range 5 {
			tt.Advance(100 * time.Millisecond)
			loop.Flush()
			tt.Frame()
		}
	}
	settle()
	for _, want := range []string{"Inbox", "Decisions", "T1439b", "Fix the flaky CI in nsl", "Ready to merge", "Duncan", "Odrade"} {
		if !tt.HasText(want) {
			t.Errorf("inbox lacks %q", want)
		}
	}
	writePNG(t, tt, "team-inbox")
	loop.Post(func() { a.inboxSel = "a:T1439c" })
	settle()
	writePNG(t, tt, "team-inbox-ask")
	loop.Post(func() { a.goPlace("x:motion"); a.threadSel = "T1455" })
	settle()
	if !tt.HasText("Agents at work") {
		t.Errorf("motion lacks its groups: %v", tt.Texts())
	}
	writePNG(t, tt, "team-motion")
	loop.Post(func() { a.goPlace("x:board") })
	settle()
	writePNG(t, tt, "team-board")
	loop.Post(func() { a.goPlace("x:numbers") })
	settle()
	writePNG(t, tt, "team-numbers")
	loop.Post(func() { a.openAgent("duncan") })
	settle()
	if !tt.HasText("Give Duncan a task") {
		t.Errorf("agent page lacks the task card")
	}
	writePNG(t, tt, "team-agent")
}

func TestSignInSnapshot(t *testing.T) {
	t.Setenv("FROSTUI_ICON_THEME", "Adwaita")
	frostui.SetIcons(frostui.NewIcons(icons.Embedded()))
	store, _ := config.Open(t.TempDir())
	loop := frostuitest.NewLoop()
	a, _ := New(Options{Post: loop.Post, Store: store, Creds: config.Credentials{Dir: store.Dir()}})
	a.signIn.err = ""
	tt := ui.NewTesterWith(a.View, 1100, 720, ui.TesterOptions{Dark: os.Getenv("POCKET_DARK") != ""})
	tt.Frame()
	if !tt.HasText("Connect to Pi Pocket") {
		t.Fatalf("no sign-in card: %v", tt.Texts())
	}
	writePNG(t, tt, "signin")
}

func TestAgentOfReadsFoldersAndWorktrees(t *testing.T) {
	a, _ := teamApp(t)
	cases := map[int]string{1: "odrade", 2: "duncan", 3: "sheeana", 4: "irulan", 5: "duncan", 6: ""}
	for id, want := range cases {
		got := ""
		if p := a.agentOf(a.byID[id]); p != nil {
			got = p.ID
		}
		if got != want {
			t.Errorf("session %d: agent %q, want %q", id, got, want)
		}
	}
}

func TestStatesSortSessions(t *testing.T) {
	a, _ := teamApp(t)
	want := map[int]state{1: stateWorking, 2: stateWaiting, 3: stateWorking, 4: stateFresh, 5: stateFailed, 6: stateSeen}
	for id, st := range want {
		if got := a.stateOf(a.byID[id]); got != st {
			t.Errorf("session %d: state %d, want %d", id, got, st)
		}
	}
	// 1 call to allow, 2 decisions, 1 PR to merge.
	if n := a.inboxCount(); n != 4 {
		t.Errorf("inbox count = %d, want 4", n)
	}
}
