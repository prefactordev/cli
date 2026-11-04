package cli

import (
	"context"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

func newAgentVersionsCommand(app *App) *cobra.Command {
	versions := &cobra.Command{Use: "agent-versions", Short: "Manage agent versions"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List agent versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			params := &api.QueryAgentVersionListParams{AgentId: stringFlag(cmd, "agent-id")}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentVersionList(ctx, params)
			})
		},
	}
	list.Flags().String("agent-id", "", "Agent ID")
	_ = list.MarkFlagRequired("agent-id")
	versions.AddCommand(list)

	versions.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve agent version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentVersionGetDetails(ctx, args[0], &api.QueryAgentVersionGetDetailsParams{})
			})
		},
	})

	create := &cobra.Command{
		Use:   "create",
		Short: "Create agent version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			details := map[string]any{
				"agent_id":            stringFlag(cmd, "agent-id"),
				"external_identifier": stringFlag(cmd, "external-identifier"),
			}
			if schemaVersionID := stringFlag(cmd, "agent-schema-version-id"); schemaVersionID != "" {
				details["agent_schema_version_id"] = schemaVersionID
			}
			if mode := stringFlag(cmd, "execution-mode"); mode != "" {
				details["execution_mode"] = mode
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return jsonRequest(map[string]any{"details": details}, func(body io.Reader) (*http.Response, error) {
					return c.ActionAgentVersionCreateWithBody(ctx, "application/json", body)
				})
			})
		},
	}
	create.Flags().String("agent-id", "", "Agent ID")
	create.Flags().String("external-identifier", "", "External identifier")
	create.Flags().String("agent-schema-version-id", "", "Agent schema version ID")
	create.Flags().String("execution-mode", "", "Where this version runs")
	_ = create.MarkFlagRequired("agent-id")
	_ = create.MarkFlagRequired("external-identifier")
	versions.AddCommand(create)

	return versions
}
