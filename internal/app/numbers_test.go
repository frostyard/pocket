package app

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/pocket/internal/pocket"
)

type numbersHTTP func(*http.Request) (*http.Response, error)

func (f numbersHTTP) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNumbersLatestScopeWinsAndRecordsUseShownScope(t *testing.T) {
	a, loop := teamApp(t)
	entered, release := make(chan struct{}), make(chan struct{})
	a.link.client.HTTP = &http.Client{Transport: numbersHTTP(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/x/desk/dashboard/records" {
			q := r.URL.Query()
			if q.Get("repo") != "example/tools" || q.Get("owner") != "" || q.Get("from") != "2026-11-06" || q.Get("to") != "2026-11-12" {
				t.Errorf("record scope %s", r.URL)
			}
			return reply(r, `{"threads":[{"id":"T1","kind":"success","state":"closed"}]}`), nil
		}
		if r.URL.Query().Get("owner") == "old" {
			close(entered)
			<-release
			return reply(r, `{"asOf":1}`), nil
		}
		if r.URL.Query().Get("repo") != "example/tools" {
			t.Errorf("dashboard scope %s", r.URL)
		}
		return reply(r, `{"asOf":2,"range":{"from":"2026-11-06","to":"2026-11-12"}}`), nil
	})}
	a.desk.dashOwner = "old"
	a.loadDashboard(30)
	<-entered
	a.desk.dashOwner = ""
	a.desk.dashRepo = "example/tools"
	a.loadDashboard(7)
	loop.RunUntil(t, func() bool { return !a.desk.dashLoading && a.desk.dash.AsOf == 2 })
	before := loop.Ran()
	close(release)
	loop.RunUntil(t, func() bool { return loop.Ran() > before })
	if a.desk.dash.AsOf != 2 || a.desk.dashShown.Repo != "example/tools" {
		t.Fatal("old response replaced current scope")
	}
	a.openNumberRecords(a.desk.dashShown, "Completion")
	loop.RunUntil(t, func() bool { return !a.desk.records.loading })
	if len(a.desk.records.data.Threads) != 1 {
		t.Fatal("records not loaded")
	}
}

func TestNumbersFiltersAndDrilldownSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	a.desk.dashShown = pocket.DashboardQuery{From: "2026-10-14", To: "2026-11-12"}
	a.desk.dash.Choices.Owners = []string{"example"}
	a.desk.dash.Choices.Repos = []string{"example/tools"}
	a.desk.dash.Coverage.Status = "started"
	a.desk.dash.Coverage.BoundaryDay = "2026-10-01"
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.numbersPane(c) }, 1100, 950, ui.TesterOptions{})
	tt.Frame()
	for _, label := range []string{"All owners", "All repositories", "All records", "Unattributed records"} {
		if !tt.HasText(label) {
			t.Errorf("missing %s", label)
		}
	}
	writePNG(t, tt, "numbers-filters")
	if !recordThreadMatches("Completion", "failed") || recordThreadMatches("Completion", "dropped") || recordThreadMatches("In motion now", "success") {
		t.Fatal("wrong record population")
	}
}

func TestNumberRecordsSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	var data pocket.DashboardRecords
	if err := pocket.Decode([]byte(`{"threads":[{"id":"T1","repo":"example/tools","state":"closed","kind":"success"},{"id":"T2","state":"closed","kind":"failed","gone":true}],"prs":[{"ref":"example/tools#8","state":"MERGED","events":["merged"],"threads":["T1"]}],"truncated":{"threads":2}}`), &data); err != nil {
		t.Fatal(err)
	}
	a.desk.records = &numberRecords{open: true, metric: "All records", query: pocket.DashboardQuery{From: "2026-11-01", To: "2026-11-12", Repo: "example/tools"}, data: &data}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Box(c).Fill(); a.numberRecordsDialog(c) }, 800, 650, ui.TesterOptions{})
	tt.Frame()
	if !tt.HasText("T2 (no longer on the Desk)") || !tt.HasText("example/tools#8") {
		t.Fatal("records missing")
	}
	writePNG(t, tt, "numbers-records")
}

func TestNumberRecordOpensClosedThreadOutsideDesk(t *testing.T) {
	for _, metric := range []string{"Closed well", "PRs merged"} {
		t.Run(metric, func(t *testing.T) {
			a, loop := teamApp(t)
			a.desk.view = &pocket.DeskView{}
			var data pocket.DashboardRecords
			if err := pocket.Decode([]byte(`{"threads":[{"id":"T900","kind":"success","state":"closed"}],"prs":[{"ref":"example/tools#9","state":"MERGED","events":["merged"],"threads":["T900"]}]}`), &data); err != nil {
				t.Fatal(err)
			}
			a.desk.records = &numberRecords{open: true, metric: metric, data: &data}
			read := make(chan struct{}, 1)
			a.link.client.HTTP = &http.Client{Transport: numbersHTTP(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/api/x/desk/threads/T900" {
					select {
					case read <- struct{}{}:
					default:
					}
					return reply(r, `{"thread":{"id":"T900","title":"Completed long ago","state":"closed"},"reports":[{"text":"Old completion report"}]}`), nil
				}
				return reply(r, `{"threads":[]}`), nil
			})}
			tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Box(c).Fill(); a.numberRecordsDialog(c) }, 800, 650, ui.TesterOptions{})
			tt.Frame()
			tt.Click("T900")
			tt.Frame()
			loop.RunUntil(t, func() bool {
				select {
				case <-read:
					return true
				default:
					return false
				}
			})
			details := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.inspector(c) }, 400, 700, ui.TesterOptions{})
			loop.RunUntil(t, func() bool { details.Frame(); return details.HasText("Completed long ago") })
			if a.selected != "x:motion" || a.threadSel != "T900" {
				t.Fatal("record did not select thread")
			}
			if !details.HasText("closed") {
				t.Fatal("closed thread marked as working")
			}
			writePNG(t, details, "numbers-closed-thread")
		})
	}
}

// meterFixture is a dashboard with a meter of 4 providers, 1 in each state:
// fresh, stale, awaiting and none.
const meterFixture = `{"asOf":1700000000000,"range":{"from":"2026-10-14","to":"2026-11-12"},"meter":{
"at":1700000000000,
"providers":[
{"provider":"anthropic","label":"Anthropic","line":"Anthropic: week 95% used (critical), resets Wed 11:00 (in 4d 13h) · ~2.0%/h over 2h · full in ~2h30m, before reset · week, Fable 20%",
 "readAt":1699999940000,"mainWindowId":"week","lastAttempt":{"at":1699999940000,"ok":true},
 "windows":[{"id":"week","label":"week","scoped":false,"state":"fresh","percent":95,"resetsAt":1700392400000,"severity":"critical","active":true,"burnPerHour":2,"burnSpanMinutes":120,"hoursToFull":2.5,"fullBeforeReset":true,"projectedAtReset":100},
  {"id":"fable","label":"Fable","scoped":true,"state":"fresh","percent":20,"active":false}],
 "grants":[{"label":"Weekly","type":"reset","resetsLeft":1,"endsAt":1700864000000},{"label":"Weekly","resetsLeft":1,"endsAt":1700277200000},{"label":"Fable","resetsLeft":2}],
 "ledger":{"lastHour":70.4,"today":228.49,"days7":2262.6,"partial":true,"top":[{"model":"claude-opus-5-5","cost":2262.6},{"model":"claude-sonnet-5","cost":0.4}],"unknownCost":3}},
{"provider":"example-a","label":"Example A","line":"Example A: week 40% used, resets Thu 09:00 (in 5d 2h)",
 "readAt":1699998470000,"mainWindowId":"week",
 "windows":[{"id":"week","label":"week","scoped":false,"state":"stale","percent":40,"active":true}],
 "grants":[{"label":"Weekly","resetsLeft":1,"endsAt":1700019800000}],
 "ledger":{"lastHour":0,"today":12.5,"days7":1234567.4,"partial":false,"top":[]}},
{"provider":"example-b","label":"Example B","line":"Example B: week reset passed, awaiting provider report",
 "mainWindowId":"week",
 "windows":[{"id":"week","label":"week","scoped":false,"state":"awaiting","active":true}],
 "grantsNote":"not readable (surface)",
 "ledger":{"lastHour":0,"today":0,"days7":0,"partial":false,"top":[]}},
{"provider":"openai","label":"OpenAI","line":"OpenAI: no provider report (Pi's ChatGPT sign-in has no usage endpoint)",
 "off":"no usage endpoint","windows":[],
 "ledger":{"lastHour":1.5,"today":3,"days7":9,"partial":false,"top":[{"model":"gpt-6","cost":9}]}}],
"coverage":{"since":1699400000000,"complete":true,"done":12,"total":12}}}`

func decodeMeterDashboard(t *testing.T, body string) *pocket.Dashboard {
	t.Helper()
	var d pocket.Dashboard
	if err := pocket.Decode([]byte(body), &d); err != nil {
		t.Fatal(err)
	}
	return &d
}

func TestMeterDecode(t *testing.T) {
	d := decodeMeterDashboard(t, meterFixture)
	m := d.Meter
	if m == nil || len(m.Providers) != 4 || m.At != 1700000000000 {
		t.Fatalf("meter = %+v", m)
	}
	if !m.Coverage.Complete || m.Coverage.Done != 12 || m.Coverage.Since == nil || *m.Coverage.Since != 1699400000000 || m.Hold != nil {
		t.Errorf("coverage = %+v, hold = %v", m.Coverage, m.Hold)
	}
	p := m.Providers[0]
	w := p.Windows[0]
	if p.MainWindowID != "week" || p.ReadAt == nil || *p.ReadAt != 1699999940000 || p.LastAttempt == nil || !p.LastAttempt.OK {
		t.Errorf("provider = %+v", p)
	}
	if w.Percent == nil || *w.Percent != 95 || w.ResetsAt == nil || w.FullBeforeReset == nil || !*w.FullBeforeReset || w.HoursToFull == nil || *w.HoursToFull != 2.5 {
		t.Errorf("window = %+v", w)
	}
	if len(p.Grants) != 3 || p.Grants[0].Type != "reset" || p.Grants[2].EndsAt != nil || p.Grants[2].ResetsLeft != 2 {
		t.Errorf("grants = %+v", p.Grants)
	}
	if l := p.Ledger; l.Days7 != 2262.6 || !l.Partial || len(l.Top) != 2 || l.Top[0].Model != "claude-opus-5-5" || l.UnknownCost == nil || *l.UnknownCost != 3 {
		t.Errorf("ledger = %+v", l)
	}
	if o := m.Providers[3]; o.MainWindowID != "" || len(o.Windows) != 0 || o.Off == "" || o.ReadAt != nil {
		t.Errorf("none provider = %+v", o)
	}
	if m.Providers[2].GrantsNote != "not readable (surface)" || m.Providers[2].Windows[0].Percent != nil {
		t.Errorf("awaiting provider = %+v", m.Providers[2])
	}
}

func TestMeterRows(t *testing.T) {
	rows := meterRows(decodeMeterDashboard(t, meterFixture).Meter)
	if len(rows) != 4 {
		t.Fatalf("rows = %d", len(rows))
	}
	type want struct{ state, status, gauge, line, grants, ledger string }
	wants := []want{
		{"fresh", "", "95% of the week used",
			"Anthropic: week 95% used (critical), resets Wed 11:00 (in 4d 13h) · ~2.0%/h over 2h · full in ~2h30m, before reset · week, Fable 20%",
			"resets granted: Weekly ×2 (first expires in 3d) · Fable ×2",
			"list price: $70 last hour, $228 today, $2,263 in 7 days (partial); top: claude-opus-5-5 $2,263, claude-sonnet-5 $0; 3 answers with no cost"},
		{"stale", "week 40% used, read 25 min ago", "",
			"Example A: week 40% used, resets Thu 09:00 (in 5d 2h)",
			"resets granted: Weekly ×1 (first expires in 5h)",
			"list price: $0 last hour, $13 today, $1,234,567 in 7 days"},
		{"awaiting", "reset passed, awaiting provider report", "",
			"Example B: week reset passed, awaiting provider report",
			"grants: not readable (surface)",
			"list price: $0 last hour, $0 today, $0 in 7 days"},
		{"none", "no provider report (Pi's ChatGPT sign-in has no usage endpoint)", "", "", "",
			"list price: $2 last hour, $3 today, $9 in 7 days; top: gpt-6 $9"},
	}
	for i, w := range wants {
		r := rows[i]
		gauge := ""
		if r.Gauge != nil {
			gauge = r.Gauge.Label
			if r.Gauge.Percent != 95 {
				t.Errorf("%d: gauge percent %v", i, r.Gauge.Percent)
			}
		}
		got := want{r.State, r.Status, gauge, r.Line, r.Grants, r.Ledger}
		if got != w {
			t.Errorf("%d:\n got %q\nwant %q", i, got, w)
		}
	}
	if meterRows(nil) != nil {
		t.Error("rows without a meter")
	}
}

func TestNumbersWithoutMeterHasNoCard(t *testing.T) {
	a, _ := teamApp(t)
	a.desk.dash = decodeMeterDashboard(t, `{"asOf":1700000000000,"range":{"from":"2026-10-14","to":"2026-11-12"}}`)
	if a.desk.dash.Meter != nil {
		t.Fatal("meter without a field")
	}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.numbersPane(c) }, 1100, 950, ui.TesterOptions{})
	tt.Frame()
	if tt.HasText("Subscriptions") {
		t.Fatal("card without a meter")
	}
}

func TestNumbersMeterSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	a.desk.dashShown = pocket.DashboardQuery{From: "2026-10-14", To: "2026-11-12"}
	a.desk.dash.Meter = decodeMeterDashboard(t, meterFixture).Meter
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.numbersPane(c) }, 1100, 1100, ui.TesterOptions{})
	tt.Frame()
	for _, text := range []string{"Subscriptions", "95% of the week used", "week 40% used, read 25 min ago",
		"reset passed, awaiting provider report", "no provider report (Pi's ChatGPT sign-in has no usage endpoint)",
		"grants: not readable (surface)", meterNote} {
		if !tt.HasText(text) {
			t.Errorf("missing %q", text)
		}
	}
	writePNG(t, tt, "numbers-meter")
}

// TestMeterFractionalGrants checks that a grant's resetsLeft, a number on
// the wire, decodes and adds up when it is not a whole number.
func TestMeterFractionalGrants(t *testing.T) {
	d := decodeMeterDashboard(t, `{"meter":{"at":1700000000000,"providers":[{"provider":"example-a","label":"Example A","line":"Example A: week 10% used",
"mainWindowId":"week","windows":[{"id":"week","label":"week","scoped":false,"state":"fresh","percent":10,"active":true}],
"grants":[{"label":"Weekly","resetsLeft":1.5,"endsAt":1700277200000},{"label":"Weekly","resetsLeft":1}],
"ledger":{"lastHour":0,"today":0,"days7":0,"partial":false,"top":[]}}],"coverage":{"complete":true,"done":1,"total":1}}}`)
	if d.Meter == nil || len(d.Meter.Providers) != 1 || len(d.Meter.Providers[0].Grants) != 2 {
		t.Fatalf("meter = %+v", d.Meter)
	}
	// Compared as text, so the test also builds (and fails) with an int type.
	if got := fmt.Sprint(d.Meter.Providers[0].Grants[0].ResetsLeft); got != "1.5" {
		t.Fatalf("resetsLeft = %s", got)
	}
	if got, want := meterRows(d.Meter)[0].Grants, "resets granted: Weekly ×2.5 (first expires in 3d)"; got != want {
		t.Fatalf("grants = %q, want %q", got, want)
	}
}

// agentCostFixture is a dashboard with the Desk's cost by agent: an agent
// with 2 providers and their models, an agent with a 4-figure 7-day cost, a
// row the Desk could not attribute, and conversations not attributed yet.
const agentCostFixture = `{"asOf":1763089000000,"range":{"from":"2026-10-14","to":"2026-11-12"},"agents":{
"at":1763089000000,"partial":true,"pending":2,"rows":[
{"kind":"agent","agent":"sheeana","label":"Sheeana","sessions":4,
 "today":{"cost":12.5,"input":1000,"output":200,"cacheRead":5000,"cacheWrite":300,"entries":9,"unknownCost":0},
 "days7":{"cost":80.25,"input":9000,"output":1500,"cacheRead":40000,"cacheWrite":2000,"entries":60,"unknownCost":1},
 "providers":[
  {"provider":"anthropic",
   "today":{"cost":9.44,"input":800,"output":140,"cacheRead":4000,"cacheWrite":200,"entries":6,"unknownCost":0},
   "days7":{"cost":70,"input":8000,"output":1300,"cacheRead":36000,"cacheWrite":1500,"entries":50,"unknownCost":1},
   "models":[{"model":"claude-opus-5-5",
    "today":{"cost":9.44,"input":800,"output":140,"cacheRead":4000,"cacheWrite":200,"entries":6,"unknownCost":0},
    "days7":{"cost":70,"input":8000,"output":1300,"cacheRead":36000,"cacheWrite":1500,"entries":50,"unknownCost":1}}]},
  {"provider":"openai",
   "today":{"cost":3.06,"input":200,"output":60,"cacheRead":1000,"cacheWrite":100,"entries":3,"unknownCost":0},
   "days7":{"cost":10.25,"input":1000,"output":200,"cacheRead":4000,"cacheWrite":500,"entries":10,"unknownCost":0},
   "models":[{"model":"gpt-6.1",
    "today":{"cost":3.06,"input":200,"output":60,"cacheRead":1000,"cacheWrite":100,"entries":3,"unknownCost":0},
    "days7":{"cost":10.25,"input":1000,"output":200,"cacheRead":4000,"cacheWrite":500,"entries":10,"unknownCost":0}}]}]},
{"kind":"agent","agent":"odrade","label":"Odrade","sessions":9,
 "today":{"cost":0.4,"input":20000,"output":3000,"cacheRead":5000,"cacheWrite":0,"entries":4,"unknownCost":0},
 "days7":{"cost":1234.5,"input":2000000,"output":300000,"cacheRead":500000,"cacheWrite":0,"entries":120,"unknownCost":2},
 "providers":[
  {"provider":"anthropic",
   "today":{"cost":0.4,"input":20000,"output":3000,"cacheRead":5000,"cacheWrite":0,"entries":4,"unknownCost":0},
   "days7":{"cost":1234.5,"input":2000000,"output":300000,"cacheRead":500000,"cacheWrite":0,"entries":120,"unknownCost":2},
   "models":[{"model":"claude-opus-5-5",
    "today":{"cost":0.4,"input":20000,"output":3000,"cacheRead":5000,"cacheWrite":0,"entries":4,"unknownCost":0},
    "days7":{"cost":1234.5,"input":2000000,"output":300000,"cacheRead":500000,"cacheWrite":0,"entries":120,"unknownCost":2}}]}]},
{"kind":"unattributed","label":"Unattributed","sessions":1,
 "today":{"cost":0,"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"entries":0,"unknownCost":0},
 "days7":{"cost":0.5,"input":100,"output":20,"cacheRead":0,"cacheWrite":0,"entries":1,"unknownCost":0},
 "providers":[]},
{"kind":"pending","label":"Not attributed yet","sessions":2,
 "today":{"cost":0,"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"entries":0,"unknownCost":0},
 "days7":{"cost":0,"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"entries":0,"unknownCost":0},
 "providers":[]}
]}}`

func TestAgentCostDecode(t *testing.T) {
	d := decodeMeterDashboard(t, agentCostFixture)
	v := d.Agents
	if v == nil || v.At != 1763089000000 || !v.Partial || v.Pending != 2 || len(v.Rows) != 4 {
		t.Fatalf("agents = %+v", v)
	}
	r := v.Rows[0]
	today, days7 := r.Today, r.Days7
	if r.Kind != "agent" || r.Agent != "sheeana" || r.Label != "Sheeana" || r.Sessions != 4 {
		t.Errorf("row = %+v", r)
	}
	if today.Cost != 12.5 || today.Input != 1000 || today.Output != 200 || today.CacheRead != 5000 ||
		today.CacheWrite != 300 || today.Entries != 9 || today.UnknownCost != 0 {
		t.Errorf("today = %+v", today)
	}
	if days7.Cost != 80.25 || days7.Input != 9000 || days7.Output != 1500 || days7.CacheRead != 40000 ||
		days7.CacheWrite != 2000 || days7.Entries != 60 || days7.UnknownCost != 1 {
		t.Errorf("days7 = %+v", days7)
	}
	if len(r.Providers) != 2 || r.Providers[0].Provider != "anthropic" || r.Providers[1].Days7.Cost != 10.25 {
		t.Fatalf("providers = %+v", r.Providers)
	}
	m := r.Providers[0].Models
	if len(m) != 1 || m[0].Model != "claude-opus-5-5" || m[0].Today.Cost != 9.44 || m[0].Days7.Entries != 50 {
		t.Errorf("models = %+v", m)
	}
	if u := v.Rows[2]; u.Kind != "unattributed" || u.Agent != "" || u.Sessions != 1 || u.Days7.Cost != 0.5 {
		t.Errorf("unattributed = %+v", u)
	}
	if p := v.Rows[3]; p.Kind != "pending" || p.Label != "Not attributed yet" || p.Sessions != 2 || len(p.Providers) != 0 {
		t.Errorf("pending = %+v", p)
	}
}

func TestAgentCostRows(t *testing.T) {
	rows := agentCostRows(decodeMeterDashboard(t, agentCostFixture).Agents)
	if len(rows) != 4 {
		t.Fatalf("rows = %d", len(rows))
	}
	type want struct{ label, today, days7, tokens, providers, models string }
	wants := []want{
		{"Sheeana", "today $12.50", "7 days $80.25", "51k in / 2k out",
			"anthropic $70.00 · openai $10.25", "claude-opus-5-5 $70.00 · gpt-6.1 $10.25"},
		{"Odrade", "today $0.40", "7 days $1,235", "2.5M in / 300k out",
			"anthropic $1,235", "claude-opus-5-5 $1,235"},
		{"Unattributed", "today $0.00", "7 days $0.50", "100 in / 20 out", "", ""},
		{"Not attributed yet", "today $0.00", "7 days $0.00", "0 in / 0 out", "", ""},
	}
	for i, w := range wants {
		r := rows[i]
		got := want{r.Label, r.Today, r.Days7, r.Tokens, r.Providers, r.Models}
		if got != w {
			t.Errorf("%d:\n got %q\nwant %q", i, got, w)
		}
	}
	// The server's order is kept, never sorted here.
	if rows[0].Label != "Sheeana" || rows[1].Label != "Odrade" {
		t.Error("rows reordered")
	}
	if agentCostRows(nil) != nil {
		t.Error("rows without agents")
	}
}

func TestAgentMoney(t *testing.T) {
	for _, tt := range []struct {
		usd  float64
		want string
	}{{0, "$0.00"}, {0.4, "$0.40"}, {99.994, "$99.99"}, {100, "$100"}, {1234.5, "$1,235"}, {1234567.4, "$1,234,567"}} {
		if got := agentMoney(tt.usd); got != tt.want {
			t.Errorf("%v = %q, want %q", tt.usd, got, tt.want)
		}
	}
}

func TestNumbersWithoutAgentsHasNoCard(t *testing.T) {
	a, _ := teamApp(t)
	a.desk.dash = decodeMeterDashboard(t, meterFixture)
	if a.desk.dash.Agents != nil {
		t.Fatal("agents without a field")
	}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.numbersPane(c) }, 1100, 950, ui.TesterOptions{})
	tt.Frame()
	if tt.HasText("Cost by agent") {
		t.Fatal("card without agents")
	}
}

func TestNumbersAgentCostSnapshot(t *testing.T) {
	a, _ := teamApp(t)
	a.desk.dashShown = pocket.DashboardQuery{From: "2026-10-14", To: "2026-11-12"}
	a.desk.dash.Agents = decodeMeterDashboard(t, agentCostFixture).Agents
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.numbersPane(c) }, 1100, 1200, ui.TesterOptions{})
	tt.Frame()
	for _, text := range []string{"Cost by agent", "Sheeana", "today $12.50", "7 days $80.25", "51k in / 2k out",
		"anthropic $70.00 · openai $10.25", "claude-opus-5-5 $70.00 · gpt-6.1 $10.25", "Unattributed",
		"Not attributed yet", "2 conversations not attributed yet.", agentCostNote} {
		if !tt.HasText(text) {
			t.Errorf("missing %q", text)
		}
	}
	if tt.HasText("Subscriptions") {
		t.Error("a card without a meter")
	}
	writePNG(t, tt, "numbers-agent-cost")
}

// TestAgentCostCardEmptyRows checks the card's empty list and its absence:
// a dashboard with agents but no row still draws the card.
func TestAgentCostCardEmptyRows(t *testing.T) {
	a, _ := teamApp(t)
	a.desk.dash.Agents = &pocket.AgentCost{At: 1763089000000}
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); a.numbersPane(c) }, 1100, 950, ui.TesterOptions{})
	tt.Frame()
	if !tt.HasText("No model use in the last 7 days.") || !tt.HasText("Cost by agent") {
		t.Fatal("empty card missing")
	}
}
