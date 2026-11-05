package cli

import (
	"context"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

func newAgentSchemaVersionsCommand(app *App) *cobra.Command {
	schemaVersions := &cobra.Command{Use: "agent-schema-versions", Short: "Manage agent schema versions"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List agent schema versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			agentID := stringFlag(cmd, "agent-id")
			q, err := parseListFlags(cmd)
			if err != nil {
				return err
			}
			params := &api.QueryAgentSchemaVersionListParams{AgentId: &agentID, PaginationOffset: q.Offset, PaginationPageSize: q.PageSize}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentSchemaVersionList(ctx, params, sortingQuery(q.Sorting))
			})
		},
	}
	list.Flags().String("agent-id", "", "Agent ID")
	addListFlags(list, "id", "external_identifier")
	_ = list.MarkFlagRequired("agent-id")
	schemaVersions.AddCommand(list)

	schemaVersions.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve agent schema version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentSchemaVersionGetDetails(ctx, args[0], &api.QueryAgentSchemaVersionGetDetailsParams{})
			})
		},
	})

	create := &cobra.Command{
		Use:   "create",
		Short: "Create agent schema version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateSpanSchemaOptions(cmd); err != nil {
				return err
			}

			details := map[string]any{
				"agent_id":            stringFlag(cmd, "agent-id"),
				"external_identifier": stringFlag(cmd, "external-identifier"),
			}
			for _, option := range []struct{ flag, field, expected string }{
				{"span-schemas", "span_schemas", "object"},
				{"span-type-schemas", "span_type_schemas", "array"},
				{"span-result-schemas", "span_result_schemas", "object"},
			} {
				value := stringFlag(cmd, option.flag)
				if value == "" {
					continue
				}
				parsed, err := parseJSONOption(value, "--"+option.flag, option.expected)
				if err != nil {
					return err
				}
				details[option.field] = parsed
			}

			schemas, err := payloadSchemaOptions(cmd)
			if err != nil {
				return err
			}
			for field, schema := range schemas {
				details[field] = schema
			}
			if err := validateOptionalPfid(stringFlag(cmd, "id"), "--id"); err != nil {
				return err
			}
			if err := fieldsBody(cmd, details, "id", "alert_schemas", "quality_schemas"); err != nil {
				return err
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return jsonRequest(map[string]any{"details": details}, func(body io.Reader) (*http.Response, error) {
					return c.ActionAgentSchemaVersionCreateWithBody(ctx, "application/json", body)
				})
			})
		},
	}
	create.Flags().String("agent-id", "", "Agent ID")
	create.Flags().String("external-identifier", "", "External identifier")
	create.Flags().String("span-schemas", "", "JSON object or @file")
	create.Flags().String("span-type-schemas", "", "JSON array or @file")
	create.Flags().String("span-result-schemas", "", "JSON object or @file")
	addPayloadSchemaFlags(create)
	create.Flags().String("id", "", "Agent schema version ID")
	addSchemaListFlags(create)
	_ = create.MarkFlagRequired("agent-id")
	_ = create.MarkFlagRequired("external-identifier")
	schemaVersions.AddCommand(create)

	return schemaVersions
}
