package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
	"github.com/prefactordev/cli/internal/output"
)

var agentInstanceStatuses = []string{"preparing", "pending", "active", "complete", "failed", "cancelled", "terminated"}

// parseStatuses splits --statuses and checks each one is a known status.
func parseStatuses(value string) ([]string, error) {
	ids, err := parseIDList(value, "--statuses")
	if err != nil {
		return nil, err
	}

	for _, status := range ids {
		if !slices.Contains(agentInstanceStatuses, status) {
			return nil, fmt.Errorf("--statuses has an unknown status '%s'. Allowed: %s.", status, strings.Join(agentInstanceStatuses, ", "))
		}
	}

	return ids, nil
}

// newIdempotencyKey returns a random UUID v4, like crypto.randomUUID in
// ensureIdempotencyKey from @prefactor/core.
func newIdempotencyKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// optionalJSONObject parses flag as a JSON object (or @file) into body[field]
// when the flag is non-empty.
func optionalJSONObject(cmd *cobra.Command, body map[string]any, flag, field string) error {
	value := stringFlag(cmd, flag)
	if value == "" {
		return nil
	}

	parsed, err := parseJSONOption(value, "--"+flag, "object")
	if err != nil {
		return err
	}
	body[field] = parsed
	return nil
}

// optionalString copies a non-empty string flag into body[field].
func optionalString(cmd *cobra.Command, body map[string]any, flag, field string) {
	if value := stringFlag(cmd, flag); value != "" {
		body[field] = value
	}
}

// extractAgentContextBody returns agent_context.body from an agent context
// response.
func extractAgentContextBody(raw []byte) (json.RawMessage, error) {
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, errors.New("Response missing agent_context.")
	}

	agentContext := bytes.TrimSpace(result["agent_context"])
	if len(agentContext) == 0 || agentContext[0] != '{' {
		return nil, errors.New("Response missing agent_context.")
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(agentContext, &fields); err != nil {
		return nil, errors.New("Response missing agent_context.")
	}

	body, ok := fields["body"]
	if !ok {
		return nil, errors.New("Response missing agent_context.body.")
	}

	return body, nil
}

func newAgentInstancesCommand(app *App) *cobra.Command {
	agentInstances := &cobra.Command{Use: "agent-instances", Short: "Manage agent instances"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List agent instances",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			agentID := stringFlag(cmd, "agent-id")
			params := &api.QueryAgentInstanceListParams{AgentId: &agentID}
			var statuses []string
			if flagSet(cmd, "statuses") {
				var err error
				if statuses, err = parseStatuses(stringFlag(cmd, "statuses")); err != nil {
					return err
				}
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentInstanceList(ctx, params, idListQuery("statuses", statuses))
			})
		},
	}
	list.Flags().String("agent-id", "", "Agent ID")
	list.Flags().String("statuses", "", "Comma-separated statuses to include ("+strings.Join(agentInstanceStatuses, ", ")+")")
	_ = list.MarkFlagRequired("agent-id")
	agentInstances.AddCommand(list)

	agentInstances.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve agent instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentInstanceGetDetailsById(ctx, args[0], &api.QueryAgentInstanceGetDetailsByIdParams{})
			})
		},
	})

	show := &cobra.Command{
		Use:   "show",
		Short: "Show agent instance by id or external identifier",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			agentInstanceID := stringFlag(cmd, "agent-instance-id")
			externalIdentifier := stringFlag(cmd, "external-identifier")
			if agentInstanceID == "" && externalIdentifier == "" {
				return errors.New("Specify --agent-instance-id or --external-identifier.")
			}

			params := &api.QueryAgentInstanceGetDetailsByLookupParams{}
			if agentInstanceID != "" {
				params.AgentInstanceId = &agentInstanceID
			}
			if externalIdentifier != "" {
				params.ExternalIdentifier = &externalIdentifier
			}
			yes := true
			if boolFlag(cmd, "include-counts") {
				params.IncludeCounts = &yes
			}
			if boolFlag(cmd, "include-costs") {
				params.IncludeCosts = &yes
			}
			if boolFlag(cmd, "include-risk-score") {
				params.IncludeRiskScore = &yes
			}
			if boolFlag(cmd, "include-alert-count") {
				params.IncludeAlertCount = &yes
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentInstanceGetDetailsByLookup(ctx, params)
			})
		},
	}
	show.Flags().String("agent-instance-id", "", "Agent instance ID")
	show.Flags().String("external-identifier", "", "External identifier")
	show.Flags().Bool("include-counts", false, "Include span counts")
	show.Flags().Bool("include-costs", false, "Include cost breakdown")
	show.Flags().Bool("include-risk-score", false, "Include risk score")
	show.Flags().Bool("include-alert-count", false, "Include raised alert count")
	agentInstances.AddCommand(show)

	agentContext := &cobra.Command{
		Use:   "agent-context <id>",
		Short: "Export debugging context JSON for an agent instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			call := func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentInstanceGetAgentContext(ctx, args[0], &api.QueryAgentInstanceGetAgentContextParams{})
			}

			outputPath := stringFlag(cmd, "output")
			if outputPath == "" {
				return app.runAPI(cmd, call)
			}

			raw, err := app.callAPI(cmd, call)
			if err != nil {
				return err
			}

			body, err := extractAgentContextBody(raw)
			if err != nil {
				return err
			}

			formatted, err := output.Format(body)
			if err != nil {
				return err
			}
			if err := os.WriteFile(outputPath, append(formatted, '\n'), 0o644); err != nil {
				return err
			}
			if app.jsonOutput() {
				return app.printJSONValue(cmd, map[string]string{"output": outputPath})
			}
			return nil
		},
	}
	agentContext.Flags().String("output", "", "Write context JSON body to this file")
	agentInstances.AddCommand(agentContext)

	register := &cobra.Command{
		Use:   "register",
		Short: "Register agent instance",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateSpanSchemaOptions(cmd); err != nil {
				return err
			}
			id := stringFlag(cmd, "id")
			if err := validateOptionalPfid(id, "--id"); err != nil {
				return err
			}

			agentVersion := map[string]any{
				"external_identifier": stringFlag(cmd, "agent-version-external-identifier"),
				"name":                stringFlag(cmd, "agent-version-name"),
			}
			optionalString(cmd, agentVersion, "agent-version-description", "description")

			schemaVersion := map[string]any{
				"external_identifier": stringFlag(cmd, "agent-schema-version-external-identifier"),
			}
			if err := optionalJSONObject(cmd, schemaVersion, "span-schemas", "span_schemas"); err != nil {
				return err
			}
			if value := stringFlag(cmd, "span-type-schemas"); value != "" {
				parsed, err := parseJSONOption(value, "--span-type-schemas", "array")
				if err != nil {
					return err
				}
				schemaVersion["span_type_schemas"] = parsed
			}
			if err := optionalJSONObject(cmd, schemaVersion, "span-result-schemas", "span_result_schemas"); err != nil {
				return err
			}
			schemas, err := payloadSchemaOptions(cmd)
			if err != nil {
				return err
			}
			for field, schema := range schemas {
				schemaVersion[field] = schema
			}

			body := map[string]any{
				"agent_id":             stringFlag(cmd, "agent-id"),
				"agent_version":        agentVersion,
				"agent_schema_version": schemaVersion,
			}
			optionalString(cmd, body, "environment-id", "environment_id")
			optionalString(cmd, body, "purpose", "purpose")
			if err := optionalJSONObject(cmd, body, "params-payload", "params_payload"); err != nil {
				return err
			}
			if err := optionalJSONObject(cmd, body, "metadata", "metadata"); err != nil {
				return err
			}
			if id != "" {
				body["id"] = id
			}
			if boolFlag(cmd, "update-current-version") {
				body["update_current_version"] = true
			}
			body["idempotency_key"] = newIdempotencyKey()

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				reader, err := jsonBody(body)
				if err != nil {
					return nil, err
				}
				return c.ActionAgentInstanceRegisterWithBody(ctx, "application/json", reader)
			})
		},
	}
	register.Flags().String("agent-id", "", "Agent ID")
	register.Flags().String("agent-version-external-identifier", "", "Agent version external identifier")
	register.Flags().String("agent-version-name", "", "Agent version name")
	register.Flags().String("agent-version-description", "", "Agent version description")
	register.Flags().String("agent-schema-version-external-identifier", "", "Agent schema version external identifier")
	register.Flags().String("span-schemas", "", "JSON object or @file")
	register.Flags().String("span-type-schemas", "", "JSON array or @file")
	register.Flags().String("span-result-schemas", "", "JSON object or @file")
	register.Flags().String("environment-id", "", "Environment ID")
	register.Flags().String("purpose", "", "Why the run happened")
	addPayloadSchemaFlags(register)
	register.Flags().String("params-payload", "", "Run input: JSON object or @file")
	register.Flags().String("metadata", "", "Run metadata: JSON object or @file")
	register.Flags().String("id", "", "Agent instance ID")
	register.Flags().Bool("update-current-version", false, "Update current version")
	_ = register.MarkFlagRequired("agent-id")
	_ = register.MarkFlagRequired("agent-version-external-identifier")
	_ = register.MarkFlagRequired("agent-version-name")
	_ = register.MarkFlagRequired("agent-schema-version-external-identifier")
	agentInstances.AddCommand(register)

	start := &cobra.Command{
		Use:   "start <id>",
		Short: "Start agent instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{}
			optionalString(cmd, body, "timestamp", "timestamp")
			body["idempotency_key"] = newIdempotencyKey()

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				reader, err := jsonBody(body)
				if err != nil {
					return nil, err
				}
				return c.ActionAgentInstanceStartWithBody(ctx, args[0], "application/json", reader)
			})
		},
	}
	start.Flags().String("timestamp", "", "Timestamp")
	agentInstances.AddCommand(start)

	finish := &cobra.Command{
		Use:   "finish <id>",
		Short: "Finish agent instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{}
			optionalString(cmd, body, "timestamp", "timestamp")
			optionalString(cmd, body, "status", "status")
			if err := optionalJSONObject(cmd, body, "result-payload", "result_payload"); err != nil {
				return err
			}
			body["idempotency_key"] = newIdempotencyKey()

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				reader, err := jsonBody(body)
				if err != nil {
					return nil, err
				}
				return c.ActionAgentInstanceFinishWithBody(ctx, args[0], "application/json", reader)
			})
		},
	}
	finish.Flags().String("timestamp", "", "Timestamp")
	finish.Flags().String("status", "", "Status")
	finish.Flags().String("result-payload", "", "Run result: JSON object or @file. Recorded on complete or failed; ignored on cancelled")
	agentInstances.AddCommand(finish)

	prepare := &cobra.Command{
		Use:   "prepare <id>",
		Short: "Set the input of a preparing instance and move it to pending",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			paramsPayload, err := parseJSONOption(stringFlag(cmd, "params-payload"), "--params-payload", "object")
			if err != nil {
				return err
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				reader, err := jsonBody(map[string]any{"params_payload": paramsPayload})
				if err != nil {
					return nil, err
				}
				return c.ActionAgentInstancePrepareWithBody(ctx, args[0], "application/json", reader)
			})
		},
	}
	prepare.Flags().String("params-payload", "", "Run input: JSON object or @file")
	_ = prepare.MarkFlagRequired("params-payload")
	agentInstances.AddCommand(prepare)

	recordQuality := &cobra.Command{
		Use:   "record-quality <id>",
		Short: "Record a named quality payload on an instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var payload any
			if value := stringFlag(cmd, "payload"); value != "null" {
				parsed, err := parseJSONOption(value, "--payload", "object")
				if err != nil {
					return err
				}
				payload = parsed
			}

			body := map[string]any{
				"name":            stringFlag(cmd, "name"),
				"payload":         payload,
				"idempotency_key": newIdempotencyKey(),
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				reader, err := jsonBody(body)
				if err != nil {
					return nil, err
				}
				return c.ActionAgentInstanceRecordQualityWithBody(ctx, args[0], "application/json", reader)
			})
		},
	}
	recordQuality.Flags().String("name", "", "Quality schema name")
	recordQuality.Flags().String("payload", "", "Quality payload: JSON object or @file, or null to clear")
	_ = recordQuality.MarkFlagRequired("name")
	_ = recordQuality.MarkFlagRequired("payload")
	agentInstances.AddCommand(recordQuality)

	agentInstances.AddCommand(&cobra.Command{
		Use:   "discard-sensitive <id>",
		Short: "Permanently delete the stored sensitive values in an instance's input, result, and metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionAgentInstanceDiscardSensitiveWithBody(ctx, args[0], "application/json", emptyObject())
			})
		},
	})

	terminate := &cobra.Command{
		Use:   "terminate <id>",
		Short: "Terminate agent instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{"reason": stringFlag(cmd, "reason")}
			optionalString(cmd, body, "timestamp", "timestamp")
			body["idempotency_key"] = newIdempotencyKey()

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				reader, err := jsonBody(body)
				if err != nil {
					return nil, err
				}
				return c.ActionAgentInstanceTerminateWithBody(ctx, args[0], "application/json", reader)
			})
		},
	}
	terminate.Flags().String("reason", "", "Termination reason")
	terminate.Flags().String("timestamp", "", "Timestamp")
	_ = terminate.MarkFlagRequired("reason")
	agentInstances.AddCommand(terminate)

	return agentInstances
}
