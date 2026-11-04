package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type integrationRequest struct {
	method, path string
	body         any
}

// newIntegrationServer serves a configured slack integration and answers
// other requests with response.
func newIntegrationServer(t *testing.T, response string) (*httptest.Server, *[]integrationRequest) {
	t.Helper()
	var requests []integrationRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body any
		_ = json.Unmarshal(raw, &body)
		requests = append(requests, integrationRequest{r.Method, r.URL.Path, body})
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/account_integration" {
			_, _ = io.WriteString(w, `{"status":"success","summaries":[{"id":"int1","integration_type":"slack"}]}`)
			return
		}
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func TestIntegrationsTestFailsWhenNotDelivered(t *testing.T) {
	server, requests := newIntegrationServer(t, `{"status":"success","delivered":false}`)
	ta := newTestApp(t)
	ta.addProfile(t, "default", "tok", server.URL)

	_, err := ta.run(t, "integrations", "test", "slack")
	if err == nil || err.Error() != "Integration 'slack' test notification was not delivered." {
		t.Fatalf("err = %v", err)
	}
	if got := (*requests)[1]; got.method != http.MethodPost || got.path != "/api/v1/account_integration/int1/test" {
		t.Fatalf("request = %+v", got)
	}
}

func TestIntegrationsUpdateReplacesConfiguredIntegration(t *testing.T) {
	server, requests := newIntegrationServer(t, `{"status":"success"}`)
	ta := newTestApp(t)
	ta.addProfile(t, "default", "tok", server.URL)

	if _, err := ta.run(t, "integrations", "update", "slack", "--webhook-url", "https://hooks.example/x", "--enabled-events", "a, b"); err != nil {
		t.Fatal(err)
	}

	got := (*requests)[1]
	want := `{"details":{"config":{"_version":1,"enabled_events":["a","b"],"webhook_url":"https://hooks.example/x"}}}`
	body, _ := json.Marshal(got.body)
	if got.method != http.MethodPut || got.path != "/api/v1/account_integration/int1" || string(body) != want {
		t.Fatalf("request = %s %s %s", got.method, got.path, body)
	}
}
