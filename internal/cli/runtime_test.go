package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestAgentContextOutputWritesTheBodyInServerKeyOrder(t *testing.T) {
	ta := newTestApp(t)
	server := newRecordingServer(t, `{"status":"success","agent_context":{"span_count":1,"body":{"z":1,"a":{"list":[1,{"b":null}],"empty":{}}}}}`)
	ta.addProfile(t, "default", "tok", server.URL)

	path := filepath.Join(t.TempDir(), "context.json")
	out, err := ta.run(t, "agent-instances", "agent-context", "i1", "--output", path)
	if err != nil || out != "" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if server.method != "GET" || server.path != "/api/v1/agent_instance/i1/agent_context" {
		t.Fatalf("request = %s %s", server.method, server.path)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"z\": 1,\n  \"a\": {\n    \"list\": [\n      1,\n      {\n        \"b\": null\n      }\n    ],\n    \"empty\": {}\n  }\n}\n"
	if string(written) != want {
		t.Fatalf("file = %q, want %q", written, want)
	}
}

func TestAgentContextOutputRequiresTheBody(t *testing.T) {
	for response, want := range map[string]string{
		`{"agent_context":{"span_count":1}}`: "Response missing agent_context.body.",
		`{"agent_context":[]}`:               "Response missing agent_context.",
		`{"status":"success"}`:               "Response missing agent_context.",
	} {
		ta := newTestApp(t)
		server := newRecordingServer(t, response)
		ta.addProfile(t, "default", "tok", server.URL)

		path := filepath.Join(t.TempDir(), "context.json")
		if _, err := ta.run(t, "agent-instances", "agent-context", "i1", "--output", path); err == nil || err.Error() != want {
			t.Fatalf("%s: err = %v, want %q", response, err, want)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s: file was written", response)
		}
	}
}

func TestInstanceActionsSendAFreshIdempotencyKey(t *testing.T) {
	ta := newTestApp(t)
	server := newRecordingServer(t, `{"status":"success"}`)
	ta.addProfile(t, "default", "tok", server.URL)

	uuidV4 := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for range 3 {
		if _, err := ta.run(t, "agent-instances", "start", "i1"); err != nil {
			t.Fatal(err)
		}
		var body struct {
			IdempotencyKey string `json:"idempotency_key"`
		}
		if err := json.Unmarshal([]byte(server.body), &body); err != nil {
			t.Fatal(err)
		}
		if !uuidV4.MatchString(body.IdempotencyKey) || seen[body.IdempotencyKey] {
			t.Fatalf("idempotency_key = %q", body.IdempotencyKey)
		}
		seen[body.IdempotencyKey] = true
	}
}
