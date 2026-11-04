package cli

import (
	"context"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

func newAccountsCommand(app *App) *cobra.Command {
	accounts := &cobra.Command{Use: "accounts", Short: "Manage accounts"}

	accounts.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAccountList(ctx, &api.QueryAccountListParams{})
			})
		},
	})

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
	_ = update.MarkFlagRequired("name")
	accounts.AddCommand(update)

	return accounts
}
