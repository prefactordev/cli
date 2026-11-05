package cli

import (
	"context"
	"errors"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

func newAgentKnowledgebaseLinksCommand(app *App) *cobra.Command {
	links := &cobra.Command{Use: "agent-knowledgebase-links", Short: "Manage links between agents and knowledgebases"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List agent knowledgebase links",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			agentID := stringFlag(cmd, "agent-id")
			knowledgebaseID := stringFlag(cmd, "knowledgebase-id")
			if agentID == "" && knowledgebaseID == "" {
				return errors.New("Specify --agent-id or --knowledgebase-id.")
			}
			q, err := parseListFlags(cmd)
			if err != nil {
				return err
			}

			params := &api.QueryAgentKnowledgebaseLinkListParams{PaginationOffset: q.Offset, PaginationPageSize: q.PageSize}
			if agentID != "" {
				params.AgentId = &agentID
			}
			if knowledgebaseID != "" {
				params.KnowledgebaseId = &knowledgebaseID
			}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentKnowledgebaseLinkList(ctx, params, sortingQuery(q.Sorting))
			})
		},
	}
	list.Flags().String("agent-id", "", "Agent ID")
	list.Flags().String("knowledgebase-id", "", "Knowledgebase ID")
	addListFlags(list, "id")
	links.AddCommand(list)

	links.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve agent knowledgebase link",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentKnowledgebaseLinkGetDetails(ctx, args[0], &api.QueryAgentKnowledgebaseLinkGetDetailsParams{})
			})
		},
	})

	create := &cobra.Command{
		Use:   "create",
		Short: "Link an agent to a knowledgebase",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id := stringFlag(cmd, "id")
			if err := validateOptionalPfid(id, "--id"); err != nil {
				return err
			}
			details := map[string]any{
				"agent_id":         stringFlag(cmd, "agent-id"),
				"knowledgebase_id": stringFlag(cmd, "knowledgebase-id"),
			}
			if id != "" {
				details["id"] = id
			}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": details})
				if err != nil {
					return nil, err
				}
				return c.ActionAgentKnowledgebaseLinkCreateWithBody(ctx, "application/json", body)
			})
		},
	}
	create.Flags().String("agent-id", "", "Agent ID")
	create.Flags().String("knowledgebase-id", "", "Knowledgebase ID")
	create.Flags().String("id", "", "Agent knowledgebase link ID")
	_ = create.MarkFlagRequired("agent-id")
	_ = create.MarkFlagRequired("knowledgebase-id")
	links.AddCommand(create)

	links.AddCommand(&cobra.Command{
		Use:   "delete <id>",
		Short: "Delete agent knowledgebase link",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := app.callAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionAgentKnowledgebaseLinkDeleteWithBody(ctx, args[0], "application/json", nil)
			}); err != nil {
				return err
			}
			return app.printJSONValue(cmd, map[string]bool{"ok": true})
		},
	})

	return links
}
