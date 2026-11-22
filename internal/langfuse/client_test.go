package langfuse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testPublic = "pk-lf-test-public"
	testSecret = "sk-lf-test-secret"
)

// testClient returns a client for server whose sleeps are recorded rather
// than waited.
func testClient(t *testing.T, handler http.HandlerFunc) (*Client, *[]time.Duration) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c := NewClient(Keys{PublicKey: NewSecret(testPublic), SecretKey: NewSecret(testSecret), BaseURL: server.URL}, "1.2.3")
	var slept []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}
	return c, &slept
}

func TestProjectsSendsBasicAuth(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != testPublic || pass != testSecret {
			t.Errorf("auth = %q %q %v", user, pass, ok)
		}
		if r.URL.Path != "/api/public/projects" || r.Header.Get("User-Agent") != "prefactor-cli/1.2.3" {
			t.Errorf("request = %s %s", r.URL.Path, r.Header.Get("User-Agent"))
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"p1","name":"My Project","organization":{"id":"o1","name":"Acme"},"metadata":{}}]}`)
	})

	projects, err := c.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := Project{ID: "p1", Name: "My Project", Organization: Organization{ID: "o1", Name: "Acme"}}
	if len(projects) != 1 || projects[0] != want {
		t.Fatalf("projects = %+v", projects)
	}
}

func TestUnauthorizedKeepsLangfuseMessage(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"Invalid credentials. Confirm that you've configured the correct host."}`)
	})

	_, err := c.Projects(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 || apiErr.Message != "Invalid credentials. Confirm that you've configured the correct host." {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckTracesReadAsksForOneObservation(t *testing.T) {
	from := time.Date(2026, 10, 19, 1, 2, 3, 0, time.UTC)
	to := from.AddDate(0, 0, 30)
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/api/public/v2/observations" || q.Get("limit") != "1" || q.Get("fields") != "core" ||
			q.Get("fromStartTime") != "2026-10-19T01:02:03.000Z" || q.Get("toStartTime") != "2026-11-18T01:02:03.000Z" {
			t.Errorf("request = %s", r.URL)
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"Access denied. The \"traces:read\" permission is required to access this resource.","error":"ForbiddenError"}`)
	})

	err := c.CheckTracesRead(context.Background(), from, to)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 || !strings.Contains(apiErr.Message, `"traces:read" permission`) {
		t.Fatalf("err = %v", err)
	}
}

func TestTraceCountsSendsOneMetricsQuery(t *testing.T) {
	requests := 0
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/public/v2/metrics" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var query map[string]any
		if err := json.Unmarshal([]byte(r.URL.Query().Get("query")), &query); err != nil {
			t.Fatal(err)
		}
		filters, _ := json.Marshal(query["filters"])
		dimensions, _ := json.Marshal(query["dimensions"])
		if query["view"] != "observations" || query["fromTimestamp"] == nil || query["toTimestamp"] == nil ||
			string(filters) != `[{"column":"isRootObservation","operator":"=","type":"boolean","value":true}]` ||
			string(dimensions) != `[{"field":"traceName"},{"field":"environment"}]` {
			t.Errorf("query = %v", query)
		}
		_, _ = io.WriteString(w, `{"data":[
			{"traceName":"linear-daily-report","environment":"development","count_count":1},
			{"traceName":"linear-daily-report","environment":"production","count_count":"100"},
			{"traceName":null,"environment":null,"count_count":2}
		]}`)
	})

	counts, err := c.TraceCounts(context.Background(), time.Now().AddDate(0, 0, -30), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := []TraceCount{
		{"linear-daily-report", "development", 1},
		{"linear-daily-report", "production", 100},
		{"", "", 2},
	}
	if requests != 1 || len(counts) != len(want) {
		t.Fatalf("%d requests, counts = %+v", requests, counts)
	}
	for i := range want {
		if counts[i] != want[i] {
			t.Errorf("counts[%d] = %+v, want %+v", i, counts[i], want[i])
		}
	}
}

func TestRateLimitWaitsAsRetryAfterAsks(t *testing.T) {
	calls := 0
	c, slept := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{"data":[]}`)
	})

	if _, err := c.Projects(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(*slept) != 1 || (*slept)[0] != 7*time.Second {
		t.Fatalf("calls = %d, slept = %v", calls, *slept)
	}
}

func TestRateLimitGivesUpEventually(t *testing.T) {
	c, slept := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "100000")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	_, err := c.Projects(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 429 {
		t.Fatalf("err = %v", err)
	}
	if len(*slept) != maxRateLimitRetries || (*slept)[0] != maxRetryAfter {
		t.Fatalf("slept = %v", *slept)
	}
}

func TestServerErrorsAreRetried(t *testing.T) {
	calls := 0
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, `{"data":[]}`)
	})

	if _, err := c.Projects(context.Background()); err != nil || calls != 3 {
		t.Fatalf("err = %v, calls = %d", err, calls)
	}
}

func TestResponsesOver5MBAreRefused(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":"`+strings.Repeat("x", maxBodySize)+`"}`)
	})

	if _, err := c.Projects(context.Background()); err == nil || !strings.Contains(err.Error(), "larger than 5 MB") {
		t.Fatalf("err = %v", err)
	}
}

func TestErrorsNeverContainTheKeys(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "bad key "+testPublic+":"+testSecret)
	})
	_, err := c.Projects(context.Background())
	if err == nil || strings.Contains(err.Error(), testPublic) || strings.Contains(err.Error(), testSecret) {
		t.Fatalf("err = %v", err)
	}

	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	c = NewClient(Keys{PublicKey: NewSecret(testPublic), SecretKey: NewSecret(testSecret), BaseURL: server.URL}, "1.2.3")
	c.sleep = func(context.Context, time.Duration) error { return nil }
	_, err = c.Projects(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Unable to reach Langfuse at "+server.URL) ||
		strings.Contains(err.Error(), testPublic) || strings.Contains(err.Error(), testSecret) {
		t.Fatalf("err = %v", err)
	}
}

func TestRetryAfterGrowsWithoutAHeader(t *testing.T) {
	if got := retryAfter("", 2); got != 10*time.Second {
		t.Fatalf("retryAfter without header = %v", got)
	}
}

func TestTraceCountsSplitsAWindowThatFillsTheRowLimit(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 2)
	mid := from.AddDate(0, 0, 1)
	// rows returns n rows named agent-start to agent-(start+n-1), with one
	// run each in production.
	rows := func(start, n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = fmt.Sprintf(`{"traceName":"agent-%d","environment":"production","count_count":1}`, start+i)
		}
		return `{"data":[` + strings.Join(parts, ",") + `]}`
	}
	var windows []string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var query struct {
			From   string         `json:"fromTimestamp"`
			To     string         `json:"toTimestamp"`
			Config map[string]int `json:"config"`
		}
		if err := json.Unmarshal([]byte(r.URL.Query().Get("query")), &query); err != nil || query.Config["row_limit"] != metricsRowLimit {
			t.Errorf("query = %s", r.URL.Query().Get("query"))
		}
		windows = append(windows, query.From+" "+query.To)
		switch query.From + " " + query.To {
		case Timestamp(from) + " " + Timestamp(to):
			// The whole window fills the limit, so its rows are cut off.
			_, _ = io.WriteString(w, rows(0, metricsRowLimit))
		case Timestamp(from) + " " + Timestamp(mid):
			_, _ = io.WriteString(w, rows(0, 700))
		case Timestamp(mid.Add(time.Millisecond)) + " " + Timestamp(to):
			// agent-600 to agent-699 have runs in both halves.
			_, _ = io.WriteString(w, rows(600, 500))
		default:
			t.Errorf("unexpected window %s %s", query.From, query.To)
		}
	})

	counts, err := c.TraceCounts(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, count := range counts {
		total += count.Count
	}
	if len(windows) != 3 || len(counts) != 1100 || total != 1200 {
		t.Fatalf("windows = %v, %d counts adding to %d", windows, len(counts), total)
	}
	if counts[650] != (TraceCount{"agent-650", "production", 2}) {
		t.Fatalf("counts[650] = %+v", counts[650])
	}
}

func TestTraceCountsGivesUpOnAWindowTooShortToSplit(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		parts := make([]string, metricsRowLimit)
		for i := range parts {
			parts[i] = fmt.Sprintf(`{"traceName":"agent-%d","environment":"production","count_count":1}`, i)
		}
		_, _ = io.WriteString(w, `{"data":[`+strings.Join(parts, ",")+`]}`)
	})

	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if _, err := c.TraceCounts(context.Background(), from, from.Add(minCountWindow)); err == nil || !strings.Contains(err.Error(), "too many to count") {
		t.Fatalf("err = %v", err)
	}
}

func TestRedirectsToAnotherHostOrSchemeAreRefused(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); ok {
			t.Errorf("the keys followed the redirect to %s", r.Host)
		}
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	t.Cleanup(other.Close)
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	})

	_, err := c.Projects(context.Background())
	if err == nil || !strings.Contains(err.Error(), "redirected") {
		t.Fatalf("err = %v", err)
	}
}

func TestRedirectsOnTheSameHostAreFollowed(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/public/projects" {
			http.Redirect(w, r, "/moved/api/public/projects", http.StatusFound)
			return
		}
		if _, _, ok := r.BasicAuth(); !ok {
			t.Error("no keys after a same-host redirect")
		}
		_, _ = io.WriteString(w, `{"data":[]}`)
	})

	if _, err := c.Projects(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTraceCountsRefusesAMissingCount(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"traceName":"a","environment":"production","count":5}]}`)
	})

	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if _, err := c.TraceCounts(context.Background(), from, from.AddDate(0, 0, 1)); err == nil || !strings.Contains(err.Error(), "no count") {
		t.Fatalf("err = %v", err)
	}
}

func TestTraceCountsStopsSplittingAfterMaxSplitDepth(t *testing.T) {
	requests := 0
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		parts := make([]string, metricsRowLimit)
		for i := range parts {
			parts[i] = fmt.Sprintf(`{"traceName":"chat-%d-%d","environment":"production","count_count":1}`, requests, i)
		}
		_, _ = io.WriteString(w, `{"data":[`+strings.Join(parts, ",")+`]}`)
	})

	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	_, err := c.TraceCounts(context.Background(), from, from.AddDate(0, 0, 30))
	if err == nil || !strings.Contains(err.Error(), "trace names often contain an ID") {
		t.Fatalf("err = %v", err)
	}
	if limit := 1<<(maxSplitDepth+1) - 1; requests > limit {
		t.Fatalf("%d requests, more than %d", requests, limit)
	}
}
