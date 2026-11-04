package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/output"
)

// agentEnvVars are set by coding agents in the shells they run. They are
// community conventions rather than vendor documentation, so --no-input and
// PREFACTOR_NO_INPUT remain the reliable way to turn prompts off.
var agentEnvVars = []string{"CLAUDECODE", "CURSOR_AGENT", "GEMINI_CLI", "CODEX_SANDBOX", "CODEX_CI", "AGENT", "AI_AGENT"}

// session holds the settings decided once per run.
type session struct {
	// interactive is true when the CLI may prompt: stdin and stdout are
	// terminals, no coding agent is detected and input is not turned off.
	interactive bool
	// yes accepts the default answer to every prompt.
	yes bool
	// json forces JSON output on every command.
	json bool
	// fields limits JSON output to these dot-separated paths.
	fields []string
}

// addSessionFlags registers the flags every command accepts.
func addSessionFlags(root *cobra.Command) {
	flags := root.PersistentFlags()
	flags.Bool("json", false, "Print JSON output on every command")
	flags.String("fields", "", "Limit JSON output to these comma-separated fields (dot notation for nested fields)")
	flags.Bool("no-input", false, "Never prompt; fail with the flag to pass when input is missing")
	flags.Bool("yes", false, "Accept the default answer to every prompt")
}

// startSession decides the session settings from flags, the environment and
// the terminal. It runs before every command.
func (a *App) startSession(cmd *cobra.Command) error {
	s := &session{
		yes:  boolFlag(cmd, "yes"),
		json: boolFlag(cmd, "json"),
	}

	switch format := a.Getenv("PREFACTOR_OUTPUT_FORMAT"); format {
	case "":
	case "json":
		s.json = true
	default:
		return errors.New(`PREFACTOR_OUTPUT_FORMAT must be "json".`)
	}

	if value := stringFlag(cmd, "fields"); flagSet(cmd, "fields") {
		fields, err := parseFields(value)
		if err != nil {
			return err
		}
		s.fields = fields
		s.json = true
	}

	noInput := boolFlag(cmd, "no-input") || envTrue(a.Getenv("PREFACTOR_NO_INPUT"))
	s.interactive = !noInput && a.terminals() && a.detectAgent() == ""

	a.session = s
	return nil
}

// terminals reports whether stdin and stdout are both terminals.
func (a *App) terminals() bool {
	return a.StdinIsTerminal != nil && a.StdoutIsTerminal != nil && a.StdinIsTerminal() && a.StdoutIsTerminal()
}

// detectAgent returns the first coding agent variable that is set, or "".
func (a *App) detectAgent() string {
	for _, name := range agentEnvVars {
		if envTrue(a.Getenv(name)) {
			return name
		}
	}
	return ""
}

// interactive reports whether the command may prompt.
func (a *App) interactive() bool {
	return a.session != nil && a.session.interactive
}

// jsonOutput reports whether JSON output is forced for this run.
func (a *App) jsonOutput() bool {
	return a.session != nil && a.session.json
}

// printResult prints a raw JSON response, limited to --fields when set.
func (a *App) printResult(cmd *cobra.Command, raw []byte) error {
	if a.session != nil && len(a.session.fields) > 0 {
		return output.JSONFields(cmd.OutOrStdout(), raw, a.session.fields)
	}
	return output.JSON(cmd.OutOrStdout(), raw)
}

// printJSONValue prints a value the CLI builds itself, limited to --fields
// when set.
func (a *App) printJSONValue(cmd *cobra.Command, value any) error {
	raw, err := marshalValue(value)
	if err != nil {
		return err
	}
	return a.printResult(cmd, raw)
}

// parseFields splits --fields into dot-separated paths.
func parseFields(value string) ([]string, error) {
	var fields []string
	for _, part := range strings.Split(value, ",") {
		field := strings.TrimSpace(part)
		if field == "" {
			continue
		}
		for _, segment := range strings.Split(field, ".") {
			if segment == "" {
				return nil, errors.New("--fields has an empty field name in '" + field + "'.")
			}
		}
		fields = append(fields, field)
	}

	if len(fields) == 0 {
		return nil, errors.New("--fields requires at least one field.")
	}
	return fields, nil
}

// envTrue reports whether an environment variable is set to something other
// than an empty string, 0 or false.
func envTrue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}
