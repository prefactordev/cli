package cli

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

// newJudgesCommand registers judge commands. A judge is an agent with the
// judge role.
func newJudgesCommand(app *App) *cobra.Command {
	judges := &cobra.Command{Use: "judges", Short: "Manage judge agents"}

	judges.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List judges",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			role := api.QueryAgentListParamsRoleJudge
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentList(ctx, &api.QueryAgentListParams{Role: &role})
			})
		},
	})

	judges.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve a judge",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentGetDetailsById(ctx, args[0], &api.QueryAgentGetDetailsByIdParams{})
			})
		},
	})

	show := &cobra.Command{
		Use:   "show",
		Short: "Show a judge by id or external identifier",
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
	show.Flags().String("agent-id", "", "Judge agent ID")
	show.Flags().String("external-identifier", "", "External identifier")
	show.Flags().String("environment-id", "", "Environment ID")
	show.Flags().Bool("include-counts", false, "Include instance counts")
	show.Flags().Bool("include-risk-rollup", false, "Include risk rollup")
	judges.AddCommand(show)

	create := &cobra.Command{
		Use:   "create",
		Short: "Create a judge",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			executionMode := stringFlag(cmd, "execution-mode")
			id := stringFlag(cmd, "id")
			if err := validateOptionalPfid(id, "--id"); err != nil {
				return err
			}

			details := map[string]any{
				"name":           stringFlag(cmd, "name"),
				"role":           "judge",
				"execution_mode": executionMode,
			}
			if description := stringFlag(cmd, "description"); description != "" {
				details["description"] = description
			}
			if id != "" {
				details["id"] = id
			}
			if teamID := stringFlag(cmd, "team-id"); teamID != "" {
				details["team_id"] = teamID
			}
			if ownerPersonID := stringFlag(cmd, "owner-person-id"); ownerPersonID != "" {
				details["owner_person_id"] = ownerPersonID
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return jsonRequest(map[string]any{"details": details}, func(body io.Reader) (*http.Response, error) {
					return c.ActionAgentCreateWithBody(ctx, "application/json", body)
				})
			})
		},
	}
	create.Flags().String("name", "", "Judge name")
	create.Flags().String("description", "", "Judge description")
	create.Flags().String("id", "", "Judge agent ID")
	create.Flags().String("team-id", "", "Team ID")
	create.Flags().String("owner-person-id", "", "Owner person ID")
	create.Flags().String("execution-mode", "hosted", "Expected execution")
	_ = create.MarkFlagRequired("name")
	judges.AddCommand(create)

	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a judge",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			details := map[string]any{}
			for _, option := range []struct{ flag, field string }{
				{"name", "name"},
				{"description", "description"},
				{"team-id", "team_id"},
				{"owner-person-id", "owner_person_id"},
			} {
				if flagSet(cmd, option.flag) {
					details[option.field] = stringFlag(cmd, option.flag)
				}
			}
			if len(details) == 0 {
				return errors.New("No fields provided to update. Specify --name, --description, --team-id, or --owner-person-id.")
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return jsonRequest(map[string]any{"details": details}, func(body io.Reader) (*http.Response, error) {
					return c.ActionAgentUpdateWithBody(ctx, args[0], "application/json", body)
				})
			})
		},
	}
	update.Flags().String("name", "", "Judge name")
	update.Flags().String("description", "", "Judge description")
	update.Flags().String("team-id", "", "Team ID")
	update.Flags().String("owner-person-id", "", "Owner person ID")
	judges.AddCommand(update)

	judges.AddCommand(&cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a judge",
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

	judges.AddCommand(&cobra.Command{
		Use:   "retire <id>",
		Short: "Retire a judge",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionAgentRetireWithBody(ctx, args[0], "application/json", emptyObject())
			})
		},
	})

	judges.AddCommand(&cobra.Command{
		Use:   "reinstate <id>",
		Short: "Reinstate a judge",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionAgentReinstateWithBody(ctx, args[0], "application/json", emptyObject())
			})
		},
	})

	createVersion := &cobra.Command{
		Use:   "create-version",
		Short: "Find or create a hosted judge version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			criterion, err := readTextOption(stringFlag(cmd, "criterion"), "--criterion")
			if err != nil {
				return err
			}

			details := map[string]any{
				"agent_id":                     stringFlag(cmd, "agent-id"),
				"hosted_llm_provider_model_id": stringFlag(cmd, "hosted-llm-provider-model-id"),
				"criterion":                    criterion,
			}
			if externalIdentifier := stringFlag(cmd, "external-identifier"); externalIdentifier != "" {
				details["external_identifier"] = externalIdentifier
			}
			if template := stringFlag(cmd, "system-prompt-template"); template != "" {
				contents, err := readTextOption(template, "--system-prompt-template")
				if err != nil {
					return err
				}
				details["system_prompt_template"] = contents
			}
			if err := optionalPfid(cmd, details, "id", "id"); err != nil {
				return err
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return jsonRequest(map[string]any{"details": details}, func(body io.Reader) (*http.Response, error) {
					return c.ActionAgentVersionCreateHostedJudgeWithBody(ctx, "application/json", body)
				})
			})
		},
	}
	createVersion.Flags().String("agent-id", "", "Judge agent ID")
	createVersion.Flags().String("hosted-llm-provider-model-id", "", "LLM provider model ID")
	createVersion.Flags().String("criterion", "", "Judge criterion, or @path to read a text file")
	createVersion.Flags().String("external-identifier", "", "External identifier")
	createVersion.Flags().String("system-prompt-template", "", "Liquid system prompt template, or @path to read a text file. criterion is the only variable")
	createVersion.Flags().String("id", "", "Agent version ID")
	_ = createVersion.MarkFlagRequired("agent-id")
	_ = createVersion.MarkFlagRequired("hosted-llm-provider-model-id")
	_ = createVersion.MarkFlagRequired("criterion")
	judges.AddCommand(createVersion)

	judges.AddCommand(&cobra.Command{
		Use:   "templates",
		Short: "List the built-in judge templates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAgentVersionListJudgeTemplates(ctx, &api.QueryAgentVersionListJudgeTemplatesParams{})
			})
		},
	})

	score := &cobra.Command{
		Use:   "score <agent_instance_id>",
		Short: "Ask a judge to score an agent instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			judgeAgentID := stringFlag(cmd, "judge-agent-id")
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return jsonRequest(map[string]any{"judge_agent_id": judgeAgentID}, func(body io.Reader) (*http.Response, error) {
					return c.ActionAgentInstanceJudgeWithBody(ctx, args[0], "application/json", body)
				})
			})
		},
	}
	score.Flags().String("judge-agent-id", "", "Judge that should score this instance")
	_ = score.MarkFlagRequired("judge-agent-id")
	judges.AddCommand(score)

	return judges
}
