package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// prefactorServer is a fake Prefactor API for imports. statuses sets what
// add_traces answers for a trace ID ("skipped", or a rejection reason);
// other traces are written.
type prefactorServer struct {
	*httptest.Server
	mu          sync.Mutex
	accounts    string
	statuses    map[string]string
	rateLimited int
	// failTraces answers add_traces with this status when set.
	failTraces int
	created    []map[string]any
	batches    [][]map[string]any
	bodySizes  []int
	finished   []string
	requests   []string
}

func newPrefactorServer(t *testing.T) *prefactorServer {
	t.Helper()
	ps := &prefactorServer{
		accounts: `{"status":"success","summaries":[{"id":"acc1","name":"Acme"}]}`,
		statuses: map[string]string{},
	}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ps.mu.Lock()
		defer ps.mu.Unlock()
		ps.requests = append(ps.requests, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("%s %s: Authorization = %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/account":
			_, _ = io.WriteString(w, ps.accounts)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/import":
			var parsed map[string]any
			_ = json.Unmarshal(body, &parsed)
			ps.created = append(ps.created, parsed)
			_, _ = io.WriteString(w, `{"status":"success","details":{"type":"import","id":"imp1","status":"open"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/import/imp1/traces":
			if ps.rateLimited > 0 {
				ps.rateLimited--
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, `{"status":"error","code":"rate_limited","message":"Slow down","retry_after_ms":1500}`)
				return
			}
			if ps.failTraces != 0 {
				w.WriteHeader(ps.failTraces)
				_, _ = io.WriteString(w, `{"status":"error","code":"invalid_state","message":"Import is not open"}`)
				return
			}
			if len(body) > 8_000_000 {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			var parsed struct {
				Traces []map[string]any `json:"traces"`
			}
			if err := json.Unmarshal(body, &parsed); err != nil {
				t.Errorf("add_traces body is not JSON: %v", err)
			}
			ps.batches = append(ps.batches, parsed.Traces)
			ps.bodySizes = append(ps.bodySizes, len(body))
			var results []string
			for _, trace := range parsed.Traces {
				id, _ := trace["id"].(string)
				switch status := ps.statuses[id]; status {
				case "":
					results = append(results, fmt.Sprintf(`{"trace_id":%q,"status":"written","agent_instance_id":"ai","reason":null}`, id))
				case "skipped":
					results = append(results, fmt.Sprintf(`{"trace_id":%q,"status":"skipped","agent_instance_id":"ai","reason":"already_imported"}`, id))
				default:
					results = append(results, fmt.Sprintf(`{"trace_id":%q,"status":"rejected","agent_instance_id":null,"reason":%q}`, id, status))
				}
			}
			_, _ = io.WriteString(w, `{"status":"success","results":[`+strings.Join(results, ",")+`],"details":{"id":"imp1"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/import/imp1/finish":
			var parsed struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(body, &parsed)
			ps.finished = append(ps.finished, parsed.Status)
			_, _ = io.WriteString(w, `{"status":"success","details":{"id":"imp1"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ps.Close)
	return ps
}

// importApp is a langfuseApp signed in to ps, with retry waits recorded.
func importApp(t *testing.T, ls *langfuseServer, ps *prefactorServer) (*testApp, *[]time.Duration) {
	t.Helper()
	ta := langfuseApp(t, ls, lfViewerPublic, lfViewerSecret)
	ta.addProfile(t, "default", "tok", ps.URL)
	var slept []time.Duration
	ta.importSleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}
	return ta, &slept
}

// lfObservation is an observation as the Observations API v2 returns it,
// of a trace named name, starting at start.
func lfObservation(trace, id, name string, start time.Time, root bool, input string) string {
	parent := `"root-` + trace + `"`
	if root {
		parent = `null`
	}
	return fmt.Sprintf(`{"id":%q,"traceId":%q,"traceName":%q,"type":"SPAN","name":"step","startTime":%q,"endTime":null,`+
		`"isRootObservation":%v,"parentObservationId":%s,"environment":"production","release":"v1","userId":"u1","sessionId":"","tags":[],`+
		`"input":%q,"output":"\"ok\"","metadata":{"feature":"orders","resourceAttributes.process.pid":"4242","resourceAttributes.process.command_args":"[\"node\",\"--secret\"]"}}`,
		id, trace, name, start.UTC().Format("2006-01-02T15:04:05.000Z"), root, parent, input)
}

func observationPage(cursor string, observations ...string) string {
	meta := `{"cursor":null}`
	if cursor != "" {
		meta = fmt.Sprintf(`{"cursor":%q}`, cursor)
	}
	return `{"data":[` + strings.Join(observations, ",") + `],"meta":` + meta + `}`
}

// tracesByID indexes the traces Prefactor received.
func (ps *prefactorServer) tracesByID() map[string]map[string]any {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	traces := map[string]map[string]any{}
	for _, batch := range ps.batches {
		for _, trace := range batch {
			traces[trace["id"].(string)] = trace
		}
	}
	return traces
}

func observationIDs(trace map[string]any) []string {
	var ids []string
	for _, o := range trace["observations"].([]any) {
		ids = append(ids, o.(map[string]any)["id"].(string))
	}
	return ids
}

func TestImportLangfuseUploadsCompleteTraces(t *testing.T) {
	ls := newLangfuseServer(t)
	ls.metrics = `{"data":[{"traceName":"support","environment":"production","count_count":3},{"traceName":"triage","environment":"production","count_count":1}]}`
	now := time.Now().UTC()
	base := now.Add(-time.Hour)
	windowStart := now.AddDate(0, 0, -30)
	ls.observations = func(q url.Values) string {
		switch {
		case q.Get("cursor") == "":
			// t1 runs across both pages; t2 is whole on the first.
			return observationPage("p2",
				lfObservation("t1", "t1-c2", "support", base.Add(50*time.Second), false, "{}"),
				lfObservation("t2", "t2-r", "support", base.Add(45*time.Second), true, "{}"),
				lfObservation("t1", "t1-c1", "support", base.Add(40*time.Second), false, "{}"))
		default:
			// old started before the window, so only its child is here,
			// and the metrics do not count it.
			return observationPage("",
				lfObservation("t1", "t1-r", "support", base.Add(30*time.Second), true, "{}"),
				lfObservation("t3", "t3-r", "support", base.Add(20*time.Second), true, "{}"),
				lfObservation("t4", "t4-r", "triage", base.Add(10*time.Second), true, "{}"),
				lfObservation("old", "old-c", "triage", windowStart.Add(time.Hour), false, "{}"))
		}
	}
	ps := newPrefactorServer(t)
	ta, _ := importApp(t, ls, ps)

	out, errOut, err := ta.runWithStderr(t, "import", "langfuse", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	assertNoKeys(t, out, errOut)

	// The window query reads the chosen trace names in small pages with
	// every field group the server needs.
	first := ls.queries[0]
	if first.Get("limit") != "50" || first.Get("fields") != "core,basic,time,io,metadata,model,usage,trace_context" ||
		!strings.Contains(first.Get("filter"), `"value":["support","triage"]`) || first.Get("fromStartTime") == "" {
		t.Fatalf("first query = %v", first)
	}
	if len(ls.queries) != 2 {
		t.Fatalf("queries = %v", ls.queries)
	}

	if len(ps.created) != 1 || fmt.Sprint(ps.created[0]["details"]) != "map[account_id:acc1 source:langfuse]" {
		t.Fatalf("created = %v", ps.created)
	}
	traces := ps.tracesByID()
	want := map[string]string{"t1": "[t1-r t1-c1 t1-c2]", "t2": "[t2-r]", "t3": "[t3-r]", "t4": "[t4-r]"}
	if len(traces) != len(want) {
		t.Fatalf("traces = %v", traces)
	}
	for id, obs := range want {
		if got := fmt.Sprint(observationIDs(traces[id])); got != obs {
			t.Errorf("%s observations = %s, want %s", id, got, obs)
		}
	}
	t1 := traces["t1"]
	if t1["name"] != "support" || t1["environment"] != "production" || t1["release"] != "v1" || t1["userId"] != "u1" ||
		t1["timestamp"] != base.Add(30*time.Second).Format("2006-01-02T15:04:05.000Z") {
		t.Fatalf("t1 = %v", t1)
	}
	if traces["t4"]["name"] != "triage" {
		t.Fatalf("t4 name = %v", traces["t4"]["name"])
	}
	for _, batch := range ps.batches {
		raw, _ := json.Marshal(batch)
		if strings.Contains(string(raw), "resourceAttributes.process") || strings.Contains(string(raw), "--secret") {
			t.Fatalf("process metadata was uploaded: %s", raw)
		}
		if !strings.Contains(string(raw), `"feature":"orders"`) {
			t.Fatalf("other metadata was dropped: %s", raw)
		}
	}
	if fmt.Sprint(ps.finished) != "[finished]" {
		t.Fatalf("finished = %v", ps.finished)
	}

	var result langfuseImportResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if result.ImportID != "imp1" || result.AccountID != "acc1" || result.Status != "finished" ||
		result.Runs != (importCounts{Sent: 4, Written: 4}) || result.AgentsURL != ps.URL+"/agents" ||
		!strings.Contains(result.NextStep, "prefactor setup") || len(result.Rejected) != 0 {
		t.Fatalf("result = %+v", result)
	}
	for _, want := range []string{"Importing into the account Acme  acc1.", "Opened import imp1.", "Sent 4 of about 4 runs"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
}

func TestImportLangfuseSendsBatchesOfTwentyUnderTheSizeLimit(t *testing.T) {
	ls := newLangfuseServer(t)
	ls.metrics = `{"data":[{"traceName":"support","environment":"production","count_count":30}]}`
	start := time.Now().UTC().Add(-time.Hour)
	big := strings.Repeat("x", 1_500_000)
	ls.observations = func(q url.Values) string {
		cursor := q.Get("cursor")
		switch {
		case cursor == "":
			// 25 small traces.
			var page []string
			for i := 0; i < 25; i++ {
				page = append(page, lfObservation(fmt.Sprintf("s%02d", i), fmt.Sprintf("s%02d-r", i), "support", start.Add(-time.Duration(i)*time.Second), true, "{}"))
			}
			return observationPage("big0", page...)
		case strings.HasPrefix(cursor, "big"):
			// Six traces of 1.5 MB each, one a page, as Langfuse's 5 MB
			// response limit needs.
			n := int(cursor[3] - '0')
			next := fmt.Sprintf("big%d", n+1)
			if n == 5 {
				next = "huge0"
			}
			id := fmt.Sprintf("b%d", n)
			return observationPage(next, lfObservation(id, id+"-r", "support", start.Add(-time.Duration(100+n)*time.Minute), true, big))
		default:
			// One trace too large to send: three observations of 3 MB.
			n := int(cursor[4] - '0')
			next := fmt.Sprintf("huge%d", n+1)
			if n == 2 {
				next = ""
			}
			return observationPage(next, lfObservation("huge", fmt.Sprintf("huge-%d", n), "support", start.Add(-time.Duration(200+n)*time.Minute), n == 2, strings.Repeat("y", 3_000_000)))
		}
	}
	ps := newPrefactorServer(t)
	ta, _ := importApp(t, ls, ps)

	out, errOut, err := ta.runWithStderr(t, "import", "langfuse", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	sizes := make([]int, len(ps.batches))
	for i, batch := range ps.batches {
		sizes[i] = len(batch)
		if ps.bodySizes[i] > 8_000_000 {
			t.Errorf("batch %d is %d bytes", i, ps.bodySizes[i])
		}
	}
	if fmt.Sprint(sizes) != "[20 10 1]" {
		t.Fatalf("batch sizes = %v", sizes)
	}
	var result langfuseImportResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Runs != (importCounts{Sent: 31, Written: 31, Rejected: 1}) ||
		fmt.Sprint(result.Rejected) != "[{huge too_large}]" {
		t.Fatalf("result = %+v", result)
	}
}

func TestImportLangfuseCarriesOnAfterAnInterruption(t *testing.T) {
	ls := newLangfuseServer(t)
	ls.metrics = `{"data":[{"traceName":"support","environment":"production","count_count":3}]}`
	start := time.Now().UTC().Add(-time.Hour)
	ls.observations = func(url.Values) string {
		return observationPage("",
			lfObservation("t1", "t1-r", "support", start, true, "{}"),
			lfObservation("t2", "t2-r", "support", start.Add(-time.Second), true, "{}"),
			lfObservation("t3", "t3-r", "support", start.Add(-2*time.Second), true, "{}"))
	}
	ps := newPrefactorServer(t)
	ps.statuses["t1"] = "skipped"
	ps.statuses["t2"] = "skipped"
	ps.statuses["t3"] = "agent_retired"
	ps.rateLimited = 2
	ta, slept := importApp(t, ls, ps)
	ta.Terminal = func(io.Writer) bool { return true }
	ta.env["NO_COLOR"] = "1"

	out, errOut, err := ta.runWithStderr(t, "import", "langfuse", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if fmt.Sprint(*slept) != "[1.5s 1.5s]" {
		t.Fatalf("waits = %v", *slept)
	}
	for _, want := range []string{
		"Langfuse import finished",
		"Runs: 0 imported, 2 skipped because they were already imported, 1 rejected.",
		"1: agent_retired (trace t3)",
		ps.URL + "/agents",
		"prefactor setup",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "Sent 3 of about 3 runs (0 imported, 2 skipped, 1 rejected).") {
		t.Errorf("no progress line:\n%q", errOut)
	}
}

func TestImportLangfuseFinishesAsFailedWhenItStops(t *testing.T) {
	ls := newLangfuseServer(t)
	ls.metrics = `{"data":[{"traceName":"support","environment":"production","count_count":3}]}`
	start := time.Now().UTC().Add(-time.Hour)
	ls.observations = func(url.Values) string {
		return observationPage("", lfObservation("t1", "t1-r", "support", start, true, "{}"))
	}
	ps := newPrefactorServer(t)
	ps.failTraces = http.StatusConflict
	ta, _ := importApp(t, ls, ps)

	out, errOut, err := ta.runWithStderr(t, "import", "langfuse", "--yes")
	if err == nil || !strings.Contains(err.Error(), "HTTP request failed with status 409: Import is not open") ||
		!strings.Contains(err.Error(), "Run the same command again to carry on") {
		t.Fatalf("err = %v\n%s", err, errOut)
	}
	if fmt.Sprint(ps.finished) != "[failed]" {
		t.Fatalf("finished = %v", ps.finished)
	}
	var result langfuseImportResult
	if err := json.Unmarshal([]byte(out), &result); err != nil || result.Status != "failed" {
		t.Fatalf("result = %+v, %v\n%s", result, err, out)
	}
}

func TestImportLangfuseFinishesAsFailedWhenLangfuseFails(t *testing.T) {
	ls := newLangfuseServer(t)
	ls.metrics = `{"data":[{"traceName":"support","environment":"production","count_count":3}]}`
	ls.observations = func(url.Values) string { return `not json` }
	ps := newPrefactorServer(t)
	ta, _ := importApp(t, ls, ps)

	_, _, err := ta.runWithStderr(t, "import", "langfuse", "--yes")
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("err = %v", err)
	}
	if fmt.Sprint(ps.finished) != "[failed]" {
		t.Fatalf("finished = %v", ps.finished)
	}
}

func TestImportLangfuseNeedsAnAccountWhenThereAreSeveral(t *testing.T) {
	ls := newLangfuseServer(t)
	ps := newPrefactorServer(t)
	ps.accounts = `{"status":"success","summaries":[{"id":"acc1","name":"Acme"},{"id":"acc2","name":"Beta"}]}`
	ta, _ := importApp(t, ls, ps)

	_, err := ta.run(t, "import", "langfuse", "--yes")
	if err == nil || err.Error() != "You can import into 2 accounts. Pass --account-id <id> to choose one: Acme  acc1, Beta  acc2." {
		t.Fatalf("err = %v", err)
	}
	if len(ls.users) != 0 || len(ps.created) != 0 {
		t.Fatalf("sent %d Langfuse requests and opened %d imports", len(ls.users), len(ps.created))
	}

	ls.metrics = `{"data":[]}`
	out, errOut, err := ta.runWithStderr(t, "import", "langfuse", "--yes", "--account-id", "acc2")
	if err != nil || !strings.Contains(errOut, "No runs found in the last 30 days, so there is nothing to import.") || out != "" {
		t.Fatalf("err = %v, out = %q, stderr = %s", err, out, errOut)
	}
}

func TestImportLangfuseLeavesOutRunsWithoutATraceName(t *testing.T) {
	ls := newLangfuseServer(t)
	ls.metrics = `{"data":[{"traceName":null,"environment":"production","count_count":5},{"traceName":"support","environment":"production","count_count":1}]}`
	start := time.Now().UTC().Add(-time.Hour)
	ls.observations = func(url.Values) string {
		return observationPage("", lfObservation("t1", "t1-r", "support", start, true, "{}"))
	}
	ps := newPrefactorServer(t)
	ta, _ := importApp(t, ls, ps)

	_, errOut, err := ta.runWithStderr(t, "import", "langfuse", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "Leaving out 5 runs without a trace name") || !strings.Contains(ls.queries[0].Get("filter"), `"value":["support"]`) {
		t.Fatalf("stderr = %s\nfilter = %s", errOut, ls.queries[0].Get("filter"))
	}
}

func TestImportLangfuseSignsInWhenThereIsNoProfile(t *testing.T) {
	ls := newLangfuseServer(t)
	ta := langfuseApp(t, ls, lfViewerPublic, lfViewerSecret)
	var started []string
	ta.Login = LoginDeps{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			started = append(started, r.URL.String())
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
		})},
		Hostname: func() (string, error) { return "box", nil },
	}

	_, errOut, err := ta.runWithStderr(t, "import", "langfuse", "--yes")
	if err == nil || err.Error() != "Login could not start (HTTP 503)." {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(errOut, "You are not signed in to Prefactor") || len(started) != 1 || !strings.HasSuffix(started[0], "/cli/login/start") {
		t.Fatalf("stderr = %s, requests = %v", errOut, started)
	}
	if len(ls.users) != 0 {
		t.Fatalf("%d requests sent to Langfuse before signing in", len(ls.users))
	}
}
