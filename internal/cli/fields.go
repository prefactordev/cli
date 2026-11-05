package cli

import (
	"net/url"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/prefactordev/cli/internal/api"
)

// Options that the TypeScript CLI does not have are named after the request
// field they send, in kebab case: time_range[start_at] is
// --time-range-start-at. These helpers register them and read them into a
// request.

const (
	// choicesAnnotation holds the values an option allows, for options
	// that commanderOptionChoices does not cover.
	choicesAnnotation = "prefactor_choices"
	// jsonAnnotation marks an option that takes JSON or @file, and holds
	// the kind of value it must be: "object" or "array".
	jsonAnnotation = "prefactor_json"
	// placeholderAnnotation names an option's value in help and errors,
	// as in "--sort-field <field>".
	placeholderAnnotation = "prefactor_placeholder"
	// parserAnnotation names the optionParsers entry that checks an
	// option's value.
	parserAnnotation = "prefactor_parser"
)

var fieldFlagNames = strings.NewReplacer("_", "-", "[", "-", "]", "")

// fieldFlag is the option that sends field.
func fieldFlag(field string) string {
	return fieldFlagNames.Replace(field)
}

// addChoiceFlag registers a string option that allows only choices.
func addChoiceFlag(cmd *cobra.Command, name, usage string, choices ...string) {
	cmd.Flags().String(name, "", usage)
	_ = cmd.Flags().SetAnnotation(name, choicesAnnotation, choices)
}

// addJSONFlag registers an option that takes JSON of kind ("object" or
// "array"), or @file.
func addJSONFlag(cmd *cobra.Command, name, usage, kind string) {
	cmd.Flags().String(name, "", usage)
	_ = cmd.Flags().SetAnnotation(name, jsonAnnotation, []string{kind})
}

// optionChoices returns the values an option allows, if it limits them.
func optionChoices(cmd *cobra.Command, f *pflag.Flag) ([]string, bool) {
	if choices, ok := commanderOptionChoices[cmd.CommandPath()+" --"+f.Name]; ok {
		return choices, true
	}
	choices, ok := f.Annotations[choicesAnnotation]
	return choices, ok
}

// optionParser returns the name of the parser that checks an option's value,
// if it has one.
func optionParser(cmd *cobra.Command, f *pflag.Flag) (string, bool) {
	if name, ok := commanderOptionParsers[cmd.CommandPath()+" --"+f.Name]; ok {
		return name, true
	}
	if parser, ok := f.Annotations[parserAnnotation]; ok {
		return parser[0], true
	}
	return "", false
}

// fieldsQuery sends each field whose option was passed, and is not empty, as
// a query parameter. A boolean option sends true.
func fieldsQuery(cmd *cobra.Command, fields ...string) api.RequestEditorFn {
	values := url.Values{}
	for _, field := range fields {
		f := cmd.Flags().Lookup(fieldFlag(field))
		if !f.Changed {
			continue
		}
		if takesValue(f) {
			if value := f.Value.String(); value != "" {
				values.Set(field, value)
			}
		} else if boolFlag(cmd, f.Name) {
			values.Set(field, "true")
		}
	}
	return withQuery(values)
}

// fieldsBody copies each field whose option was passed, and is not empty,
// into body. JSON options are parsed and boolean options send true.
func fieldsBody(cmd *cobra.Command, body map[string]any, fields ...string) error {
	for _, field := range fields {
		if err := fieldBody(cmd, body, fieldFlag(field), field); err != nil {
			return err
		}
	}
	return nil
}

// fieldBody copies the option flag into body[field] as fieldsBody does.
func fieldBody(cmd *cobra.Command, body map[string]any, flag, field string) error {
	f := cmd.Flags().Lookup(flag)
	if !f.Changed || takesValue(f) && f.Value.String() == "" {
		return nil
	}
	if !takesValue(f) {
		if boolFlag(cmd, flag) {
			body[field] = true
		}
		return nil
	}
	if kind, ok := f.Annotations[jsonAnnotation]; ok {
		parsed, err := parseJSONOption(f.Value.String(), "--"+flag, kind[0])
		if err != nil {
			return err
		}
		body[field] = parsed
		return nil
	}
	body[field] = f.Value.String()
	return nil
}
