package cli

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// canonicalBody re-encodes a JSON body with sorted keys and without the
// random idempotency_key.
func canonicalBody(t *testing.T, raw string) string {
	t.Helper()
	if raw == "" {
		return ""
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body %q: %v", raw, err)
	}
	delete(body, "idempotency_key")
	out, _ := json.Marshal(body)
	return string(out)
}

func canonicalQuery(t *testing.T, raw string) string {
	t.Helper()
	values, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	return values.Encode()
}

// TestCommandsSendTheRequestFieldsTheirOptionsName runs the commands added
// beyond the TypeScript CLI, and options added to its commands, and checks
// the request each sends.
func TestCommandsSendTheRequestFieldsTheirOptionsName(t *testing.T) {
	samples := filepath.Join(t.TempDir(), "samples.json")
	if err := os.WriteFile(samples, []byte(`[{"payload":{"name":"world"}}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		args         []string
		method, path string
		query, body  string
	}{
		{
			args:   []string{"agent-instances", "create", "--agent-version-id", "v1", "--environment-id", "e1", "--purpose", "eval", "--status", "preparing", "--params-payload", `{"q":1}`, "--external-identifier", "run-1", "--no-update-current-version"},
			method: "POST", path: "/api/v1/agent_instance",
			body: `{"agent_version_id":"v1","environment_id":"e1","external_identifier":"run-1","params_payload":{"q":1},"purpose":"eval","status":"preparing","update_current_version":false}`,
		},
		{
			args:   []string{"agent-instances", "create", "--agent-version-id", "v1", "--environment-id", "e1"},
			method: "POST", path: "/api/v1/agent_instance",
			body: `{"agent_version_id":"v1","environment_id":"e1"}`,
		},
		{
			args:   []string{"agent-instances", "send-message", "i1", "--message", "Hello"},
			method: "POST", path: "/api/v1/agent_instance/i1/send_message",
			body: `{"message":"Hello"}`,
		},
		{
			args:   []string{"admin-users", "update-role", "u1", "--role", "full_access"},
			method: "POST", path: "/api/v1/admin_user/u1/update_role",
			body: `{"details":{"role":"full_access"}}`,
		},
		{
			args:   []string{"admin-user-invites", "resend", "inv1"},
			method: "POST", path: "/api/v1/admin_user_invite/inv1/resend",
			body: `{}`,
		},
		{
			args:   []string{"api-tokens", "update", "t1", "--role", "read_only"},
			method: "PUT", path: "/api/v1/api_token/t1",
			body: `{"details":{"role":"read_only"}}`,
		},
		{
			args:   []string{"utils", "render-liquid-template", "--template", "Hello {{name}}", "--samples", "@" + samples},
			method: "POST", path: "/api/v1/utils/render_liquid_template",
			body: `{"samples":[{"payload":{"name":"world"}}],"template":"Hello {{name}}"}`,
		},
		{
			args:   []string{"utils", "validate-schema", "--schema", `{"type":"object"}`, "--payload", "null"},
			method: "POST", path: "/api/v1/utils/validate_schema",
			body: `{"payload":null,"schema":{"type":"object"}}`,
		},
		{
			args:   []string{"utils", "process-sensitive-payload", "--payload", `{"email":"a@example.com"}`},
			method: "POST", path: "/api/v1/utils/process_sensitive_payload",
			body: `{"payload":{"email":"a@example.com"}}`,
		},
		{
			args:   []string{"agent-knowledgebase-links", "list", "--knowledgebase-id", "kb1", "--sort-field", "id", "--sort-direction", "desc", "--pagination-page-size", "5"},
			method: "GET", path: "/api/v1/agent_knowledgebase_link",
			query: "knowledgebase_id=kb1&pagination[page_size]=5&sorting[direction]=desc&sorting[field]=id",
		},
		{
			args:   []string{"agent-knowledgebase-links", "retrieve", "l1"},
			method: "GET", path: "/api/v1/agent_knowledgebase_link/l1",
		},
		{
			args:   []string{"agent-knowledgebase-links", "create", "--agent-id", "a1", "--knowledgebase-id", "kb1"},
			method: "POST", path: "/api/v1/agent_knowledgebase_link",
			body: `{"details":{"agent_id":"a1","knowledgebase_id":"kb1"}}`,
		},
		{
			args:   []string{"agent-knowledgebase-links", "delete", "l1"},
			method: "DELETE", path: "/api/v1/agent_knowledgebase_link/l1",
		},
		{
			args:   []string{"agent-risk-versions", "list", "--agent-id", "a1", "--pagination-offset", "10"},
			method: "GET", path: "/api/v1/agent_risk_version",
			query: "agent_id=a1&pagination[offset]=10",
		},
		{
			args:   []string{"agent-risk-versions", "retrieve", "r1"},
			method: "GET", path: "/api/v1/agent_risk_version/r1",
		},
		{
			args:   []string{"environments", "create", "--name", "Prod", "--account-id", "acc1", "--purpose", "production", "--description", "Live traffic"},
			method: "POST", path: "/api/v1/environment",
			body: `{"details":{"account_id":"acc1","description":"Live traffic","name":"Prod","purpose":"production"}}`,
		},
		{
			args:   []string{"environments", "update", "env1", "--purpose", "staging"},
			method: "PUT", path: "/api/v1/environment/env1",
			body: `{"details":{"purpose":"staging"}}`,
		},
		{
			args:   []string{"agents", "list", "--role", "judge", "--team-id", "t1", "--agent-ids", "a1,a2", "--instances-active-during-start-at", "2026-01-01T00:00:00Z", "--sort-field", "name"},
			method: "GET", path: "/api/v1/agent",
			query: "agent_ids[]=a1&agent_ids[]=a2&instances_active_during[start_at]=2026-01-01T00:00:00Z&role=judge&sorting[direction]=asc&sorting[field]=name&team_id=t1",
		},
		{
			args:   []string{"agents", "retrieve", "a1", "--include-counts", "--environment-id", "e1"},
			method: "GET", path: "/api/v1/agent/a1",
			query: "environment_id=e1&include_counts=true",
		},
		{
			args:   []string{"agents", "create", "--name", "Bot", "--role", "standard", "--execution-mode", "hosted", "--owner-person-id", "p1"},
			method: "POST", path: "/api/v1/agent",
			body: `{"details":{"execution_mode":"hosted","name":"Bot","owner_person_id":"p1","role":"standard"}}`,
		},
		{
			args:   []string{"agent-instances", "list", "--agent-id", "a1", "--agent-version-id", "v1", "--include-risk-score", "--pagination-page-with-id", "i9", "--sort-field", "id", "--active-during-finish-at", "2026-02-01T00:00:00Z"},
			method: "GET", path: "/api/v1/agent_instance",
			query: "active_during[finish_at]=2026-02-01T00:00:00Z&agent_id=a1&agent_version_id=v1&include_risk_score=true&pagination[page_with_id]=i9&sorting[direction]=asc&sorting[field]=id",
		},
		{
			args:   []string{"agent-instances", "retrieve", "i1", "--include-judges", "--time-range-start-at", "2026-01-01T00:00:00Z"},
			method: "GET", path: "/api/v1/agent_instance/i1",
			query: "include_judges=true&time_range[start_at]=2026-01-01T00:00:00Z",
		},
		{
			args:   []string{"agent-instances", "agent-context", "i1", "--include-risk-level"},
			method: "GET", path: "/api/v1/agent_instance/i1/agent_context",
			query: "include_risk_level=true",
		},
		{
			args:   []string{"agent-spans", "list", "--agent-instance-id", "i1", "--start-time", "s", "--end-time", "e", "--purpose", "alert", "--redacted"},
			method: "GET", path: "/api/v1/agent_spans",
			query: "agent_instance_id=i1&end_time=e&purpose=alert&redacted=true&start_time=s",
		},
		{
			args:   []string{"agent-spans", "finish", "s1", "--status", "complete", "--sensitive-encoding"},
			method: "POST", path: "/api/v1/agent_spans/s1/finish",
			body: `{"sensitive_encoding":true,"status":"complete"}`,
		},
		{
			args:   []string{"agent-versions", "create", "--agent-id", "a1", "--external-identifier", "v2", "--name", "Two", "--runtime-environment", `{"os":"linux"}`},
			method: "POST", path: "/api/v1/agent_version",
			body: `{"details":{"agent_id":"a1","external_identifier":"v2","name":"Two","runtime_environment":{"os":"linux"}}}`,
		},
		{
			args:   []string{"agent-schema-versions", "create", "--agent-id", "a1", "--external-identifier", "s2", "--alert-schemas", `[{"name":"x","schema":{}}]`},
			method: "POST", path: "/api/v1/agent_schema_version",
			body: `{"details":{"agent_id":"a1","alert_schemas":[{"name":"x","schema":{}}],"external_identifier":"s2"}}`,
		},
		{
			args:   []string{"alerts", "count", "--active-during-start-at", "2026-01-01T00:00:00Z"},
			method: "GET", path: "/api/v1/alerts/count",
			query: "active_during[start_at]=2026-01-01T00:00:00Z",
		},
		{
			args:   []string{"accounts", "update", "acc1", "--name", "Acme", "--purpose", "business", "--website", "https://acme.example"},
			method: "PUT", path: "/api/v1/account/acc1",
			body: `{"details":{"name":"Acme","purpose":"business","website":"https://acme.example"}}`,
		},
		{
			args:   []string{"api-tokens", "create", "--token-scope", "account", "--role", "read_only"},
			method: "POST", path: "/api/v1/api_token",
			body: `{"details":{"role":"read_only","token_scope":"account"}}`,
		},
		{
			args:   []string{"knowledgebases", "create", "--name", "Docs", "--id", "01m6jcdypz3tq23qmke2ckg5jpt7k0fg"},
			method: "POST", path: "/api/v1/knowledgebase",
			body: `{"details":{"id":"01m6jcdypz3tq23qmke2ckg5jpt7k0fg","name":"Docs"}}`,
		},
		{
			args:   []string{"integrations", "update", "slack", "--webhook-url", "https://hooks.example", "--enabled-events", "", "--status", "paused"},
			method: "POST", path: "/api/v1/account_integration",
			body: `{"details":{"config":{"_version":1,"enabled_events":[],"webhook_url":"https://hooks.example"},"integration_type":"slack","status":"paused"}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.args[0]+" "+tc.args[1], func(t *testing.T) {
			ta := newTestApp(t)
			server := newRecordingServer(t, `{"status":"success","summaries":[],"details":{}}`)
			ta.addProfile(t, "default", "tok", server.URL)

			if _, err := ta.run(t, tc.args...); err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			if server.method != tc.method || server.path != tc.path {
				t.Errorf("%v sent %s %s, want %s %s", tc.args, server.method, server.path, tc.method, tc.path)
			}
			if got, want := canonicalQuery(t, server.query), canonicalQuery(t, tc.query); got != want {
				t.Errorf("%v query = %s, want %s", tc.args, got, want)
			}
			if got := canonicalBody(t, server.body); got != tc.body {
				t.Errorf("%v body = %s, want %s", tc.args, got, tc.body)
			}
		})
	}
}

func TestNewOptionsCheckTheirValues(t *testing.T) {
	ta := newTestApp(t)
	ta.addProfile(t, "default", "tok", "http://127.0.0.1:1")
	cases := map[string][]string{
		"error: option '--role <role>' argument 'admin' is invalid. Allowed choices are read_only, full_access.": {"api-tokens", "update", "t1", "--role", "admin"},
		"error: option '--sort-field <field>' argument 'name' is invalid. Allowed choices are id.":               {"agent-risk-versions", "list", "--agent-id", "a1", "--sort-field", "name"},
		"--samples must be a JSON array.":                                   {"utils", "render-liquid-template", "--template", "x", "--samples", "{}"},
		"--payload must be a JSON object or null.":                          {"utils", "validate-schema", "--schema", "{}", "--payload", "[]"},
		"Specify --agent-id or --knowledgebase-id.":                         {"agent-knowledgebase-links", "list"},
		"--pagination-page-with-id requires --sort-field.":                  {"agent-instances", "list", "--agent-id", "a1", "--pagination-page-with-id", "i1"},
		"Use only one of --pagination-offset or --pagination-page-with-id.": {"agent-instances", "list", "--agent-id", "a1", "--pagination-offset", "0", "--pagination-page-with-id", "i1"},
		"--id must be a valid Prefactor ID.":                                {"knowledgebases", "create", "--name", "Docs", "--id", "bad"},
	}
	for want, args := range cases {
		if _, err := ta.run(t, args...); err == nil || err.Error() != want {
			t.Errorf("%v: err = %v, want %q", args, err, want)
		}
	}
}
