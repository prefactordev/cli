package cli

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// specSchema is the part of an OpenAPI schema these tests read.
type specSchema struct {
	Ref         string                `json:"$ref"`
	Type        string                `json:"type"`
	Description string                `json:"description"`
	Enum        []any                 `json:"enum"`
	Properties  map[string]specSchema `json:"properties"`
	OneOf       []specSchema          `json:"oneOf"`
}

type specOperation struct {
	OperationID string `json:"operationId"`
	Parameters  []struct {
		Name   string     `json:"name"`
		In     string     `json:"in"`
		Schema specSchema `json:"schema"`
	} `json:"parameters"`
	RequestBody struct {
		Content map[string]struct {
			Schema specSchema `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
}

type specDocument struct {
	Paths      map[string]map[string]specOperation `json:"paths"`
	Components struct {
		Schemas map[string]specSchema `json:"schemas"`
	} `json:"components"`
}

func loadSpec(t *testing.T) *specDocument {
	t.Helper()
	raw, err := os.ReadFile("../../api/openapi.normalized.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc specDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return &doc
}

// resolve follows $ref, and picks the non-null schema of a nullable oneOf.
func (d *specDocument) resolve(s specSchema) specSchema {
	for s.Ref != "" {
		s = d.Components.Schemas[strings.TrimPrefix(s.Ref, "#/components/schemas/")]
	}
	for _, option := range s.OneOf {
		if option = d.resolve(option); option.Type != "null" {
			if option.Description == "" {
				option.Description = s.Description
			}
			return option
		}
	}
	return s
}

// requestField is one value an operation's request carries.
type requestField struct {
	name   string
	schema specSchema
}

// requestFields lists the query parameters and body fields of op. The fields
// of details stand in for details, and an object at the top of the body
// with declared fields stands in for its fields, as object.field.
func (d *specDocument) requestFields(op specOperation) []requestField {
	var fields []requestField
	for _, p := range op.Parameters {
		if p.In == "query" && p.Name != "idempotency_key" {
			fields = append(fields, requestField{p.Name, d.resolve(p.Schema)})
		}
	}
	body := d.resolve(op.RequestBody.Content["application/json"].Schema)
	for name, prop := range body.Properties {
		prop = d.resolve(prop)
		switch {
		case name == "idempotency_key":
		case name == "details":
			for field, schema := range prop.Properties {
				fields = append(fields, requestField{field, d.resolve(schema)})
			}
		case len(prop.Properties) > 0:
			for field, schema := range prop.Properties {
				fields = append(fields, requestField{name + "." + field, d.resolve(schema)})
			}
		default:
			fields = append(fields, requestField{name, prop})
		}
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].name < fields[j].name })
	return fields
}

// fieldOptions names the options that send a field under a name other than
// the field's, keyed by "operationId field". Most are names the TypeScript
// CLI chose.
var fieldOptions = map[string]string{
	"Action.AgentInstance.Create update_current_version":                                "no-update-current-version",
	"Action.AgentInstance.Register agent_schema_version.alert_schemas":                  "alert-schemas",
	"Action.AgentInstance.Register agent_schema_version.instance_metadata_schema":       "instance-metadata-schema",
	"Action.AgentInstance.Register agent_schema_version.instance_params_payload_schema": "instance-params-payload-schema",
	"Action.AgentInstance.Register agent_schema_version.instance_result_payload_schema": "instance-result-payload-schema",
	"Action.AgentInstance.Register agent_schema_version.quality_schemas":                "quality-schemas",
	"Action.AgentInstance.Register agent_schema_version.span_result_schemas":            "span-result-schemas",
	"Action.AgentInstance.Register agent_schema_version.span_schemas":                   "span-schemas",
	"Action.AgentInstance.Register agent_schema_version.span_type_schemas":              "span-type-schemas",
	"Query.Alert.List sorting":       "sorting",
	"Query.Person.List sorting":      "sorting",
	"Query.RiskProfile.List sorting": "sorting",
	"Query.Team.List sorting":        "sorting",
}

// fieldsWithoutOptions are fields no option sends, keyed by "operationId
// field", with the reason.
var fieldsWithoutOptions = map[string]string{
	"Action.AccountIntegration.Create config":           "built from --webhook-url and --enabled-events",
	"Action.AccountIntegration.Create integration_type": "the <type> argument",
	"Action.AccountIntegration.Update config":           "built from --webhook-url and --enabled-events",
}

var supportedSortFields = regexp.MustCompile("`([a-z_]+)`")

// TestEveryRequestFieldHasAnOption checks each command has an option for
// every query parameter and body field of the operation it calls, named
// after the field in kebab case, with the field's choices.
func TestEveryRequestFieldHasAnOption(t *testing.T) {
	doc := loadSpec(t)
	coverage := loadCoverage(t)
	root := newRootCommand(&App{Version: "test"})

	// The TypeScript CLI checks some fields' values itself, after reading
	// them, so its options keep no choices here either.
	typescriptOptions := map[string]bool{}
	for _, ts := range loadTypescriptCommands(t) {
		path := strings.TrimSpace("prefactor " + strings.ReplaceAll(ts.Path, "_", "-"))
		for _, opt := range ts.Options {
			typescriptOptions[path+" "+kebabNames(opt.Long)] = true
		}
	}

	for _, item := range doc.Paths {
		for _, op := range item {
			command := coverage[op.OperationID].Command
			if command == "" {
				continue
			}
			cmd, _, _ := root.Find(strings.Fields(command))

			for _, field := range doc.requestFields(op) {
				key := op.OperationID + " " + field.name
				if _, ok := fieldsWithoutOptions[key]; ok {
					continue
				}

				options, choices := fieldOptionNames(key, field)
				for _, name := range options {
					if cmd.Flags().Lookup(name) == nil {
						t.Errorf("%s (%s): no option --%s for %s", command, op.OperationID, name, field.name)
					}
				}
				_, hasChoices := commanderOptionChoices[cmd.CommandPath()+" --"+options[0]]
				if choices != nil && (hasChoices || !typescriptOptions[cmd.CommandPath()+" --"+options[0]]) {
					checkChoices(t, cmd, options[0], choices)
				}
			}
		}
	}
}

// fieldOptionNames lists the options that send field, keyed as in
// fieldOptions, and the values the first of them must allow.
func fieldOptionNames(key string, field requestField) (options, choices []string) {
	if name, ok := fieldOptions[key]; ok && field.name == "sorting" {
		return []string{name}, nil
	}
	switch {
	case field.name == "sorting":
		for _, m := range supportedSortFields.FindAllStringSubmatch(field.schema.Description, -1) {
			choices = append(choices, m[1])
		}
		return []string{"sort-field", "sort-direction"}, choices
	case strings.HasPrefix(field.name, "pagination["):
		return []string{fieldFlag(field.name)}, nil
	}

	name, ok := fieldOptions[key]
	if !ok {
		name = fieldFlag(strings.ReplaceAll(field.name, ".", "_"))
	}
	for _, value := range field.schema.Enum {
		choices = append(choices, value.(string))
	}
	return []string{name}, choices
}

// specOptions lists, for each command in api/coverage.yaml, the options
// that send its operation's request fields.
func specOptions(t *testing.T) map[string]map[string]bool {
	t.Helper()
	doc := loadSpec(t)
	coverage := loadCoverage(t)
	options := map[string]map[string]bool{}
	for _, item := range doc.Paths {
		for _, op := range item {
			command := coverage[op.OperationID].Command
			if command == "" {
				continue
			}
			path := "prefactor " + command
			if options[path] == nil {
				options[path] = map[string]bool{}
			}
			for _, field := range doc.requestFields(op) {
				names, _ := fieldOptionNames(op.OperationID+" "+field.name, field)
				for _, name := range names {
					options[path][name] = true
				}
			}
		}
	}
	return options
}

// checkChoices checks the option allows exactly want, when cmd has it.
func checkChoices(t *testing.T, cmd *cobra.Command, option string, want []string) {
	t.Helper()
	f := cmd.Flags().Lookup(option)
	if f == nil {
		return
	}
	if got, _ := optionChoices(cmd, f); !slices.Equal(got, want) {
		t.Errorf("%s --%s: choices %v, the API allows %v", cmd.CommandPath(), option, got, want)
	}
}
