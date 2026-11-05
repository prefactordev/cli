package cli

import (
	"context"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

func newAccountsCommand(app *App) *cobra.Command {
	accounts := &cobra.Command{Use: "accounts", Short: "Manage accounts"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q, err := parseListFlags(cmd)
			if err != nil {
				return err
			}
			params := &api.QueryAccountListParams{PaginationOffset: q.Offset, PaginationPageSize: q.PageSize}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAccountList(ctx, params, sortingQuery(q.Sorting))
			})
		},
	}
	addListFlags(list, "id", "name")
	accounts.AddCommand(list)

	accounts.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve account",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAccountGetDetails(ctx, args[0], &api.QueryAccountGetDetailsParams{})
			})
		},
	})

	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update account",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			details := map[string]any{"name": stringFlag(cmd, "name")}
			if err := fieldsBody(cmd, details, "organisation_name", "website", "purpose", "onboarding_agent_created_at", "onboarding_cli_connected_at"); err != nil {
				return err
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": details})
				if err != nil {
					return nil, err
				}
				return c.ActionAccountUpdateWithBody(ctx, args[0], "application/json", body)
			})
		},
	}
	update.Flags().String("name", "", "Account name")
	update.Flags().String("organisation-name", "", "Organisation name")
	update.Flags().String("website", "", "Organisation website")
	addChoiceFlag(update, "purpose", "What the account is for", "business", "personal", "nonprofit", "education", "government")
	update.Flags().String("onboarding-agent-created-at", "", "When onboarding created the first agent")
	update.Flags().String("onboarding-cli-connected-at", "", "When onboarding connected the CLI")
	_ = update.MarkFlagRequired("name")
	accounts.AddCommand(update)

	return accounts
}
