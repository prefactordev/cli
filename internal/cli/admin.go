package cli

import (
	"context"
	"errors"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

// clearNullableSentinel is the value that clears a nullable admin user field.
const clearNullableSentinel = "null"

func newAdminUsersCommand(app *App) *cobra.Command {
	adminUsers := &cobra.Command{Use: "admin-users", Short: "Manage admin users"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List admin users",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			params := &api.QueryAdminUserListParams{}
			if accountID := stringFlag(cmd, "account-id"); accountID != "" {
				params.AccountId = &accountID
			}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAdminUserList(ctx, params)
			})
		},
	}
	list.Flags().String("account-id", "", "Account ID")
	adminUsers.AddCommand(list)

	adminUsers.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve admin user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAdminUserGetDetails(ctx, args[0], &api.QueryAdminUserGetDetailsParams{})
			})
		},
	})

	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update admin user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !flagSet(cmd, "name") && !flagSet(cmd, "job-title") && !flagSet(cmd, "profile-completed-at") {
				return errors.New("No fields provided to update. Specify --name, --job-title, or --profile-completed-at.")
			}

			details := map[string]any{}
			if flagSet(cmd, "name") {
				details["name"] = stringFlag(cmd, "name")
			}
			for _, entry := range []struct{ flag, field string }{
				{"job-title", "job_title"},
				{"profile-completed-at", "profile_completed_at"},
			} {
				if !flagSet(cmd, entry.flag) {
					continue
				}
				if value := stringFlag(cmd, entry.flag); value != clearNullableSentinel {
					details[entry.field] = value
				} else {
					details[entry.field] = nil
				}
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": details})
				if err != nil {
					return nil, err
				}
				return c.ActionAdminUserUpdateWithBody(ctx, args[0], "application/json", body)
			})
		},
	}
	update.Flags().String("name", "", "Name")
	update.Flags().String("job-title", "", "Job title ("+clearNullableSentinel+" clears the field)")
	update.Flags().String("profile-completed-at", "", "Profile completed at ("+clearNullableSentinel+" clears the field)")
	adminUsers.AddCommand(update)

	return adminUsers
}

func newAdminUserInvitesCommand(app *App) *cobra.Command {
	invites := &cobra.Command{Use: "admin-user-invites", Short: "Manage admin user invites"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List admin user invites",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			params := &api.QueryAdminUserInviteListParams{}
			if accountID := stringFlag(cmd, "account-id"); accountID != "" {
				params.AccountId = &accountID
			}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAdminUserInviteList(ctx, params)
			})
		},
	}
	list.Flags().String("account-id", "", "Account ID")
	invites.AddCommand(list)

	invites.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve admin user invite",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAdminUserInviteGetDetails(ctx, args[0], &api.QueryAdminUserInviteGetDetailsParams{})
			})
		},
	})

	create := &cobra.Command{
		Use:   "create",
		Short: "Create admin user invite",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			details := map[string]any{"email": stringFlag(cmd, "email")}
			if accountID := stringFlag(cmd, "account-id"); accountID != "" {
				details["account_id"] = accountID
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": details})
				if err != nil {
					return nil, err
				}
				return c.ActionAdminUserInviteCreateWithBody(ctx, "application/json", body)
			})
		},
	}
	create.Flags().String("email", "", "Email")
	create.Flags().String("account-id", "", "Account ID")
	_ = create.MarkFlagRequired("email")
	invites.AddCommand(create)

	invites.AddCommand(&cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke admin user invite",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionAdminUserInviteRevokeWithBody(ctx, args[0], "application/json", emptyObject())
			})
		},
	})

	return invites
}
