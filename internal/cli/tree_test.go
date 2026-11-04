package cli

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// typescriptCommand is one command from testdata/typescript-commands.json,
// dumped from the TypeScript CLI's Commander program.
type typescriptCommand struct {
	Path      string `json:"path"`
	Arguments []struct {
		Name     string `json:"name"`
		Required bool   `json:"required"`
		Variadic bool   `json:"variadic"`
	} `json:"arguments"`
	Options []struct {
		Flags      string `json:"flags"`
		Long       string `json:"long"`
		Mandatory  bool   `json:"mandatory"`
		TakesValue bool   `json:"takesValue"`
		Hidden     bool   `json:"hidden"`
	} `json:"options"`
}

// laterCommands are TypeScript commands that later issues add, or that the
// CLI leaves out on purpose.
var laterCommands = map[string]string{
	"setup":      "PRE-709",
	"install":    "PRE-712",
	"update":     "PRE-712",
	"uninstall":  "PRE-712",
	"doctor":     "PRE-712",
	"playground": "excluded: seeds demo data",
}

// goOnlyFlags are the session flags this CLI adds to every command.
var goOnlyFlags = map[string]bool{"json": true, "fields": true, "no-input": true, "yes": true, "help": true}

var snakeName = regexp.MustCompile(`(--|<)([a-z0-9]+(?:_[a-z0-9]+)+)`)

func kebabNames(s string) string {
	return snakeName.ReplaceAllStringFunc(s, func(m string) string { return strings.ReplaceAll(m, "_", "-") })
}

func loadTypescriptCommands(t *testing.T) []typescriptCommand {
	t.Helper()
	raw, err := os.ReadFile("testdata/typescript-commands.json")
	if err != nil {
		t.Fatal(err)
	}
	var commands []typescriptCommand
	if err := json.Unmarshal(raw, &commands); err != nil {
		t.Fatal(err)
	}
	return commands
}

// TestCommandTreeMatchesTypeScript checks every TypeScript command, argument
// and option exists here, in kebab case, with the same required-ness and the
// same option string in error messages.
func TestCommandTreeMatchesTypeScript(t *testing.T) {
	root := newRootCommand(&App{Version: "test"})
	seen := map[string]bool{}

	for _, ts := range loadTypescriptCommands(t) {
		words := strings.Fields(ts.Path)
		if len(words) > 0 {
			if _, later := laterCommands[words[0]]; later {
				continue
			}
		}

		kebab := make([]string, len(words))
		for i, w := range words {
			kebab[i] = strings.ReplaceAll(w, "_", "-")
		}
		path := strings.TrimSpace("prefactor " + strings.Join(kebab, " "))
		seen[path] = true

		cmd, rest, err := root.Find(kebab)
		if err != nil || len(rest) != 0 || cmd.CommandPath() != path {
			t.Errorf("%s: no such command", path)
			continue
		}

		declared := declaredArguments(cmd)
		if len(declared) != len(ts.Arguments) {
			t.Errorf("%s: %d arguments, TypeScript has %d", path, len(declared), len(ts.Arguments))
		} else {
			for i, arg := range ts.Arguments {
				if declared[i].name != arg.Name || declared[i].required != arg.Required || declared[i].variadic != arg.Variadic {
					t.Errorf("%s: argument %d is %+v, TypeScript has %+v", path, i, declared[i], arg)
				}
			}
		}

		tsFlags := map[string]bool{}
		for _, opt := range ts.Options {
			if opt.Long == "--version" {
				continue
			}
			name := strings.TrimPrefix(kebabNames(opt.Long), "--")
			tsFlags[name] = true

			f := cmd.Flags().Lookup(name)
			if f == nil {
				f = cmd.InheritedFlags().Lookup(name)
			}
			if f == nil {
				t.Errorf("%s: missing option --%s", path, name)
				continue
			}
			if takesValue(f) != opt.TakesValue {
				t.Errorf("%s --%s: takes a value = %v, TypeScript %v", path, name, takesValue(f), opt.TakesValue)
			}
			_, required := f.Annotations[requiredAnnotation]
			if required != opt.Mandatory {
				t.Errorf("%s --%s: required = %v, TypeScript %v", path, name, required, opt.Mandatory)
			}
			if f.Hidden != opt.Hidden {
				t.Errorf("%s --%s: hidden = %v, TypeScript %v", path, name, f.Hidden, opt.Hidden)
			}
			if got, want := optionFlags(cmd, f), kebabNames(opt.Flags); got != want {
				t.Errorf("%s --%s: option string %q, TypeScript %q", path, name, got, want)
			}
		}

		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if !tsFlags[f.Name] && !goOnlyFlags[f.Name] && f.Name != "version" {
				t.Errorf("%s: option --%s is not in the TypeScript CLI", path, f.Name)
			}
		})
	}

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Name() != "help" && !seen[cmd.CommandPath()] {
			t.Errorf("%s is not in the TypeScript CLI", cmd.CommandPath())
		}
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(root)
}
