package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/prefactordev/cli/internal/mcpconfig"
	"github.com/prefactordev/cli/internal/profile"
)

// fakeToken returns an account API token shaped as Prefactor makes them: a
// JWT whose "_" claim has t = "ba". The MCP server reads the claim to route
// the token; Prefactor checks the signature.
func fakeToken(name string) string {
	enc := base64.RawURLEncoding.EncodeToString
	payload, _ := json.Marshal(map[string]any{"_": map[string]any{"t": "ba"}, "sub": name})
	return enc([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc(payload) + "." + enc([]byte("sig-"+name))
}

// fakePrefactor answers the API calls prefactor mcp makes: creating and
// revoking API tokens and ping, for one full-access token and the read-only
// tokens it creates.
type fakePrefactor struct {
	*httptest.Server
	full string

	mu sync.Mutex
	// readOnly maps each created token to its id.
	readOnly map[string]string
	// refused holds tokens Prefactor no longer accepts.
	refused map[string]bool
	created []map[string]any
	revoked []string
	// readAuth holds the token of every GET request.
	readAuth []string
	// createStatus, when set, is the status token creation fails with.
	createStatus int
	// createRole is the role created tokens get; empty means read_only.
	createRole string
}

func newFakePrefactor(t *testing.T) *fakePrefactor {
	t.Helper()
	f := &fakePrefactor{full: fakeToken("full"), readOnly: map[string]string{}, refused: map[string]bool{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakePrefactor) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	w.Header().Set("Content-Type", "application/json")
	reply := func(status int, body any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	refuse := func() {
		reply(http.StatusUnauthorized, map[string]any{"status": "error", "code": "not_authenticated", "message": "Not authenticated"})
	}
	if r.Method == http.MethodGet {
		f.readAuth = append(f.readAuth, token)
	}

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/api_token":
		if _, ok := f.readOnly[token]; ok && !f.refused[token] {
			reply(http.StatusForbidden, map[string]any{"status": "error", "code": "not_permitted", "message": "Not permitted"})
			return
		}
		if token != f.full {
			refuse()
			return
		}
		if f.createStatus != 0 {
			reply(f.createStatus, map[string]any{"status": "error", "code": "not_permitted", "message": "Not permitted"})
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.created = append(f.created, body)
		n := len(f.created)
		ro, id := fakeToken(fmt.Sprintf("ro-%d", n)), fmt.Sprintf("tok_%d", n)
		f.readOnly[ro] = id
		details, _ := body["details"].(map[string]any)
		role := f.createRole
		if role == "" {
			role = "read_only"
		}
		reply(http.StatusOK, map[string]any{"status": "success", "token": ro, "details": map[string]any{
			"id": id, "role": role, "token_scope": "account", "expires_at": details["expires_at"],
		}})
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/api_token/") && strings.HasSuffix(r.URL.Path, "/revoke"):
		if token != f.full {
			refuse()
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/api_token/"), "/revoke")
		f.revoked = append(f.revoked, id)
		for tok, tokID := range f.readOnly {
			if tokID == id {
				f.refused[tok] = true
			}
		}
		reply(http.StatusOK, map[string]any{"status": "success", "details": map[string]any{"id": id, "status": "revoked"}})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/ping":
		details := map[string]any{"token_type": "api", "account_id": "acct_1"}
		switch id, ok := f.readOnly[token]; {
		case token == f.full:
			details["api_token_id"], details["read_only"] = "tok_full", false
		case ok && !f.refused[token]:
			details["api_token_id"], details["read_only"] = id, true
		default:
			refuse()
			return
		}
		reply(http.StatusOK, map[string]any{"status": "success", "details": details})
	default:
		reply(http.StatusNotFound, map[string]any{"status": "error", "code": "not_found", "message": "Not found"})
	}
}

func (f *fakePrefactor) state() (created []map[string]any, revoked, readAuth []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.created...), append([]string(nil), f.revoked...), append([]string(nil), f.readAuth...)
}

// addReadOnly makes Prefactor accept token as a read-only token that it
// did not create in this test.
func (f *fakePrefactor) addReadOnly(token, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readOnly[token] = id
}

func (f *fakePrefactor) setCreate(status int, role string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createStatus, f.createRole = status, role
}

func (f *fakePrefactor) refuse(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refused[token] = true
}

// mcpTestApp is a test app with a home directory and a project.
func mcpTestApp(t *testing.T) (*testApp, string, string) {
	t.Helper()
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	return newTestApp(t), home, project
}

func storedMCP(t *testing.T, ta *testApp, name string) *profile.MCPToken {
	t.Helper()
	p, ok := profile.Load(ta.configPath).Get(name)
	if !ok {
		t.Fatalf("profile %s missing", name)
	}
	return p.MCP
}

func readCodexEntry(t *testing.T, path string) map[string]any {
	t.Helper()
	var v map[string]any
	if _, err := toml.DecodeFile(path, &v); err != nil {
		t.Fatal(err)
	}
	return v["mcp_servers"].(map[string]any)["prefactor"].(map[string]any)
}

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestMCPInstallConfiguresTheClientsFound writes a remote entry with the
// profile's URL and no credentials for each client found in the home
// directory, so each client signs in with OAuth. It makes no request to
// Prefactor, keeps other servers and does nothing on a second run.
func TestMCPInstallConfiguresTheClientsFound(t *testing.T) {
	fake := newFakePrefactor(t)
	ta, home, _ := mcpTestApp(t)
	ta.addProfile(t, profile.DefaultName, fake.full, fake.URL)
	for _, dir := range []string{".claude", ".codex", ".cursor"} {
		if err := os.Mkdir(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cursorPath := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.WriteFile(cursorPath, []byte(`{"mcpServers": {"linear": {"url": "https://mcp.linear.app/mcp"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := ta.run(t, "mcp", "install")
	if err != nil {
		t.Fatal(err)
	}
	url := fake.URL + "/mcp"
	codex := readCodexEntry(t, filepath.Join(home, ".codex", "config.toml"))
	if fmt.Sprint(codex) != fmt.Sprint(map[string]any{"url": url}) {
		t.Fatalf("codex entry = %v", codex)
	}
	cursor := readJSONFile(t, cursorPath)["mcpServers"].(map[string]any)
	if cursor["linear"] == nil || fmt.Sprint(cursor["prefactor"]) != fmt.Sprint(map[string]any{"url": url}) {
		t.Fatalf("cursor servers = %v", cursor)
	}
	claude := readJSONFile(t, filepath.Join(home, ".claude.json"))["mcpServers"].(map[string]any)["prefactor"]
	if fmt.Sprint(claude) != fmt.Sprint(map[string]any{"type": "http", "url": url}) {
		t.Fatalf("claude entry = %v", claude)
	}
	for _, want := range []string{
		"Added the prefactor MCP server to Claude Code in " + filepath.Join(home, ".claude.json") + "\n",
		"Added the prefactor MCP server to Cursor in " + cursorPath + " (backup: " + cursorPath + mcpconfig.BackupSuffix + ")\n",
		"It connects to " + url + " and signs you in to Prefactor in the browser.\n",
		"codex mcp login prefactor",
		"/mcp",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "VS Code") || strings.Contains(out, mcpconfig.TokenEnvVar) {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if created, _, readAuth := fake.state(); len(created) != 0 || len(readAuth) != 0 {
		t.Fatal("an OAuth install called Prefactor")
	}
	if storedMCP(t, ta, profile.DefaultName) != nil {
		t.Fatal("an OAuth install stored a token")
	}

	again, err := ta.run(t, "mcp", "install")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again, "Codex already has the prefactor MCP server") {
		t.Fatalf("second run:\n%s", again)
	}
}

// TestMCPInstallForAProjectAndProfile writes project files with the
// profile's URL, for the clients given.
func TestMCPInstallForAProjectAndProfile(t *testing.T) {
	ta, home, project := mcpTestApp(t)
	ta.addProfile(t, "acme", fakeToken("acme"), "https://acme.example.com/ignored/path")

	out, err := ta.run(t, "mcp", "install", "--project", "--client", "vscode", "--client", "codex,claude-code", "--profile", "acme", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Profile  string       `json:"profile"`
		URL      string       `json:"url"`
		Auth     string       `json:"auth"`
		TokenID  *string      `json:"token_id"`
		Installs []mcpInstall `json:"installs"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	url := "https://acme.example.com/mcp"
	if got.Profile != "acme" || got.URL != url || got.Auth != "oauth" || got.TokenID != nil || len(got.Installs) != 3 {
		t.Fatalf("got %+v", got)
	}
	vscode := readJSONFile(t, filepath.Join(project, ".vscode", "mcp.json"))
	if fmt.Sprint(vscode["servers"].(map[string]any)["prefactor"]) != fmt.Sprint(map[string]any{"type": "http", "url": url}) || vscode["inputs"] != nil {
		t.Fatalf("vscode file = %v", vscode)
	}
	if codex := readCodexEntry(t, filepath.Join(project, ".codex", "config.toml")); codex["url"] != url {
		t.Fatalf("codex entry = %v", codex)
	}
	if claude := readJSONFile(t, filepath.Join(project, ".mcp.json")); claude["mcpServers"] == nil {
		t.Fatalf(".mcp.json = %v", claude)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); !os.IsNotExist(err) {
		t.Fatal("a project install wrote to the home directory")
	}
}

// TestMCPInstallWithoutAProfile uses PREFACTOR_API_URL, or Prefactor's
// usual URL, for OAuth, which needs no token.
func TestMCPInstallWithoutAProfile(t *testing.T) {
	ta, home, _ := mcpTestApp(t)
	if _, err := ta.run(t, "mcp", "install", "--client", "cursor"); err != nil {
		t.Fatal(err)
	}
	cursor := readJSONFile(t, filepath.Join(home, ".cursor", "mcp.json"))["mcpServers"].(map[string]any)["prefactor"].(map[string]any)
	if cursor["url"] != strings.TrimRight(profile.DefaultBaseURL, "/")+"/mcp" {
		t.Fatalf("cursor entry = %v", cursor)
	}

	ta.env["PREFACTOR_API_URL"] = "https://staging.example.com"
	if _, err := ta.run(t, "mcp", "install", "--client", "cursor"); err != nil {
		t.Fatal(err)
	}
	cursor = readJSONFile(t, filepath.Join(home, ".cursor", "mcp.json"))["mcpServers"].(map[string]any)["prefactor"].(map[string]any)
	if cursor["url"] != "https://staging.example.com/mcp" {
		t.Fatalf("cursor entry = %v", cursor)
	}
}

// TestMCPInstallWithAnAPITokenWritesNoToken creates the read-only token
// and writes entries that read it from the environment or a VS Code
// prompt, so no file holds the token.
func TestMCPInstallWithAnAPITokenWritesNoToken(t *testing.T) {
	fake := newFakePrefactor(t)
	ta, _, project := mcpTestApp(t)
	ta.addProfile(t, profile.DefaultName, fake.full, fake.URL)

	out, err := ta.run(t, "mcp", "install", "--project", "--client", "cursor,codex,vscode,claude-code", "--auth", "api-token")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"It connects to " + fake.URL + "/mcp for profile 'default' with a new read-only API token tok_1. Revoke it with 'prefactor api-tokens revoke tok_1'.\n",
		`export PREFACTOR_MCP_TOKEN="$(prefactor mcp token)"`,
		"VS Code asks for the token",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output is missing %q:\n%s", want, out)
		}
	}
	stored := storedMCP(t, ta, profile.DefaultName)
	if stored == nil || stored.ID != "tok_1" {
		t.Fatalf("stored = %+v", stored)
	}
	for _, rel := range []string{".cursor/mcp.json", ".codex/config.toml", ".vscode/mcp.json", ".mcp.json"} {
		raw, err := os.ReadFile(filepath.Join(project, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), stored.Token) || strings.Contains(string(raw), fake.full) {
			t.Fatalf("%s holds a token", rel)
		}
		if !strings.Contains(string(raw), fake.URL+"/mcp") {
			t.Fatalf("%s has no URL:\n%s", rel, raw)
		}
	}
	if codex := readCodexEntry(t, filepath.Join(project, ".codex", "config.toml")); codex["bearer_token_env_var"] != "PREFACTOR_MCP_TOKEN" {
		t.Fatalf("codex entry = %v", codex)
	}
	cursor := readJSONFile(t, filepath.Join(project, ".cursor", "mcp.json"))["mcpServers"].(map[string]any)["prefactor"].(map[string]any)
	if fmt.Sprint(cursor["headers"]) != fmt.Sprint(map[string]any{"Authorization": "Bearer ${env:PREFACTOR_MCP_TOKEN}"}) {
		t.Fatalf("cursor entry = %v", cursor)
	}
	claude := readJSONFile(t, filepath.Join(project, ".mcp.json"))["mcpServers"].(map[string]any)["prefactor"].(map[string]any)
	if fmt.Sprint(claude["headers"]) != fmt.Sprint(map[string]any{"Authorization": "Bearer ${PREFACTOR_MCP_TOKEN}"}) {
		t.Fatalf("claude entry = %v", claude)
	}

	// A second run reuses the token, and the JSON output says which.
	out, err = ta.run(t, "mcp", "install", "--project", "--client", "codex", "--auth", "api-token", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got["auth"] != "api-token" || got["token_id"] != "tok_1" || got["token_created"] != false || got["token_env_var"] != "PREFACTOR_MCP_TOKEN" {
		t.Fatalf("got %v", got)
	}
	if created, _, _ := fake.state(); len(created) != 1 {
		t.Fatalf("created %d tokens, want 1", len(created))
	}
}

func TestMCPInstallChecksItsOptions(t *testing.T) {
	fake := newFakePrefactor(t)
	ta, _, _ := mcpTestApp(t)

	for args, want := range map[string]string{
		"--project --global": "Pass --project or --global, not both.",
		"--client emacs":     "--client must be one of: claude-code, codex, cursor, vscode.",
	} {
		if _, err := ta.run(t, append([]string{"mcp", "install"}, strings.Fields(args)...)...); err == nil || err.Error() != want {
			t.Errorf("%s: err = %v, want %q", args, err, want)
		}
	}
	if _, err := ta.run(t, "mcp", "install", "--client", "codex", "--auth", "password"); err == nil || !strings.Contains(err.Error(), "Allowed choices are oauth, api-token.") {
		t.Errorf("bad --auth: %v", err)
	}
	if _, err := ta.run(t, "mcp", "install"); err == nil || !strings.Contains(err.Error(), "No MCP client found") {
		t.Errorf("no client: %v", err)
	}
	if _, err := ta.run(t, "mcp", "install", "--client", "codex", "--profile", "nope"); err == nil || !strings.Contains(err.Error(), "No profile found for 'nope'") {
		t.Errorf("missing named profile: %v", err)
	}
	if _, err := ta.run(t, "mcp", "install", "--client", "codex", "--auth", "api-token"); err == nil || !strings.Contains(err.Error(), "No profile found for 'default'") {
		t.Errorf("no profile: %v", err)
	}
	ta.env["PREFACTOR_API_TOKEN"] = fake.full
	if _, err := ta.run(t, "mcp", "install", "--client", "codex", "--auth", "api-token"); err == nil || !strings.Contains(err.Error(), "needs a saved profile") {
		t.Errorf("environment token: %v", err)
	}
}

// TestMCPInstallReportsAFileItCannotEdit configures the other clients and
// fails the run when one file cannot be read safely.
func TestMCPInstallReportsAFileItCannotEdit(t *testing.T) {
	fake := newFakePrefactor(t)
	ta, _, project := mcpTestApp(t)
	ta.addProfile(t, profile.DefaultName, fake.full, fake.URL)
	jsonc := filepath.Join(project, ".vscode", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(jsonc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonc, []byte("{\n  // mine\n  \"servers\": {}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, stderr, err := ta.runWithStderr(t, "mcp", "install", "--project", "--client", "vscode,cursor")
	if code, ok := ExitCode(err); !ok || code != 1 {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(stderr, "Could not configure VS Code: could not update "+jsonc) {
		t.Fatalf("stderr:\n%s", stderr)
	}
	if !strings.Contains(out, "Added the prefactor MCP server to Cursor") {
		t.Fatalf("stdout:\n%s", out)
	}
}

func TestMCPUninstallRemovesOnlyThePrefactorEntry(t *testing.T) {
	fake := newFakePrefactor(t)
	ta, home, _ := mcpTestApp(t)
	ta.addProfile(t, profile.DefaultName, fake.full, fake.URL)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	codexPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.WriteFile(codexPath, []byte("[mcp_servers.linear]\nurl = \"https://mcp.linear.app/mcp\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ta.run(t, "mcp", "install", "--client", "codex,cursor"); err != nil {
		t.Fatal(err)
	}

	out, err := ta.run(t, "mcp", "uninstall")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Removed the prefactor MCP server from Codex in "+codexPath) || !strings.Contains(out, "from Cursor") {
		t.Fatalf("output:\n%s", out)
	}
	raw, _ := os.ReadFile(codexPath)
	if string(raw) != "[mcp_servers.linear]\nurl = \"https://mcp.linear.app/mcp\"\n" {
		t.Fatalf("codex config after uninstall:\n%s", raw)
	}

	if out, err := ta.run(t, "mcp", "uninstall"); err != nil || out != "No client has the prefactor MCP server.\n" {
		t.Fatalf("second uninstall: %q, %v", out, err)
	}
	if out, err := ta.run(t, "mcp", "uninstall", "--client", "codex"); err != nil || !strings.Contains(out, "Codex has no prefactor MCP server in "+codexPath) {
		t.Fatalf("named uninstall: %q, %v", out, err)
	}
}

// TestMCPUninstallRemovesOldStdioEntries removes the entries the earlier
// install wrote, which ran 'prefactor mcp serve', and the VS Code input of
// a token install.
func TestMCPUninstallRemovesOldStdioEntries(t *testing.T) {
	ta, home, _ := mcpTestApp(t)
	vscodeDir, err := mcpconfig.VSCodeUserDir(mcpconfig.Locations{Home: home, GOOS: runtime.GOOS, Getenv: ta.Getenv})
	if err != nil {
		t.Skip(err)
	}
	files := map[string]string{
		filepath.Join(home, ".codex", "config.toml"): "[mcp_servers.prefactor]\ncommand = \"/opt/prefactor/bin/prefactor\"\nargs = [\"mcp\", \"serve\"]\nstartup_timeout_sec = 30\n\n[mcp_servers.linear]\nurl = \"https://mcp.linear.app/mcp\"\n",
		filepath.Join(home, ".cursor", "mcp.json"):   `{"mcpServers": {"prefactor": {"command": "/opt/prefactor/bin/prefactor", "args": ["mcp", "serve"]}, "linear": {"url": "https://mcp.linear.app/mcp"}}}`,
		filepath.Join(home, ".claude.json"):          `{"mcpServers": {"prefactor": {"type": "stdio", "command": "/opt/prefactor/bin/prefactor", "args": ["mcp", "serve"], "env": {}}, "linear": {"type": "http", "url": "https://mcp.linear.app/mcp"}}}`,
		filepath.Join(vscodeDir, "mcp.json"):         `{"inputs": [{"id": "prefactor-mcp-token", "type": "promptString"}], "servers": {"prefactor": {"type": "http", "url": "https://x/mcp", "headers": {"Authorization": "Bearer ${input:prefactor-mcp-token}"}}, "linear": {"type": "http", "url": "https://mcp.linear.app/mcp"}}}`,
	}
	for path, contents := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := ta.run(t, "mcp", "uninstall")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Claude Code", "Codex", "Cursor", "VS Code"} {
		if !strings.Contains(out, "Removed the prefactor MCP server from "+name) {
			t.Fatalf("output is missing %s:\n%s", name, out)
		}
	}
	for path, before := range files {
		raw, _ := os.ReadFile(path)
		if strings.Contains(string(raw), "prefactor") || !strings.Contains(string(raw), "linear") {
			t.Fatalf("%s after uninstall:\n%s", path, raw)
		}
		if backup, _ := os.ReadFile(path + mcpconfig.BackupSuffix); string(backup) != before {
			t.Fatalf("%s backup differs from the original", path)
		}
	}
}

// TestMCPConfigPrintsWithoutWriting prints a client's snippet and makes no
// request to Prefactor and no file.
func TestMCPConfigPrintsWithoutWriting(t *testing.T) {
	fake := newFakePrefactor(t)
	ta, home, _ := mcpTestApp(t)
	ta.addProfile(t, "acme", fake.full, fake.URL)

	out, err := ta.run(t, "mcp", "config", "--client", "codex", "--profile", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if want := "[mcp_servers.prefactor]\nurl = " + tomlQuote(fake.URL+"/mcp") + "\n"; out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}

	out, err = ta.run(t, "mcp", "config", "--client", "vscode", "--profile", "acme", "--auth", "api-token", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got["client"] != "vscode" || !strings.Contains(got["config"], "${input:prefactor-mcp-token}") || got["path"] == "" {
		t.Fatalf("got %v", got)
	}

	if _, err := ta.run(t, "mcp", "config"); err == nil || !strings.Contains(err.Error(), "Pass --client") {
		t.Fatalf("no client: %v", err)
	}
	if _, err := ta.run(t, "mcp", "config", "--client", "codex,cursor"); err == nil || err.Error() != "Pass one --client." {
		t.Fatalf("two clients: %v", err)
	}
	if created, _, readAuth := fake.state(); len(created) != 0 || len(readAuth) != 0 {
		t.Fatal("config called Prefactor")
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("config wrote to the home directory: %v", entries)
	}
}

func tomlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// TestMCPTokenPrintsTheReadOnlyToken prints the stored read-only token,
// creating it once with a 90-day expiry, and never the profile's own token.
func TestMCPTokenPrintsTheReadOnlyToken(t *testing.T) {
	fake := newFakePrefactor(t)
	ta, _, _ := mcpTestApp(t)
	ta.addProfile(t, profile.DefaultName, fake.full, fake.URL)
	start := time.Now()

	out, err := ta.run(t, "mcp", "token")
	if err != nil {
		t.Fatal(err)
	}
	if out != fakeToken("ro-1")+"\n" {
		t.Fatalf("output = %q", out)
	}
	created, _, _ := fake.state()
	if len(created) != 1 {
		t.Fatalf("created %d tokens, want 1", len(created))
	}
	details := created[0]["details"].(map[string]any)
	if details["token_scope"] != "account" || details["role"] != "read_only" {
		t.Fatalf("create details = %v", details)
	}
	expires, err := time.Parse(time.RFC3339, details["expires_at"].(string))
	if err != nil || expires.Sub(start) < 89*24*time.Hour || expires.Sub(start) > 91*24*time.Hour {
		t.Fatalf("expires_at = %v (%v), want about 90 days from now", details["expires_at"], err)
	}
	if stored := storedMCP(t, ta, profile.DefaultName); stored == nil || stored.ID != "tok_1" || stored.ExpiresAt != details["expires_at"] {
		t.Fatalf("stored token = %+v", stored)
	}
	if info, err := os.Stat(ta.configPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("profiles file mode = %v, %v", info.Mode().Perm(), err)
	}

	out, err = ta.run(t, "mcp", "token", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got["token"] != fakeToken("ro-1") || got["id"] != "tok_1" || got["created"] != false {
		t.Fatalf("got %v", got)
	}
	if created, _, _ := fake.state(); len(created) != 1 {
		t.Fatalf("created %d tokens", len(created))
	}
}

// TestMCPTokenReplacesARefusedToken creates a new token when Prefactor
// refuses the stored one, and revokes the old one.
func TestMCPTokenReplacesARefusedToken(t *testing.T) {
	fake := newFakePrefactor(t)
	ta, _, _ := mcpTestApp(t)
	ta.addProfile(t, profile.DefaultName, fake.full, fake.URL)
	if _, err := ta.run(t, "mcp", "token"); err != nil {
		t.Fatal(err)
	}
	first := storedMCP(t, ta, profile.DefaultName)
	fake.refuse(first.Token)

	out, err := ta.run(t, "mcp", "token")
	if err != nil {
		t.Fatal(err)
	}
	created, revoked, _ := fake.state()
	second := storedMCP(t, ta, profile.DefaultName)
	if len(created) != 2 || second.ID != "tok_2" || out != second.Token+"\n" {
		t.Fatalf("created %d, stored %+v, output %q", len(created), second, out)
	}
	if len(revoked) != 1 || revoked[0] != first.ID {
		t.Fatalf("revoked = %v, want [%s]", revoked, first.ID)
	}
}

// TestMCPTokenReplacesATokenNearItsExpiry replaces a stored token within 7
// days of its expiry before using it.
func TestMCPTokenReplacesATokenNearItsExpiry(t *testing.T) {
	fake := newFakePrefactor(t)
	ta, _, _ := mcpTestApp(t)
	ta.addProfile(t, profile.DefaultName, fake.full, fake.URL)
	if _, err := ta.run(t, "mcp", "token"); err != nil {
		t.Fatal(err)
	}
	first := storedMCP(t, ta, profile.DefaultName)
	expiry, _ := time.Parse(time.RFC3339, first.ExpiresAt)

	ta.Now = func() time.Time { return expiry.Add(-8 * 24 * time.Hour) }
	if _, err := ta.run(t, "mcp", "token"); err != nil {
		t.Fatal(err)
	}
	if created, _, _ := fake.state(); len(created) != 1 {
		t.Fatalf("a token 8 days from expiry was replaced")
	}

	ta.Now = func() time.Time { return expiry.Add(-6 * 24 * time.Hour) }
	if _, err := ta.run(t, "mcp", "token"); err != nil {
		t.Fatal(err)
	}
	created, revoked, _ := fake.state()
	if len(created) != 2 || len(revoked) != 1 || revoked[0] != first.ID {
		t.Fatalf("created %d, revoked %v", len(created), revoked)
	}
}

// TestMCPTokenReadsTheGivenProfilesFile uses --config.
func TestMCPTokenReadsTheGivenProfilesFile(t *testing.T) {
	fake := newFakePrefactor(t)
	ta, _, _ := mcpTestApp(t)
	other := filepath.Join(t.TempDir(), "prefactor.json")
	if err := profile.Load(other).Set(profile.DefaultName, profile.Profile{APIKey: fake.full, BaseURL: fake.URL}); err != nil {
		t.Fatal(err)
	}
	if _, err := ta.run(t, "mcp", "token", "--config", other); err != nil {
		t.Fatal(err)
	}
	if p, _ := profile.Load(other).Get(profile.DefaultName); p.MCP == nil {
		t.Fatal("the token was not stored in the given file")
	}
}

// TestMCPTokenUsesAReadOnlyProfileToken uses the profile's own token when
// it cannot create tokens, once Prefactor says it is read-only.
func TestMCPTokenUsesAReadOnlyProfileToken(t *testing.T) {
	fake := newFakePrefactor(t)
	ta, _, _ := mcpTestApp(t)
	given := fakeToken("given")
	fake.addReadOnly(given, "tok_given")
	ta.addProfile(t, profile.DefaultName, given, fake.URL)

	out, err := ta.run(t, "mcp", "token")
	if err != nil || out != given+"\n" {
		t.Fatalf("output %q, err %v", out, err)
	}
	if created, _, _ := fake.state(); len(created) != 0 {
		t.Fatal("a read-only token created a token")
	}
}

func TestMCPTokenRefusesFullAccessTokens(t *testing.T) {
	fake := newFakePrefactor(t)

	t.Run("profile that cannot create tokens", func(t *testing.T) {
		ta, _, _ := mcpTestApp(t)
		ta.addProfile(t, profile.DefaultName, fake.full, fake.URL)
		fake.setCreate(http.StatusForbidden, "")
		defer fake.setCreate(0, "")
		out, err := ta.run(t, "mcp", "token")
		if err == nil || strings.Contains(out, fake.full) || !strings.Contains(err.Error(), "has full access but cannot create a read-only token") {
			t.Fatalf("output %q, err %v", out, err)
		}
	})

	t.Run("Prefactor ignores the role", func(t *testing.T) {
		ta, _, _ := mcpTestApp(t)
		ta.addProfile(t, profile.DefaultName, fake.full, fake.URL)
		fake.setCreate(0, "full_access")
		defer fake.setCreate(0, "")
		_, err := ta.run(t, "mcp", "token")
		if err == nil || !strings.Contains(err.Error(), "does not support read-only API tokens") {
			t.Fatalf("err = %v", err)
		}
		if _, revoked, _ := fake.state(); len(revoked) == 0 {
			t.Fatal("the full-access token was not revoked")
		}
		if storedMCP(t, ta, profile.DefaultName) != nil {
			t.Fatal("the full-access token was stored")
		}
	})

	t.Run("profile token refused", func(t *testing.T) {
		ta, _, _ := mcpTestApp(t)
		ta.addProfile(t, profile.DefaultName, fakeToken("stale"), fake.URL)
		_, err := ta.run(t, "mcp", "token")
		if err == nil || !strings.Contains(err.Error(), "refused the API token for profile 'default', so no read-only token could be created. Run 'prefactor login' again") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("no profile", func(t *testing.T) {
		ta, _, _ := mcpTestApp(t)
		_, err := ta.run(t, "mcp", "token", "--profile", "nope")
		if err == nil || !strings.Contains(err.Error(), "No profile found for 'nope'") {
			t.Fatalf("err = %v", err)
		}
	})
}

// TestMCPServeIsGone checks the local stdio server was removed.
func TestMCPServeIsGone(t *testing.T) {
	ta, _, _ := mcpTestApp(t)
	if _, err := ta.run(t, "mcp", "serve"); err == nil {
		t.Fatal("prefactor mcp serve still runs")
	}
}
