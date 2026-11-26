package mcpconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// oauth is the default entry: the client signs the person in itself.
var oauth = Entry{URL: "https://app.example.com/mcp"}

// token is the entry that sends the read-only token from the environment.
var token = Entry{URL: "https://app.example.com/mcp", Token: true}

// The entries the old stdio install wrote, which uninstall still removes.
const (
	oldCursor = `{"mcpServers": {"other": {"command": "x"}, "prefactor": {"command": "/opt/prefactor", "args": ["mcp", "serve"]}}}`
	oldClaude = `{"mcpServers": {"prefactor": {"type": "stdio", "command": "/opt/prefactor", "args": ["mcp", "serve"], "env": {}}, "other": {"type": "http", "url": "u"}}}`
	oldVSCode = `{"servers": {"prefactor": {"type": "stdio", "command": "/opt/prefactor", "args": ["mcp", "serve"]}, "other": {"type": "stdio", "command": "x"}}}`
	oldCodex  = "[mcp_servers.other]\ncommand = \"x\"\n\n[mcp_servers.prefactor]\ncommand = \"/opt/prefactor\"\nargs = [\"mcp\", \"serve\"]\nstartup_timeout_sec = 30\n"
)

func write(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func decodeJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("%v:\n%s", err, raw)
	}
	return v
}

func TestPaths(t *testing.T) {
	env := func(map[string]string) func(string) string {
		return func(string) string { return "" }
	}
	home, project := "/home/u", "/work/app"
	cases := []struct {
		client Client
		scope  Scope
		goos   string
		want   string
	}{
		{Codex, Global, "linux", "/home/u/.codex/config.toml"},
		{Codex, Project, "linux", "/work/app/.codex/config.toml"},
		{Cursor, Global, "linux", "/home/u/.cursor/mcp.json"},
		{Cursor, Project, "linux", "/work/app/.cursor/mcp.json"},
		{ClaudeCode, Global, "linux", "/home/u/.claude.json"},
		{ClaudeCode, Project, "linux", "/work/app/.mcp.json"},
		{VSCode, Global, "darwin", "/home/u/Library/Application Support/Code/User/mcp.json"},
		{VSCode, Global, "linux", "/home/u/.config/Code/User/mcp.json"},
		{VSCode, Project, "linux", "/work/app/.vscode/mcp.json"},
	}
	for _, c := range cases {
		got, err := Path(c.client, c.scope, Locations{Home: home, Project: project, GOOS: c.goos, Getenv: env(nil)})
		if err != nil || got != filepath.FromSlash(c.want) {
			t.Errorf("%s %v %s: %q, %v; want %q", c.client, c.scope, c.goos, got, err, c.want)
		}
	}

	vars := map[string]string{"CODEX_HOME": "/codex", "CLAUDE_CONFIG_DIR": "/claude", "XDG_CONFIG_HOME": "/xdg", "APPDATA": `C:\Users\u\AppData\Roaming`}
	getenv := func(k string) string { return vars[k] }
	loc := Locations{Home: home, Project: project, GOOS: "linux", Getenv: getenv}
	for client, want := range map[Client]string{Codex: "/codex/config.toml", ClaudeCode: "/claude/.claude.json", VSCode: "/xdg/Code/User/mcp.json"} {
		if got, _ := Path(client, Global, loc); got != filepath.FromSlash(want) {
			t.Errorf("%s with its variable set: %q, want %q", client, got, want)
		}
	}
	loc.GOOS = "windows"
	if got, _ := Path(VSCode, Global, loc); got != filepath.Join(`C:\Users\u\AppData\Roaming`, "Code", "User", "mcp.json") {
		t.Errorf("VS Code on Windows: %q", got)
	}
}

// TestJSONClientsMergeWithoutClobbering adds the entry to each JSON client's
// file next to the servers, inputs and settings already there, keeping
// their order and number spelling.
func TestJSONClientsMergeWithoutClobbering(t *testing.T) {
	cases := []struct {
		client  Client
		key     string
		before  string
		want    map[string]any
		wantKey string
	}{
		{Cursor, "mcpServers", `{"mcpServers": {"other": {"command": "x"}}, "zz": 1.50}`,
			map[string]any{"url": oauth.URL}, ""},
		{ClaudeCode, "mcpServers", `{"numStartups": 12345678901234, "mcpServers": {"other": {"type": "http", "url": "u"}}, "projects": {}}`,
			map[string]any{"type": "http", "url": oauth.URL}, ""},
		{VSCode, "servers", `{"inputs": [{"id": "x", "type": "promptString"}], "servers": {"other": {"type": "stdio", "command": "x"}}}`,
			map[string]any{"type": "http", "url": oauth.URL}, ""},
	}
	for _, c := range cases {
		t.Run(string(c.client), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			write(t, path, c.before, 0o600)

			res, err := Install(c.client, path, oauth)
			if err != nil {
				t.Fatal(err)
			}
			if !res.Changed || res.Backup != path+BackupSuffix {
				t.Fatalf("result %+v", res)
			}
			if read(t, res.Backup) != c.before {
				t.Fatalf("backup differs from the original")
			}
			after := read(t, path)
			got := decodeJSON(t, after)
			servers := got[c.key].(map[string]any)
			if !reflect.DeepEqual(servers[ServerName], c.want) {
				t.Fatalf("entry = %#v, want %#v", servers[ServerName], c.want)
			}
			if servers["other"] == nil {
				t.Fatalf("the other server was removed:\n%s", after)
			}
			// Untouched values keep their order and spelling.
			if c.client == Cursor && !strings.Contains(after, `"zz": 1.50`) {
				t.Fatalf("number spelling changed:\n%s", after)
			}
			if c.client == ClaudeCode && (!strings.Contains(after, "12345678901234") || strings.Index(after, "numStartups") > strings.Index(after, "mcpServers")) {
				t.Fatalf("order or numbers changed:\n%s", after)
			}
			if c.client == VSCode && len(got["inputs"].([]any)) != 1 {
				t.Fatalf("inputs changed:\n%s", after)
			}
			if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
				t.Fatalf("mode = %v, want the original 0600", info.Mode().Perm())
			}

			// A second install changes nothing and makes no backup.
			if err := os.Remove(res.Backup); err != nil {
				t.Fatal(err)
			}
			again, err := Install(c.client, path, oauth)
			if err != nil || again.Changed || again.Backup != "" {
				t.Fatalf("second install: %+v, %v", again, err)
			}
			if read(t, path) != after {
				t.Fatal("second install rewrote the file")
			}
			if _, err := os.Stat(path + BackupSuffix); !os.IsNotExist(err) {
				t.Fatal("second install made a backup")
			}

			// Uninstall removes only the prefactor entry.
			un, err := Uninstall(c.client, path)
			if err != nil || !un.Changed {
				t.Fatalf("uninstall: %+v, %v", un, err)
			}
			left := decodeJSON(t, read(t, path))
			if _, ok := left[c.key].(map[string]any)[ServerName]; ok {
				t.Fatal("entry still there after uninstall")
			}
			if left[c.key].(map[string]any)["other"] == nil {
				t.Fatal("uninstall removed the other server")
			}
			if again, err := Uninstall(c.client, path); err != nil || again.Changed {
				t.Fatalf("second uninstall: %+v, %v", again, err)
			}
		})
	}
}

func TestJSONCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", ".cursor", "mcp.json")
	res, err := Install(Cursor, path, oauth)
	if err != nil || !res.Changed || res.Backup != "" {
		t.Fatalf("%+v, %v", res, err)
	}
	want := `{
  "mcpServers": {
    "prefactor": {
      "url": "https://app.example.com/mcp"
    }
  }
}
`
	if got := read(t, path); got != want {
		t.Fatalf("file:\n%s\nwant:\n%s", got, want)
	}
	if res, err := Uninstall(Cursor, filepath.Join(t.TempDir(), "missing.json")); err != nil || res.Changed {
		t.Fatalf("uninstall of a missing file: %+v, %v", res, err)
	}
}

func TestJSONReplacesAnOutdatedEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	write(t, path, `{"mcpServers":{"prefactor":{"command":"old"},"prefactor":{"command":"dup"}}}`, 0o644)
	if _, err := Install(Cursor, path, oauth); err != nil {
		t.Fatal(err)
	}
	after := read(t, path)
	if strings.Count(after, `"prefactor"`) != 1 || strings.Contains(after, "old") || strings.Contains(after, "command") {
		t.Fatalf("file:\n%s", after)
	}
}

func TestJSONRefusesFilesItCannotRead(t *testing.T) {
	for name, contents := range map[string]string{
		"comments":    "{\n  // a comment\n  \"servers\": {}\n}",
		"not object":  `[]`,
		"bad servers": `{"servers": []}`,
	} {
		path := filepath.Join(t.TempDir(), "mcp.json")
		write(t, path, contents, 0o644)
		if _, err := Install(VSCode, path, oauth); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: err = %v", name, err)
		}
		if read(t, path) != contents {
			t.Errorf("%s: the file was changed", name)
		}
	}
}

// TestTokenEntries reads the token from the environment, or from a VS Code
// prompt, so no file holds it.
func TestTokenEntries(t *testing.T) {
	cases := map[Client]map[string]any{
		Cursor:     {"url": token.URL, "headers": map[string]any{"Authorization": "Bearer ${env:PREFACTOR_MCP_TOKEN}"}},
		ClaudeCode: {"type": "http", "url": token.URL, "headers": map[string]any{"Authorization": "Bearer ${PREFACTOR_MCP_TOKEN}"}},
		VSCode:     {"type": "http", "url": token.URL, "headers": map[string]any{"Authorization": "Bearer ${input:prefactor-mcp-token}"}},
	}
	for client, want := range cases {
		path := filepath.Join(t.TempDir(), "mcp.json")
		if _, err := Install(client, path, token); err != nil {
			t.Fatal(err)
		}
		got := decodeJSON(t, read(t, path))
		key := "mcpServers"
		if client == VSCode {
			key = "servers"
		}
		if entry := got[key].(map[string]any)[ServerName]; !reflect.DeepEqual(entry, want) {
			t.Errorf("%s: %#v, want %#v", client, entry, want)
		}
		if client == VSCode {
			inputs := got["inputs"].([]any)
			if len(inputs) != 1 || inputs[0].(map[string]any)["id"] != "prefactor-mcp-token" || inputs[0].(map[string]any)["password"] != true {
				t.Errorf("inputs = %#v", inputs)
			}
			// Installing again keeps one input; uninstalling removes it.
			if res, err := Install(client, path, token); err != nil || res.Changed {
				t.Errorf("second install: %+v, %v", res, err)
			}
			// Switching to OAuth removes the input with the header.
			if _, err := Install(client, path, oauth); err != nil {
				t.Fatal(err)
			}
			if got := decodeJSON(t, read(t, path)); len(got["inputs"].([]any)) != 0 {
				t.Errorf("input left after switching to OAuth: %#v", got["inputs"])
			}
			if _, err := Install(client, path, token); err != nil {
				t.Fatal(err)
			}
			if _, err := Uninstall(client, path); err != nil {
				t.Fatal(err)
			}
			if got := decodeJSON(t, read(t, path)); len(got["inputs"].([]any)) != 0 {
				t.Errorf("input left after uninstall: %#v", got["inputs"])
			}
		}
	}
}

const codexBefore = `# My Codex settings
model = "gpt-5"

[mcp_servers.linear]
url = "https://mcp.linear.app/mcp"

[mcp_servers.prefactor]
command = "old"
args = ["--stdio"]

[mcp_servers.prefactor.env]
FOO = "bar"

[mcp_servers.semble]
command = "uvx"
args = ["""
[not a table]
"""]

[profiles.fast]
model = "o4-mini"
`

func decodeTOML(t *testing.T, raw string) map[string]any {
	t.Helper()
	var v map[string]any
	if _, err := toml.Decode(raw, &v); err != nil {
		t.Fatalf("%v:\n%s", err, raw)
	}
	return v
}

func TestCodexMergesWithoutClobbering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, codexBefore, 0o600)

	res, err := Install(Codex, path, oauth)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || read(t, res.Backup) != codexBefore {
		t.Fatalf("result %+v", res)
	}
	after := read(t, path)
	want := `# My Codex settings
model = "gpt-5"

[mcp_servers.linear]
url = "https://mcp.linear.app/mcp"

[mcp_servers.prefactor]
url = "https://app.example.com/mcp"

[mcp_servers.semble]
command = "uvx"
args = ["""
[not a table]
"""]

[profiles.fast]
model = "o4-mini"
`
	if after != want {
		t.Fatalf("file:\n%s\nwant:\n%s", after, want)
	}
	if again, err := Install(Codex, path, oauth); err != nil || again.Changed {
		t.Fatalf("second install: %+v, %v", again, err)
	}

	un, err := Uninstall(Codex, path)
	if err != nil || !un.Changed {
		t.Fatalf("uninstall: %+v, %v", un, err)
	}
	left := decodeTOML(t, read(t, path))
	servers := left["mcp_servers"].(map[string]any)
	if _, ok := servers["prefactor"]; ok || servers["linear"] == nil || servers["semble"] == nil || left["model"] != "gpt-5" {
		t.Fatalf("after uninstall:\n%s", read(t, path))
	}
}

func TestCodexAppendsToAFileWithoutTheEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, "model = \"gpt-5\"", 0o644)
	if _, err := Install(Codex, path, token); err != nil {
		t.Fatal(err)
	}
	want := "model = \"gpt-5\"\n\n[mcp_servers.prefactor]\nurl = \"https://app.example.com/mcp\"\nbearer_token_env_var = \"PREFACTOR_MCP_TOKEN\"\n"
	if got := read(t, path); got != want {
		t.Fatalf("file:\n%q\nwant:\n%q", got, want)
	}

	fresh := filepath.Join(t.TempDir(), ".codex", "config.toml")
	odd := `https://example.com/a"b\c/mcp`
	if _, err := Install(Codex, fresh, Entry{URL: odd}); err != nil {
		t.Fatal(err)
	}
	got := decodeTOML(t, read(t, fresh))
	entry := got["mcp_servers"].(map[string]any)["prefactor"].(map[string]any)
	if entry["url"] != odd {
		t.Fatalf("url = %q", entry["url"])
	}
}

func TestCodexRefusesEntriesItCannotReplace(t *testing.T) {
	for name, contents := range map[string]string{
		"inline table": "[mcp_servers]\nprefactor = { command = \"x\" }\n",
		"dotted key":   "mcp_servers.prefactor.command = \"x\"\n",
		"invalid TOML": "model = \n",
	} {
		path := filepath.Join(t.TempDir(), "config.toml")
		write(t, path, contents, 0o644)
		if _, err := Install(Codex, path, oauth); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: err = %v", name, err)
		}
		if read(t, path) != contents {
			t.Errorf("%s: the file was changed", name)
		}
	}
}

func TestSnippets(t *testing.T) {
	got, err := Snippet(Codex, oauth)
	if err != nil {
		t.Fatal(err)
	}
	if want := "[mcp_servers.prefactor]\nurl = \"https://app.example.com/mcp\"\n"; got != want {
		t.Fatalf("codex snippet:\n%s", got)
	}
	got, err = Snippet(VSCode, oauth)
	if err != nil {
		t.Fatal(err)
	}
	if v := decodeJSON(t, got); v["inputs"] != nil {
		t.Fatalf("vscode OAuth snippet has inputs:\n%s", got)
	}
	got, err = Snippet(VSCode, token)
	if err != nil {
		t.Fatal(err)
	}
	v := decodeJSON(t, got)
	if v["servers"].(map[string]any)[ServerName] == nil || len(v["inputs"].([]any)) != 1 {
		t.Fatalf("vscode snippet:\n%s", got)
	}
	if _, err := Snippet(Client("emacs"), oauth); err == nil {
		t.Fatal("unknown client accepted")
	}
	if _, err := Snippet(Codex, Entry{}); err == nil {
		t.Fatal("an entry without a URL was accepted")
	}
}

// TestCodexKeepsCommentsOfTheNextTable keeps a comment that introduces the
// table after the prefactor entry, and leaves no blank lines at the end
// when the entry was last.
func TestCodexKeepsCommentsOfTheNextTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, "[mcp_servers.prefactor]\ncommand = \"old\"\n\n# Semble searches code\n[mcp_servers.semble]\ncommand = \"uvx\"\n\n[mcp_servers.prefactor.env]\nA = \"b\"\n\n", 0o644)
	if _, err := Uninstall(Codex, path); err != nil {
		t.Fatal(err)
	}
	if got, want := read(t, path), "# Semble searches code\n[mcp_servers.semble]\ncommand = \"uvx\"\n"; got != want {
		t.Fatalf("file:\n%q\nwant:\n%q", got, want)
	}
	// Nothing to remove leaves the file as it is.
	if res, err := Uninstall(Codex, path); err != nil || res.Changed {
		t.Fatalf("%+v, %v", res, err)
	}
}

// TestReplacesAndRemovesOldStdioEntries handles the entries the old stdio
// install wrote: install replaces them with the remote entry, and uninstall
// removes them, leaving the other servers.
func TestReplacesAndRemovesOldStdioEntries(t *testing.T) {
	cases := []struct {
		client Client
		file   string
		before string
	}{
		{Cursor, "mcp.json", oldCursor},
		{ClaudeCode, ".claude.json", oldClaude},
		{VSCode, "mcp.json", oldVSCode},
		{Codex, "config.toml", oldCodex},
	}
	for _, c := range cases {
		t.Run(string(c.client), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), c.file)
			write(t, path, c.before, 0o644)
			res, err := Uninstall(c.client, path)
			if err != nil || !res.Changed || read(t, res.Backup) != c.before {
				t.Fatalf("uninstall: %+v, %v", res, err)
			}
			after := read(t, path)
			if strings.Contains(after, "prefactor") || !strings.Contains(after, "other") {
				t.Fatalf("after uninstall:\n%s", after)
			}

			write(t, path, c.before, 0o644)
			if _, err := Install(c.client, path, oauth); err != nil {
				t.Fatal(err)
			}
			after = read(t, path)
			if strings.Contains(after, "command = \"/opt") || strings.Contains(after, `"/opt/prefactor"`) || strings.Contains(after, "stdio\", \"command\": \"/opt") {
				t.Fatalf("old entry left after install:\n%s", after)
			}
			if !strings.Contains(after, oauth.URL) || !strings.Contains(after, "other") {
				t.Fatalf("after install:\n%s", after)
			}
		})
	}
}
