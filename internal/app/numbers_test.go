package app

import (
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
