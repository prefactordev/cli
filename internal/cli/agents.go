package cli

import (
	"context"
	"errors"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

func newAgentsCommand(app *App) *cobra.Command {
	agents := &cobra.Command{Use: "agents", Short: "Manage agents"}

	agents.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List agents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentList(ctx, &api.QueryAgentListParams{})
			})
		},
	})

	agents.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentGetDetailsById(ctx, args[0], &api.QueryAgentGetDetailsByIdParams{})
			})
		},
	})

	show := &cobra.Command{
		Use:   "show",
		Short: "Show agent by id or external identifier",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			agentID := stringFlag(cmd, "agent-id")
			externalIdentifier := stringFlag(cmd, "external-identifier")
			if agentID == "" && externalIdentifier == "" {
				return errors.New("Specify --agent-id or --external-identifier.")
			}

			params := &api.QueryAgentGetDetailsByLookupParams{}
			if agentID != "" {
				params.AgentId = &agentID
			}
			if externalIdentifier != "" {
				params.ExternalIdentifier = &externalIdentifier
			}
			if environmentID := stringFlag(cmd, "environment-id"); environmentID != "" {
				params.EnvironmentId = &environmentID
			}
			if boolFlag(cmd, "include-counts") {
				includeCounts := true
				params.IncludeCounts = &includeCounts
			}
			if boolFlag(cmd, "include-risk-rollup") {
				includeRiskRollup := true
				params.IncludeRiskRollup = &includeRiskRollup
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentGetDetailsByLookup(ctx, params)
			})
		},
	}
	show.Flags().String("agent-id", "", "Agent ID")
	show.Flags().String("external-identifier", "", "External identifier")
	show.Flags().String("environment-id", "", "Environment ID")
	show.Flags().Bool("include-counts", false, "Include instance counts")
	show.Flags().Bool("include-risk-rollup", false, "Include risk rollup")
	agents.AddCommand(show)

	create := &cobra.Command{
		Use:   "create",
		Short: "Create agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id := stringFlag(cmd, "id")
			if err := validateOptionalPfid(id, "--id"); err != nil {
				return err
			}

			details := map[string]any{"name": stringFlag(cmd, "name")}
			if description := stringFlag(cmd, "description"); description != "" {
				details["description"] = description
			}
			if id != "" {
				details["id"] = id
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": details})
				if err != nil {
					return nil, err
				}
				return c.ActionAgentCreateWithBody(ctx, "application/json", body)
			})
		},
	}
	create.Flags().String("name", "", "Agent name")
	create.Flags().String("description", "", "Agent description")
	create.Flags().String("id", "", "Agent ID")
	_ = create.MarkFlagRequired("name")
	agents.AddCommand(create)

	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			details := map[string]any{}
			if name := stringFlag(cmd, "name"); name != "" {
				details["name"] = name
			}
			if description := stringFlag(cmd, "description"); description != "" {
				details["description"] = description
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": details})
				if err != nil {
					return nil, err
				}
				return c.ActionAgentUpdateWithBody(ctx, args[0], "application/json", body)
			})
		},
	}
	update.Flags().String("name", "", "Agent name")
	update.Flags().String("description", "", "Agent description")
	agents.AddCommand(update)

	agents.AddCommand(&cobra.Command{
		Use:   "delete <id>",
		Short: "Delete agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := app.callAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionAgentDeleteWithBody(ctx, args[0], "application/json", nil)
			}); err != nil {
				return err
			}
			return app.printJSONValue(cmd, map[string]bool{"ok": true})
		},
	})

	agents.AddCommand(&cobra.Command{
		Use:   "retire <id>",
		Short: "Retire agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionAgentRetireWithBody(ctx, args[0], "application/json", emptyObject())
			})
		},
	})

	agents.AddCommand(&cobra.Command{
		Use:   "reinstate <id>",
		Short: "Reinstate agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionAgentReinstateWithBody(ctx, args[0], "application/json", emptyObject())
			})
		},
	})

	return agents
}
