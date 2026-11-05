package cli

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

//go:generate go run ../../tools/commandergen testdata/typescript-commands.json commander_flags.go

// The CLI reports command-line mistakes in the same words and the same order
// as the TypeScript CLI, which uses Commander. Commander checks, in order:
// --version, then each option's value as it reads it (choices, argument
// parsers and missing values, in argv order, skipping unknown options), then
// --help, missing required options (in definition order), the first unknown
// option, then missing and extra arguments. A command group given nothing
// prints its help to stderr and exits 1; given an unknown command, it
// suggests similar names.

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
// checks: Cobra's own argument, unknown flag and help checks are turned off,
// boolean flags given a value are left for the unknown option check, command
// groups get a handler, and the Cobra completion command is removed. argv
// returns the full argument list given to the CLI.
func prepareCommandLine(root *cobra.Command, help *bool, argv func() []string) {
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().VarPF(helpFlag{requested: help}, "help", "h", "display help for command").NoOptDefVal = "true"
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		// pflag stops at a value missing from the last option. Commander
		// reads every option before it first.
		if versionRequested(cmd, argv()) {
			return printVersion(cmd)
		}
		if err := checkOptionValues(cmd, argv()); err != nil {
			return err
		}
		return flagError(cmd, err)
	})

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		cmd.Args = cobra.ArbitraryArgs
		cmd.FParseErrWhitelist.UnknownFlags = true
		// Commander reports and lists options in definition order.
		cmd.Flags().SortFlags = false
		cmd.LocalFlags().SortFlags = false
		cmd.PersistentFlags().SortFlags = false
		for _, flags := range []*pflag.FlagSet{cmd.LocalNonPersistentFlags(), cmd.PersistentFlags()} {
			flags.VisitAll(func(f *pflag.Flag) {
				if f.Value.Type() == "bool" && f.Name != "help" {
					f.Value = lenientBool{f.Value}
				}
			})
		}
		if cmd.HasSubCommands() && cmd.RunE == nil && cmd.Run == nil {
			cmd.RunE = func(*cobra.Command, []string) error { return nil }
		}
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

// addHelpCommands gives every command group Commander's "help [command]":
// the group's help, or one of its commands' help, on stdout. Any other name
// prints the group's help to stderr and fails. Options other than --version
// are ignored, as Commander ignores them there.
func addHelpCommands(cmd *cobra.Command, argv func() []string) {
	if !isGroup(cmd) {
		return
	}
	for _, sub := range cmd.Commands() {
		addHelpCommands(sub, argv)
	}

	help := &cobra.Command{
		Use:                "help [command]",
		Short:              "display help for command",
		DisableFlagParsing: true,
		PersistentPreRunE: func(help *cobra.Command, _ []string) error {
			if versionRequested(help, argv()) {
				return printVersion(help)
			}
			return nil
		},
		RunE: func(help *cobra.Command, args []string) error {
			group := help.Parent()
			names := operands(help, args)
			if len(names) == 0 {
				return group.Help()
			}
			for _, sub := range group.Commands() {
				if sub.Name() == names[0] && sub != help {
					return sub.Help()
				}
			}
			group.SetOut(group.ErrOrStderr())
			_ = group.Help()
			return &exitError{code: 1}
		},
	}
	if cmd.HasParent() {
		cmd.AddCommand(help)
	} else {
		cmd.SetHelpCommand(help)
	}
}

// operands returns the arguments Commander's root reads as operands: those
// before the first unknown option that are not options or their values.
func operands(cmd *cobra.Command, args []string) []string {
	flags := cmd.Root().Flags()
	var result []string
	for i := 0; i < len(args); i++ {
		token := args[i]
		var f *pflag.Flag
		switch {
		case token == "--":
			return append(result, args[i+1:]...)
		case strings.HasPrefix(token, "--"):
			name, _, hasValue := strings.Cut(token[2:], "=")
			if f = flags.Lookup(name); f == nil {
				return result
			}
			if takesValue(f) && !hasValue {
				i++
			}
		case strings.HasPrefix(token, "-") && len(token) > 1:
			if f = flags.ShorthandLookup(token[1:2]); f == nil {
				return result
			}
			if takesValue(f) && len(token) == 2 {
				i++
			}
		default:
			result = append(result, token)
		}
	}
	return result
}

// helpFlag records --help and -h so that prepareRun shows help where
// Commander does. Cobra always reads it as false, so it never shows help
// before the option values are checked.
type helpFlag struct {
	requested *bool
}

func (h helpFlag) String() string   { return "false" }
func (h helpFlag) Set(string) error { *h.requested = true; return nil }
func (h helpFlag) Type() string     { return "bool" }
func (h helpFlag) IsBoolFlag() bool { return true }

// lenientBool accepts any value, so that --flag=value on a boolean flag is
// reported as an unknown option, in Commander's order, rather than stopping
// flag parsing.
type lenientBool struct {
	pflag.Value
}

func (b lenientBool) Set(value string) error {
	if _, err := strconv.ParseBool(value); err != nil {
		return nil
	}
	return b.Value.Set(value)
}

func (b lenientBool) IsBoolFlag() bool { return true }

func printVersion(cmd *cobra.Command) error {
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), cmd.Root().Version); err != nil {
		return err
	}
	return &exitError{code: 0}
}

// checkEarly handles what Commander deals with before required options:
// --version anywhere, option values, command groups given nothing, --help,
// and command groups given an unknown command or option. argv is the full
// argument list given to the CLI.
func checkEarly(cmd *cobra.Command, args, argv []string, help bool) error {
	if versionRequested(cmd, argv) {
		return printVersion(cmd)
	}
	if err := checkOptionValues(cmd, argv); err != nil {
		return err
	}

	unknown := firstUnknownOption(cmd, argv)
	if isGroup(cmd) && len(args) == 0 && unknown == "" && !help {
		cmd.SetOut(cmd.ErrOrStderr())
		_ = cmd.Help()
		return &exitError{code: 1}
	}
	if help {
		_ = cmd.Help()
		return &exitError{code: 0}
	}

	if isGroup(cmd) {
		if len(args) > 0 {
			return &UsageError{Message: fmt.Sprintf("error: unknown command '%s'%s", args[0], suggestSimilar(args[0], subcommandNames(cmd)))}
		}
		return unknownOptionError(cmd, unknown)
	}
	return nil
}

// optionParsers implements the TypeScript argument parsers named in
// commanderOptionParsers. Their errors are printed as they are.
var optionParsers = map[string]func(string) error{
	"parsePaginationOffset": func(value string) error { _, err := parsePaginationOffset(value); return err },
	"parsePositiveInt":      func(value string) error { _, err := parsePositiveInt(value); return err },
}

// checkOptionValues checks each option value in argv as Commander reads it:
// in order, against the option's choices and argument parser. Unknown
// options are skipped without taking a value, and a value missing from the
// last option is left for pflag to report.
func checkOptionValues(cmd *cobra.Command, argv []string) error {
	flags := cmd.Flags()
	var group string // the rest of a group of short options, such as -ab
	for i := 0; i < len(argv) || group != ""; {
		token := group
		if token == "" {
			token = argv[i]
			i++
		}
		group = ""

		var f *pflag.Flag
		var value string
		switch {
		case token == "--":
			return nil
		case strings.HasPrefix(token, "--"):
			name, inline, hasValue := strings.Cut(token[2:], "=")
			if f = flags.Lookup(name); f == nil || !takesValue(f) {
				continue
			}
			if hasValue {
				value = inline
			} else if i < len(argv) {
				value = argv[i]
				i++
			} else {
				return nil
			}
		case strings.HasPrefix(token, "-") && len(token) > 1:
			if f = flags.ShorthandLookup(token[1:2]); f == nil {
				continue
			}
			if !takesValue(f) {
				if len(token) > 2 {
					group = "-" + token[2:]
				}
				continue
			}
			if len(token) > 2 {
				value = token[2:]
			} else if i < len(argv) {
				value = argv[i]
				i++
			} else {
				return nil
			}
		default:
			continue
		}

		if err := checkOptionValue(cmd, f, value); err != nil {
			return err
		}
	}
	return nil
}

func checkOptionValue(cmd *cobra.Command, f *pflag.Flag, value string) error {
	if choices, ok := optionChoices(cmd, f); ok && !slices.Contains(choices, value) {
		return &UsageError{Message: fmt.Sprintf("error: option '%s' argument '%s' is invalid. Allowed choices are %s.", optionFlags(cmd, f), value, strings.Join(choices, ", "))}
	}
	if name, ok := optionParser(cmd, f); ok {
		if err := optionParsers[name](value); err != nil {
			return err
		}
	}
	if f.Name == "fields" {
		if _, err := parseFields(value); err != nil {
			return err
		}
	}
	return nil
}

// checkLate reports missing required options, then the first unknown
// option, then missing or extra arguments, as Commander does.
// pickable, when set, reports the missing required inputs a picker will
// supply; those are not errors.
func checkLate(cmd *cobra.Command, args, argv []string, pickable *pickableInputs) error {
	if err := checkRequiredOptions(cmd, pickable); err != nil {
		return err
	}

	if unknown := firstUnknownOption(cmd, argv); unknown != "" {
		return unknownOptionError(cmd, unknown)
	}

	return checkArguments(cmd, args, pickable)
}

// pickableInputs are the required arguments (by position) and flags (by
// name) that a picker can supply on this run.
type pickableInputs struct {
	args  map[int]bool
	flags map[string]bool
}

// any reports whether a picker will supply anything.
func (p *pickableInputs) any() bool {
	return p != nil && (len(p.args) > 0 || len(p.flags) > 0)
}

func isGroup(cmd *cobra.Command) bool {
	return cmd.HasSubCommands()
}

// checkRequiredOptions reports the first required option, in definition
// order, that was not passed.
func checkRequiredOptions(cmd *cobra.Command, pickable *pickableInputs) error {
	var missing *pflag.Flag
	local := cmd.LocalFlags()
	local.SortFlags = false
	local.VisitAll(func(f *pflag.Flag) {
		if missing != nil {
			return
		}
		if _, required := f.Annotations[requiredAnnotation]; required && !f.Changed {
			if pickable != nil && pickable.flags[f.Name] {
				return
			}
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
func checkArguments(cmd *cobra.Command, args []string, pickable *pickableInputs) error {
	declared := declaredArguments(cmd)

	for i, arg := range declared {
		if arg.required && i >= len(args) {
			if pickable != nil && pickable.args[i] {
				continue
			}
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
// option rather than as another option's value or after --. Like
// Commander's root, it knows only the root options, so --version after a
// subcommand option that takes a value still counts.
func versionRequested(cmd *cobra.Command, argv []string) bool {
	flags := cmd.Root().Flags()
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
		placeholder := f.Name
		if annotation, ok := f.Annotations[placeholderAnnotation]; ok {
			placeholder = annotation[0]
		}
		flags += " <" + placeholder + ">"
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
