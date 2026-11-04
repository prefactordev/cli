package cli

import (
	"context"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

func newAgentInstanceAssociationsCommand(app *App) *cobra.Command {
	associations := &cobra.Command{
		Use:   "agent-instance-associations",
		Short: "Read associations between agent instances",
	}

	listFrom := &cobra.Command{
		Use:   "list-from",
		Short: "List associations from agent instances",
		Args:  cobra.NoArgs,
	}
	listFrom.Flags().String("agent-instance-ids", "", "Comma-separated agent instance IDs (the judge runs)")
	_ = listFrom.MarkFlagRequired("agent-instance-ids")
	listFromOptions := addListFlags(listFrom, "id")
	listFrom.RunE = func(cmd *cobra.Command, _ []string) error {
		ids, q, err := parseListFlagsWithIDs(cmd, listFromOptions, "agent-instance-ids")
		if err != nil {
			return err
		}

		params := &api.QueryAgentInstanceAssociationListFromParams{
			AgentInstanceIds:   ids,
			Sorting:            q.Sorting,
			PaginationOffset:   q.Offset,
			PaginationPageSize: q.PageSize,
		}
		return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
			return c.QueryAgentInstanceAssociationListFrom(ctx, params)
		})
	}
	associations.AddCommand(listFrom)

	listTo := &cobra.Command{
		Use:   "list-to",
		Short: "List associations to an agent instance",
		Args:  cobra.NoArgs,
	}
	listTo.Flags().String("agent-instance-id", "", "Judged agent instance ID")
	_ = listTo.MarkFlagRequired("agent-instance-id")
	listToOptions := addListFlags(listTo, "id")
	listTo.RunE = func(cmd *cobra.Command, _ []string) error {
		q, err := listToOptions.parse(cmd)
		if err != nil {
			return err
		}

		params := &api.QueryAgentInstanceAssociationListToParams{
			AgentInstanceId:    stringFlag(cmd, "agent-instance-id"),
			Sorting:            q.Sorting,
			PaginationOffset:   q.Offset,
			PaginationPageSize: q.PageSize,
		}
		return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
			return c.QueryAgentInstanceAssociationListTo(ctx, params)
		})
	}
	associations.AddCommand(listTo)

	associations.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve an agent instance association",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentInstanceAssociationGetDetails(ctx, args[0], &api.QueryAgentInstanceAssociationGetDetailsParams{})
			})
		},
	})

	return associations
}
