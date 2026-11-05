package cli

import (
	"context"
	"errors"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

var (
	agentRoles     = []string{"standard", "judge"}
	executionModes = []string{"hosted", "remote"}
)

// addOwnershipFlags registers the team, owner and risk profile of an agent.
func addOwnershipFlags(cmd *cobra.Command) {
	cmd.Flags().String("team-id", "", "Team ID")
	cmd.Flags().String("owner-person-id", "", "Owner person ID")
	cmd.Flags().String("risk-profile-id", "", "Risk profile ID")
}

// addTimeRangeFlags registers prefix-start-at and prefix-finish-at, the
// bounds of a time range, described as the range.
func addTimeRangeFlags(cmd *cobra.Command, prefix, rangeName string) {
	cmd.Flags().String(prefix+"-start-at", "", "Start of "+rangeName+" (ISO 8601)")
	cmd.Flags().String(prefix+"-finish-at", "", "End of "+rangeName+" (ISO 8601)")
}

func newAgentsCommand(app *App) *cobra.Command {
	agents := &cobra.Command{Use: "agents", Short: "Manage agents"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List agents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var agentIDs []string
			if flagSet(cmd, "agent-ids") {
				var err error
				if agentIDs, err = parseIDList(stringFlag(cmd, "agent-ids"), "--agent-ids"); err != nil {
					return err
				}
			}
			q, err := parseListFlags(cmd)
			if err != nil {
				return err
			}
			params := &api.QueryAgentListParams{PaginationOffset: q.Offset, PaginationPageSize: q.PageSize}
			filters := fieldsQuery(cmd, "role", "team_id", "owner_person_id", "risk_profile_id", "instances_active_during[start_at]", "instances_active_during[finish_at]")

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentList(ctx, params, filters, idListQuery("agent_ids", agentIDs), sortingQuery(q.Sorting))
			})
		},
	}
	addChoiceFlag(list, "role", "Agent role", agentRoles...)
	addOwnershipFlags(list)
	list.Flags().String("agent-ids", "", "Comma-separated agent IDs")
	addTimeRangeFlags(list, "instances-active-during", "the range instance counts cover")
	addListFlags(list, "id", "name", "status", "last_activity_span_at")
	agents.AddCommand(list)

	retrieve := &cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := fieldsQuery(cmd, "environment_id", "include_counts", "include_risk_rollup", "instances_active_during[start_at]", "instances_active_during[finish_at]")
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentGetDetailsById(ctx, args[0], &api.QueryAgentGetDetailsByIdParams{}, query)
			})
		},
	}
	retrieve.Flags().String("environment-id", "", "Environment ID")
	retrieve.Flags().Bool("include-counts", false, "Include instance counts")
	retrieve.Flags().Bool("include-risk-rollup", false, "Include risk rollup")
	addTimeRangeFlags(retrieve, "instances-active-during", "the range instance counts cover")
	agents.AddCommand(retrieve)

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

			activeDuring := fieldsQuery(cmd, "instances_active_during[start_at]", "instances_active_during[finish_at]")
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentGetDetailsByLookup(ctx, params, activeDuring)
			})
		},
	}
	show.Flags().String("agent-id", "", "Agent ID")
	show.Flags().String("external-identifier", "", "External identifier")
	show.Flags().String("environment-id", "", "Environment ID")
	show.Flags().Bool("include-counts", false, "Include instance counts")
	show.Flags().Bool("include-risk-rollup", false, "Include risk rollup")
	addTimeRangeFlags(show, "instances-active-during", "the range instance counts cover")
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
			if err := fieldsBody(cmd, details, "external_identifier", "role", "execution_mode", "team_id", "owner_person_id", "risk_profile_id"); err != nil {
				return err
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
	create.Flags().String("external-identifier", "", "External identifier")
	addChoiceFlag(create, "role", "Agent role", agentRoles...)
	addChoiceFlag(create, "execution-mode", "Where the agent runs", executionModes...)
	addOwnershipFlags(create)
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
			if err := fieldsBody(cmd, details, "team_id", "owner_person_id", "risk_profile_id"); err != nil {
				return err
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
	addOwnershipFlags(update)
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
