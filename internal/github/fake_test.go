package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// testToken is clearly fake; the fake server can echo it to test Redact.
const testToken = "ghp_TESTTOKEN0000"

// mergeOid is the merge commit the fake gives a merge.
const mergeOid = "c0ffee0000000000000000000000000000000001"

// The allowlist the fake holds every mutation to, independent of the code:
// the field and the exact keys of its `input`.
var allowedInputs = map[string][]string{
	"addPullRequestReview": {"commitOID", "event", "pullRequestId"},
	"mergePullRequest":     {"expectedHeadOid", "mergeMethod", "pullRequestId"},
	"enqueuePullRequest":   {"expectedHeadOid", "pullRequestId"},
}

var mutationField = regexp.MustCompile(`^mutation \w+\(\$input: \w+!\) \{\s+(\w+)\(input: \$input\) \{`)

// How a mutation answers.
const (
	answerOK    = ""
	answer4xx   = "4xx"
	answerGQL   = "graphql error"
	answer502   = "502"
	answerDrop  = "drop"
	answerEmpty = "empty"
	// answerGQLData is `errors` with a non-null result: no proof of a refusal.
	answerGQLData = "graphql error with data"
	// answerNoData is neither `data` nor `errors`.
	answerNoData = "no data"
	// answerBadData is a result that does not decode.
	answerBadData = "bad data"
	// answerRedirect is a 307 to another path, which the client never follows.
	answerRedirect = "redirect"
)

// fake is GitHub: it serves the recorded answers, changed field by field,
// records every request, and holds each one to the allowlist.
type fake struct {
	t   *testing.T
	srv *httptest.Server
	ref Ref

	mu       sync.Mutex
	data     map[string]any // the PR query's data
	raw      string         // a whole PR answer to serve instead
	rules    []any
	rulesRaw string // a whole rules page to serve instead
	rulesets map[string]any
	branch   map[string]any
	pages    [][]any        // check contexts by page; nil: as in data
	pageHead map[int]string // headRefOid on a later page

	reads  int // PR queries served
	onRead func(n int, pr map[string]any)
	// prFail and restFail answer a request with an error status (0: none).
	prFail   func(n int) (int, string, http.Header)
	restFail func(path string) (int, string, http.Header)

	// mode answers each mutation; land says a lost answer took effect.
	mode         map[string]string
	land         map[string]bool
	echoToken    bool
	message      string // GitHub's message in a 4xx; "" for the default
	afterApprove func(pr map[string]any)
	afterMerge   func(pr map[string]any)
	afterEnqueue func(pr map[string]any)

	// user is the login GET /user answers; expiry its expiry header.
	user, expiry string
	// gets are the REST paths read, in order.
	gets []string

	mutations []string         // the mutations received, in order
	inputs    []map[string]any // their inputs
	requests  int
	waits     []time.Duration
}

func load(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", path))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func loadList(t *testing.T, path string) []any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", path))
	if err != nil {
		t.Fatal(err)
	}
	var l []any
	if err := json.Unmarshal(b, &l); err != nil {
		t.Fatal(err)
	}
	return l
}

func obj(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		m = m[k].(map[string]any)
	}
	return m
}

func copyJSON[T any](v T) T {
	b, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(b, &out)
	return out
}

func checkRun(name, status, conclusion string, app any) map[string]any {
	var c any
	if conclusion != "" {
		c = conclusion
	}
	var a any
	if app != nil {
		a = map[string]any{"databaseId": app}
	}
	return map[string]any{"__typename": "CheckRun", "name": name, "status": status, "conclusion": c, "checkSuite": map[string]any{"app": a}}
}

func statusCtx(context, state string) map[string]any {
	return map[string]any{"__typename": "StatusContext", "context": context, "state": state}
}

func labels(names ...string) map[string]any {
	nodes := []any{}
	for _, n := range names {
		nodes = append(nodes, map[string]any{"name": n})
	}
	return map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": nodes}
}

// newFake serves the recorded answers of dir and PR, in the owner's view:
// the viewer is bketelsen, who did not author the PR, and it is ready.
func newFake(t *testing.T, ref Ref, dir, prFile string) *fake {
	f := &fake{t: t, ref: ref, mode: map[string]string{}, land: map[string]bool{}, rulesets: map[string]any{}}
	f.data = load(t, filepath.Join(dir, prFile))["data"].(map[string]any)
	f.rules = loadList(t, filepath.Join(dir, "rules-branches-main.json"))
	for _, r := range f.rules {
		id := strconv.FormatInt(int64(r.(map[string]any)["ruleset_id"].(float64)), 10)
		f.rulesets[id] = load(t, filepath.Join(dir, "ruleset-"+id+".json"))
	}
	f.branch = load(t, filepath.Join(dir, "branches-main.json"))
	obj(f.data, "viewer")["login"] = Login
	pr := f.pr()
	pr["viewerDidAuthor"] = false
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// direct is bketelsen/pi-pocket-team#238 (no queue; required check Test of
// app 15368), every check green.
func direct(t *testing.T) *fake {
	f := newFake(t, Ref{"bketelsen", "pi-pocket-team", 238}, "bketelsen-pi-pocket-team", "pr-238-open.json")
	pr := f.pr()
	pr["labels"] = labels(labelReady, "level:L1")
	f.setChecks(
		checkRun("Test", "COMPLETED", "SUCCESS", 15368),
		checkRun("pr-triage / pr-triage", "COMPLETED", "SUCCESS", 15368),
		checkRun("build", "COMPLETED", "SUCCESS", 15368),
		checkRun("deploy", "COMPLETED", "SKIPPED", 15368),
	)
	return f
}

// queue is frostyard/core#256 (merge queue, SQUASH), without risk:high.
func queue(t *testing.T) *fake {
	f := newFake(t, Ref{"frostyard", "core", 256}, "frostyard-core", "pr-256-open.json")
	f.pr()["labels"] = labels(labelReady, "level:L2")
	return f
}

func (f *fake) pr() map[string]any { return obj(f.data, "repository", "pullRequest") }
func (f *fake) repo() map[string]any {
	return obj(f.data, "repository")
}
func (f *fake) head() string { return f.pr()["headRefOid"].(string) }

func (f *fake) commit() map[string]any {
	return f.pr()["commits"].(map[string]any)["nodes"].([]any)[0].(map[string]any)["commit"].(map[string]any)
}

func (f *fake) setChecks(nodes ...any) {
	if nodes == nil {
		nodes = []any{}
	}
	obj(f.commit(), "statusCheckRollup", "contexts")["nodes"] = nodes
}

// setHead moves the PR to a new head.
func (f *fake) setHead(oid string) {
	f.pr()["headRefOid"] = oid
	f.commit()["oid"] = oid
}

func (f *fake) client() *Client {
	c := New(testToken)
	c.BaseURL = f.srv.URL
	c.sleep = func(ctx context.Context, d time.Duration) error {
		f.mu.Lock()
		f.waits = append(f.waits, d)
		f.mu.Unlock()
		return ctx.Err()
	}
	c.now = func() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) }
	return c
}

// reset forgets what the fake saw, after a preview.
func (f *fake) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads, f.requests, f.mutations, f.inputs, f.waits = 0, 0, nil, nil, nil
}

func (f *fake) write(w http.ResponseWriter, status int, body string, h http.Header) {
	for k, v := range h {
		w.Header()[k] = v
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	t := f.t
	if r.Header.Get("Authorization") != "Bearer "+testToken || r.Header.Get("Accept") != "application/vnd.github+json" ||
		r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" || r.Header.Get("User-Agent") == "" {
		t.Errorf("headers of %s %s: %v", r.Method, r.URL, r.Header)
	}
	if r.URL.Path == "/graphql" {
		if r.Method != http.MethodPost {
			t.Errorf("%s /graphql", r.Method)
		}
		f.graphql(w, r)
		return
	}
	if r.Method != http.MethodGet {
		t.Errorf("REST %s %s: only GET is allowed", r.Method, r.URL)
		return
	}
	f.gets = append(f.gets, r.URL.Path)
	if f.restFail != nil {
		if status, body, h := f.restFail(r.URL.Path); status != 0 {
			f.write(w, status, body, h)
			return
		}
	}
	switch r.URL.Path {
	case "/user":
		user := f.user
		if user == "" {
			user = Login
		}
		var h http.Header
		if f.expiry != "" {
			h = http.Header{"Github-Authentication-Token-Expiration": {f.expiry}}
		}
		f.write(w, 200, `{"login":"`+user+`","id":1}`, h)
		return
	case "/repos/" + f.ref.Owner + "/" + f.ref.Name:
		f.write(w, 200, `{"full_name":"`+f.ref.Owner+"/"+f.ref.Name+`"}`, nil)
		return
	}
	prefix := "/repos/" + f.ref.Owner + "/" + f.ref.Name + "/"
	rest, ok := strings.CutPrefix(r.URL.Path, prefix)
	if !ok {
		t.Errorf("REST path %s", r.URL.Path)
		return
	}
	switch {
	case strings.HasPrefix(rest, "rules/branches/"):
		if r.URL.Query().Get("per_page") != "100" {
			t.Errorf("rules without per_page=100: %s", r.URL)
		}
		if f.rulesRaw != "" {
			f.write(w, 200, f.rulesRaw, nil)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		lo, hi := (page-1)*100, page*100
		lo, hi = min(lo, len(f.rules)), min(hi, len(f.rules))
		b, _ := json.Marshal(f.rules[lo:hi])
		f.write(w, 200, string(b), nil)
	case strings.HasPrefix(rest, "rulesets/"):
		rs, ok := f.rulesets[strings.TrimPrefix(rest, "rulesets/")]
		if !ok {
			f.write(w, 404, `{"message":"Not Found"}`, nil)
			return
		}
		b, _ := json.Marshal(rs)
		f.write(w, 200, string(b), nil)
	case strings.HasPrefix(rest, "branches/"):
		b, _ := json.Marshal(f.branch)
		f.write(w, 200, string(b), nil)
	default:
		t.Errorf("REST path %s", r.URL.Path)
		f.write(w, 404, `{"message":"Not Found"}`, nil)
	}
}

func (f *fake) graphql(w http.ResponseWriter, r *http.Request) {
	t := f.t
	var body struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("GraphQL body: %v", err)
		return
	}
	if body.Query == prQueryFile(t) {
		f.query(w, body.Variables)
		return
	}
	m := mutationField.FindStringSubmatch(body.Query)
	if m == nil {
		t.Errorf("GraphQL text is neither the PR query nor a mutation: %q", body.Query)
		return
	}
	field := m[1]
	keys, ok := allowedInputs[field]
	if !ok || strings.Count(body.Query, "(input:") != 1 || len(body.Variables) != 1 {
		t.Errorf("mutation not allowed: %s %v", field, body.Variables)
		return
	}
	input, _ := body.Variables["input"].(map[string]any)
	got := []string{}
	for k := range input {
		got = append(got, k)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(keys, ",") {
		t.Errorf("mutation %s inputs %v, want exactly %v", field, got, keys)
	}
	if input["pullRequestId"] != f.pr()["id"] {
		t.Errorf("mutation %s on %v", field, input["pullRequestId"])
	}
	if field == "addPullRequestReview" && input["event"] != "APPROVE" {
		t.Errorf("review event %v", input["event"])
	}
	f.mutations = append(f.mutations, field)
	f.inputs = append(f.inputs, input)
	shown := input["commitOID"]
	if shown == nil {
		shown = input["expectedHeadOid"]
	}

	mode := f.mode[field]
	land := mode == answerOK || mode == answerEmpty || f.land[field]
	if land {
		pr := f.pr()
		switch field {
		case "addPullRequestReview":
			pr["viewerLatestReview"] = map[string]any{"state": "APPROVED", "commit": map[string]any{"oid": shown}}
			pr["reviewDecision"] = "APPROVED"
			if pr["mergeStateStatus"] == "BLOCKED" {
				pr["mergeStateStatus"] = "CLEAN"
			}
			if f.afterApprove != nil {
				f.afterApprove(pr)
			}
		case "mergePullRequest":
			pr["state"] = "MERGED"
			pr["mergedBy"] = map[string]any{"login": Login}
			pr["mergeCommit"] = map[string]any{"oid": mergeOid}
			pr["mergedAt"] = "2026-11-14T12:00:00Z"
			if f.afterMerge != nil {
				f.afterMerge(pr)
			}
		case "enqueuePullRequest":
			pr["isInMergeQueue"] = true
			pr["mergeQueueEntry"] = map[string]any{"enqueuer": map[string]any{"login": Login}, "headCommit": map[string]any{"oid": shown}}
			if f.afterEnqueue != nil {
				f.afterEnqueue(pr)
			}
		}
	}
	switch mode {
	case answerOK:
		answers := map[string]string{
			"addPullRequestReview": `{"data":{"addPullRequestReview":{"pullRequestReview":{"state":"APPROVED"}}}}`,
			"mergePullRequest":     `{"data":{"mergePullRequest":{"pullRequest":{"state":"MERGED","mergeCommit":{"oid":"` + mergeOid + `"}}}}}`,
			"enqueuePullRequest":   `{"data":{"enqueuePullRequest":{"mergeQueueEntry":{"id":"MQE_test"}}}}`,
		}
		f.write(w, 200, answers[field], nil)
	case answerEmpty:
		f.write(w, 200, `{"data":{"`+field+`":null}}`, nil)
	case answer4xx:
		msg := "Resource not accessible by personal access token"
		if f.message != "" {
			msg = f.message
		}
		if f.echoToken {
			msg += " (" + testToken + ")"
		}
		f.write(w, 403, `{"message":"`+msg+`"}`, nil)
	case answerGQL:
		f.write(w, 200, `{"data":{"`+field+`":null},"errors":[{"message":"Head branch was modified. Review and try the merge again."}]}`, nil)
	case answerGQLData:
		f.write(w, 200, `{"data":{"`+field+`":{"clientMutationId":null}},"errors":[{"message":"Something went wrong while executing your query."}]}`, nil)
	case answerNoData:
		f.write(w, 200, `{}`, nil)
	case answerBadData:
		f.write(w, 200, `{"data":{"`+field+`":"done"}}`, nil)
	case answerRedirect:
		w.Header().Set("Location", "/elsewhere")
		w.WriteHeader(http.StatusTemporaryRedirect)
	case answer502:
		f.write(w, 502, `{"message":"Server Error"}`, nil)
	case answerDrop:
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}
}

func (f *fake) query(w http.ResponseWriter, vars map[string]any) {
	t := f.t
	if vars["owner"] != f.ref.Owner || vars["name"] != f.ref.Name || vars["number"] != float64(f.ref.Number) {
		t.Errorf("PR query vars %v", vars)
	}
	f.reads++
	if f.prFail != nil {
		if status, body, h := f.prFail(f.reads); status != 0 {
			f.write(w, status, body, h)
			return
		}
	}
	if f.raw != "" {
		f.write(w, 200, f.raw, nil)
		return
	}
	data := copyJSON(f.data)
	repo, _ := data["repository"].(map[string]any)
	pr, _ := repo["pullRequest"].(map[string]any)
	if f.onRead != nil && pr != nil {
		f.onRead(f.reads, pr)
	}
	if f.pages != nil && pr != nil {
		page := 0
		if c, ok := vars["cursor"].(string); ok {
			page, _ = strconv.Atoi(strings.TrimPrefix(c, "p"))
		}
		ctxs := obj(pr["commits"].(map[string]any)["nodes"].([]any)[0].(map[string]any), "commit", "statusCheckRollup", "contexts")
		ctxs["nodes"] = f.pages[page]
		ctxs["pageInfo"] = map[string]any{"hasNextPage": page < len(f.pages)-1, "endCursor": fmt.Sprintf("p%d", page+1)}
		if oid, ok := f.pageHead[page]; ok {
			pr["headRefOid"] = oid
		}
	}
	b, _ := json.Marshal(map[string]any{"data": data})
	f.write(w, 200, string(b), nil)
}

var (
	prQueryOnce sync.Once
	prQueryText string
)

// prQueryFile is testdata/pr-query.graphql, the text the fake accepts.
func prQueryFile(t *testing.T) string {
	prQueryOnce.Do(func() {
		b, err := os.ReadFile(filepath.Join("testdata", "pr-query.graphql"))
		if err != nil {
			t.Error(err)
		}
		prQueryText = string(b)
	})
	return prQueryText
}
