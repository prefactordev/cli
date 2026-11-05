package cli

import (
	"context"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

func newAgentRiskVersionsCommand(app *App) *cobra.Command {
	riskVersions := &cobra.Command{Use: "agent-risk-versions", Short: "View agent risk versions"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List agent risk versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q, err := parseListFlags(cmd)
			if err != nil {
				return err
			}
			params := &api.QueryAgentRiskVersionListParams{
				AgentId:            stringFlag(cmd, "agent-id"),
				PaginationOffset:   q.Offset,
				PaginationPageSize: q.PageSize,
			}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentRiskVersionList(ctx, params, sortingQuery(q.Sorting))
			})
		},
	}
	list.Flags().String("agent-id", "", "Agent ID")
	addListFlags(list, "id")
	_ = list.MarkFlagRequired("agent-id")
	riskVersions.AddCommand(list)

	riskVersions.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve agent risk version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentRiskVersionGetDetails(ctx, args[0], &api.QueryAgentRiskVersionGetDetailsParams{})
			})
		},
	})

	return riskVersions
}
