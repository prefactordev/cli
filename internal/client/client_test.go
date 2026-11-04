package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prefactordev/cli/internal/api"
)

func newTestClient(t *testing.T, serverURL string) *api.Client {
	t.Helper()
	c, err := New(serverURL+"/ignored/path", "tok", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	doer := c.Client.(*retryingDoer)
	doer.sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func TestRequestsSendAuthAndSDKHeadersToTheOrigin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ping" {
			t.Errorf("path = %q, want /api/v1/ping", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Prefactor-SDK"); got != "prefactor-cli@1.2.3" {
			t.Errorf("X-Prefactor-SDK = %q", got)
		}
		_, _ = io.WriteString(w, `{"status":"success"}`)
	}))
	defer server.Close()

	raw, err := ReadJSON(newTestClient(t, server.URL).QueryPingGet(context.Background(), &api.QueryPingGetParams{}))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"status":"success"}` {
		t.Fatalf("body = %s", raw)
	}
}

func TestRetriesServerErrorsAndResendsTheBody(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"count":2}` {
			t.Errorf("attempt %d body = %s", calls.Load()+1, body)
		}
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"pfids":["a","b"]}`)
	}))
	defer server.Close()

	_, err := ReadJSON(newTestClient(t, server.URL).ActionPfidGenerate(context.Background(), api.ActionPfidGenerateJSONRequestBody{Count: 2}))
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3 (two retries)", calls.Load())
	}
}

func TestStopsAfterTwoRetriesAndReportsTheStatus(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"code":"rate_limited"}`)
	}))
	defer server.Close()

	_, err := ReadJSON(newTestClient(t, server.URL).QueryPingGet(context.Background(), &api.QueryPingGetParams{}))
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if httpErr.Message != "HTTP request failed with status 429" || string(httpErr.Body) != `{"code":"rate_limited"}` {
		t.Fatalf("err = %+v", httpErr)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

func TestDoesNotRetryClientErrors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := ReadJSON(newTestClient(t, server.URL).QueryPingGet(context.Background(), &api.QueryPingGetParams{}))
	if err == nil || err.Error() != "HTTP request failed with status 401" {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestNetworkFailureUsesTheTypeScriptMessage(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	serverURL := server.URL
	server.Close()

	_, err := ReadJSON(newTestClient(t, serverURL).QueryPingGet(context.Background(), &api.QueryPingGetParams{}))
	if err == nil || err.Error() != "HTTP request failed due to network error" {
		t.Fatalf("err = %v", err)
	}
}

func TestRetryDelayDoublesUpToTheCap(t *testing.T) {
	full := func() float64 { return 1 }
	for attempt, want := range []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 2 * time.Second} {
		if got := retryDelay(attempt, full); got != want {
			t.Errorf("retryDelay(%d) = %v, want %v", attempt, got, want)
		}
	}
	if got := retryDelay(0, func() float64 { return 0 }); got != 125*time.Millisecond {
		t.Errorf("minimum jitter delay = %v, want 125ms", got)
	}
}
