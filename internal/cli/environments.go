package cli

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

func newEnvironmentsCommand(app *App) *cobra.Command {
	environments := &cobra.Command{Use: "environments", Short: "Manage environments"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List environments",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			accountID := stringFlag(cmd, "account-id")
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryEnvironmentList(ctx, &api.QueryEnvironmentListParams{AccountId: &accountID})
			})
		},
	}
	list.Flags().String("account-id", "", "Account ID")
	_ = list.MarkFlagRequired("account-id")
	environments.AddCommand(list)

	environments.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryEnvironmentGetDetailsById(ctx, args[0], &api.QueryEnvironmentGetDetailsByIdParams{})
			})
		},
	})

	show := &cobra.Command{
		Use:   "show",
		Short: "Show environment by id or external identifier",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			environmentID := stringFlag(cmd, "environment-id")
			externalIdentifier := stringFlag(cmd, "external-identifier")
			if environmentID == "" && externalIdentifier == "" {
				return errors.New("Specify --environment-id or --external-identifier.")
			}

			params := &api.QueryEnvironmentGetDetailsByLookupParams{}
			if environmentID != "" {
				params.EnvironmentId = &environmentID
			}
			if externalIdentifier != "" {
				params.ExternalIdentifier = &externalIdentifier
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryEnvironmentGetDetailsByLookup(ctx, params)
			})
		},
	}
	show.Flags().String("environment-id", "", "Environment ID")
	show.Flags().String("external-identifier", "", "External identifier")
	environments.AddCommand(show)

	create := &cobra.Command{
		Use:   "create",
		Short: "Create environment",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id := stringFlag(cmd, "id")
			if err := validateOptionalPfid(id, "--id"); err != nil {
				return err
			}

			details := map[string]any{
				"name":       stringFlag(cmd, "name"),
				"account_id": stringFlag(cmd, "account-id"),
			}
			if id != "" {
				details["id"] = id
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": details})
				if err != nil {
					return nil, err
				}
				return c.ActionEnvironmentCreateWithBody(ctx, "application/json", body)
			})
		},
	}
	create.Flags().String("name", "", "Environment name")
	create.Flags().String("account-id", "", "Account ID")
	create.Flags().String("id", "", "Environment ID")
	_ = create.MarkFlagRequired("name")
	_ = create.MarkFlagRequired("account-id")
	environments.AddCommand(create)

	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := stringFlag(cmd, "name")
			if strings.TrimSpace(name) == "" {
				return errors.New("No fields provided to update. Specify --name <name>.")
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": map[string]any{"name": name}})
				if err != nil {
					return nil, err
				}
				return c.ActionEnvironmentUpdateWithBody(ctx, args[0], "application/json", body)
			})
		},
	}
	update.Flags().String("name", "", "Environment name")
	environments.AddCommand(update)

	environments.AddCommand(&cobra.Command{
		Use:   "delete <id>",
		Short: "Delete environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := app.callAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionEnvironmentDeleteWithBody(ctx, args[0], "application/json", nil)
			}); err != nil {
				return err
			}
			return app.printJSONValue(cmd, map[string]bool{"ok": true})
		},
	})

	return environments
}
