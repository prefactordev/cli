package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/prefactordev/cli/skills"
)

// skillCommand matches a command line and the lines it continues onto.
var skillCommand = regexp.MustCompile(`(?m)^prefactor (?:[^\n]*\\\n)*[^\n]*`)

// TestSkillsUseCommandsThatExist checks every prefactor command in a code
// block of skills/*/SKILL.md names a command and options this CLI has, so
// the skills cannot drift from the CLI.
func TestSkillsUseCommandsThatExist(t *testing.T) {
	files, err := filepath.Glob("../../skills/*/SKILL.md")
	if err != nil || len(files) == 0 {
		t.Fatalf("no skills found: %v", err)
	}
	root := newRootCommand(&App{Version: "test"})

	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range skillCommand.FindAllString(string(raw), -1) {
			words := strings.Fields(strings.ReplaceAll(line, "\\\n", " "))[1:]
			var path []string
			var options []string
			for i := 0; i < len(words); i++ {
				switch word := words[i]; {
				case word == "--profile":
					// Its value is not a command word.
					i++
				case strings.HasPrefix(word, "--"):
					options = append(options, strings.TrimPrefix(word, "--"))
				case len(options) == 0 && !strings.HasPrefix(word, "<") && !strings.HasPrefix(word, `"`):
					path = append(path, word)
				}
			}

			cmd, rest, err := root.Find(path)
			if err != nil || len(rest) != 0 || cmd == root {
				t.Errorf("%s: %q is not a command", filepath.Base(filepath.Dir(file)), line)
				continue
			}
			for _, option := range options {
				if cmd.Flags().Lookup(option) == nil && cmd.InheritedFlags().Lookup(option) == nil {
					t.Errorf("%s: %q uses --%s, which %s does not have", filepath.Base(filepath.Dir(file)), line, option, cmd.CommandPath())
				}
			}
		}
	}
}

// TestSkillsInstallForTheAgentsFound installs into the project for the
// agents found in the home directory, replacing an earlier copy.
func TestSkillsInstallForTheAgentsFound(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(project)
	for _, dir := range []string{".claude", ".cursor"} {
		if err := os.Mkdir(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stale := filepath.Join(project, ".claude", "skills", "inspect-agent-run-with-prefactor-cli", "old.md")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := newTestApp(t).run(t, "skills", "install")
	if err != nil {
		t.Fatal(err)
	}
	names := "bootstrap-existing-agent-with-prefactor-cli, inspect-agent-run-with-prefactor-cli"
	want := "Installed " + names + " for Claude Code in " + filepath.Join(project, ".claude", "skills") + "\n" +
		"Installed " + names + " for Cursor in " + filepath.Join(project, ".agents", "skills") + "\n"
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
	for _, dir := range []string{".claude", ".agents"} {
		got, err := os.ReadFile(filepath.Join(project, dir, "skills", "bootstrap-existing-agent-with-prefactor-cli", "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		bundled, _ := fs.ReadFile(skills.FS, "bootstrap-existing-agent-with-prefactor-cli/SKILL.md")
		if string(got) != string(bundled) {
			t.Fatalf("%s: SKILL.md differs from the bundled skill", dir)
		}
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("the earlier copy was kept: %v", err)
	}
}

// TestSkillsInstallGlobalForChosenAgents installs into the home directory
// for the agents given, once per directory they share.
func TestSkillsInstallGlobalForChosenAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(t.TempDir())

	out, err := newTestApp(t).run(t, "skills", "install", "--global", "--agent", "amp", "--agent", "universal,codex", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Skills   []string       `json:"skills"`
		Installs []skillInstall `json:"installs"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	want := []skillInstall{
		{Path: filepath.Join(home, ".config", "agents", "skills"), Agents: []string{"Amp", "Other agents"}},
		{Path: filepath.Join(home, ".agents", "skills"), Agents: []string{"Codex"}},
	}
	if !reflect.DeepEqual(got.Installs, want) || len(got.Skills) != 2 {
		t.Fatalf("got %+v, want installs %+v", got, want)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "inspect-agent-run-with-prefactor-cli", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsInstallNeedsAnAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(t.TempDir())

	if _, err := newTestApp(t).run(t, "skills", "install"); err == nil || !strings.Contains(err.Error(), "No coding agent found") {
		t.Fatalf("err = %v", err)
	}
	if _, err := newTestApp(t).run(t, "skills", "install", "--agent", "emacs"); err == nil || !strings.Contains(err.Error(), "--agent must be one of: claude-code,") {
		t.Fatalf("err = %v", err)
	}
}
