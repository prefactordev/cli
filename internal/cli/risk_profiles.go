package cli

import (
	"context"
	"errors"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

func newRiskProfilesCommand(app *App) *cobra.Command {
	riskProfiles := &cobra.Command{Use: "risk-profiles", Short: "Manage risk profiles"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List risk profiles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q, err := parseSortingListFlags(cmd)
			if err != nil {
				return err
			}
			params := &api.QueryRiskProfileListParams{Sorting: q.Sorting, PaginationOffset: q.Offset, PaginationPageSize: q.PageSize}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryRiskProfileList(ctx, params)
			})
		},
	}
	addSortingListFlags(list)
	riskProfiles.AddCommand(list)

	riskProfiles.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve risk profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryRiskProfileGetDetails(ctx, args[0], &api.QueryRiskProfileGetDetailsParams{})
			})
		},
	})

	template := &cobra.Command{
		Use:   "template",
		Short: "Get a risk profile template ruleset",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			params := &api.QueryRiskProfileGetTemplateRulesetParams{TemplateName: stringFlag(cmd, "template-name")}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryRiskProfileGetTemplateRuleset(ctx, params)
			})
		},
	}
	template.Flags().String("template-name", "", "Template name")
	_ = template.MarkFlagRequired("template-name")
	riskProfiles.AddCommand(template)

	create := &cobra.Command{
		Use:   "create",
		Short: "Create risk profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id := stringFlag(cmd, "id")
			if err := validateOptionalPfid(id, "--id"); err != nil {
				return err
			}

			ruleset, err := parseJSONOption(stringFlag(cmd, "ruleset"), "--ruleset", "object")
			if err != nil {
				return err
			}

			details := map[string]any{
				"name":    stringFlag(cmd, "name"),
				"ruleset": ruleset,
			}
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
				return c.ActionRiskProfileCreateWithBody(ctx, "application/json", body)
			})
		},
	}
	create.Flags().String("name", "", "Risk profile name")
	create.Flags().String("ruleset", "", "JSON object or @file")
	create.Flags().String("description", "", "Description")
	create.Flags().String("id", "", "Risk profile ID")
	_ = create.MarkFlagRequired("name")
	_ = create.MarkFlagRequired("ruleset")
	riskProfiles.AddCommand(create)

	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update risk profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !flagSet(cmd, "name") && !flagSet(cmd, "description") && !flagSet(cmd, "ruleset") {
				return errors.New("No fields provided to update. Specify --name, --description, or --ruleset.")
			}

			details := map[string]any{}
			for _, field := range []string{"name", "description"} {
				if flagSet(cmd, field) {
					details[field] = stringFlag(cmd, field)
				}
			}
			if flagSet(cmd, "ruleset") {
				ruleset, err := parseJSONOption(stringFlag(cmd, "ruleset"), "--ruleset", "object")
				if err != nil {
					return err
				}
				details["ruleset"] = ruleset
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": details})
				if err != nil {
					return nil, err
				}
				return c.ActionRiskProfileUpdateWithBody(ctx, args[0], "application/json", body)
			})
		},
	}
	update.Flags().String("name", "", "Risk profile name")
	update.Flags().String("description", "", "Description")
	update.Flags().String("ruleset", "", "JSON object or @file")
	riskProfiles.AddCommand(update)

	riskProfiles.AddCommand(&cobra.Command{
		Use:   "delete <id>",
		Short: "Delete risk profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionRiskProfileDeleteWithBody(ctx, args[0], "application/json", nil)
			})
		},
	})

	return riskProfiles
}
