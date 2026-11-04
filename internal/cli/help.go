package cli

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"
)

// Help is laid out as Commander lays it out, so it reads like the
// TypeScript CLI's: usage, description, options, then commands, each item
// padded to one column and wrapped to the terminal width. Subcommands also
// list the root options under "Global Options:", at the end.

const (
	helpItemIndent    = 2
	helpSpacerWidth   = 2
	helpMinWrapWidth  = 40
	defaultHelpWidth  = 80
	helpOptionTerm    = "-h, --help"
	helpOptionSummary = "display help for command"
)

// helpItem is one term and its description in a help list.
type helpItem struct {
	term, description string
}

// writeHelp writes cmd's help to its output.
func writeHelp(cmd *cobra.Command) {
	out := cmd.OutOrStdout()
	_, _ = io.WriteString(out, formatHelp(cmd, helpWidth(out)))
}

// helpWidth is the width of the terminal out writes to, or 80, as
// Commander uses.
func helpWidth(out io.Writer) int {
	if file, ok := out.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		if width, _, err := term.GetSize(int(file.Fd())); err == nil && width > 0 {
			return width
		}
	}
	return defaultHelpWidth
}

func formatHelp(cmd *cobra.Command, width int) string {
	options := helpOptions(cmd)
	commands := helpCommands(cmd)
	termWidth := 0
	for _, item := range append(options, commands...) {
		termWidth = max(termWidth, utf8.RuneCountInString(item.term))
	}

	lines := []string{"Usage: " + commandUsage(cmd), ""}
	if cmd.Short != "" {
		lines = append(lines, boxWrap(cmd.Short, width), "")
	}
	lines = append(lines, formatItemList("Options:", options, termWidth, width)...)
	lines = append(lines, formatItemList("Commands:", commands, termWidth, width)...)

	if cmd.HasParent() {
		globals := globalOptions(cmd)
		globalWidth := 0
		for _, item := range globals {
			globalWidth = max(globalWidth, utf8.RuneCountInString(item.term))
		}
		lines = append(lines, formatItemList("Global Options:", globals, globalWidth, width)...)
	}

	help := strings.Join(lines, "\n")
	if cmd.Example != "" {
		help += "\nExamples:\n" + cmd.Example + "\n"
	}
	return help
}

// commandUsage is the command path followed by [options], [command] for a
// group, and the arguments.
func commandUsage(cmd *cobra.Command) string {
	parts := []string{cmd.CommandPath(), "[options]"}
	if isGroup(cmd) {
		parts = append(parts, "[command]")
	}
	if args := argumentTerms(cmd); args != "" {
		parts = append(parts, args)
	}
	return strings.Join(parts, " ")
}

func argumentTerms(cmd *cobra.Command) string {
	var terms []string
	for _, arg := range declaredArguments(cmd) {
		name := arg.name
		if arg.variadic {
			name += "..."
		}
		if arg.required {
			terms = append(terms, "<"+name+">")
		} else {
			terms = append(terms, "["+name+"]")
		}
	}
	return strings.Join(terms, " ")
}

// ownOptions lists the options a command defines, in definition order: for
// the root, --version first and then the global options.
func ownOptions(cmd *cobra.Command) []*pflag.Flag {
	var flags []*pflag.Flag
	add := func(f *pflag.Flag) {
		if !f.Hidden && f.Name != "help" {
			flags = append(flags, f)
		}
	}
	if cmd.HasParent() {
		local := cmd.LocalNonPersistentFlags()
		local.SortFlags = false
		local.VisitAll(add)
		return flags
	}
	if version := cmd.Flags().Lookup("version"); version != nil {
		add(version)
	}
	cmd.PersistentFlags().VisitAll(add)
	return flags
}

func helpOptions(cmd *cobra.Command) []helpItem {
	var items []helpItem
	for _, f := range ownOptions(cmd) {
		items = append(items, helpItem{term: optionFlags(cmd, f), description: optionDescription(cmd, f)})
	}
	return append(items, helpItem{term: helpOptionTerm, description: helpOptionSummary})
}

// optionDescription adds the choices and default to an option's usage, as
// Commander does.
func optionDescription(cmd *cobra.Command, f *pflag.Flag) string {
	var extra []string
	if choices, ok := commanderOptionChoices[cmd.CommandPath()+" --"+f.Name]; ok {
		quoted := make([]string, len(choices))
		for i, choice := range choices {
			quoted[i] = fmt.Sprintf("%q", choice)
		}
		extra = append(extra, "choices: "+strings.Join(quoted, ", "))
	}
	if takesValue(f) && f.DefValue != "" {
		extra = append(extra, fmt.Sprintf("default: %q", f.DefValue))
	}
	if len(extra) == 0 {
		return f.Usage
	}
	return f.Usage + " (" + strings.Join(extra, ", ") + ")"
}

// helpCommands lists the visible subcommands, in the order they were added,
// with help last.
func helpCommands(cmd *cobra.Command) []helpItem {
	var items []helpItem
	var help *cobra.Command
	for _, sub := range cmd.Commands() {
		switch {
		case sub.Name() == "help":
			help = sub
		case !sub.Hidden:
			items = append(items, helpItem{term: subcommandTerm(sub), description: sub.Short})
		}
	}
	if help != nil {
		items = append(items, helpItem{term: help.Use, description: help.Short})
	}
	return items
}

// subcommandTerm is the name, [options] when the command has options of its
// own, and the arguments.
func subcommandTerm(cmd *cobra.Command) string {
	term := cmd.Name()
	if len(ownOptions(cmd)) > 0 {
		term += " [options]"
	}
	if args := argumentTerms(cmd); args != "" {
		term += " " + args
	}
	return term
}

// globalOptions lists the root's options other than --version and --help.
func globalOptions(cmd *cobra.Command) []helpItem {
	root := cmd.Root()
	var items []helpItem
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if !f.Hidden && f.Name != "help" {
			items = append(items, helpItem{term: optionFlags(root, f), description: f.Usage})
		}
	})
	return items
}

func formatItemList(heading string, items []helpItem, termWidth, width int) []string {
	if len(items) == 0 {
		return nil
	}
	lines := []string{heading}
	for _, item := range items {
		lines = append(lines, formatItem(item, termWidth, width))
	}
	return append(lines, "")
}

var preformatted = regexp.MustCompile(`\n[^\S\r\n]`)

func formatItem(item helpItem, termWidth, width int) string {
	indent := strings.Repeat(" ", helpItemIndent)
	if item.description == "" {
		return indent + item.term
	}

	padded := item.term + strings.Repeat(" ", termWidth-utf8.RuneCountInString(item.term))
	description := item.description
	remaining := width - termWidth - helpSpacerWidth - helpItemIndent
	if remaining >= helpMinWrapWidth && !preformatted.MatchString(description) {
		description = strings.ReplaceAll(boxWrap(description, remaining), "\n", "\n"+strings.Repeat(" ", termWidth+helpSpacerWidth))
	}
	return indent + padded + strings.Repeat(" ", helpSpacerWidth) + strings.ReplaceAll(description, "\n", "\n"+indent)
}

var wrapChunk = regexp.MustCompile(`\s*\S+`)

// boxWrap wraps text to width at whitespace, keeping existing line breaks.
func boxWrap(text string, width int) string {
	if width < helpMinWrapWidth {
		return text
	}

	var wrapped []string
	for _, line := range regexp.MustCompile(`\r\n|\n`).Split(text, -1) {
		chunks := wrapChunk.FindAllString(line, -1)
		if chunks == nil {
			wrapped = append(wrapped, "")
			continue
		}
		current := chunks[0]
		currentWidth := utf8.RuneCountInString(current)
		for _, chunk := range chunks[1:] {
			chunkWidth := utf8.RuneCountInString(chunk)
			if currentWidth+chunkWidth <= width {
				current += chunk
				currentWidth += chunkWidth
				continue
			}
			wrapped = append(wrapped, current)
			current = strings.TrimLeft(chunk, " \t\n\r\f\v")
			currentWidth = utf8.RuneCountInString(current)
		}
		wrapped = append(wrapped, current)
	}
	return strings.Join(wrapped, "\n")
}
