//go:build parity

// Package parity runs the same commands through the TypeScript CLI and this
// CLI against a recording server and compares what each sends and prints.
//
// Run with:
//
//	PARITY_TS_CLI="bun ~/code/typescript-sdk/packages/cli/src/bin/cli.ts" go test -tags parity ./test/parity/
//
// Each line in cases/*.txt is one case written with the TypeScript CLI's
// names: "<command words> | <arguments>". The command words and flag names
// are converted to kebab case for this CLI; argument values are not. A line
// starting with "help:" prints help: its text is compared in kebab case and
// without this CLI's "Global Options:" section, except for the root's help,
// which lists options and commands only one CLI has, so only which streams
// have output is compared.
package parity

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/prefactordev/cli/internal/lifecycle"
)

// responseBody includes a float written as 1.0, an escaped non-ASCII
// character and U+2028, which JSON.stringify prints differently from the raw
// text, so every case checks the CLI re-encodes JSON as the TypeScript CLI
// does.
const responseBody = `{"status":"success","details":{"id":"01m6jcdypz3tq23qmke2ckg5jpt7k0fg","content":"document body\n","score":1.0,"label":"caf\u00e9 \u2028"},"summaries":[]}`

var goBinary string

// TestMain builds this CLI with the TypeScript CLI's version, so version
// output compares too.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "parity-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	version := "0.0.0"
	if tsCLI := tsCommand(); tsCLI != nil {
		out, err := exec.Command(tsCLI[0], append(tsCLI[1:], "--version")...).Output()
		if err != nil {
			fmt.Fprintln(os.Stderr, "TypeScript CLI --version:", err)
			os.Exit(1)
		}
		version = strings.TrimSpace(string(out))
	}

	goBinary = filepath.Join(dir, "prefactor")
	build := exec.Command("go", "build", "-ldflags", "-X main.version="+version, "-o", goBinary, "../../cmd/prefactor")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type recordedRequest struct {
	Method string
	Path   string
	Query  url.Values
	Body   any
	Auth   string
}

type recorder struct {
	mu       sync.Mutex
	requests []recordedRequest
	// routes, when set, answers "METHOD path" with its body instead of
	// responseBody.
	routes map[string]string
}

func (r *recorder) handler(w http.ResponseWriter, req *http.Request) {
	raw, _ := io.ReadAll(req.Body)
	var body any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			body = string(raw)
		}
	}

	r.mu.Lock()
	r.requests = append(r.requests, recordedRequest{
		Method: req.Method,
		Path:   req.URL.Path,
		Query:  req.URL.Query(),
		Body:   body,
		Auth:   req.Header.Get("Authorization"),
	})
	r.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if body, ok := r.routes[req.Method+" "+req.URL.Path]; ok {
		_, _ = io.WriteString(w, body)
		return
	}
	_, _ = io.WriteString(w, responseBody)
}

func (r *recorder) take() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	taken := r.requests
	r.requests = nil
	return taken
}

type result struct {
	stdout, stderr string
	exit           int
	requests       []recordedRequest
}

type parityCase struct {
	file  string
	line  int
	help  bool
	words []string
	args  []string
}

func TestParity(t *testing.T) {
	tsCommand := tsCommand()
	if tsCommand == nil {
		t.Skip("PARITY_TS_CLI is not set")
	}

	cases := loadCases(t)
	if len(cases) == 0 {
		t.Fatal("no cases found")
	}

	rec := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer server.Close()

	for _, tc := range cases {
		name := fmt.Sprintf("%s:%d %s %s", tc.file, tc.line, strings.Join(tc.words, " "), strings.Join(tc.args, " "))
		t.Run(name, func(t *testing.T) {
			tsArgs := append(append([]string{}, tc.words...), tc.args...)
			goArgs := append(kebabWords(tc.words), kebabFlags(tc.args)...)

			ts := run(t, rec, server.URL, append(append([]string{}, tsCommand...), tsArgs...))
			got := run(t, rec, server.URL, append([]string{goBinary}, goArgs...))

			if ts.exit != got.exit {
				t.Errorf("exit: ts=%d go=%d\nts stderr: %s\ngo stderr: %s", ts.exit, got.exit, ts.stderr, got.stderr)
			}
			if !reflect.DeepEqual(normalizeGenerated(asP2Accepts(ts.requests)), normalizeGenerated(got.requests)) {
				t.Errorf("requests differ\nts: %s\ngo: %s", dump(ts.requests), dump(got.requests))
			}
			if tc.help {
				compareHelp(t, ts, got)
				return
			}
			if ts.stdout != got.stdout {
				t.Errorf("stdout differs\nts:\n%s\ngo:\n%s", ts.stdout, got.stdout)
			}
			if want := kebabMessage(ts.stderr); want != got.stderr {
				t.Errorf("stderr differs\nts (kebab):\n%s\ngo:\n%s", want, got.stderr)
			}
		})
	}
}

// tsCommand returns the TypeScript CLI command from PARITY_TS_CLI, or nil.
func tsCommand() []string {
	tsCLI := os.Getenv("PARITY_TS_CLI")
	if tsCLI == "" {
		return nil
	}
	return strings.Fields(os.ExpandEnv(strings.ReplaceAll(tsCLI, "~", "$HOME")))
}

func run(t *testing.T, rec *recorder, serverURL string, argv []string) result {
	t.Helper()
	return runWithEnv(t, rec, serverURL, argv, nil)
}

// runWithEnv is run with extra environment variables.
func runWithEnv(t *testing.T, rec *recorder, serverURL string, argv, env []string) result {
	t.Helper()

	work := t.TempDir()
	if err := os.Mkdir(filepath.Join(work, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	profile := fmt.Sprintf(`{"default":{"api_key":"tok","base_url":%q}}`, serverURL)
	if err := os.WriteFile(filepath.Join(work, "prefactor.json"), []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtures, _ := filepath.Abs("fixtures")
	if err := os.Symlink(fixtures, filepath.Join(work, "fixtures")); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = work
	cmd.Env = append(append(filteredEnv(), "PREFACTOR_CLI_SELF_PATH="+filepath.Join(work, "bin", "prefactor")), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Stdin = strings.NewReader("")

	exit := 0
	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %v: %v", argv, err)
		}
		exit = exitErr.ExitCode()
	}

	return result{
		stdout:   strings.ReplaceAll(stdout.String(), work, "<work>"),
		stderr:   strings.ReplaceAll(stderr.String(), work, "<work>"),
		exit:     exit,
		requests: rec.take(),
	}
}

// filteredEnv drops Prefactor variables so cases control them.
func filteredEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PREFACTOR_") {
			env = append(env, kv)
		}
	}
	return env
}

func loadCases(t *testing.T) []parityCase {
	t.Helper()

	files, err := filepath.Glob("cases/*.txt")
	if err != nil {
		t.Fatal(err)
	}

	var cases []parityCase
	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			t.Fatal(err)
		}

		scanner := bufio.NewScanner(f)
		line := 0
		for scanner.Scan() {
			line++
			text := strings.TrimSpace(scanner.Text())
			if text == "" || strings.HasPrefix(text, "#") {
				continue
			}

			tc := parityCase{file: filepath.Base(file), line: line}
			if rest, ok := strings.CutPrefix(text, "help:"); ok {
				tc.help = true
				text = strings.TrimSpace(rest)
			}

			words, args, _ := strings.Cut(text, "|")
			tc.words = strings.Fields(words)
			parsed, err := splitArgs(args)
			if err != nil {
				t.Fatalf("%s:%d: %v", file, line, err)
			}
			tc.args = parsed
			cases = append(cases, tc)
		}
		_ = f.Close()
	}

	return cases
}

// splitArgs splits a line into arguments, honouring single and double quotes.
func splitArgs(s string) ([]string, error) {
	var args []string
	var current strings.Builder
	inArg := false
	var quote rune

	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inArg = true
		case r == ' ' || r == '\t':
			if inArg {
				args = append(args, current.String())
				current.Reset()
				inArg = false
			}
		default:
			current.WriteRune(r)
			inArg = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	if inArg {
		args = append(args, current.String())
	}

	return args, nil
}

func kebabWords(words []string) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = strings.ReplaceAll(w, "_", "-")
	}
	return out
}

var flagName = regexp.MustCompile(`^--[a-z0-9_]+`)

func kebabFlags(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = flagName.ReplaceAllStringFunc(a, func(f string) string { return strings.ReplaceAll(f, "_", "-") })
	}
	return out
}

// kebabMessage converts option names and their placeholders
// (--agent_id <agent_id>) in TypeScript CLI messages to kebab case, but not
// argument names such as <agent_id>, which both CLIs write in snake case.
func kebabMessage(s string) string {
	return helpOption.ReplaceAllStringFunc(s, func(f string) string { return strings.ReplaceAll(f, "_", "-") })
}

func dump(requests []recordedRequest) string {
	raw, _ := json.MarshalIndent(requests, "", "  ")
	return string(raw)
}

// asP2Accepts rewrites the TypeScript CLI's requests where this CLI
// deliberately sends the form p2 accepts instead (see the README):
// sorting=name or sorting=-name becomes sorting[field] and
// sorting[direction], and repeated agent_ids / agent_instance_ids become
// agent_ids[] / agent_instance_ids[]. Everything else must match exactly.
func asP2Accepts(requests []recordedRequest) []recordedRequest {
	out := make([]recordedRequest, len(requests))
	for i, r := range requests {
		query := url.Values{}
		for key, values := range r.Query {
			switch key {
			case "sorting":
				field, direction := values[0], "asc"
				if rest, ok := strings.CutPrefix(field, "-"); ok {
					field, direction = rest, "desc"
				}
				query["sorting[field]"] = []string{field}
				query["sorting[direction]"] = []string{direction}
			case "agent_ids", "agent_instance_ids", "statuses":
				query[key+"[]"] = values
			default:
				query[key] = values
			}
		}
		r.Query = query
		out[i] = r
	}
	return out
}

// compareHelp compares help output; see the package comment.
func compareHelp(t *testing.T, ts, got result) {
	t.Helper()
	if strings.HasPrefix(ts.stdout+ts.stderr, "Usage: prefactor [options] [command]") {
		if (ts.stdout == "") != (got.stdout == "") || (ts.stderr == "") != (got.stderr == "") {
			t.Errorf("help went to different streams\nts: stdout %d bytes, stderr %d bytes\ngo: stdout %d bytes, stderr %d bytes",
				len(ts.stdout), len(ts.stderr), len(got.stdout), len(got.stderr))
		}
		return
	}
	if want, have := sameHelpItems(kebabHelp(t, ts.stdout), withoutGlobalOptions(got.stdout)); want != have {
		t.Errorf("help on stdout differs\nts (kebab):\n%s\ngo:\n%s", want, have)
	}
	if want, have := sameHelpItems(kebabHelp(t, ts.stderr), withoutGlobalOptions(got.stderr)); want != have {
		t.Errorf("help on stderr differs\nts (kebab):\n%s\ngo:\n%s", want, have)
	}
}

// helpLine is one line of help, or one option or command with its wrapped
// description.
type helpLine struct {
	text string
	// name is the option's long name or the command's name, for items.
	name string
}

var helpItemName = regexp.MustCompile(`^  (?:-\S, )?(--\S+|\S+)`)

// helpLines splits help into lines, joining each item's lines.
func helpLines(help string) []helpLine {
	var lines []helpLine
	for _, line := range strings.Split(help, "\n") {
		switch {
		case strings.HasPrefix(line, "   ") && len(lines) > 0 && lines[len(lines)-1].name != "":
			lines[len(lines)-1].text += "\n" + line
		case helpItemName.MatchString(line):
			lines = append(lines, helpLine{text: line, name: helpItemName.FindStringSubmatch(line)[1]})
		default:
			lines = append(lines, helpLine{text: line})
		}
	}
	return lines
}

// sameHelpItems returns the two helps to compare. Options and commands this
// CLI adds from the API, which the TypeScript CLI lacks, are dropped from
// got. Since they widen the term column and add [options] to commands that
// had none, when there are any the rest is compared with whitespace
// collapsed and without [options] in command terms.
func sameHelpItems(want, got string) (string, string) {
	tsItems := map[string]string{}
	for _, line := range helpLines(want) {
		tsItems[line.name] = line.text
	}
	var kept []string
	added := false
	for _, line := range helpLines(got) {
		tsText, inTS := tsItems[line.name]
		switch {
		case line.name == "":
		case !inTS:
			added = true
			continue
		case !strings.HasPrefix(line.name, "-") && strings.Contains(line.text, " [options]") && !strings.Contains(tsText, " [options]"):
			added = true
		}
		kept = append(kept, line.text)
	}
	if !added {
		return want, got
	}

	loose := func(help string) string {
		var out []string
		for _, line := range helpLines(help) {
			text := strings.Join(strings.Fields(line.text), " ")
			if line.name != "" && !strings.HasPrefix(line.name, "-") {
				text = strings.Replace(text, " [options]", "", 1)
			}
			out = append(out, text)
		}
		return strings.Join(out, "\n")
	}
	return loose(want), loose(strings.Join(kept, "\n"))
}

// typescriptCommands reads the TypeScript CLI's command paths.
func typescriptCommands(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../../internal/cli/testdata/typescript-commands.json")
	if err != nil {
		t.Fatal(err)
	}
	var commands []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &commands); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(commands))
	for i, c := range commands {
		paths[i] = c.Path
	}
	return paths
}

var (
	commandWordsOnce sync.Once
	commandWords     *regexp.Regexp
)

// kebabHelp converts the TypeScript CLI's help to kebab case: option names,
// their placeholders and command names, but not argument names or choices
// such as "on_demand".
func kebabHelp(t *testing.T, help string) string {
	t.Helper()
	commandWordsOnce.Do(func() {
		seen := map[string]bool{}
		var words []string
		for _, path := range typescriptCommands(t) {
			for _, word := range strings.Fields(path) {
				if strings.Contains(word, "_") && !seen[word] {
					seen[word] = true
					words = append(words, regexp.QuoteMeta(word))
				}
			}
		}
		commandWords = regexp.MustCompile(`\b(` + strings.Join(words, "|") + `)\b`)
	})
	kebab := func(w string) string { return strings.ReplaceAll(w, "_", "-") }
	return commandWords.ReplaceAllStringFunc(helpOption.ReplaceAllStringFunc(help, kebab), kebab)
}

// helpOption matches an option name and its placeholder, but not argument
// names, which both CLIs write in snake case.
var helpOption = regexp.MustCompile(`--[a-z0-9_]+(=| <[a-z0-9_]+>)?`)

// withoutGlobalOptions drops the "Global Options:" section this CLI adds at
// the end of a subcommand's help.
func withoutGlobalOptions(help string) string {
	if before, _, found := strings.Cut(help, "\nGlobal Options:\n"); found {
		return before
	}
	return help
}

// laterCommands are TypeScript commands this CLI adds later or leaves out.
var laterCommands = map[string]bool{"playground": true}

// TestHelpMatchesTypeScript compares every command's --help with the
// TypeScript CLI's.
func TestHelpMatchesTypeScript(t *testing.T) {
	tsCLI := tsCommand()
	if tsCLI == nil {
		t.Skip("PARITY_TS_CLI is not set")
	}

	for _, path := range typescriptCommands(t) {
		words := strings.Fields(path)
		if len(words) == 0 || laterCommands[words[0]] {
			continue
		}
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			ts := run(t, &recorder{}, "http://127.0.0.1:1", append(append(append([]string{}, tsCLI...), words...), "--help"))
			got := run(t, &recorder{}, "http://127.0.0.1:1", append(append([]string{goBinary}, kebabWords(words)...), "--help"))
			if ts.exit != got.exit {
				t.Errorf("exit: ts=%d go=%d", ts.exit, got.exit)
			}
			compareHelp(t, ts, got)
		})
	}
}

// TestParitySetup runs setup through both CLIs against a server that answers
// as p2 does, so the whole run is compared: every request with its token,
// the setup values and the errors.
func TestParitySetup(t *testing.T) {
	tsCLI := tsCommand()
	if tsCLI == nil {
		t.Skip("PARITY_TS_CLI is not set")
	}

	base := map[string]string{
		"GET /api/v1/agent/a1":         `{"status":"success","details":{"id":"a1"}}`,
		"POST /api/v1/agent":           `{"status":"success","details":{"id":"a1"}}`,
		"GET /api/v1/agent_deployment": `{"status":"success","summaries":[{"environment_id":"env1"}]}`,
		"POST /api/v1/api_token":       `{"status":"success","details":{"id":"t1","token_scope":"agent_deployment"},"token":"deploy-token"}`,
		"GET /api/v1/ping":             `{"status":"success","details":{"token_type":"api_session_agent_deployment_scope","agent_id":"a1"}}`,
	}
	cases := []struct {
		name   string
		args   []string
		routes map[string]string
	}{
		{name: "one deployment", args: []string{"setup", "a1"}},
		{name: "json", args: []string{"setup", "a1", "--json"}},
		{name: "create", args: []string{"setup", "--create", "--name", "Bot", "--description", "Does things"}},
		{name: "current version", args: []string{"setup", "a1"}, routes: map[string]string{
			"GET /api/v1/agent_deployment": `{"summaries":[{"environment_id":"env1","current_version_id":null},{"environment_id":"env2","current_version_id":"v1"}]}`,
		}},
		{name: "ambiguous deployments", args: []string{"setup", "a1"}, routes: map[string]string{
			"GET /api/v1/agent_deployment": `{"summaries":[{"environment_id":"env1"},{"environment_id":"env2"}]}`,
		}},
		{name: "first environment", args: []string{"setup", "a1"}, routes: map[string]string{
			"GET /api/v1/agent_deployment": `{"summaries":[]}`,
			"GET /api/v1/account":          `{"summaries":[{"id":"acc1"},{"id":"acc2"}]}`,
			"GET /api/v1/environment":      `{"details":[{"id":"env3"},{"id":"env4"}]}`,
		}},
		{name: "account without id", args: []string{"setup", "a1"}, routes: map[string]string{
			"GET /api/v1/agent_deployment": `{"summaries":[]}`,
			"GET /api/v1/account":          `{"summaries":[{"name":"x"}]}`,
		}},
		{name: "deployment without environment", args: []string{"setup", "a1"}, routes: map[string]string{
			"GET /api/v1/agent_deployment": `{"summaries":[{"environment_id":null}]}`,
		}},
		{name: "ping status", args: []string{"setup", "a1"}, routes: map[string]string{
			"GET /api/v1/ping": `{"status":"error","details":{}}`,
		}},
		{name: "ping token type", args: []string{"setup", "a1"}, routes: map[string]string{
			"GET /api/v1/ping": `{"status":"success","details":{"token_type":"api_session_account_scope","agent_id":"a1"}}`,
		}},
		{name: "ping agent", args: []string{"setup", "a1"}, routes: map[string]string{
			"GET /api/v1/ping": `{"status":"success","details":{"token_type":"api_session_agent_deployment_scope"}}`,
		}},
		{name: "agent not found", args: []string{"setup", "a1"}, routes: map[string]string{
			"GET /api/v1/agent/a1": `{"status":"error","code":"not_found"}`,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			routes := map[string]string{}
			for route, body := range base {
				routes[route] = body
			}
			for route, body := range tc.routes {
				routes[route] = body
			}
			rec := &recorder{routes: routes}
			server := httptest.NewServer(http.HandlerFunc(rec.handler))
			defer server.Close()

			ts := run(t, rec, server.URL, append(append([]string{}, tsCLI...), tc.args...))
			got := run(t, rec, server.URL, append([]string{goBinary}, tc.args...))
			if ts.exit != got.exit {
				t.Errorf("exit: ts=%d go=%d\nts stderr: %s\ngo stderr: %s", ts.exit, got.exit, ts.stderr, got.stderr)
			}
			if !reflect.DeepEqual(normalizeGenerated(ts.requests), normalizeGenerated(got.requests)) {
				t.Errorf("requests differ\nts: %s\ngo: %s", dump(ts.requests), dump(got.requests))
			}
			if want := strings.ReplaceAll(ts.stdout, server.URL, "<server>"); want != strings.ReplaceAll(got.stdout, server.URL, "<server>") {
				t.Errorf("stdout differs\nts:\n%s\ngo:\n%s", ts.stdout, got.stdout)
			}
			if ts.stderr != got.stderr {
				t.Errorf("stderr differs\nts:\n%s\ngo:\n%s", ts.stderr, got.stderr)
			}
		})
	}
}

// TestParityLifecycle runs install, update, uninstall and doctor through
// both CLIs, each with its own home directory, against a release server
// whose archive holds this CLI. The TypeScript CLI's update therefore
// installs this CLI as the TypeScript installs will once they move over.
func TestParityLifecycle(t *testing.T) {
	tsCLI := tsCommand()
	if tsCLI == nil {
		t.Skip("PARITY_TS_CLI is not set")
	}

	platform, err := lifecycle.DetectPlatform(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	asset := lifecycle.AssetName(platform)
	archive := releaseArchive(t, goBinary)
	sum := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest/download/" + asset:
			http.Redirect(w, r, "/releases/download/v9.9.9/"+asset, http.StatusFound)
		case "/releases/download/v9.9.9/" + asset:
			_, _ = w.Write(archive)
		case "/releases/download/v9.9.9/SHA256SUMS":
			_, _ = fmt.Fprintf(w, "%x  %s\n", sum, asset)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source := filepath.Join(t.TempDir(), "prefactor-source")
	if err := os.WriteFile(source, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	steps := [][]string{
		{"doctor"},
		{"update"},
		{"uninstall"},
		{"install", "--source-binary", source, "--resolved-tag", "v0.1.0", "--asset-name", "custom.tar.gz", "--channel", "latest"},
		{"doctor"},
		{"install", "--source-binary", source, "--wait-for-pid", "abc"},
		{"install", "--source-binary", source, "--wait-for-pid", "0"},
		{"install", "--channel", "nightly"},
		{"install"},
		{"update", "--channel", "stable"},
		{"doctor"},
		{"uninstall"},
		{"doctor"},
	}

	tsHome, goHome := t.TempDir(), t.TempDir()
	env := func(home string) []string {
		return []string{"HOME=" + home, "PREFACTOR_RELEASE_BASE_URL=" + server.URL + "/releases/download"}
	}
	installedAt := regexp.MustCompile(`installedAt: \S+`)
	normalize := func(s, home string) string {
		return installedAt.ReplaceAllString(strings.ReplaceAll(s, home, "<home>"), "installedAt: <time>")
	}
	rec := &recorder{}
	for _, step := range steps {
		ts := runWithEnv(t, rec, server.URL, append(append([]string{}, tsCLI...), step...), env(tsHome))
		got := runWithEnv(t, rec, server.URL, append([]string{goBinary}, step...), env(goHome))
		name := strings.Join(step, " ")
		if ts.exit != got.exit {
			t.Errorf("%s: exit ts=%d go=%d\nts stderr: %s\ngo stderr: %s", name, ts.exit, got.exit, ts.stderr, got.stderr)
		}
		if want, have := normalize(ts.stdout, tsHome), normalize(got.stdout, goHome); want != have {
			t.Errorf("%s: stdout differs\nts:\n%s\ngo:\n%s", name, want, have)
		}
		if want, have := normalize(kebabMessage(ts.stderr), tsHome), normalize(got.stderr, goHome); want != have {
			t.Errorf("%s: stderr differs\nts:\n%s\ngo:\n%s", name, want, have)
		}
	}
	// A TypeScript installation is one this CLI can read and update.
	shared := t.TempDir()
	if r := runWithEnv(t, rec, server.URL, append(append([]string{}, tsCLI...), "install", "--source-binary", source, "--channel", "latest"), env(shared)); r.exit != 0 {
		t.Fatalf("TypeScript install: %s", r.stderr)
	}
	tsDoctor := runWithEnv(t, rec, server.URL, append(append([]string{}, tsCLI...), "doctor"), env(shared))
	goDoctor := runWithEnv(t, rec, server.URL, []string{goBinary, "doctor"}, env(shared))
	if tsDoctor.stdout != goDoctor.stdout {
		t.Errorf("doctor on a TypeScript install differs\nts:\n%s\ngo:\n%s", tsDoctor.stdout, goDoctor.stdout)
	}
	if r := runWithEnv(t, rec, server.URL, []string{goBinary, "update", "--channel", "stable"}, env(shared)); r.exit != 0 || !strings.Contains(r.stdout, "Updated Prefactor CLI to v9.9.9.") {
		t.Errorf("update of a TypeScript install: exit %d\n%s%s", r.exit, r.stdout, r.stderr)
	}
}

// releaseArchive packs binary as a release archive for this platform.
func releaseArchive(t *testing.T, binary string) []byte {
	t.Helper()
	contents, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "prefactor", Mode: 0o755, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
