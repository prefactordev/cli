//go:build parity

package parity

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
)

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// normalizeGenerated replaces a top-level body idempotency_key that is a
// random UUID v4 with a placeholder. Both CLIs generate one per request when
// the user gives none, so only its format can be compared.
func normalizeGenerated(requests []recordedRequest) []recordedRequest {
	out := make([]recordedRequest, len(requests))
	for i, req := range requests {
		out[i] = req
		body, ok := req.Body.(map[string]any)
		if !ok {
			continue
		}
		key, ok := body["idempotency_key"].(string)
		if !ok || !uuidV4.MatchString(key) {
			continue
		}
		copied := make(map[string]any, len(body))
		for k, v := range body {
			copied[k] = v
		}
		copied["idempotency_key"] = "<generated uuid v4>"
		out[i].Body = copied
	}
	return out
}

// TestParityAgentContextOutput compares the file that
// agent_instances agent_context --output writes, for responses the default
// recording server does not send.
func TestParityAgentContextOutput(t *testing.T) {
	tsCommand := tsCommand()
	if tsCommand == nil {
		t.Skip("PARITY_TS_CLI is not set")
	}

	responses := map[string]string{
		"body object": `{"status":"success","agent_context":{"generated_at":"2026-01-01T00:00:00Z","span_count":2,` +
			`"body":{"zeta":1,"alpha":{"nested":[1,2.5,{"b":null,"a":true}],"empty":{},"list":[]},"text":"a <b> & é \"q\""}}}`,
		"body null":          `{"status":"success","agent_context":{"body":null}}`,
		"body string":        `{"status":"success","agent_context":{"body":"plain"}}`,
		"missing body":       `{"status":"success","agent_context":{"span_count":0}}`,
		"agent_context list": `{"status":"success","agent_context":[]}`,
		"agent_context null": `{"status":"success","agent_context":null}`,
	}

	for name, response := range responses {
		t.Run(name, func(t *testing.T) {
			rec := &recorder{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				rec.handler(httptest.NewRecorder(), req)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, response)
			}))
			defer server.Close()

			outDir := t.TempDir()
			tsFile := filepath.Join(outDir, "ts.json")
			goFile := filepath.Join(outDir, "go.json")

			ts := run(t, rec, server.URL, append(append([]string{}, tsCommand...),
				"agent_instances", "agent_context", "abc", "--output", tsFile))
			got := run(t, rec, server.URL, []string{goBinary,
				"agent-instances", "agent-context", "abc", "--output", goFile})

			if ts.exit != got.exit {
				t.Errorf("exit: ts=%d go=%d\nts stderr: %s\ngo stderr: %s", ts.exit, got.exit, ts.stderr, got.stderr)
			}
			if !reflect.DeepEqual(ts.requests, got.requests) {
				t.Errorf("requests differ\nts: %s\ngo: %s", dump(ts.requests), dump(got.requests))
			}
			if ts.stdout != got.stdout {
				t.Errorf("stdout differs\nts:\n%s\ngo:\n%s", ts.stdout, got.stdout)
			}
			if ts.stderr != got.stderr {
				t.Errorf("stderr differs\nts:\n%s\ngo:\n%s", ts.stderr, got.stderr)
			}

			tsBytes, tsErr := os.ReadFile(tsFile)
			goBytes, goErr := os.ReadFile(goFile)
			if (tsErr == nil) != (goErr == nil) {
				t.Fatalf("file written: ts=%v go=%v", tsErr == nil, goErr == nil)
			}
			if string(tsBytes) != string(goBytes) {
				t.Errorf("file contents differ\nts:\n%s\ngo:\n%s", tsBytes, goBytes)
			}
			if tsErr == nil {
				tsInfo, _ := os.Stat(tsFile)
				goInfo, _ := os.Stat(goFile)
				if tsInfo.Mode() != goInfo.Mode() {
					t.Errorf("file mode: ts=%v go=%v", tsInfo.Mode(), goInfo.Mode())
				}
			}
			t.Logf("ts exit %d, file %d bytes", ts.exit, len(tsBytes))
		})
	}
}
