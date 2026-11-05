package cli

import (
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
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

// helpSection is a heading in the root help and the commands under it.
type helpSection struct {
	title    string
	commands []string
}

// helpSections orders the root help's commands under headings. Every
// command must be in one; TestEveryCommandIsInAHelpSection checks.
var helpSections = []helpSection{
	{"Get started", []string{"login", "setup", "profiles", "ping"}},
	{"Agents & runs", []string{"agents", "agent-versions", "agent-schema-versions", "agent-deployments", "agent-instances", "agent-spans", "alerts", "agent-risk-versions"}},
	{"Judges", []string{"judges", "agent-associations", "agent-instance-associations", "llm-providers", "llm-provider-models"}},
	{"Knowledge", []string{"knowledgebases", "knowledgebase-documents", "knowledgebase-document-revisions", "knowledgebase-revisions", "agent-knowledgebase-links"}},
	{"Account & access", []string{"accounts", "environments", "teams", "people", "risk-profiles", "integrations", "api-tokens", "admin-users", "admin-user-invites"}},
	{"Tools", []string{"bulk", "utils", "pfid", "version"}},
	{"Install", []string{"install", "update", "uninstall", "doctor"}},
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
		b.WriteString("\n" + indentLines(human.Banner(), "  ") + "\n\n")
	}
	b.WriteString("  " + t.Accent.Render(root.Short) + "\n\n")

	commands := map[string]*cobra.Command{}
	for _, cmd := range root.Commands() {
		if !cmd.Hidden && cmd.Name() != "help" {
			commands[cmd.Name()] = cmd
		}
	}

	// Each group's name is on the left; its description and, below it,
	// its subcommands wrap in the right column, aligned across sections.
	const margin, gap = "  ", "   "
	left := 0
	for name := range commands {
		left = max(left, len(name))
	}
	right := max(20, width-len(margin)-left-len(gap))
	for i, section := range helpSections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(t.Title.Render(section.title) + "\n")
		for _, name := range section.commands {
			cmd, ok := commands[name]
			if !ok {
				continue
			}
			details := strings.Split(ansi.Wordwrap(cmd.Short, right, ""), "\n")
			if subs := visibleSubcommands(cmd); len(subs) > 0 {
				for _, line := range wrapList(subs, " · ", right) {
					details = append(details, t.Muted.Render(line))
				}
			} else if args := argumentTerms(cmd); args != "" {
				details = append(details, t.Muted.Render(args))
			}
			for j, detail := range details {
				line := margin + strings.Repeat(" ", left)
				if j == 0 {
					line = margin + t.Command.Render(name) + strings.Repeat(" ", left-len(name))
				}
				b.WriteString(line + gap + detail + "\n")
			}
		}
	}

	b.WriteString("\n" + t.Title.Render("Flags") + "\n")
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

	b.WriteString("\n" + t.Title.Render("Environment variables") + "\n")
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

// wrapList joins items with sep into lines no wider than width, breaking
// only between items and leaving the separator at the end of a line.
func wrapList(items []string, sep string, width int) []string {
	var lines []string
	line := ""
	for i, item := range items {
		piece := item
		if i < len(items)-1 {
			piece += sep
		}
		if line != "" && len(strings.TrimRight(line+piece, " ")) > width {
			lines = append(lines, strings.TrimRight(line, " "))
			line = ""
		}
		line += piece
	}
	return append(lines, strings.TrimRight(line, " "))
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
