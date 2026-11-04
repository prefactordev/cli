package cli

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The CLI reports command-line mistakes in the same words and the same order
// as the TypeScript CLI, which uses Commander. Commander checks, in order:
// option values while reading options, missing required options (in
// definition order), the first unknown option, then missing and extra
// arguments. A command group given no subcommand prints its help to stderr
// and exits 1; given an unknown one, it suggests similar names.

// exitError ends the run with a status code and prints nothing more.
type exitError struct {
	code int
}

func (e *exitError) Error() string {
	return fmt.Sprintf("exit status %d", e.code)
}

// ExitCode reports the status for an error that only ends the run.
func ExitCode(err error) (int, bool) {
	var exit *exitError
	if errors.As(err, &exit) {
		return exit.code, true
	}
	return 0, false
}

// requiredAnnotation marks a flag as required, as MarkFlagRequired does.
const requiredAnnotation = cobra.BashCompOneRequiredFlag

// prepareCommandLine sets up every command in the tree for Commander-style
// checks: Cobra's own argument and unknown flag checks are turned off,
// command groups get a handler, and the Cobra completion command is removed.
func prepareCommandLine(root *cobra.Command) {
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetFlagErrorFunc(flagError)

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		cmd.Args = cobra.ArbitraryArgs
		cmd.FParseErrWhitelist.UnknownFlags = true
		// Commander reports and lists options in definition order.
		cmd.Flags().SortFlags = false
		cmd.LocalFlags().SortFlags = false
		cmd.PersistentFlags().SortFlags = false
		if cmd.HasSubCommands() && cmd.RunE == nil && cmd.Run == nil {
			cmd.RunE = func(*cobra.Command, []string) error { return nil }
		}
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

// checkCommandLine reports the first command-line mistake the way Commander
// would. argv is the full argument list given to the CLI.
func checkCommandLine(cmd *cobra.Command, args, argv []string) error {
	// Commander handles the root's --version and -V wherever they appear,
	// before any other check.
	if versionRequested(cmd, argv) {
		root := cmd.Root()
		_, err := fmt.Fprintln(cmd.OutOrStdout(), root.Version)
		if err != nil {
			return err
		}
		return &exitError{code: 0}
	}

	if isGroup(cmd) {
		if len(args) > 0 {
			return &UsageError{Message: fmt.Sprintf("error: unknown command '%s'%s", args[0], suggestSimilar(args[0], subcommandNames(cmd)))}
		}
		if unknown := firstUnknownOption(cmd, argv); unknown != "" {
			return unknownOptionError(cmd, unknown)
		}
		cmd.SetOut(cmd.ErrOrStderr())
		_ = cmd.Help()
		return &exitError{code: 1}
	}

	if err := checkRequiredOptions(cmd); err != nil {
		return err
	}

	if unknown := firstUnknownOption(cmd, argv); unknown != "" {
		return unknownOptionError(cmd, unknown)
	}

	return checkArguments(cmd, args)
}

func isGroup(cmd *cobra.Command) bool {
	return cmd.HasSubCommands()
}

// checkRequiredOptions reports the first required option, in definition
// order, that was not passed.
func checkRequiredOptions(cmd *cobra.Command) error {
	var missing *pflag.Flag
	local := cmd.LocalFlags()
	local.SortFlags = false
	local.VisitAll(func(f *pflag.Flag) {
		if missing != nil {
			return
		}
		if _, required := f.Annotations[requiredAnnotation]; required && !f.Changed {
			missing = f
		}
	})
	if missing == nil {
		return nil
	}

	return &UsageError{Message: fmt.Sprintf("error: required option '%s' not specified", optionFlags(cmd, missing))}
}

// checkArguments compares the positional arguments with the <name> and
// [name] placeholders in the command's Use line.
func checkArguments(cmd *cobra.Command, args []string) error {
	declared := declaredArguments(cmd)

	for i, arg := range declared {
		if arg.required && i >= len(args) {
			return &UsageError{Message: fmt.Sprintf("error: missing required argument '%s'", arg.name)}
		}
	}

	if len(declared) > 0 && declared[len(declared)-1].variadic {
		return nil
	}
	if len(args) > len(declared) {
		plural := "s"
		if len(declared) == 1 {
			plural = ""
		}
		forCommand := ""
		if cmd.HasParent() {
			forCommand = fmt.Sprintf(" for '%s'", cmd.Name())
		}
		return &UsageError{Message: fmt.Sprintf("error: too many arguments%s. Expected %d argument%s but got %d.", forCommand, len(declared), plural, len(args))}
	}

	return nil
}

type declaredArgument struct {
	name     string
	required bool
	variadic bool
}

// declaredArguments reads <name>, [name] and name... from the Use line.
func declaredArguments(cmd *cobra.Command) []declaredArgument {
	var declared []declaredArgument
	for _, word := range strings.Fields(cmd.Use)[1:] {
		var arg declaredArgument
		switch {
		case strings.HasPrefix(word, "<") && strings.HasSuffix(word, ">"):
			arg = declaredArgument{name: strings.Trim(word, "<>"), required: true}
		case strings.HasPrefix(word, "[") && strings.HasSuffix(word, "]"):
			arg = declaredArgument{name: strings.Trim(word, "[]")}
		default:
			continue
		}
		if strings.HasSuffix(arg.name, "...") {
			arg.name = strings.TrimSuffix(arg.name, "...")
			arg.variadic = true
		}
		declared = append(declared, arg)
	}
	return declared
}

// versionRequested reports whether --version or -V appears in argv as an
// option rather than as another option's value or after --.
func versionRequested(cmd *cobra.Command, argv []string) bool {
	flags := cmd.Flags()
	for i := 0; i < len(argv); i++ {
		token := argv[i]
		switch {
		case token == "--":
			return false
		case token == "--version" || token == "-V":
			return true
		case strings.HasPrefix(token, "--"):
			name, _, hasValue := strings.Cut(token[2:], "=")
			if f := flags.Lookup(name); f != nil && takesValue(f) && !hasValue {
				i++
			}
		case strings.HasPrefix(token, "-") && len(token) == 2:
			if f := flags.ShorthandLookup(token[1:]); f != nil && takesValue(f) {
				i++
			}
		}
	}
	return false
}

// firstUnknownOption returns the first option in argv that the command does
// not define, or "".
func firstUnknownOption(cmd *cobra.Command, argv []string) string {
	flags := cmd.Flags()
	for i := 0; i < len(argv); i++ {
		token := argv[i]
		switch {
		case token == "--":
			return ""
		case strings.HasPrefix(token, "--"):
			name, _, hasValue := strings.Cut(token[2:], "=")
			if name == "help" {
				continue
			}
			f := flags.Lookup(name)
			// Commander treats --flag=value as unknown when the option
			// takes no value.
			if f == nil || (hasValue && !takesValue(f)) {
				return token
			}
			if takesValue(f) && !hasValue {
				i++
			}
		case strings.HasPrefix(token, "-") && len(token) > 1:
			short := token[1:2]
			if short == "h" {
				continue
			}
			f := flags.ShorthandLookup(short)
			if f == nil {
				return "-" + short
			}
			if takesValue(f) && len(token) == 2 {
				i++
			}
		}
	}
	return ""
}

func unknownOptionError(cmd *cobra.Command, flag string) error {
	suggestion := ""
	if strings.HasPrefix(flag, "--") {
		suggestion = suggestSimilar(flag, optionNames(cmd))
	}
	return &UsageError{Message: fmt.Sprintf("error: unknown option '%s'%s", flag, suggestion)}
}

// flagError turns Cobra's flag parsing errors into Commander's wording.
func flagError(cmd *cobra.Command, err error) error {
	message := err.Error()

	if name, ok := strings.CutPrefix(message, "flag needs an argument: --"); ok {
		if f := cmd.Flags().Lookup(name); f != nil {
			return &UsageError{Message: fmt.Sprintf("error: option '%s' argument missing", optionFlags(cmd, f))}
		}
	}
	// A value given to a flag that takes none, such as --include-counts=yes:
	// invalid argument "yes" for "--include-counts" flag: ...
	if rest, ok := strings.CutPrefix(message, `invalid argument "`); ok {
		value, rest, _ := strings.Cut(rest, `" for "`)
		name, _, _ := strings.Cut(rest, `"`)
		if strings.HasPrefix(name, "--") {
			return &UsageError{Message: fmt.Sprintf("error: unknown option '%s=%s'", name, value)}
		}
	}

	return &UsageError{Message: "error: " + message}
}

// optionFlags is Commander's flags string for an option, such as
// "--name <name>" or "--include-counts". Commands ported from the TypeScript
// CLI use its placeholder names, in kebab case.
func optionFlags(cmd *cobra.Command, f *pflag.Flag) string {
	if flags, ok := commanderOptionFlags[cmd.CommandPath()+" --"+f.Name]; ok {
		return flags
	}

	flags := "--" + f.Name
	if f.Shorthand != "" {
		flags = "-" + f.Shorthand + ", " + flags
	}
	if takesValue(f) {
		flags += " <" + f.Name + ">"
	}
	return flags
}

func takesValue(f *pflag.Flag) bool {
	return f.NoOptDefVal == ""
}

// optionNames lists the long options Commander would suggest from: the
// command's own, then each ancestor's, plus --help.
func optionNames(cmd *cobra.Command) []string {
	var names []string
	for c := cmd; c != nil; c = c.Parent() {
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if !f.Hidden {
				names = append(names, "--"+f.Name)
			}
		})
		names = append(names, "--help")
	}
	return names
}

// subcommandNames lists the visible subcommands, plus help, as Commander
// suggests from them.
func subcommandNames(cmd *cobra.Command) []string {
	var names []string
	for _, sub := range cmd.Commands() {
		if sub.IsAvailableCommand() || sub.Name() == "help" {
			names = append(names, sub.Name())
		}
	}
	return append(names, "help")
}

const maxEditDistance = 3

// suggestSimilar is Commander's suggestSimilar: names within edit distance
// 3 and more than 40% similar, keeping only the closest.
func suggestSimilar(word string, candidates []string) string {
	seen := map[string]bool{}
	var unique []string
	for _, c := range candidates {
		if !seen[c] {
			seen[c] = true
			unique = append(unique, c)
		}
	}

	searchingOptions := strings.HasPrefix(word, "--")
	if searchingOptions {
		word = word[2:]
		for i, c := range unique {
			unique[i] = strings.TrimPrefix(c, "--")
		}
	}

	var similar []string
	best := maxEditDistance
	for _, candidate := range unique {
		if len([]rune(candidate)) <= 1 {
			continue
		}
		distance := editDistance(word, candidate)
		length := max(len([]rune(word)), len([]rune(candidate)))
		similarity := float64(length-distance) / float64(length)
		if similarity > 0.4 {
			if distance < best {
				best = distance
				similar = []string{candidate}
			} else if distance == best {
				similar = append(similar, candidate)
			}
		}
	}

	// localeCompare orders mostly by letters, ignoring - and _.
	sort.SliceStable(similar, func(i, j int) bool {
		a, b := collationKey(similar[i]), collationKey(similar[j])
		if a != b {
			return a < b
		}
		return similar[i] < similar[j]
	})
	if searchingOptions {
		for i, c := range similar {
			similar[i] = "--" + c
		}
	}

	switch len(similar) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("\n(Did you mean %s?)", similar[0])
	default:
		return fmt.Sprintf("\n(Did you mean one of %s?)", strings.Join(similar, ", "))
	}
}

func collationKey(s string) string {
	return strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(s))
}

// editDistance is the optimal string alignment distance Commander uses.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if abs(len(ra)-len(rb)) > maxEditDistance {
		return max(len(ra), len(rb))
	}

	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := 0; j <= len(rb); j++ {
		d[0][j] = j
	}

	for j := 1; j <= len(rb); j++ {
		for i := 1; i <= len(ra); i++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}

	return d[len(ra)][len(rb)]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
