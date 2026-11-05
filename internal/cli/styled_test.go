package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// styledApp is a test app whose output is a colour terminal.
func styledApp(t *testing.T) *testApp {
	t.Helper()
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	ta := newTestApp(t)
	ta.Terminal = func(io.Writer) bool { return true }
	return ta
}

func TestPeopleAtATerminalGetTables(t *testing.T) {
	ta := styledApp(t)
	server := newRecordingServer(t, `{"status":"success","summaries":[{"type":"agent","id":"a1","name":"Support bot","status":"active"}],"pagination":{"item_count":1,"item_start":1,"item_end":1}}`)
	ta.addProfile(t, "default", "tok", server.URL)

	out, err := ta.run(t, "agents", "list")
	if err != nil {
		t.Fatal(err)
	}
	plain := ansi.Strip(out)
	if !strings.HasPrefix(plain, " NAME") || !strings.Contains(plain, "Support bot") || !strings.Contains(plain, "Showing 1–1 of 1") {
		t.Fatalf("out:\n%s", plain)
	}

	// --json, --fields and a coding agent get JSON.
	for name, setup := range map[string]func() []string{
		"--json":     func() []string { return []string{"--json"} },
		"--fields":   func() []string { return []string{"--fields", "summaries.id"} },
		"CLAUDECODE": func() []string { ta.env["CLAUDECODE"] = "1"; return nil },
	} {
		out, err := ta.run(t, append([]string{"agents", "list"}, setup()...)...)
		if err != nil || !strings.HasPrefix(out, "{") {
			t.Errorf("%s: out = %q, %v", name, out, err)
		}
	}
}

func TestStyledRootHelpListsEveryGroup(t *testing.T) {
	ta := styledApp(t)
	out, err := ta.run(t, "--help")
	if code, ok := ExitCode(err); err != nil && (!ok || code != 0) {
		t.Fatal(err)
	}
	plain := ansi.Strip(out)
	for _, want := range []string{"██████╗", "$ prefactor agents list | retrieve | show", "$ prefactor setup [agent_id]", "Environment Variables:", "PREFACTOR_API_TOKEN", "try: prefactor setup"} {
		if !strings.Contains(plain, want) {
			t.Errorf("root help is missing %q:\n%s", want, plain)
		}
	}
}

func TestStyledSubcommandHelpKeepsItsText(t *testing.T) {
	plain, _ := newTestApp(t).run(t, "agents", "create", "--help")
	styled, _ := styledApp(t).run(t, "agents", "create", "--help")
	if styled == plain || ansi.Strip(styled) != plain {
		t.Fatalf("styled help differs from the plain text:\n%s\n---\n%s", ansi.Strip(styled), plain)
	}
}
