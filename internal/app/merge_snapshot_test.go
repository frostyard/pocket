package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"
	"github.com/frostyard/frostui/frostuitest"

	"github.com/frostyard/pocket/internal/github"
	"github.com/frostyard/pocket/internal/pocket"
)

func frames(tt *ui.Tester, loop *frostuitest.Loop) {
	for range 4 {
		tt.Advance(100 * time.Millisecond)
		loop.Flush()
		tt.Frame()
	}
}

func inboxTester(t *testing.T, a *App, loop *frostuitest.Loop) *ui.Tester {
	t.Helper()
	tt := ui.NewTesterWith(a.View, 1440, 1100, ui.TesterOptions{Dark: os.Getenv("POCKET_DARK") != ""})
	frames(tt, loop)
	return tt
}

func settingsTester(t *testing.T, a *App) *ui.Tester {
	t.Helper()
	a.openSettings("github")
	tt := ui.NewTesterWith(func(c *ui.Context) { frostui.Adapt(c); ui.Box(c).Fill(); a.settingsDialog(c) }, 1100, 1000,
		ui.TesterOptions{Dark: os.Getenv("POCKET_DARK") != ""})
	tt.Frame()
	tt.Frame()
	return tt
}

// githubServer is GitHub on loopback for a real *github.Client: the
// recorded answers of bketelsen/pi-pocket-team, with the owner's view, a
// ready label and green checks. It applies the approval and the merge.
type githubServer struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	data      map[string]any
	rules     []byte
	rulesets  map[string][]byte
	branch    []byte
	mutations []string
	// refuse answers every request with a 401 that echoes the token, as a
	// careless proxy might.
	refuse bool
	// journal is the operations.json to read at each mutation; steps are
	// the step of its last attempt at that moment, in order.
	journal string
	steps   []string
}

// journalStep is the step of the journal's last attempt on disk now.
func (g *githubServer) journalStep() string {
	if g.journal == "" {
		return ""
	}
	b, err := os.ReadFile(g.journal)
	if err != nil {
		return "no journal: " + err.Error()
	}
	var f journalFile
	if err := json.Unmarshal(b, &f); err != nil || len(f.Ops) == 0 {
		return "no attempt"
	}
	return f.Ops[len(f.Ops)-1].Step
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "github", "testdata", "bketelsen-pi-pocket-team", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newGitHubServer(t *testing.T) *githubServer {
	g := &githubServer{t: t, rulesets: map[string][]byte{}}
	var answer struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(readTestdata(t, "pr-238-open.json"), &answer); err != nil {
		t.Fatal(err)
	}
	g.data = answer.Data
	g.data["viewer"].(map[string]any)["login"] = github.Login
	pr := g.pr()
	pr["viewerDidAuthor"] = false
	pr["labels"] = map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{map[string]any{"name": "ready-for-human"}}}
	commit := pr["commits"].(map[string]any)["nodes"].([]any)[0].(map[string]any)["commit"].(map[string]any)
	commit["statusCheckRollup"] = map[string]any{"contexts": map[string]any{
		"pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil},
		"nodes": []any{map[string]any{"__typename": "CheckRun", "name": "Test", "status": "COMPLETED", "conclusion": "SUCCESS",
			"checkSuite": map[string]any{"app": map[string]any{"databaseId": 15368}}}},
	}}
	g.data["repository"].(map[string]any)["viewerDefaultMergeMethod"] = "MERGE"
	g.rules = readTestdata(t, "rules-branches-main.json")
	for _, id := range []string{"25664287", "26867289"} {
		g.rulesets[id] = readTestdata(t, "ruleset-"+id+".json")
	}
	g.branch = readTestdata(t, "branches-main.json")
	g.srv = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *githubServer) pr() map[string]any {
	return g.data["repository"].(map[string]any)["pullRequest"].(map[string]any)
}

func (g *githubServer) head() string { return g.pr()["headRefOid"].(string) }

func (g *githubServer) write(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (g *githubServer) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+testGitHubToken {
		g.t.Errorf("authorization %q", r.Header.Get("Authorization"))
	}
	if g.refuse {
		g.write(w, 401, []byte(`{"message":"Bad credentials for `+testGitHubToken+`"}`))
		return
	}
	repo := "/repos/bketelsen/pi-pocket-team/"
	switch {
	case r.URL.Path == "/graphql":
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &body); err != nil {
			g.t.Error(err)
			return
		}
		g.graphql(w, body.Query, body.Variables)
	case r.Method != http.MethodGet:
		g.t.Errorf("REST %s %s", r.Method, r.URL.Path)
	case strings.HasPrefix(r.URL.Path, repo+"rules/branches/"):
		g.write(w, 200, g.rules)
	case strings.HasPrefix(r.URL.Path, repo+"rulesets/"):
		g.write(w, 200, g.rulesets[strings.TrimPrefix(r.URL.Path, repo+"rulesets/")])
	case strings.HasPrefix(r.URL.Path, repo+"branches/"):
		g.write(w, 200, g.branch)
	default:
		g.t.Errorf("path %s", r.URL.Path)
		g.write(w, 404, []byte(`{"message":"Not Found"}`))
	}
}

func (g *githubServer) graphql(w http.ResponseWriter, query string, vars map[string]any) {
	pr := g.pr()
	switch {
	case strings.HasPrefix(query, "query PocketPR"):
		b, _ := json.Marshal(map[string]any{"data": g.data})
		g.write(w, 200, b)
	case strings.Contains(query, "addPullRequestReview("):
		in := vars["input"].(map[string]any)
		g.steps = append(g.steps, g.journalStep())
		g.mutations = append(g.mutations, "approve "+in["commitOID"].(string))
		pr["viewerLatestReview"] = map[string]any{"state": "APPROVED", "commit": map[string]any{"oid": in["commitOID"]}}
		pr["reviewDecision"], pr["mergeStateStatus"] = "APPROVED", "CLEAN"
		g.write(w, 200, []byte(`{"data":{"addPullRequestReview":{"pullRequestReview":{"state":"APPROVED"}}}}`))
	case strings.Contains(query, "mergePullRequest("):
		in := vars["input"].(map[string]any)
		g.steps = append(g.steps, g.journalStep())
		g.mutations = append(g.mutations, "merge "+in["expectedHeadOid"].(string)+" "+in["mergeMethod"].(string))
		pr["state"], pr["mergedBy"] = "MERGED", map[string]any{"login": github.Login}
		pr["mergeCommit"] = map[string]any{"oid": strings.Repeat("c", 40)}
		g.write(w, 200, []byte(`{"data":{"mergePullRequest":{"pullRequest":{"state":"MERGED","mergeCommit":{"oid":"`+strings.Repeat("c", 40)+`"}}}}}`))
	default:
		g.t.Errorf("GraphQL %q", query)
	}
}

// realGitHubApp is mergeApp with a real *github.Client on g.
func realGitHubApp(t *testing.T, g *githubServer) (*App, *frostuitest.Loop, *receiptHTTP) {
	t.Helper()
	ref := "bketelsen/pi-pocket-team#238"
	a, loop, _, _ := mergeApp(t)
	head := g.head()
	a.desk.view.Ready = []pocket.ReadyPR{readyPR(ref, head)}
	a.invalidateDerived()
	a.gh.api = func(owner string) (ghAPI, error) {
		c := github.New(testGitHubToken)
		c.BaseURL = g.srv.URL
		return c, nil
	}
	h := &receiptHTTP{}
	a.link.client.HTTP = &http.Client{Transport: h}
	return a, loop, h
}

// TestConfirmClickApprovesAndMergesWithARealClient drives a real
// *github.Client on loopback from the card's button and the dialog's
// confirm click to merged: the journal holds "about to approve" and "about
// to merge" on disk when each mutation reaches GitHub, and the Desk gets
// the receipt.
func TestConfirmClickApprovesAndMergesWithARealClient(t *testing.T) {
	g := newGitHubServer(t)
	a, loop, h := realGitHubApp(t, g)
	g.journal = filepath.Join(a.opts.Store.Dir(), journalName)
	ref := "bketelsen/pi-pocket-team#238"
	startMerge(t, a, loop)
	mc, _ := a.mergeCardOf(&a.desk.view.Ready[0])
	if !mc.eligible || mc.risk {
		t.Fatalf("card %+v (preview %+v, err %q)", mc, mc.prev, mc.readErr)
	}
	tt := inboxTester(t, a, loop)
	if err := tt.Click("Approve + merge " + ref); err != nil {
		t.Fatal(err)
	}
	d := a.gh.dialog
	if d == nil {
		t.Fatal("no dialog")
	}
	loop.RunUntil(t, func() bool { return !d.items[0].reading })
	frames(tt, loop)
	g.mu.Lock()
	if len(g.mutations) != 0 {
		t.Fatalf("a mutation before the confirm click: %v", g.mutations)
	}
	g.mu.Unlock()
	if err := tt.Click("Approve + merge as " + github.Login); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	frames(tt, loop)
	if !d.started {
		t.Fatalf("the confirm click did not start: %q", tt.Texts())
	}
	loop.RunUntil(t, func() bool { return d.done })
	if r := d.items[0].res; r.Kind != "merged" || r.Method != "merge" {
		t.Fatalf("result %+v", r)
	}
	head := g.head()
	if len(head) != 40 {
		t.Fatalf("head %q", head)
	}
	g.mu.Lock()
	muts := strings.Join(g.mutations, "; ")
	steps := strings.Join(g.steps, "; ")
	g.mu.Unlock()
	if muts != "approve "+head+"; merge "+head+" MERGE" {
		t.Errorf("mutations %s", muts)
	}
	if steps != "about to approve; about to merge" {
		t.Errorf("journal steps at the mutations: %s", steps)
	}
	loop.RunUntil(t, func() bool { return h.count() == 1 && a.gh.j.latest(ref).Receipt.State == receiptRecorded })
	o := a.gh.j.latest(ref)
	want := map[string]any{"op": o.ID, "ref": ref, "head": head, "result": "merged", "method": "merge", "via": "pocket"}
	got := h.bodies[0]
	if len(got) != len(want) {
		t.Errorf("body %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if h.paths[0] != "/api/x/desk/threads/T1/merges" {
		t.Errorf("path %s", h.paths[0])
	}
	if !a.mergeHidden(ref) {
		t.Error("the merged card stays")
	}
	data, _ := os.ReadFile(filepath.Join(a.opts.Store.Dir(), journalName))
	for _, step := range []string{`"step": "merge"`, `"result": "merged"`, `"state": "recorded"`} {
		if !strings.Contains(string(data), step) {
			t.Errorf("journal lacks %s:\n%s", step, data)
		}
	}
}

// TestTheTokenNeverLeaks drives a real client against a GitHub that echoes
// the token in its errors: the journal, Activity and toasts never hold it.
func TestTheTokenNeverLeaks(t *testing.T) {
	g := newGitHubServer(t)
	a, loop, _ := realGitHubApp(t, g)
	ref := "bketelsen/pi-pocket-team#238"
	startMerge(t, a, loop)
	a.openMergeDialog([]pocket.ReadyPR{a.desk.view.Ready[0]}, false)
	d := a.gh.dialog
	loop.RunUntil(t, func() bool { return !d.items[0].reading })
	g.mu.Lock()
	g.refuse = true
	g.mu.Unlock()
	a.mergeConfirm(d)
	loop.RunUntil(t, func() bool { return d.done })
	if r := d.items[0].res; r.Kind != "failed" {
		t.Fatalf("result %+v", r)
	}
	a.closeMergeDialog()
	// A preview read and a Check with the refused token.
	a.gh.previews[ref].at = time.Time{}
	delete(a.gh.bad, "bketelsen")
	a.ghPreviewReady()
	loop.RunUntil(t, func() bool { return a.gh.inflight == 0 })
	a.gh.newClient = func(tok string) ghAPI {
		c := github.New(tok)
		c.BaseURL = g.srv.URL
		return c
	}
	f := a.tokenForm("bketelsen")
	f.text = testGitHubToken
	a.githubCheck("bketelsen")
	loop.RunUntil(t, func() bool { return !f.busy })
	f.text = ""
	var texts []string
	for _, l := range a.console.Lines(nil, "") {
		texts = append(texts, l.Text)
	}
	data, _ := os.ReadFile(filepath.Join(a.opts.Store.Dir(), journalName))
	for name, s := range map[string]string{
		"Activity": strings.Join(texts, "\n"), "toasts": strings.Join(a.pendingToasts, "\n"), "journal": string(data),
		"card": a.gh.previews[ref].err, "settings": f.fail,
	} {
		if strings.Contains(s, testGitHubToken) {
			t.Errorf("%s holds the token:\n%s", name, s)
		}
	}
	if !strings.Contains(strings.Join(texts, "\n"), "[token]") {
		t.Error("the redacted error is not in Activity")
	}
	if len(g.mutations) != 0 {
		t.Errorf("mutations %v", g.mutations)
	}
}

// mergeSnapshotApp is a team with ready cards in every state.
func mergeSnapshotApp(t *testing.T) (*App, *frostuitest.Loop, *fakeGH) {
	refs := []string{"frostyard/core#301", "frostyard/pocket#95", "bketelsen/pi-pocket-team#240", "frostyard/nsl#88", "frostyard/frostui#150", "frostyard/snow#42"}
	a, loop, gh := func() (*App, *frostuitest.Loop, *fakeGH) { a, l, g, _ := mergeApp(t, refs...); return a, l, g }()
	// Only the ready cards: no sessions, decisions or lessons.
	a.setSessions(nil)
	a.desk.view.Threads = nil
	a.lessons.memory = nil
	a.invalidateDerived()
	titles := []string{"core: vendored risk-paths v2", "pocket: Approve + merge on the Inbox", "desk: merge receipts route", "nsl: bump golangci-lint", "frostui: dialog focus ring", "snow: kernel 6.18"}
	for i := range a.desk.view.Ready {
		a.desk.view.Ready[i].Title = titles[i]
		p := gh.previews[refs[i]]
		p.Title = titles[i]
		gh.previews[refs[i]] = p
	}
	a.settings.GitHubBatches = true
	p := gh.previews[refs[1]]
	p.RiskHigh = true
	gh.previews[refs[1]] = p
	a.desk.view.Ready[1].Level = "L1"
	p = gh.previews[refs[2]]
	p.Eligible, p.Reason = false, "required check verify has not run"
	gh.previews[refs[2]] = p
	p = gh.previews[refs[3]]
	p.Queue = true
	gh.previews[refs[3]] = p
	gh.act = func(in github.Intent, _ string, _ func(string) error) github.Result {
		switch in.Preview.Ref.Name {
		case "nsl":
			return github.Result{Kind: "queued", Step: "merge", Method: "queue"}
		case "frostui":
			return github.Result{Kind: "unknown", Step: "reconcile merge", Reason: "merge may be pending"}
		}
		return github.Result{Kind: "refused", Step: "read", Reason: "the PR is behind its base"}
	}
	startMerge(t, a, loop)
	for _, ref := range refs[3:] {
		runDialog(t, a, loop, false, ref)
		a.closeMergeDialog()
	}
	// The snapshot app's server answers 404: the receipts wait.
	loop.RunUntil(t, func() bool {
		o := a.gh.j.latest(refs[5])
		return o != nil && o.Receipt.Failed
	})
	a.gh.sel[refs[0]] = true
	a.pendingToasts = nil
	a.invalidateDerived()
	return a, loop, gh
}

func TestMergeSnapshots(t *testing.T) {
	a, loop, gh := mergeSnapshotApp(t)
	tt := ui.NewTesterWith(a.View, 1440, 1300, ui.TesterOptions{Dark: os.Getenv("POCKET_DARK") != ""})
	frames(tt, loop)
	for _, want := range []string{"Approve + merge", "Open diff", "required check verify has not run", "In merge queue",
		"Check again", "Try again", "not recorded on the Desk", "Select all eligible", "Approve + merge 1", "Review on GitHub"} {
		if !tt.HasText(want) {
			t.Errorf("cards lack %q", want)
		}
	}
	writePNG(t, tt, "merge-cards")

	// The single dialog.
	a.openMergeDialog([]pocket.ReadyPR{a.desk.view.Ready[0]}, false)
	frames(tt, loop)
	loop.RunUntil(t, func() bool { return !a.gh.dialog.items[0].reading })
	frames(tt, loop)
	if !tt.HasText("Approve + merge as bketelsen") {
		t.Errorf("single dialog: %q", tt.Texts())
	}
	writePNG(t, tt, "merge-dialog-single")
	a.closeMergeDialog()

	// risk:high, after the diff.
	ref := a.desk.view.Ready[1].Ref
	a.gh.diff[ref] = a.gh.previews[ref].p.Head
	a.openMergeDialog([]pocket.ReadyPR{a.desk.view.Ready[1]}, false)
	loop.RunUntil(t, func() bool { return !a.gh.dialog.items[0].reading })
	frames(tt, loop)
	if !tt.HasText("I read the diff at") || !tt.HasText("risk:high") {
		t.Errorf("risk dialog: %q", tt.Texts())
	}
	writePNG(t, tt, "merge-dialog-risk")
	a.closeMergeDialog()

	// Settings > GitHub after a Check and a preflight.
	gh.rows = map[string]github.PreflightRow{
		"frostyard/nsl":  {Repo: "frostyard/nsl", Readable: true, Bypass: "always", Reason: "you can bypass a rule here (always): Pocket refuses to merge"},
		"frostyard/snow": {Repo: "frostyard/snow", Missing: "pull_requests=read", Reason: "GitHub answered 403: Resource not accessible by personal access token"},
	}
	a.gh.forms["frostyard"] = &tokenForm{login: github.Login, checked: true, expires: time.Date(2027, 2, 12, 10, 0, 0, 0, time.UTC),
		result: "Signs in as bketelsen; kept in the keyring"}
	a.githubPreflight("frostyard")
	loop.RunUntil(t, func() bool { return !a.gh.preflight["frostyard"].busy })
	st := settingsTester(t, a)
	for _, want := range []string{"Signs in as bketelsen", "Repository", "frostyard/nsl", "pull_requests=read", "Batches", "Run preflight"} {
		if !st.HasText(want) {
			t.Errorf("settings lack %q", want)
		}
	}
	st.Move(650, 600)
	st.Scroll(650, 600, 0, 380)
	for range 3 {
		st.Advance(100 * time.Millisecond)
		st.Frame()
	}
	writePNG(t, st, "settings-github")

	// A batch in progress, with results.
	b, loopB, ghB, _ := mergeApp(t, "frostyard/core#1", "frostyard/core#2", "bketelsen/pi-pocket-team#3", "frostyard/pocket#4")
	release := make(chan struct{})
	ghB.act = func(in github.Intent, _ string, _ func(string) error) github.Result {
		switch in.Preview.Ref.Number {
		case 1:
			return github.Result{Kind: "merged", Step: "merge", Method: "merge"}
		case 2:
			return github.Result{Kind: "refused", Step: "read", Reason: "new commit since you confirmed (eeeeeee)"}
		}
		<-release
		return github.Result{Kind: "merged", Step: "merge", Method: "merge"}
	}
	b.settings.GitHubBatches = true
	startMerge(t, b, loopB)
	var prs []pocket.ReadyPR
	prs = append(prs, b.desk.view.Ready...)
	b.openMergeDialog(prs, true)
	d := b.gh.dialog
	loopB.RunUntil(t, func() bool {
		for _, it := range d.items {
			if it.reading {
				return false
			}
		}
		return true
	})
	tb := inboxTester(t, b, loopB)
	if !tb.HasText(fmt.Sprintf("Approve + merge %d as bketelsen", 4)) {
		t.Errorf("batch dialog: %q", tb.Texts())
	}
	b.mergeConfirm(d)
	loopB.RunUntil(t, func() bool { return d.items[2].state == "working" })
	frames(tb, loopB)
	if !tb.HasText("3 of 4") || !tb.HasText("Stop") {
		t.Errorf("batch progress: %q", tb.Texts())
	}
	writePNG(t, tb, "merge-dialog-batch")
	close(release)
	loopB.RunUntil(t, func() bool { return d.done })
}
