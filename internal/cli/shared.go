package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
	"github.com/prefactordev/cli/internal/client"
)

const maxJSONOptionFileSize = 1024 * 1024

// apiCall sends one request with the generated client.
type apiCall func(ctx context.Context, c *api.Client) (*http.Response, error)

// runAPI sends call with the selected profile's client and prints the JSON
// response, like executeAuthed plus printJson in the TypeScript CLI.
func (a *App) runAPI(cmd *cobra.Command, call apiCall) error {
	raw, err := a.callAPI(cmd, call)
	if err != nil {
		return err
	}

	return a.printResult(cmd, raw)
}

// callAPI sends call with the selected profile's client and returns the raw
// response body.
func (a *App) callAPI(cmd *cobra.Command, call apiCall) ([]byte, error) {
	apiClient, err := a.authedClient(cmd)
	if err != nil {
		return nil, err
	}

	return client.ReadJSON(call(cmd.Context(), apiClient))
}

// marshalValue encodes a value the CLI builds itself as JSON.
func marshalValue(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// jsonBody encodes a request body without HTML escaping.
func jsonBody(value any) (io.Reader, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}

	return bytes.NewReader(bytes.TrimSuffix(buf.Bytes(), []byte("\n"))), nil
}

// withQuery adds query parameters that the generated params structs cannot
// express, such as nullable ID filters modelled as anonymous unions.
func withQuery(values url.Values) api.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		query := req.URL.Query()
		for key, list := range values {
			for _, value := range list {
				query.Add(key, value)
			}
		}
		req.URL.RawQuery = query.Encode()
		return nil
	}
}

// stringFlag returns a string flag's value.
func stringFlag(cmd *cobra.Command, name string) string {
	value, _ := cmd.Flags().GetString(name)
	return value
}

// boolFlag returns a boolean flag's value.
func boolFlag(cmd *cobra.Command, name string) bool {
	value, _ := cmd.Flags().GetBool(name)
	return value
}

// flagSet reports whether the user passed the flag.
func flagSet(cmd *cobra.Command, name string) bool {
	return cmd.Flags().Changed(name)
}

// parseJSONOption parses a JSON value, or a file when value starts with @,
// and checks it is an object or an array. The JSON is returned unchanged so
// its key order and numbers reach the API as written.
func parseJSONOption(value, optionName, expected string) (json.RawMessage, error) {
	contents := value
	if strings.HasPrefix(value, "@") {
		var err error
		contents, err = readOptionFile(value[1:], optionName)
		if err != nil {
			return nil, err
		}
	}

	var parsed any
	if err := json.Unmarshal([]byte(contents), &parsed); err != nil {
		return nil, fmt.Errorf("Invalid JSON for %s: %w", optionName, err)
	}

	switch expected {
	case "array":
		if _, ok := parsed.([]any); !ok {
			return nil, fmt.Errorf("%s must be a JSON array.", optionName)
		}
	case "object":
		if _, ok := parsed.(map[string]any); !ok {
			return nil, fmt.Errorf("%s must be a JSON object.", optionName)
		}
	}

	return json.RawMessage(strings.TrimSpace(contents)), nil
}

// readTextOption returns value, or the contents of a file when value starts
// with @. The file is read as text and not parsed.
func readTextOption(value, optionName string) (string, error) {
	if !strings.HasPrefix(value, "@") {
		return value, nil
	}

	return readOptionFile(value[1:], optionName)
}

// readOptionFile reads an @file option value. It is a local convenience for
// trusted user input.
func readOptionFile(path, optionName string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("Unable to read file for %s: %w", optionName, err)
	}

	if info.Size() > maxJSONOptionFileSize {
		return "", fmt.Errorf(
			"Unable to read file for %s: File for %s is too large (%d bytes). Maximum allowed size is %d bytes.",
			optionName, optionName, info.Size(), maxJSONOptionFileSize,
		)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("Unable to read file for %s: %w", optionName, err)
	}

	return string(contents), nil
}

// addListFlags registers the sorting and pagination flags of list commands
// that use --sort-field and --sort-direction. Their choices are checked as
// Commander checks them, from commanderOptionChoices.
func addListFlags(cmd *cobra.Command) {
	cmd.Flags().String("sort-field", "", "Sort field")
	cmd.Flags().String("sort-direction", "", "Sort direction")
	addPaginationFlags(cmd)
}

// addPaginationFlags registers --pagination-offset and --pagination-page-size.
func addPaginationFlags(cmd *cobra.Command) {
	cmd.Flags().String("pagination-offset", "", "Pagination offset")
	cmd.Flags().String("pagination-page-size", "", "Pagination page size")
}

// listQuery holds the parsed sorting and pagination values.
type listQuery struct {
	Sorting  *string
	Offset   *int
	PageSize *int
}

// parseListFlags reads the list flags. Sorting is the field name, or -field
// when the direction is desc.
func parseListFlags(cmd *cobra.Command) (listQuery, error) {
	var q listQuery

	pagination, err := parsePaginationFlags(cmd)
	if err != nil {
		return q, err
	}
	q.Offset, q.PageSize = pagination.Offset, pagination.PageSize

	field := stringFlag(cmd, "sort-field")
	direction := stringFlag(cmd, "sort-direction")
	if flagSet(cmd, "sort-direction") && !flagSet(cmd, "sort-field") {
		return q, errors.New("--sort-direction requires --sort-field.")
	}
	if flagSet(cmd, "sort-field") {
		sorting := field
		if direction == "desc" {
			sorting = "-" + field
		}
		q.Sorting = &sorting
	}

	return q, nil
}

// parsePaginationFlags parses --pagination-offset and --pagination-page-size.
func parsePaginationFlags(cmd *cobra.Command) (listQuery, error) {
	var q listQuery

	if flagSet(cmd, "pagination-offset") {
		offset, err := parsePaginationOffset(stringFlag(cmd, "pagination-offset"))
		if err != nil {
			return q, err
		}
		q.Offset = &offset
	}

	if flagSet(cmd, "pagination-page-size") {
		size, err := parsePositiveInt(stringFlag(cmd, "pagination-page-size"))
		if err != nil {
			return q, err
		}
		q.PageSize = &size
	}

	return q, nil
}

// UsageError is an error in how a command was called. Like Commander,
// the CLI prints a pointer to --help after it.
type UsageError struct {
	Message string
}

func (e *UsageError) Error() string {
	return e.Message
}

var nonNegativeInteger = regexp.MustCompile(`^\d+$`)

// parsePaginationOffset accepts a non-negative integer.
func parsePaginationOffset(value string) (int, error) {
	if !nonNegativeInteger.MatchString(value) {
		return 0, errors.New("--pagination-offset must be a non-negative integer.")
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, errors.New("--pagination-offset must be a non-negative integer.")
	}

	return parsed, nil
}

// parsePositiveInt matches the TypeScript CLI's parsePositiveInt, which uses
// parseInt and so accepts a leading integer such as "3abc".
func parsePositiveInt(value string) (int, error) {
	trimmed := strings.TrimLeft(value, " \t\n\r")
	end := 0
	for end < len(trimmed) && (trimmed[end] >= '0' && trimmed[end] <= '9' || (end == 0 && (trimmed[end] == '-' || trimmed[end] == '+'))) {
		end++
	}

	parsed, err := strconv.Atoi(trimmed[:end])
	if err != nil || parsed <= 0 {
		return 0, errors.New("Value must be a positive integer.")
	}

	return parsed, nil
}

var pfidPattern = regexp.MustCompile(`^[0-7][0-9abcdefghjkmnpqrstvwxyz]{31}$`)

// isPfid matches isPfid from @prefactor/pfid.
func isPfid(value string) bool {
	return pfidPattern.MatchString(value)
}

// validateOptionalPfid checks value is a PFID when it is set.
func validateOptionalPfid(value, optionName string) error {
	if value == "" || isPfid(value) {
		return nil
	}

	return fmt.Errorf("%s must be a valid Prefactor ID.", optionName)
}

// parseIDList splits a comma-separated list of IDs, dropping blanks, and
// fails when none remain.
func parseIDList(value, optionName string) ([]string, error) {
	var ids []string
	for _, part := range strings.Split(value, ",") {
		if id := strings.TrimSpace(part); id != "" {
			ids = append(ids, id)
		}
	}

	if len(ids) == 0 {
		return nil, fmt.Errorf("%s requires at least one ID.", optionName)
	}

	return ids, nil
}

var validTokenScopes = []string{"account", "agent_deployment"}

// validateTokenScope checks --token-scope is a known scope.
func validateTokenScope(scope string) error {
	for _, valid := range validTokenScopes {
		if scope == valid {
			return nil
		}
	}

	return fmt.Errorf("Invalid --token-scope '%s'. Allowed values: %s.", scope, strings.Join(validTokenScopes, ", "))
}

// validateTokenCreateOptions checks the agent and environment flags match the
// token scope.
func validateTokenCreateOptions(tokenScope, agentID, environmentID string) error {
	hasAgentID := strings.TrimSpace(agentID) != ""
	hasEnvironmentID := strings.TrimSpace(environmentID) != ""

	switch tokenScope {
	case "account":
		if hasAgentID || hasEnvironmentID {
			return errors.New("--agent-id and --environment-id must be omitted when --token-scope is 'account'.")
		}
	case "agent_deployment":
		if !hasAgentID || !hasEnvironmentID {
			return errors.New("--agent-id and --environment-id are required when --token-scope is 'agent_deployment'.")
		}
	}

	return nil
}

// validateSpanSchemaOptions checks the span schema flags are not combined in
// ways the API rejects.
func validateSpanSchemaOptions(cmd *cobra.Command) error {
	spanSchemas := stringFlag(cmd, "span-schemas")
	spanTypeSchemas := stringFlag(cmd, "span-type-schemas")
	spanResultSchemas := stringFlag(cmd, "span-result-schemas")

	if spanSchemas != "" && spanTypeSchemas != "" {
		return errors.New("Use only one of --span-schemas or --span-type-schemas.")
	}

	if spanResultSchemas != "" && spanSchemas == "" {
		return errors.New("--span-result-schemas can only be used with --span-schemas.")
	}

	return nil
}

// payloadSchemaFlags are the three instance payload schema flags shared by
// register and create commands, keyed by their request field.
var payloadSchemaFlags = []struct{ flag, field string }{
	{"instance-params-payload-schema", "instance_params_payload_schema"},
	{"instance-result-payload-schema", "instance_result_payload_schema"},
	{"instance-metadata-schema", "instance_metadata_schema"},
}

// addPayloadSchemaFlags registers the instance payload schema flags.
func addPayloadSchemaFlags(cmd *cobra.Command) {
	cmd.Flags().String("instance-params-payload-schema", "", "Instance params payload schema (JSON object or @file)")
	cmd.Flags().String("instance-result-payload-schema", "", "Instance result payload schema (JSON object or @file)")
	cmd.Flags().String("instance-metadata-schema", "", "Instance metadata schema (JSON object or @file)")
}

// payloadSchemaOptions parses the instance payload schema flags that are set
// into request fields.
func payloadSchemaOptions(cmd *cobra.Command) (map[string]json.RawMessage, error) {
	schemas := map[string]json.RawMessage{}
	for _, entry := range payloadSchemaFlags {
		value := stringFlag(cmd, entry.flag)
		if value == "" {
			continue
		}

		parsed, err := parseJSONOption(value, "--"+entry.flag, "object")
		if err != nil {
			return nil, err
		}
		schemas[entry.field] = parsed
	}

	return schemas, nil
}

// printDocumentContent prints a retrieve response's details.content without
// the JSON envelope.
func printDocumentContent(cmd *cobra.Command, raw []byte) error {
	var result struct {
		Details struct {
			Content any `json:"content"`
		} `json:"details"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return errors.New("Response is missing document content.")
	}

	content, ok := result.Details.Content.(string)
	if !ok {
		return errors.New("Response is missing document content.")
	}

	_, err := io.WriteString(cmd.OutOrStdout(), content)
	return err
}

// emptyObject is the {} body the TypeScript CLI sends on POST actions that
// take no input.
func emptyObject() io.Reader {
	return strings.NewReader("{}")
}

// sortingQuery sends a sorting value ("name", or "-name" for descending) as
// sorting[field] and sorting[direction], the form p2 accepts. The
// TypeScript CLI sends the documented sorting=-name form, which p2 rejects.
func sortingQuery(sorting *string) api.RequestEditorFn {
	return func(ctx context.Context, req *http.Request) error {
		if sorting == nil {
			return nil
		}
		field, direction := *sorting, "asc"
		if rest, ok := strings.CutPrefix(field, "-"); ok {
			field, direction = rest, "desc"
		}
		return withQuery(url.Values{"sorting[field]": {field}, "sorting[direction]": {direction}})(ctx, req)
	}
}

// idListQuery sends values as name[]=value, the form p2 accepts for list
// parameters. The TypeScript CLI repeats name=value, which p2 rejects.
func idListQuery(name string, ids []string) api.RequestEditorFn {
	return withQuery(url.Values{name + "[]": ids})
}
