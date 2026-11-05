package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// setupServer answers setup's requests by "METHOD path" and records each
// request with the token it used.
type setupServer struct {
	*httptest.Server
	mu       sync.Mutex
	routes   map[string]string
	requests []string
}

func newSetupServer(t *testing.T, routes map[string]string) *setupServer {
	t.Helper()
	s := &setupServer{routes: map[string]string{
		"GET /api/v1/agent/a1":         `{"status":"success","details":{"id":"a1"}}`,
		"GET /api/v1/agent_deployment": `{"status":"success","summaries":[{"environment_id":"env1"}]}`,
		"POST /api/v1/api_token":       `{"status":"success","details":{"id":"t1"},"token":"deploy-token"}`,
		"GET /api/v1/ping":             `{"status":"success","details":{"token_type":"api_session_agent_deployment_scope","agent_id":"a1"}}`,
	}}
	for route, body := range routes {
		s.routes[route] = body
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		route := r.Method + " " + r.URL.Path
		request := route
		if r.URL.RawQuery != "" {
			request += "?" + r.URL.RawQuery
		}
		request += " " + strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(raw) > 0 {
			request += " " + canonicalBody(t, string(raw))
		}
		s.mu.Lock()
		s.requests = append(s.requests, request)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, s.routes[route])
	}))
	t.Cleanup(s.Close)
	return s
}

func TestSetupPrintsTheValuesForTheAgentsDeployment(t *testing.T) {
	ta := newTestApp(t)
	server := newSetupServer(t, nil)
	ta.addProfile(t, "default", "tok", server.URL)

	out, err := ta.run(t, "setup", "a1")
	if err != nil {
		t.Fatal(err)
	}
	want := "PREFACTOR_API_URL=" + server.URL + "\nPREFACTOR_API_TOKEN=deploy-token\nPREFACTOR_AGENT_ID=a1\nPREFACTOR_AGENT_IDENTIFIER=1.0.0\n"
	if out != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
	wantRequests := []string{
		"GET /api/v1/agent/a1 tok",
		"GET /api/v1/agent_deployment?agent_id=a1 tok",
		`POST /api/v1/api_token tok {"details":{"agent_id":"a1","environment_id":"env1","token_scope":"agent_deployment"}}`,
		"GET /api/v1/ping deploy-token",
	}
	if strings.Join(server.requests, "\n") != strings.Join(wantRequests, "\n") {
		t.Fatalf("requests =\n%s\nwant\n%s", strings.Join(server.requests, "\n"), strings.Join(wantRequests, "\n"))
	}

	for _, args := range [][]string{{"setup", "a1", "--json"}, {"--json", "setup", "a1"}} {
		out, err = ta.run(t, args...)
		wantJSON := "{\n  \"api_url\": \"" + server.URL + "\",\n  \"api_token\": \"deploy-token\",\n  \"agent_id\": \"a1\",\n  \"agent_identifier\": \"1.0.0\"\n}\n"
		if err != nil || out != wantJSON {
			t.Fatalf("%v = %q, %v", args, out, err)
		}
	}
}

func TestSetupCreatesTheAgent(t *testing.T) {
	ta := newTestApp(t)
	server := newSetupServer(t, map[string]string{
		"POST /api/v1/agent": `{"status":"success","details":{"id":"a1"}}`,
	})
	ta.addProfile(t, "default", "tok", server.URL)

	if _, err := ta.run(t, "setup", "--create", "--name", " Bot ", "--description", " Does things "); err != nil {
		t.Fatal(err)
	}
	if want := `POST /api/v1/agent tok {"details":{"description":"Does things","name":"Bot"}}`; server.requests[0] != want {
		t.Fatalf("first request = %s, want %s", server.requests[0], want)
	}
}

// TestSetupChoosesTheEnvironmentAsTheTypeScriptCLIDoes covers the
// environment choice outside interactive mode.
func TestSetupChoosesTheEnvironmentAsTheTypeScriptCLIDoes(t *testing.T) {
	cases := []struct {
		name        string
		routes      map[string]string
		environment string
		err         string
	}{
		{
			name:        "one deployment with a current version",
			routes:      map[string]string{"GET /api/v1/agent_deployment": `{"summaries":[{"environment_id":"env1","current_version_id":null},{"environment_id":"env2","current_version_id":"v1"}]}`},
			environment: "env2",
		},
		{
			name:   "several deployments with current versions",
			routes: map[string]string{"GET /api/v1/agent_deployment": `{"summaries":[{"environment_id":"env1","current_version_id":"v1"},{"environment_id":"env2","current_version_id":"v2"}]}`},
			err:    "Unable to choose an agent deployment for 'a1'. Expected one deployment, or one deployment with current_version_id.",
		},
		{
			name:   "a deployment without an environment",
			routes: map[string]string{"GET /api/v1/agent_deployment": `{"summaries":[{"environment_id":""}]}`},
			err:    "Agent deployment for 'a1' is missing environment_id.",
		},
		{
			name: "no deployment: the first environment of the first account",
			routes: map[string]string{
				"GET /api/v1/agent_deployment": `{"summaries":[]}`,
				"GET /api/v1/account":          `{"summaries":[{"id":"acc1"},{"id":"acc2"}]}`,
				"GET /api/v1/environment":      `{"summaries":[{"id":"env3"},{"id":"env4"}]}`,
			},
			environment: "env3",
		},
		{
			name:   "no accounts",
			routes: map[string]string{"GET /api/v1/agent_deployment": `{"summaries":[]}`, "GET /api/v1/account": `{"summaries":[]}`},
			err:    "No accounts accessible to this profile; cannot create a deployment token.",
		},
		{
			name: "no environments",
			routes: map[string]string{
				"GET /api/v1/agent_deployment": `{"summaries":[]}`,
				"GET /api/v1/account":          `{"summaries":[{"id":"acc1"}]}`,
				"GET /api/v1/environment":      `{"details":[]}`,
			},
			err: "No environments found for account acc1; create one first.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ta := newTestApp(t)
			server := newSetupServer(t, tc.routes)
			ta.addProfile(t, "default", "tok", server.URL)

			_, err := ta.run(t, "setup", "a1")
			if tc.err != "" {
				if err == nil || err.Error() != tc.err {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if token := server.requests[len(server.requests)-2]; !strings.Contains(token, `"environment_id":"`+tc.environment+`"`) {
				t.Fatalf("token request = %s, want environment %s", token, tc.environment)
			}
		})
	}
}

func TestSetupChecksTheNewToken(t *testing.T) {
	cases := map[string]string{
		`{"status":"error"}`: `Setup token ping failed: expected status "success", got "error".`,
		`{"details":{}}`:     `Setup token ping failed: expected status "success", got undefined.`,
		`{"status":"success","details":{"token_type":"api_session_account_scope","agent_id":"a1"}}`:          `Setup token ping failed: expected token_type "api_session_agent_deployment_scope", got "api_session_account_scope".`,
		`{"status":"success","details":{"token_type":"api_session_agent_deployment_scope","agent_id":null}}`: `Setup token ping failed: expected agent_id "a1", got null.`,
	}
	for ping, want := range cases {
		ta := newTestApp(t)
		server := newSetupServer(t, map[string]string{"GET /api/v1/ping": ping})
		ta.addProfile(t, "default", "tok", server.URL)

		if _, err := ta.run(t, "setup", "a1"); err == nil || err.Error() != want {
			t.Errorf("ping %s: err = %v, want %q", ping, err, want)
		}
	}
}

func TestSetupRejectsConflictingOptions(t *testing.T) {
	ta := newTestApp(t)
	server := newSetupServer(t, nil)
	ta.addProfile(t, "default", "tok", server.URL)

	cases := map[string][]string{
		"prefactor setup requires <agent_id>, or --create --name '<agent-name>' to create one.": {"setup"},
		"prefactor setup --create requires --name '<agent-name>'.":                              {"setup", "--create", "--name", "  "},
		"prefactor setup --create does not accept an agent_id argument.":                        {"setup", "a1", "--create", "--name", "Bot"},
		"--name is only valid with --create.":                                                   {"setup", "a1", "--name", "Bot"},
		"--description is only valid with --create.":                                            {"setup", "a1", "--description", "x"},
	}
	for want, args := range cases {
		if _, err := ta.run(t, args...); err == nil || err.Error() != want {
			t.Errorf("%v: err = %v, want %q", args, err, want)
		}
	}
	if len(server.requests) != 0 {
		t.Fatalf("sent %v", server.requests)
	}
}

func TestSetupPicksOrCreatesTheAgentInInteractiveMode(t *testing.T) {
	agents := `{"summaries":[{"id":"a1","name":"Support bot"},{"id":"a2","name":"Triage bot"}]}`

	t.Run("pick an agent", func(t *testing.T) {
		ta := newTestApp(t)
		ta.terminal = true
		picker := &fakePicker{answers: []string{"Support bot"}}
		ta.Picker = picker
		server := newSetupServer(t, map[string]string{"GET /api/v1/agent": agents})
		ta.addProfile(t, "default", "tok", server.URL)

		if _, err := ta.run(t, "setup"); err != nil {
			t.Fatal(err)
		}
		if picker.offered[0][0].Label != "Create a new agent" || len(picker.offered[0]) != 3 {
			t.Fatalf("offered %v", picker.offered[0])
		}
		if server.requests[1] != "GET /api/v1/agent/a1 tok" {
			t.Fatalf("requests = %v", server.requests)
		}
	})

	t.Run("create one", func(t *testing.T) {
		ta := newTestApp(t)
		ta.terminal = true
		ta.Picker = &fakePicker{answers: []string{"Create a new agent", "Bot", ""}}
		server := newSetupServer(t, map[string]string{
			"GET /api/v1/agent":  agents,
			"POST /api/v1/agent": `{"details":{"id":"a1"}}`,
		})
		ta.addProfile(t, "default", "tok", server.URL)

		if _, err := ta.run(t, "setup"); err != nil {
			t.Fatal(err)
		}
		if want := `POST /api/v1/agent tok {"details":{"name":"Bot"}}`; server.requests[1] != want {
			t.Fatalf("requests = %v", server.requests)
		}
	})

	t.Run("--create asks for the name", func(t *testing.T) {
		ta := newTestApp(t)
		ta.terminal = true
		picker := &fakePicker{answers: []string{"Bot"}}
		ta.Picker = picker
		server := newSetupServer(t, map[string]string{"POST /api/v1/agent": `{"details":{"id":"a1"}}`})
		ta.addProfile(t, "default", "tok", server.URL)

		if _, err := ta.run(t, "setup", "--create"); err != nil {
			t.Fatal(err)
		}
		if picker.prompts[0] != "Agent name" {
			t.Fatalf("prompts = %v", picker.prompts)
		}
	})

	t.Run("--json does not pick", func(t *testing.T) {
		ta := newTestApp(t)
		ta.terminal = true
		ta.Picker = &fakePicker{}
		server := newSetupServer(t, nil)
		ta.addProfile(t, "default", "tok", server.URL)

		if _, err := ta.run(t, "setup", "--json"); err == nil || !strings.HasPrefix(err.Error(), "prefactor setup requires <agent_id>") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestSetupAsksForTheEnvironmentInInteractiveMode(t *testing.T) {
	t.Run("several deployments", func(t *testing.T) {
		ta := newTestApp(t)
		ta.terminal = true
		picker := &fakePicker{answers: []string{"Staging"}}
		ta.Picker = picker
		server := newSetupServer(t, map[string]string{
			"GET /api/v1/agent_deployment": `{"summaries":[{"environment_id":"env1","current_version_id":"v1"},{"environment_id":"env2","current_version_id":null}]}`,
			"GET /api/v1/environment/env1": `{"details":{"id":"env1","name":"Production"}}`,
			"GET /api/v1/environment/env2": `{"details":{"id":"env2","name":"Staging"}}`,
		})
		ta.addProfile(t, "default", "tok", server.URL)

		if _, err := ta.run(t, "setup", "a1"); err != nil {
			t.Fatal(err)
		}
		if got := picker.offered[0][0].Label; got != "Production  env1  (has a current version)" {
			t.Fatalf("first choice = %q", got)
		}
		if token := server.requests[len(server.requests)-2]; !strings.Contains(token, `"environment_id":"env2"`) {
			t.Fatalf("token request = %s", token)
		}
	})

	t.Run("no deployment, several accounts", func(t *testing.T) {
		ta := newTestApp(t)
		ta.terminal = true
		picker := &fakePicker{answers: []string{"Second", "Dev"}}
		ta.Picker = picker
		server := newSetupServer(t, map[string]string{
			"GET /api/v1/agent_deployment": `{"summaries":[]}`,
			"GET /api/v1/account":          `{"summaries":[{"id":"acc1","name":"First"},{"id":"acc2","name":"Second"}]}`,
			"GET /api/v1/environment":      `{"summaries":[{"id":"env3","name":"Prod"},{"id":"env4","name":"Dev"}]}`,
		})
		ta.addProfile(t, "default", "tok", server.URL)

		if _, err := ta.run(t, "setup", "a1"); err != nil {
			t.Fatal(err)
		}
		if strings.Join(picker.prompts, "; ") != "Choose an account; Choose an environment" {
			t.Fatalf("prompts = %v", picker.prompts)
		}
		if !strings.Contains(strings.Join(server.requests, "\n"), "GET /api/v1/environment?account_id=acc2") {
			t.Fatalf("requests = %v", server.requests)
		}
		if token := server.requests[len(server.requests)-2]; !strings.Contains(token, `"environment_id":"env4"`) {
			t.Fatalf("token request = %s", token)
		}
	})
}
