package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInteractiveModeNeedsTerminalsAndNoAgent(t *testing.T) {
	cases := []struct {
		name     string
		terminal bool
		env      map[string]string
		args     []string
		want     bool
	}{
		{name: "terminal, no agent", terminal: true, want: true},
		{name: "not a terminal", terminal: false, want: false},
		{name: "--no-input", terminal: true, args: []string{"--no-input"}, want: false},
		{name: "PREFACTOR_NO_INPUT=1", terminal: true, env: map[string]string{"PREFACTOR_NO_INPUT": "1"}, want: false},
		{name: "PREFACTOR_NO_INPUT=0", terminal: true, env: map[string]string{"PREFACTOR_NO_INPUT": "0"}, want: true},
		{name: "AGENT=0 is not an agent", terminal: true, env: map[string]string{"AGENT": "0"}, want: true},
	}
	for _, name := range agentEnvVars {
		cases = append(cases, struct {
			name     string
			terminal bool
			env      map[string]string
			args     []string
			want     bool
		}{name: name, terminal: true, env: map[string]string{name: "1"}, want: false})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ta := newTestApp(t)
			ta.terminal = tc.terminal
			for k, v := range tc.env {
				ta.env[k] = v
			}

			if _, err := ta.run(t, append(tc.args, "version")...); err != nil {
				t.Fatal(err)
			}
			if got := ta.interactive(); got != tc.want {
				t.Fatalf("interactive = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInteractiveModeNeedsBothStreamsToBeTerminals(t *testing.T) {
	ta := newTestApp(t)
	ta.StdinIsTerminal = func() bool { return true }
	ta.StdoutIsTerminal = func() bool { return false }

	if _, err := ta.run(t, "version"); err != nil {
		t.Fatal(err)
	}
	if ta.interactive() {
		t.Fatal("interactive with stdout piped")
	}
}

func TestJSONOutputForTextCommands(t *testing.T) {
	ta := newTestApp(t)

	out, err := ta.run(t, "--json", "version")
	if err != nil || out != "{\n  \"version\": \"1.2.3\"\n}\n" {
		t.Fatalf("version --json = %q, %v", out, err)
	}

	ta.env["PREFACTOR_OUTPUT_FORMAT"] = "json"
	if out, _ = ta.run(t, "version"); out != "{\n  \"version\": \"1.2.3\"\n}\n" {
		t.Fatalf("version with PREFACTOR_OUTPUT_FORMAT=json = %q", out)
	}

	if out, err = ta.run(t, "profiles", "add", "staging", "https://staging.example", "--api-token", "secret"); err != nil {
		t.Fatal(err)
	}
	if out != "{\n  \"profile\": \"staging\",\n  \"saved\": true\n}\n" {
		t.Fatalf("profiles add = %q", out)
	}

	out, _ = ta.run(t, "--profile", "staging", "profiles", "list")
	want := "{\n  \"profiles\": [\n    {\n      \"name\": \"staging\",\n      \"base_url\": \"https://staging.example\",\n      \"current\": true\n    }\n  ]\n}\n"
	if out != want {
		t.Fatalf("profiles list =\n%s\nwant\n%s", out, want)
	}
	if strings.Contains(out, "secret") {
		t.Fatal("profiles list --json printed the token")
	}

	if out, _ = ta.run(t, "profiles", "remove", "nope"); out != "{\n  \"profile\": \"nope\",\n  \"removed\": false\n}\n" {
		t.Fatalf("profiles remove = %q", out)
	}
}

func TestOutputFormatRejectsOtherValues(t *testing.T) {
	ta := newTestApp(t)
	ta.env["PREFACTOR_OUTPUT_FORMAT"] = "table"

	if _, err := ta.run(t, "version"); err == nil || err.Error() != `PREFACTOR_OUTPUT_FORMAT must be "json".` {
		t.Fatalf("err = %v", err)
	}
}

func TestFieldsLimitJSONOutput(t *testing.T) {
	ta := newTestApp(t)
	server := newRecordingServer(t, `{"status":"success","summaries":[{"id":"a1","name":"First","status":"active"},{"id":"a2","name":"Second","status":"retired"}],"pagination":{"page_size":2,"item_count":2}}`)
	ta.addProfile(t, "default", "tok", server.URL)

	out, err := ta.run(t, "agents", "list", "--fields", "summaries.id,summaries.name,pagination.item_count,missing.path")
	if err != nil {
		t.Fatal(err)
	}

	want := `{
  "summaries": [
    {
      "id": "a1",
      "name": "First"
    },
    {
      "id": "a2",
      "name": "Second"
    }
  ],
  "pagination": {
    "item_count": 2
  }
}
`
	if out != want {
		t.Fatalf("out =\n%s\nwant\n%s", out, want)
	}

	if out, _ = ta.run(t, "agents", "list", "--fields", "status"); out != "{\n  \"status\": \"success\"\n}\n" {
		t.Fatalf("--fields status = %q", out)
	}

	if out, _ = ta.run(t, "version", "--fields", "version"); out != "{\n  \"version\": \"1.2.3\"\n}\n" {
		t.Fatalf("version --fields = %q", out)
	}
}

func TestFieldsRejectsEmptyNames(t *testing.T) {
	ta := newTestApp(t)

	for value, want := range map[string]string{
		"a..b": "--fields has an empty field name in 'a..b'.",
		" , ":  "--fields requires at least one field.",
	} {
		if _, err := ta.run(t, "version", "--fields", value); err == nil || err.Error() != want {
			t.Errorf("--fields %q err = %v, want %q", value, err, want)
		}
	}
}

func TestContentOnlyConflictsWithJSON(t *testing.T) {
	ta := newTestApp(t)
	server := newRecordingServer(t, `{"details":{"content":"hello"}}`)
	ta.addProfile(t, "default", "tok", server.URL)

	if out, err := ta.run(t, "knowledgebase-documents", "retrieve", "doc", "--content-only"); err != nil || out != "hello" {
		t.Fatalf("--content-only = %q, %v", out, err)
	}

	_, err := ta.run(t, "knowledgebase-documents", "retrieve", "doc", "--content-only", "--json")
	if err == nil || err.Error() != "--content-only cannot be used with --json or --fields." {
		t.Fatalf("err = %v", err)
	}
}

func TestAgentContextOutputPrintsThePathInJSONMode(t *testing.T) {
	ta := newTestApp(t)
	server := newRecordingServer(t, `{"agent_context":{"body":{"format":"v2"}}}`)
	ta.addProfile(t, "default", "tok", server.URL)
	path := filepath.Join(t.TempDir(), "ctx.json")

	if out, err := ta.run(t, "agent-instances", "agent-context", "inst", "--output", path); err != nil || out != "" {
		t.Fatalf("default mode out = %q, %v", out, err)
	}

	out, err := ta.run(t, "agent-instances", "agent-context", "inst", "--output", path, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["output"] != path {
		t.Fatalf("out = %q", out)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "{\n  \"format\": \"v2\"\n}\n" {
		t.Fatalf("file = %q", raw)
	}
}

func TestLoginJSONPrintsOnlyTheResultOnStdout(t *testing.T) {
	server := newDeviceServer(t, approved())
	h := newLoginHarness(t, server)

	out, errOut, err := h.runWithStderr(t, "login", "--json")
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]string
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("stdout is not JSON: %q", out)
	}
	if result["profile"] != "default" || result["account_name"] != "Acme" || result["base_url"] != server.URL {
		t.Fatalf("result = %v", result)
	}
	if strings.Contains(out, "granted") {
		t.Fatal("login --json printed the token")
	}
	if !strings.Contains(errOut, "Confirm this code in the browser: ABCD-EFGH") || !strings.Contains(errOut, "/cli/connect?code=ABCD-EFGH") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestLoginDoesNotPromptWhenNotInteractive(t *testing.T) {
	for name, setup := range map[string]func(h *loginHarness){
		"not a terminal": func(h *loginHarness) {},
		"agent detected": func(h *loginHarness) { h.terminal = true; h.env["CLAUDECODE"] = "1" },
		"--no-input":     func(h *loginHarness) { h.terminal = true },
	} {
		t.Run(name, func(t *testing.T) {
			server := newDeviceServer(t, approved())
			server.startStatus, server.startBody = http.StatusNotFound, ``
			h := newLoginHarness(t, server)
			setup(h)

			args := []string{"login"}
			if name == "--no-input" {
				args = append(args, "--no-input")
			}
			_, err := h.run(t, args...)
			want := "Cannot ask for an API token because the CLI is not interactive. Run 'prefactor profiles add default <baseUrl> --api-token <apiToken>' instead."
			if err == nil || err.Error() != want {
				t.Fatalf("err = %v", err)
			}
			if len(h.prompts) != 0 || len(h.opened) != 0 {
				t.Fatalf("prompted %v, opened %v", h.prompts, h.opened)
			}
		})
	}
}

func TestDefaultOutputIsUnchanged(t *testing.T) {
	ta := newTestApp(t)
	ta.terminal = true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"success","details":{"score":1.0}}`)
	}))
	defer server.Close()
	ta.addProfile(t, "default", "tok", server.URL)

	out, err := ta.run(t, "--yes", "agents", "retrieve", "a1")
	if err != nil || out != "{\n  \"status\": \"success\",\n  \"details\": {\n    \"score\": 1\n  }\n}\n" {
		t.Fatalf("out = %q, %v", out, err)
	}
}
