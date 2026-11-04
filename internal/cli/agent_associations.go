package cli

import (
	"context"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

func newAgentAssociationsCommand(app *App) *cobra.Command {
	associations := &cobra.Command{
		Use:   "agent-associations",
		Short: "Manage associations between a judge and an agent",
	}

	listFrom := &cobra.Command{
		Use:   "list-from",
		Short: "List associations from judges",
		Args:  cobra.NoArgs,
	}
	listFrom.Flags().String("agent-ids", "", "Comma-separated judge agent IDs")
	_ = listFrom.MarkFlagRequired("agent-ids")
	addListFlags(listFrom)
	listFrom.RunE = func(cmd *cobra.Command, _ []string) error {
		ids, q, err := parseListFlagsWithIDs(cmd, "agent-ids")
		if err != nil {
			return err
		}

		params := &api.QueryAgentAssociationListFromParams{
			PaginationOffset:   q.Offset,
			PaginationPageSize: q.PageSize,
		}
		return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
			return c.QueryAgentAssociationListFrom(ctx, params, idListQuery("agent_ids", ids), sortingQuery(q.Sorting))
		})
	}
	associations.AddCommand(listFrom)

	listTo := &cobra.Command{
		Use:   "list-to",
		Short: "List associations to an agent",
		Args:  cobra.NoArgs,
	}
	listTo.Flags().String("agent-id", "", "Agent ID")
	_ = listTo.MarkFlagRequired("agent-id")
	addListFlags(listTo)
	listTo.RunE = func(cmd *cobra.Command, _ []string) error {
		q, err := parseListFlags(cmd)
		if err != nil {
			return err
		}

		params := &api.QueryAgentAssociationListToParams{
			AgentId:            stringFlag(cmd, "agent-id"),
			PaginationOffset:   q.Offset,
			PaginationPageSize: q.PageSize,
		}
		return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
			return c.QueryAgentAssociationListTo(ctx, params, sortingQuery(q.Sorting))
		})
	}
	associations.AddCommand(listTo)

	associations.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve an agent association",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentAssociationGetDetails(ctx, args[0], &api.QueryAgentAssociationGetDetailsParams{})
			})
		},
	})

	create := &cobra.Command{
		Use:   "create",
		Short: "Create an agent association",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			trigger := stringFlag(cmd, "trigger")
			id := stringFlag(cmd, "id")
			if err := validateOptionalPfid(id, "--id"); err != nil {
				return err
			}

			details := map[string]any{
				"from_agent_id":    stringFlag(cmd, "from-agent-id"),
				"to_agent_id":      stringFlag(cmd, "to-agent-id"),
				"association_type": stringFlag(cmd, "association-type"),
				"trigger":          trigger,
			}
			if id != "" {
				details["id"] = id
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return jsonRequest(map[string]any{"details": details}, func(body io.Reader) (*http.Response, error) {
					return c.ActionAgentAssociationCreateWithBody(ctx, "application/json", body)
				})
			})
		},
	}
	create.Flags().String("from-agent-id", "", "Judge agent ID")
	create.Flags().String("to-agent-id", "", "Agent the judge scores")
	create.Flags().String("association-type", "", "Association type")
	create.Flags().String("trigger", "", "When the judge runs (on_demand, on_completion)")
	create.Flags().String("id", "", "Association ID")
	_ = create.MarkFlagRequired("from-agent-id")
	_ = create.MarkFlagRequired("to-agent-id")
	_ = create.MarkFlagRequired("association-type")
	_ = create.MarkFlagRequired("trigger")
	associations.AddCommand(create)

	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an agent association trigger",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			trigger := stringFlag(cmd, "trigger")

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return jsonRequest(map[string]any{"details": map[string]any{"trigger": trigger}}, func(body io.Reader) (*http.Response, error) {
					return c.ActionAgentAssociationUpdateWithBody(ctx, args[0], "application/json", body)
				})
			})
		},
	}
	update.Flags().String("trigger", "", "When the judge runs (on_demand, on_completion)")
	_ = update.MarkFlagRequired("trigger")
	associations.AddCommand(update)

	associations.AddCommand(&cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an agent association",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionAgentAssociationDeleteWithBody(ctx, args[0], "application/json", nil)
			})
		},
	})

	return associations
}
