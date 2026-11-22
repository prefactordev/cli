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

// obsAt is obs at start. Without rootMark, isRootObservation is left out,
// as in older data, so only a missing parent makes it a root.
func obsAt(trace, id string, start time.Time, root, rootMark bool) Observation {
	o := obs(trace, id, 0, root)
	o["startTime"] = json.RawMessage(`"` + start.UTC().Format("2006-01-02T15:04:05.000Z") + `"`)
	if !rootMark {
		delete(o, "isRootObservation")
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
			q.Get("cursor") != "c1" {
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

func TestWithoutProcessMetadataDropsOnlyProcessKeys(t *testing.T) {
	var o Observation
	_ = json.Unmarshal([]byte(`{"id":"o1","input":"{\"a\":\"<b>\"}","metadata":{"feature":"orders","resourceAttributes.process.pid":"123","resourceAttributes.process.command_args":"[\"--token\"]","resourceAttributes.host.name":"box"}}`), &o)

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
	at := func(minute int) time.Time { return time.Date(2026, 8, 1, 10, minute, 0, 0, time.UTC) }

	// Page 1: t1's children, and all of t2, whose root is more than the
	// margin older than the page's oldest observation only after page 2.
	got := a.Add([]Observation{obsAt("t1", "c2", at(50), false, true), obsAt("t2", "r2", at(45), true, true), obsAt("t1", "c1", at(40), false, true)})
	if len(got) != 0 {
		t.Fatalf("page 1 completed %v", ids(got))
	}
	// Page 2 ends at t1's root, which may share its start time with
	// observations on the next page.
	if got = a.Add([]Observation{obsAt("t1", "r1", at(30), true, true)}); fmt.Sprint(ids(got)) != "[t2:1]" {
		t.Fatalf("page 2 completed %v", ids(got))
	}
	// Page 3 is more than the margin older than t1's root, so t1 is
	// complete; t3 has no root yet, so it started before the window.
	got = a.Add([]Observation{obsAt("t1", "c0", at(30), false, true), obsAt("t3", "c3", at(20), false, true)})
	if fmt.Sprint(ids(got)) != "[t1:4]" {
		t.Fatalf("page 3 completed %v", ids(got))
	}
	// A repeated observation is ignored. One for a trace already completed
	// is counted as late.
	if got = a.Add([]Observation{obsAt("t1", "late", at(10), false, true), obsAt("t3", "c3", at(20), false, true)}); len(got) != 0 {
		t.Fatalf("page 4 completed %v", ids(got))
	}

	if complete := a.Finish(); len(complete) != 0 {
		t.Fatalf("complete = %v; t3 has no root, so it is left out", ids(complete))
	}
	if a.Late() != 1 {
		t.Fatalf("late = %d", a.Late())
	}
}

func TestAssemblerKeepsAChildThatStartsJustBeforeItsRoot(t *testing.T) {
	a := NewAssembler()
	root := time.Date(2026, 8, 1, 10, 30, 0, 0, time.UTC)

	if got := a.Add([]Observation{obsAt("t1", "r1", root, true, true)}); len(got) != 0 {
		t.Fatalf("page 1 completed %v", ids(got))
	}
	// Clock skew puts a child 30 seconds before its root, on the next page.
	if got := a.Add([]Observation{obsAt("t1", "c0", root.Add(-30*time.Second), false, true), obsAt("t2", "r2", root.Add(-time.Minute), true, true)}); len(got) != 0 {
		t.Fatalf("page 2 completed %v", ids(got))
	}
	got := a.Add([]Observation{obsAt("t3", "r3", root.Add(-10*time.Minute), true, true)})
	if fmt.Sprint(ids(got)) != "[t1:2 t2:1]" || a.Late() != 0 {
		t.Fatalf("page 3 completed %v, late %d", ids(got), a.Late())
	}
}

func TestAssemblerHoldsTracesWithoutARootMarkUntilFinish(t *testing.T) {
	a := NewAssembler()
	newer := time.Date(2026, 8, 1, 10, 30, 0, 0, time.UTC)
	older := newer.Add(-20 * time.Minute)

	// Older SDKs leave isRootObservation out and can send several
	// observations without a parent; the newest arrives first.
	if got := a.Add([]Observation{obsAt("t1", "a", newer, true, false)}); len(got) != 0 {
		t.Fatalf("page 1 completed %v", ids(got))
	}
	if got := a.Add([]Observation{obsAt("t1", "b", older, true, false), obsAt("t2", "r2", older.Add(-10*time.Minute), true, true)}); len(got) != 0 {
		t.Fatalf("page 2 completed %v", ids(got))
	}
	complete := a.Finish()
	if fmt.Sprint(ids(complete)) != "[t1:2 t2:1]" {
		t.Fatalf("complete = %v", ids(complete))
	}
	raw, err := complete[0].Encode()
	if err != nil {
		t.Fatal(err)
	}
	var encoded struct {
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil || encoded.Timestamp != "2026-08-01T10:10:00.000Z" {
		t.Fatalf("timestamp = %q, %v; want the earliest root's start", encoded.Timestamp, err)
	}
}

func TestAssemblerFinishCompletesTracesWithARoot(t *testing.T) {
	a := NewAssembler()
	a.Add([]Observation{obs("t1", "c1", 31, false), obs("t1", "r1", 30, true)})
	if complete := a.Finish(); fmt.Sprint(ids(complete)) != "[t1:2]" {
		t.Fatalf("complete = %v", ids(complete))
	}
}

func TestTraceEncodeTakesTheTraceFieldsFromTheRoot(t *testing.T) {
	child := obs("t1", "c1", 40, false)
	child["metadata"] = json.RawMessage(`{"resourceAttributes.process.pid":"9","feature":"orders"}`)
	trace := Trace{ID: "t1", Observations: []Observation{child, obs("t1", "r1", 30, true)}}
	if trace.TraceName() != "support" {
		t.Fatalf("trace name = %q", trace.TraceName())
	}

	raw, err := trace.Encode()
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
	if got.ID != "t1" || got.Name != "support" || got.Environment != "production" || got.Release != "v1" ||
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
