package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

// clearCurrentVersionSentinel is the --current-version-id value that clears
// a deployment's pinned version.
const clearCurrentVersionSentinel = "null"

func newAgentDeploymentsCommand(app *App) *cobra.Command {
	deployments := &cobra.Command{Use: "agent-deployments", Short: "Manage agent deployments"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List agent deployments",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			agentID := stringFlag(cmd, "agent-id")
			q, err := parseListFlags(cmd)
			if err != nil {
				return err
			}
			params := &api.QueryAgentDeploymentListParams{AgentId: &agentID, PaginationOffset: q.Offset, PaginationPageSize: q.PageSize}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentDeploymentList(ctx, params, sortingQuery(q.Sorting))
			})
		},
	}
	list.Flags().String("agent-id", "", "Agent ID")
	addListFlags(list, "id")
	_ = list.MarkFlagRequired("agent-id")
	deployments.AddCommand(list)

	deployments.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve agent deployment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentDeploymentGetDetails(ctx, args[0], &api.QueryAgentDeploymentGetDetailsParams{})
			})
		},
	})

	create := &cobra.Command{
		Use:   "create",
		Short: "Create agent deployment",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deploymentID := strings.TrimSpace(stringFlag(cmd, "id"))
			currentVersionID := strings.TrimSpace(stringFlag(cmd, "current-version-id"))
			if err := validateOptionalPfid(deploymentID, "--id"); err != nil {
				return err
			}

			details := map[string]any{
				"agent_id":       stringFlag(cmd, "agent-id"),
				"environment_id": stringFlag(cmd, "environment-id"),
			}
			if deploymentID != "" {
				details["id"] = deploymentID
			}
			if currentVersionID != "" {
				details["current_version_id"] = currentVersionID
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return jsonRequest(map[string]any{"details": details}, func(body io.Reader) (*http.Response, error) {
					return c.ActionAgentDeploymentCreateWithBody(ctx, "application/json", body)
				})
			})
		},
	}
	create.Flags().String("agent-id", "", "Agent ID")
	create.Flags().String("environment-id", "", "Environment ID")
	create.Flags().String("id", "", "Deployment ID")
	create.Flags().String("current-version-id", "", "Current agent version ID")
	_ = create.MarkFlagRequired("agent-id")
	_ = create.MarkFlagRequired("environment-id")
	deployments.AddCommand(create)

	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update agent deployment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			currentVersionID := strings.TrimSpace(stringFlag(cmd, "current-version-id"))
			if currentVersionID == "" {
				return errors.New("No update fields provided; pass --current-version-id.")
			}

			var pinned any = currentVersionID
			if currentVersionID == clearCurrentVersionSentinel {
				pinned = nil
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return jsonRequest(map[string]any{"details": map[string]any{"current_version_id": pinned}}, func(body io.Reader) (*http.Response, error) {
					return c.ActionAgentDeploymentUpdateWithBody(ctx, args[0], "application/json", body)
				})
			})
		},
	}
	update.Flags().String("current-version-id", "", "Current agent version ID ("+clearCurrentVersionSentinel+" clears the pin)")
	deployments.AddCommand(update)

	deployments.AddCommand(&cobra.Command{
		Use:   "delete <id>",
		Short: "Delete agent deployment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionAgentDeploymentDeleteWithBody(ctx, args[0], "application/json", nil)
			})
		},
	})

	listPromotions := &cobra.Command{
		Use:   "list-promotions",
		Short: "List deployments of a hosted agent that can move to the next environment",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			agentID := stringFlag(cmd, "agent-id")
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentDeploymentListPromotions(ctx, &api.QueryAgentDeploymentListPromotionsParams{AgentId: &agentID})
			})
		},
	}
	listPromotions.Flags().String("agent-id", "", "Agent ID")
	_ = listPromotions.MarkFlagRequired("agent-id")
	deployments.AddCommand(listPromotions)

	deployments.AddCommand(&cobra.Command{
		Use:   "promote <id>",
		Short: "Pin this deployment's current version in the next environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionAgentDeploymentPromoteWithBody(ctx, args[0], "application/json", emptyObject())
			})
		},
	})

	return deployments
}
