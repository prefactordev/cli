package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
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
	Description string `json:"description"`
	Options     []struct {
		Flags        string `json:"flags"`
		Long         string `json:"long"`
		Mandatory    bool   `json:"mandatory"`
		TakesValue   bool   `json:"takesValue"`
		Hidden       bool   `json:"hidden"`
		Description  string `json:"description"`
		DefaultValue any    `json:"defaultValue"`
	} `json:"options"`
}

// laterCommands are TypeScript commands that later issues add, or that the
// CLI leaves out on purpose.
var laterCommands = map[string]string{
	"playground": "excluded: seeds demo data",
}

// goOnlyCommands are commands the TypeScript CLI lacks that call no API
// operation.
var goOnlyCommands = map[string]string{
	"prefactor skills": "installs the skills built into the CLI",
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
// same option string in error messages. Commands and options the TypeScript
// CLI lacks must call an API operation or send one of its request fields.
func TestCommandTreeMatchesTypeScript(t *testing.T) {
	root := newRootCommand(&App{Version: "test"})
	seen := map[string]bool{}
	fromSpec := specOptions(t)

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
			if opt.Long == "--version" && !cmd.HasParent() {
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
			if f.Usage != opt.Description {
				t.Errorf("%s --%s: description %q, TypeScript %q", path, name, f.Usage, opt.Description)
			}
			if opt.DefaultValue != nil && f.DefValue != fmt.Sprint(opt.DefaultValue) {
				t.Errorf("%s --%s: default %q, TypeScript %v", path, name, f.DefValue, opt.DefaultValue)
			}
		}

		if cmd.Short != ts.Description {
			t.Errorf("%s: description %q, TypeScript %q", path, cmd.Short, ts.Description)
		}
		if cmd.HasParent() {
			var goOrder, tsOrder []string
			local := cmd.LocalNonPersistentFlags()
			local.SortFlags = false
			local.VisitAll(func(f *pflag.Flag) {
				if tsFlags[f.Name] {
					goOrder = append(goOrder, f.Name)
				}
			})
			for _, opt := range ts.Options {
				tsOrder = append(tsOrder, strings.TrimPrefix(kebabNames(opt.Long), "--"))
			}
			if !slices.Equal(goOrder, tsOrder) {
				t.Errorf("%s: options in order %v, TypeScript %v", path, goOrder, tsOrder)
			}
		}

		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if !tsFlags[f.Name] && !goOnlyFlags[f.Name] && !fromSpec[path][f.Name] && f.Name != "version" {
				t.Errorf("%s: option --%s is not in the TypeScript CLI", path, f.Name)
			}
		})
	}

	var walk func(cmd *cobra.Command) bool
	walk = func(cmd *cobra.Command) bool {
		if _, ok := goOnlyCommands[cmd.CommandPath()]; ok {
			return true
		}
		known := seen[cmd.CommandPath()]
		if _, ok := fromSpec[cmd.CommandPath()]; ok && !known {
			known = true
			cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
				if !goOnlyFlags[f.Name] && !fromSpec[cmd.CommandPath()][f.Name] {
					t.Errorf("%s: option --%s sends no field of the API operation", cmd.CommandPath(), f.Name)
				}
			})
		}
		for _, sub := range cmd.Commands() {
			if walk(sub) && sub.Name() != "help" {
				known = true
			}
		}
		if !known && cmd.Name() != "help" {
			t.Errorf("%s is not in the TypeScript CLI and calls no API operation", cmd.CommandPath())
		}
		return known
	}
	walk(root)
}

// TestEveryOptionParserIsImplemented fails when a TypeScript option this CLI
// has uses an argument parser optionParsers does not implement.
func TestEveryOptionParserIsImplemented(t *testing.T) {
	root := newRootCommand(&App{Version: "test"})
	for key, name := range commanderOptionParsers {
		path, flag, _ := strings.Cut(key, " --")
		cmd, rest, err := root.Find(strings.Fields(path)[1:])
		if err != nil || len(rest) != 0 || cmd.CommandPath() != path || cmd.Flags().Lookup(flag) == nil {
			continue
		}
		if optionParsers[name] == nil {
			t.Errorf("%s uses %s, which optionParsers does not implement", key, name)
		}
	}
}
