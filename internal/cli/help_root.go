package cli

import (
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/prefactordev/cli/internal/human"
)

// styled reports whether output to w is for a person at a terminal, who
// gets colour, tables and the root help's banner. Scripts, coding agents
// and JSON output get plain text and JSON.
func (a *App) styled(w io.Writer) bool {
	terminal := a.Terminal
	if terminal == nil {
		terminal = isTerminal
	}
	return terminal(w) && a.detectAgent() == "" && !a.jsonOutput()
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// environmentHelp describes the environment variables the CLI reads.
var environmentHelp = [][2]string{
	{"PREFACTOR_PROFILE", "Profile to use when --profile is not given."},
	{"PREFACTOR_API_TOKEN", "API token to use when no profile is configured."},
	{"PREFACTOR_API_URL", "API URL to use with PREFACTOR_API_TOKEN."},
	{"PREFACTOR_OUTPUT_FORMAT", "Set to json for JSON output from every command."},
	{"PREFACTOR_NO_INPUT", "Set to 1 to never prompt."},
	{"NO_COLOR", "Turn off colour (no-color.org)."},
}

// formatRootHelp is the root help for a person at a terminal: the banner,
// one line per command group with its subcommands, the flags, the
// environment variables and where to start.
func formatRootHelp(root *cobra.Command, width int, t human.Theme) string {
	var b strings.Builder
	if width >= human.BannerWidth()+4 {
		b.WriteString("\n" + indentLines(human.Banner(), "  ") + "\n")
	}
	b.WriteString("  " + t.Accent.Render(root.Short) + "\n\n")

	type entry struct {
		words []string
		short string
	}
	var entries []entry
	for _, cmd := range root.Commands() {
		if cmd.Hidden || cmd.Name() == "help" {
			continue
		}
		words := []string{"prefactor", cmd.Name()}
		if subs := visibleSubcommands(cmd); len(subs) > 0 {
			for i, sub := range subs {
				if i > 0 {
					words = append(words, "|")
				}
				words = append(words, sub)
			}
		} else if args := argumentTerms(cmd); args != "" {
			words = append(words, args)
		}
		entries = append(entries, entry{words, cmd.Short})
	}

	// The commands take the left column, up to about half the width;
	// longer lists of subcommands wrap within it.
	left := 0
	for _, e := range entries {
		left = max(left, len(strings.Join(e.words, " ")))
	}
	left = min(left, max(40, width*55/100))
	for _, e := range entries {
		lines := wrapWords(e.words, left)
		for i, line := range lines {
			prefix := "    " + t.Muted.Render("$") + " "
			if i > 0 {
				prefix = "      " + strings.Repeat(" ", len("prefactor ")+len(e.words[1])+1)
				line = strings.TrimPrefix(line, " ")
			}
			styled := t.Command.Render(line)
			pad := left - lipgloss.Width(line) - (lipgloss.Width(prefix) - 6)
			b.WriteString(prefix + styled)
			if i == 0 {
				b.WriteString(strings.Repeat(" ", max(pad, 0)+3) + t.Muted.Render(e.short))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("\n" + t.Heading.Render("Flags:") + "\n")
	// Long options line up after the short ones, as "-V, " is wide.
	flags := []helpItem{}
	alignFlag := func(term string) string {
		if !strings.HasPrefix(term, "--") {
			return term
		}
		return "    " + term
	}
	for _, f := range ownOptions(root) {
		flags = append(flags, helpItem{term: alignFlag(optionFlags(root, f)), description: f.Usage})
	}
	flags = append(flags, helpItem{term: helpOptionTerm, description: helpOptionSummary})
	termWidth := 0
	for _, item := range flags {
		termWidth = max(termWidth, len(item.term))
	}
	for _, item := range flags {
		b.WriteString(formatItem(item, termWidth, width, &t) + "\n")
	}

	b.WriteString("\n" + t.Heading.Render("Environment Variables:") + "\n")
	nameWidth := 0
	for _, v := range environmentHelp {
		nameWidth = max(nameWidth, len(v[0]))
	}
	for _, v := range environmentHelp {
		b.WriteString("  " + t.Option.Render(v[0]) + strings.Repeat(" ", nameWidth-len(v[0])+4) + t.Muted.Render(v[1]) + "\n")
	}

	b.WriteString("\n" + t.Muted.Render("try:") + " " + t.Accent.Render("prefactor setup") + "\n\n")
	b.WriteString(t.Muted.Render("Learn more at https://github.com/prefactordev/cli") + "\n")
	return b.String()
}

// visibleSubcommands lists a group's subcommands, other than help.
func visibleSubcommands(cmd *cobra.Command) []string {
	var names []string
	for _, sub := range cmd.Commands() {
		if !sub.Hidden && sub.Name() != "help" {
			names = append(names, sub.Name())
		}
	}
	return names
}

// wrapWords joins words into lines no wider than width, breaking after a
// "|" so each line after the first starts with a subcommand.
func wrapWords(words []string, width int) []string {
	var lines []string
	line := strings.Join(words[:2], " ")
	for _, w := range words[2:] {
		if len(line)+1+len(w) > width && w != "|" && strings.HasSuffix(line, "|") {
			lines = append(lines, line)
			line = ""
		}
		if line == "" {
			line = w
		} else {
			line += " " + w
		}
	}
	return append(lines, line)
}

func indentLines(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

// ErrorWriter returns where to print an error for a person at a terminal,
// with colour support, and whether to style it; scripts and coding agents
// get plain text on w.
func ErrorWriter(w io.Writer) (io.Writer, bool) {
	app := &App{Getenv: os.Getenv}
	f, ok := w.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) || app.detectAgent() != "" || os.Getenv("PREFACTOR_OUTPUT_FORMAT") == "json" {
		return w, false
	}
	return human.Writer(w, os.Environ()), true
}
