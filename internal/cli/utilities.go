package cli

import (
	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
	"github.com/prefactordev/cli/internal/client"
	"github.com/prefactordev/cli/internal/profile"
)

func newPfidCommand(app *App) *cobra.Command {
	pfid := &cobra.Command{
		Use:   "pfid",
		Short: "Generate PFIDs",
	}

	generate := &cobra.Command{
		Use:   "generate",
		Short: "Generate PFIDs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			countValue, _ := cmd.Flags().GetString("count")
			count := 1
			if cmd.Flags().Changed("count") {
				parsed, err := parsePositiveInt(countValue)
				if err != nil {
					return err
				}
				count = parsed
			}

			apiClient, err := app.authedClient(cmd)
			if err != nil {
				return err
			}

			body := api.ActionPfidGenerateJSONRequestBody{Count: count}
			if accountID, _ := cmd.Flags().GetString("account-id"); accountID != "" {
				body.AccountId = &accountID
			}

			raw, err := client.ReadJSON(apiClient.ActionPfidGenerate(cmd.Context(), body))
			if err != nil {
				return err
			}

			return app.printResult(cmd, raw)
		},
	}
	generate.Flags().String("account-id", "", "Account ID")
	generate.Flags().String("count", "", "Number of IDs to generate")

	pfid.AddCommand(generate)
	return pfid
}

func newPingCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ping",
		Short: "Verify the selected or supplied API token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var apiClient *api.Client
			var err error

			if token, _ := cmd.Flags().GetString("api-token"); token != "" {
				apiURL, _ := cmd.Flags().GetString("api-url")
				if apiURL == "" {
					apiURL = app.Getenv("PREFACTOR_API_URL")
				}
				if apiURL == "" {
					apiURL = profile.DefaultBaseURL
				}
				apiClient, err = client.New(apiURL, token, app.Version)
			} else {
				apiClient, err = app.authedClient(cmd)
			}
			if err != nil {
				return err
			}

			raw, err := client.ReadJSON(apiClient.QueryPingGet(cmd.Context(), &api.QueryPingGetParams{}))
			if err != nil {
				return err
			}

			return app.printResult(cmd, raw)
		},
	}

	cmd.Flags().String("api-token", "", "API token to verify")
	cmd.Flags().String("api-url", "", "API URL to use with --api-token")

	return cmd
}
