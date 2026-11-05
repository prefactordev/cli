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
	"bufio"
	"bytes"
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
	cmd.Env = append(filteredEnv(), "PREFACTOR_CLI_SELF_PATH="+filepath.Join(work, "bin", "prefactor"))
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

var messageFlag = regexp.MustCompile(`(--|<)[a-z0-9]+(_[a-z0-9]+)+`)

// kebabMessage converts flag names and option placeholders (<agent_id>) in
// TypeScript CLI messages to kebab case.
func kebabMessage(s string) string {
	return messageFlag.ReplaceAllStringFunc(s, func(f string) string { return strings.ReplaceAll(f, "_", "-") })
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
var laterCommands = map[string]bool{"setup": true, "install": true, "update": true, "uninstall": true, "doctor": true, "playground": true}

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
