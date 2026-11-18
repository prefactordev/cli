package langfuse

import (
	"context"
	"encoding/json"
	"errors"
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

func TestRetryAfterAcceptsADate(t *testing.T) {
	when := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if got := retryAfter(when, 1); got < 25*time.Second || got > 30*time.Second {
		t.Fatalf("retryAfter = %v", got)
	}
	if got := retryAfter("", 2); got != 10*time.Second {
		t.Fatalf("retryAfter without header = %v", got)
	}
}
