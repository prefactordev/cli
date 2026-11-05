package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prefactordev/cli/internal/profile"
)

type testApp struct {
	*App
	configPath string
	env        map[string]string
	// terminal makes stdin and stdout look like terminals.
	terminal bool
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	ta := &testApp{
		configPath: filepath.Join(t.TempDir(), "prefactor.json"),
		env:        map[string]string{},
	}
	ta.App = &App{
		Version:    "1.2.3",
		ConfigPath: func() (string, error) { return ta.configPath, nil },
		Getenv:     func(key string) string { return ta.env[key] },
		LookupEnv: func(key string) (string, bool) {
			value, ok := ta.env[key]
			return value, ok
		},
		Login: LoginDeps{},
	}
	ta.StdinIsTerminal = func() bool { return ta.terminal }
	ta.StdoutIsTerminal = func() bool { return ta.terminal }
	ta.StderrIsTerminal = func() bool { return ta.terminal }
	return ta
}

func (ta *testApp) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, _, err := ta.runWithStderr(t, args...)
	return out, err
}

func (ta *testApp) runWithStderr(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCommand(ta.App)
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	ta.Argv = args
	err := root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

func (ta *testApp) addProfile(t *testing.T, name, token, baseURL string) {
	t.Helper()
	if err := profile.Load(ta.configPath).Set(name, profile.Profile{APIKey: token, BaseURL: baseURL}); err != nil {
		t.Fatal(err)
	}
}

// recordingServer answers every request with body and records the last request.
type recordingServer struct {
	*httptest.Server
	method, path, query, auth, body string
}

func newRecordingServer(t *testing.T, body string) *recordingServer {
	t.Helper()
	rs := &recordingServer{}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rs.method, rs.path, rs.query, rs.auth, rs.body = r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(rs.Close)
	return rs
}

func TestVersionCommandPrintsVersion(t *testing.T) {
	out, err := newTestApp(t).run(t, "version")
	if err != nil || out != "1.2.3\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestProfilesAddListRemove(t *testing.T) {
	ta := newTestApp(t)

	out, err := ta.run(t, "profiles", "list")
	if err != nil || out != "No profiles configured. Use 'prefactor profiles add <name> [baseUrl] --api-token <apiToken>'.\n" {
		t.Fatalf("empty list: out = %q, err = %v", out, err)
	}

	if out, err = ta.run(t, "profiles", "add", "default", "--api-token", "tok"); err != nil || out != "Profile 'default' saved.\n" {
		t.Fatalf("add default: out = %q, err = %v", out, err)
	}
	if out, err = ta.run(t, "profiles", "add", "staging", "https://staging.example", "--api-token", "s"); err != nil || out != "Profile 'staging' saved.\n" {
		t.Fatalf("add staging: out = %q, err = %v", out, err)
	}

	out, err = ta.run(t, "profiles", "list")
	want := "default (current) - https://app.prefactorai.com\nstaging - https://staging.example\n"
	if err != nil || out != want {
		t.Fatalf("list: out = %q, want %q, err = %v", out, want, err)
	}

	out, _ = ta.run(t, "--profile", "staging", "profiles", "list")
	if want := "default - https://app.prefactorai.com\nstaging (current) - https://staging.example\n"; out != want {
		t.Fatalf("list --profile staging = %q, want %q", out, want)
	}

	ta.env["PREFACTOR_PROFILE"] = "staging"
	out, _ = ta.run(t, "profiles", "list")
	if !strings.Contains(out, "staging (current)") {
		t.Fatalf("list with PREFACTOR_PROFILE = %q", out)
	}

	if out, _ = ta.run(t, "profiles", "remove", "staging"); out != "Profile 'staging' removed.\n" {
		t.Fatalf("remove = %q", out)
	}
	if out, _ = ta.run(t, "profiles", "remove", "staging"); out != "Profile 'staging' not found.\n" {
		t.Fatalf("remove again = %q", out)
	}
}

func TestProfilesAddRejectsBadInput(t *testing.T) {
	ta := newTestApp(t)

	if _, err := ta.run(t, "profiles", "add", "x", "--api-token", "  "); err == nil || err.Error() != "Missing API token. Specify --api-token <apiToken>." {
		t.Fatalf("blank token err = %v", err)
	}
	if _, err := ta.run(t, "profiles", "add", "x", "not a url", "--api-token", "t"); err == nil || err.Error() != "--baseUrl must be a valid URL." {
		t.Fatalf("bad url err = %v", err)
	}
	if _, err := ta.run(t, "profiles", "add", "x"); err == nil {
		t.Fatal("missing --api-token succeeded")
	}
}

func TestPingPrintsTheResponseInServerKeyOrder(t *testing.T) {
	ta := newTestApp(t)
	server := newRecordingServer(t, `{"status":"success","details":{"token_type":"api_session_account_scope","account_id":"acc"}}`)
	ta.addProfile(t, "default", "tok", server.URL)

	out, err := ta.run(t, "ping")
	if err != nil {
		t.Fatal(err)
	}

	want := "{\n  \"status\": \"success\",\n  \"details\": {\n    \"token_type\": \"api_session_account_scope\",\n    \"account_id\": \"acc\"\n  }\n}\n"
	if out != want {
		t.Fatalf("out =\n%s\nwant\n%s", out, want)
	}
	if server.method != http.MethodGet || server.path != "/api/v1/ping" || server.auth != "Bearer tok" {
		t.Fatalf("request = %s %s auth=%q", server.method, server.path, server.auth)
	}
}

func TestPingKeepsAnEmptyAPIURL(t *testing.T) {
	for name, setup := range map[string]func(ta *testApp) []string{
		"--api-url":         func(*testApp) []string { return []string{"--api-url", ""} },
		"PREFACTOR_API_URL": func(ta *testApp) []string { ta.env["PREFACTOR_API_URL"] = ""; return nil },
	} {
		ta := newTestApp(t)
		args := append([]string{"ping", "--api-token", "tok"}, setup(ta)...)
		if _, err := ta.run(t, args...); err == nil || !strings.Contains(err.Error(), "invalid base URL") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestPingWithSuppliedToken(t *testing.T) {
	ta := newTestApp(t)
	server := newRecordingServer(t, `{"status":"success"}`)

	if _, err := ta.run(t, "ping", "--api-token", "given", "--api-url", server.URL); err != nil {
		t.Fatal(err)
	}
	if server.auth != "Bearer given" {
		t.Fatalf("auth = %q", server.auth)
	}

	ta.env["PREFACTOR_API_URL"] = server.URL
	server.auth = ""
	if _, err := ta.run(t, "ping", "--api-token", "from-env-url"); err != nil {
		t.Fatal(err)
	}
	if server.auth != "Bearer from-env-url" {
		t.Fatalf("auth with PREFACTOR_API_URL = %q", server.auth)
	}
}

func TestEnvironmentTokenOnlyAppliesToTheDefaultProfile(t *testing.T) {
	ta := newTestApp(t)
	server := newRecordingServer(t, `{"status":"success"}`)
	ta.env["PREFACTOR_API_TOKEN"] = "env-token"
	ta.env["PREFACTOR_API_URL"] = server.URL

	if _, err := ta.run(t, "ping"); err != nil {
		t.Fatalf("default with env token: %v", err)
	}
	if server.auth != "Bearer env-token" {
		t.Fatalf("auth = %q", server.auth)
	}

	_, err := ta.run(t, "--profile", "prod", "ping")
	want := "No profile found for 'prod'. Run 'prefactor profiles add <name> [baseUrl] --api-token <apiToken>' to configure one."
	if err == nil || err.Error() != want {
		t.Fatalf("explicit profile err = %v", err)
	}

	ta.env["PREFACTOR_PROFILE"] = "prod"
	if _, err := ta.run(t, "ping"); err == nil || err.Error() != want {
		t.Fatalf("PREFACTOR_PROFILE err = %v", err)
	}
}

func TestSavedProfileWinsOverEnvironmentToken(t *testing.T) {
	ta := newTestApp(t)
	server := newRecordingServer(t, `{"status":"success"}`)
	ta.addProfile(t, "default", "profile-token", server.URL)
	ta.env["PREFACTOR_API_TOKEN"] = "env-token"

	if _, err := ta.run(t, "ping"); err != nil {
		t.Fatal(err)
	}
	if server.auth != "Bearer profile-token" {
		t.Fatalf("auth = %q", server.auth)
	}
}

func TestPfidGenerateSendsCountAndAccount(t *testing.T) {
	ta := newTestApp(t)
	server := newRecordingServer(t, `{"account_id":"acc","pfids":["p1","p2"],"status":"success"}`)
	ta.addProfile(t, "default", "tok", server.URL)

	out, err := ta.run(t, "pfid", "generate", "--count", "2", "--account-id", "acc")
	if err != nil {
		t.Fatal(err)
	}
	if server.method != http.MethodPost || server.path != "/api/v1/pfid/generate" {
		t.Fatalf("request = %s %s", server.method, server.path)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(server.body), &body); err != nil {
		t.Fatal(err)
	}
	if body["count"] != float64(2) || body["account_id"] != "acc" || len(body) != 2 {
		t.Fatalf("body = %s", server.body)
	}
	if !strings.HasPrefix(out, "{\n  \"account_id\": \"acc\",\n  \"pfids\": [\n    \"p1\",") {
		t.Fatalf("out = %s", out)
	}

	if _, err := ta.run(t, "pfid", "generate"); err != nil {
		t.Fatal(err)
	}
	if server.body != `{"count":1}` {
		t.Fatalf("default body = %s", server.body)
	}

	if _, err := ta.run(t, "pfid", "generate", "--count", "0"); err == nil || err.Error() != "Value must be a positive integer." {
		t.Fatalf("count 0 err = %v", err)
	}
}

func TestAPIErrorKeepsStatusAndBody(t *testing.T) {
	ta := newTestApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"status":"error","code":"bad_authtoken"}`)
	}))
	defer server.Close()
	ta.addProfile(t, "default", "tok", server.URL)

	_, err := ta.run(t, "ping")
	if err == nil || err.Error() != "HTTP request failed with status 401" {
		t.Fatalf("err = %v", err)
	}
}

func TestProfileFileIsSharedWithTheTypeScriptFormat(t *testing.T) {
	ta := newTestApp(t)
	// Written by the TypeScript CLI.
	if err := os.WriteFile(ta.configPath, []byte("{\n  \"default\": {\n    \"api_key\": \"ts-token\",\n    \"base_url\": \"https://app.prefactorai.com\"\n  }\n}"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := ta.run(t, "profiles", "list")
	if err != nil || out != "default (current) - https://app.prefactorai.com\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

// fakeClock is a manual clock whose Sleep advances time.
type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return nil
}

// TestProfileFileEdgeCasesMatchTheTypeScriptCLI uses outputs captured from the
// TypeScript CLI for the same files.
func TestProfileFileEdgeCasesMatchTheTypeScriptCLI(t *testing.T) {
	cases := map[string]struct {
		file string
		want string
	}{
		"repeated name uses the last": {
			file: `{"default":{"api_key":"first","base_url":"http://first"},"default":{"api_key":"second","base_url":"http://second"}}`,
			want: "default (current) - http://second\n",
		},
		"text after the JSON is invalid": {
			file: `{"default":{"api_key":"k","base_url":"http://x"}} trailing`,
			want: "No profiles configured. Use 'prefactor profiles add <name> [baseUrl] --api-token <apiToken>'.\n",
		},
		"number-like names come first": {
			file: `{"b":{"api_key":"k","base_url":"http://b"},"2":{"api_key":"k","base_url":"http://2"},"1":{"api_key":"k","base_url":"http://1"}}`,
			want: "1 - http://1\n2 - http://2\nb - http://b\n",
		},
		"an array is read by index": {
			file: `[{"api_key":"k","base_url":"http://zero"}]`,
			want: "0 - http://zero\n",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ta := newTestApp(t)
			if err := os.WriteFile(ta.configPath, []byte(tc.file), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := ta.run(t, "profiles", "list")
			if err != nil || out != tc.want {
				t.Fatalf("out = %q, want %q (err %v)", out, tc.want, err)
			}
		})
	}
}

func TestSavingKeepsTheTypeScriptKeyOrder(t *testing.T) {
	ta := newTestApp(t)
	if err := os.WriteFile(ta.configPath, []byte(`{"b":{"api_key":"k","base_url":"http://b"},"2":{"api_key":"k","base_url":"http://2"},"1":{"api_key":"k","base_url":"http://1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ta.run(t, "profiles", "add", "10", "http://ten", "--api-token", "t"); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(ta.configPath)
	compact := strings.NewReplacer("\n", "", " ", "").Replace(string(raw))
	// Written by the TypeScript CLI for the same steps.
	want := `{"1":{"api_key":"k","base_url":"http://1"},"2":{"api_key":"k","base_url":"http://2"},"10":{"api_key":"t","base_url":"http://ten"},"b":{"api_key":"k","base_url":"http://b"}}`
	if compact != want {
		t.Fatalf("file = %s\nwant   %s", compact, want)
	}
}

func TestAgentInstancesListSendsStatusesInTheFormP2Accepts(t *testing.T) {
	ta := newTestApp(t)
	server := newRecordingServer(t, `{"status":"success","summaries":[]}`)
	ta.addProfile(t, "default", "tok", server.URL)

	if _, err := ta.run(t, "agent-instances", "list", "--agent-id", "a1", "--statuses", "active,complete"); err != nil {
		t.Fatal(err)
	}
	if want := "agent_id=a1&statuses%5B%5D=active&statuses%5B%5D=complete"; server.query != want {
		t.Fatalf("query = %q, want %q", server.query, want)
	}
}
