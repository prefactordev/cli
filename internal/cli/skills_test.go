package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
