package langfuse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// obs builds an observation of trace at the given second past 10:00.
func obs(trace, id string, second int, root bool) Observation {
	parent := `"root-` + trace + `"`
	if root {
		parent = "null"
	}
	raw := fmt.Sprintf(`{"id":%q,"traceId":%q,"traceName":"support","startTime":"2026-08-01T10:00:%02d.000Z","isRootObservation":%v,"parentObservationId":%s,"environment":"production","userId":"u1","sessionId":"s1","release":"v1","tags":["beta"],"type":"SPAN"}`,
		id, trace, second, root, parent)
	var o Observation
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		panic(err)
	}
	return o
}

func ids(traces []Trace) []string {
	var out []string
	for _, t := range traces {
		out = append(out, fmt.Sprintf("%s:%d", t.ID, len(t.Observations)))
	}
	return out
}

func TestObservationsSendsTheQuery(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/api/public/v2/observations" || q.Get("fields") != "core,basic,time,io,metadata,model,usage,trace_context" ||
			q.Get("limit") != "50" || q.Get("fromStartTime") != "2026-08-01T00:00:00.000Z" || q.Get("toStartTime") != "2026-08-02T00:00:00.000Z" ||
			q.Get("cursor") != "c1" || q.Has("traceId") {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		var filter []map[string]any
		if err := json.Unmarshal([]byte(q.Get("filter")), &filter); err != nil || len(filter) != 1 ||
			filter[0]["type"] != "stringOptions" || filter[0]["column"] != "traceName" || filter[0]["operator"] != "any of" ||
			fmt.Sprint(filter[0]["value"]) != "[support triage]" {
			t.Errorf("filter = %s", q.Get("filter"))
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"o1","traceId":"t1"}],"meta":{"cursor":"c2"}}`)
	})

	page, err := c.Observations(context.Background(), ObservationQuery{
		TraceNames: []string{"support", "triage"},
		From:       time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		To:         time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
		Limit:      50,
		Cursor:     "c1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Cursor != "c2" || len(page.Observations) != 1 || page.Observations[0].Text("traceId") != "t1" {
		t.Fatalf("page = %+v", page)
	}
}

func TestTraceReadsEveryPageOfOneTrace(t *testing.T) {
	var queries []string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		queries = append(queries, q.Get("traceId")+" "+q.Get("cursor")+" "+q.Get("fromStartTime"))
		if q.Get("cursor") == "" {
			_, _ = io.WriteString(w, `{"data":[{"id":"o3","traceId":"t1"},{"id":"o2","traceId":"t1"}],"meta":{"cursor":"next"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"o2","traceId":"t1"},{"id":"o1","traceId":"t1"}],"meta":{"cursor":null}}`)
	})

	trace, err := c.Trace(context.Background(), "t1", time.Now(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.Observations) != 3 || strings.Join(queries, "|") != "t1  |t1 next " {
		t.Fatalf("observations = %d, queries = %q", len(trace.Observations), queries)
	}
}

func TestWithoutProcessMetadataDropsOnlyProcessKeys(t *testing.T) {
	var o Observation
	_ = json.Unmarshal([]byte(`{"id":"o1","input":"{\"a\":\"<b>\"}","metadata":{"feature":"orders","resourceAttributes.process.pid":"123","resourceAttributes.process.command_args":"[\"--token\"]","resourceAttributes.host.name":"box","resourceAttributes":{"process":{"pid":1},"process.owner":"me","service.name":"svc"}}}`), &o)

	got := o.WithoutProcessMetadata()
	var metadata map[string]any
	if err := json.Unmarshal(got["metadata"], &metadata); err != nil {
		t.Fatal(err)
	}
	if _, ok := metadata["resourceAttributes.process.pid"]; ok {
		t.Errorf("process key kept: %s", got["metadata"])
	}
	if _, ok := metadata["resourceAttributes.process.command_args"]; ok {
		t.Errorf("process key kept: %s", got["metadata"])
	}
	if metadata["feature"] != "orders" || metadata["resourceAttributes.host.name"] != "box" {
		t.Errorf("other keys changed: %s", got["metadata"])
	}
	if nested := fmt.Sprint(metadata["resourceAttributes"]); nested != "map[service.name:svc]" {
		t.Errorf("nested = %s", nested)
	}
	if string(got["input"]) != string(o["input"]) {
		t.Errorf("input changed: %s", got["input"])
	}
	if !strings.Contains(string(o["metadata"]), "process.pid") {
		t.Error("the original observation was changed")
	}

	var plain Observation
	_ = json.Unmarshal([]byte(`{"id":"o2","metadata":{"feature":"<x>"}}`), &plain)
	if string(plain.WithoutProcessMetadata()["metadata"]) != `{"feature":"<x>"}` {
		t.Errorf("metadata without process keys changed: %s", plain.WithoutProcessMetadata()["metadata"])
	}
}

func TestAssemblerWaitsUntilThePagesPassTheRoot(t *testing.T) {
	a := NewAssembler()

	// Page 1: t1's children, and all of t2.
	got := a.Add([]Observation{obs("t1", "c2", 50, false), obs("t2", "r2", 45, true), obs("t1", "c1", 40, false)})
	if len(got) != 1 || got[0].ID != "t2" {
		t.Fatalf("page 1 completed %v", ids(got))
	}
	// Page 2 ends at t1's root, which may share its start time with
	// observations on the next page.
	if got = a.Add([]Observation{obs("t1", "r1", 30, true)}); len(got) != 0 {
		t.Fatalf("page 2 completed %v", ids(got))
	}
	// Page 3 is older than t1's root, so t1 is complete; t3 has no root yet.
	got = a.Add([]Observation{obs("t1", "c0", 30, false), obs("t3", "c3", 20, false)})
	if fmt.Sprint(ids(got)) != "[t1:4]" {
		t.Fatalf("page 3 completed %v", ids(got))
	}
	// A repeated observation, or one for a trace already completed, is ignored.
	if got = a.Add([]Observation{obs("t1", "late", 10, false), obs("t3", "c3", 20, false)}); len(got) != 0 {
		t.Fatalf("page 4 completed %v", ids(got))
	}

	complete, partial := a.Finish()
	if len(complete) != 0 || fmt.Sprint(partial) != "[t3]" {
		t.Fatalf("complete = %v, partial = %v", ids(complete), partial)
	}
}

func TestAssemblerFinishCompletesTracesWithARoot(t *testing.T) {
	a := NewAssembler()
	a.Add([]Observation{obs("t1", "c1", 31, false), obs("t1", "r1", 30, true)})
	complete, partial := a.Finish()
	if fmt.Sprint(ids(complete)) != "[t1:2]" || len(partial) != 0 {
		t.Fatalf("complete = %v, partial = %v", ids(complete), partial)
	}
}

func TestTraceEncodeTakesTheTraceFieldsFromTheRoot(t *testing.T) {
	child := obs("t1", "c1", 40, false)
	child["metadata"] = json.RawMessage(`{"resourceAttributes.process.pid":"9","feature":"orders"}`)
	trace := Trace{ID: "t1", Observations: []Observation{child, obs("t1", "r1", 30, true)}}
	if trace.TraceName() != "support" {
		t.Fatalf("trace name = %q", trace.TraceName())
	}

	raw, err := trace.Encode("Support bot")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ID           string            `json:"id"`
		Name         string            `json:"name"`
		Environment  string            `json:"environment"`
		Release      string            `json:"release"`
		Timestamp    string            `json:"timestamp"`
		UserID       string            `json:"userId"`
		SessionID    string            `json:"sessionId"`
		Tags         []string          `json:"tags"`
		Observations []json.RawMessage `json:"observations"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "t1" || got.Name != "Support bot" || got.Environment != "production" || got.Release != "v1" ||
		got.Timestamp != "2026-08-01T10:00:30.000Z" || got.UserID != "u1" || got.SessionID != "s1" || fmt.Sprint(got.Tags) != "[beta]" {
		t.Fatalf("trace = %+v", got)
	}
	if len(got.Observations) != 2 || !strings.Contains(string(got.Observations[0]), `"id":"r1"`) {
		t.Fatalf("observations are not oldest first: %s", raw)
	}
	if strings.Contains(string(raw), "process.pid") || !strings.Contains(string(raw), `"feature":"orders"`) {
		t.Fatalf("metadata = %s", raw)
	}
}
