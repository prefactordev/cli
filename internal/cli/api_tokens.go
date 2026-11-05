package cli

import (
	"context"
	"net/http"
	"strconv"
	"unicode/utf16"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
	"github.com/prefactordev/cli/internal/js"
	"github.com/prefactordev/cli/internal/jsjson"
)

func newAPITokensCommand(app *App) *cobra.Command {
	apiTokens := &cobra.Command{Use: "api-tokens", Short: "Manage API tokens"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List API tokens",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q, err := parseListFlags(cmd)
			if err != nil {
				return err
			}
			params := &api.QueryApiTokenListParams{PaginationOffset: q.Offset, PaginationPageSize: q.PageSize}
			if accountID := stringFlag(cmd, "account-id"); accountID != "" {
				params.AccountId = &accountID
			}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryApiTokenList(ctx, params, sortingQuery(q.Sorting))
			})
		},
	}
	list.Flags().String("account-id", "", "Account ID")
	addListFlags(list, "id", "status")
	apiTokens.AddCommand(list)

	apiTokens.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve API token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryApiTokenGetDetails(ctx, args[0], &api.QueryApiTokenGetDetailsParams{})
			})
		},
	})

	create := &cobra.Command{
		Use:   "create",
		Short: "Create API token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tokenScope := stringFlag(cmd, "token-scope")
			agentID := js.Trim(stringFlag(cmd, "agent-id"))
			environmentID := js.Trim(stringFlag(cmd, "environment-id"))

			if err := validateTokenScope(tokenScope); err != nil {
				return err
			}
			if err := validateTokenCreateOptions(tokenScope, agentID, environmentID); err != nil {
				return err
			}

			details := map[string]any{"token_scope": tokenScope}
			if accountID := stringFlag(cmd, "account-id"); accountID != "" {
				details["account_id"] = accountID
			}
			if agentID != "" {
				details["agent_id"] = agentID
			}
			if environmentID != "" {
				details["environment_id"] = environmentID
			}
			if expiresAt := stringFlag(cmd, "expires-at"); expiresAt != "" {
				details["expires_at"] = expiresAt
			}
			optionalString(cmd, details, "role", "role")

			raw, err := app.callAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": details})
				if err != nil {
					return nil, err
				}
				return c.ActionApiTokenCreateWithBody(ctx, "application/json", body)
			})
			if err != nil {
				return err
			}

			merged, err := tokenCreateOutput(raw)
			if err != nil {
				return err
			}
			return app.printResult(cmd, merged)
		},
	}
	create.Flags().String("token-scope", "", "Token scope")
	create.Flags().String("account-id", "", "Account ID")
	create.Flags().String("agent-id", "", "Agent ID (required when token_scope is agent_deployment)")
	create.Flags().String("environment-id", "", "Environment ID (required when token_scope is agent_deployment)")
	create.Flags().String("expires-at", "", "Expiration timestamp")
	addChoiceFlag(create, "role", "Access role", accessRoles...)
	_ = create.MarkFlagRequired("token-scope")
	apiTokens.AddCommand(create)

	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update API token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"details": map[string]any{"role": stringFlag(cmd, "role")}})
				if err != nil {
					return nil, err
				}
				return c.ActionApiTokenUpdateWithBody(ctx, args[0], "application/json", body)
			})
		},
	}
	addChoiceFlag(update, "role", "Access role", accessRoles...)
	_ = update.MarkFlagRequired("role")
	apiTokens.AddCommand(update)

	for _, action := range []struct {
		name, short string
		call        apiCallByID
	}{
		{"suspend", "Suspend API token", func(ctx context.Context, c *api.Client, id string) (*http.Response, error) {
			return c.ActionApiTokenSuspendWithBody(ctx, id, "application/json", emptyObject())
		}},
		{"activate", "Activate API token", func(ctx context.Context, c *api.Client, id string) (*http.Response, error) {
			return c.ActionApiTokenActivateWithBody(ctx, id, "application/json", emptyObject())
		}},
		{"revoke", "Revoke API token", func(ctx context.Context, c *api.Client, id string) (*http.Response, error) {
			return c.ActionApiTokenRevokeWithBody(ctx, id, "application/json", emptyObject())
		}},
	} {
		apiTokens.AddCommand(&cobra.Command{
			Use:   action.name + " <id>",
			Short: action.short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
					return action.call(ctx, c, args[0])
				})
			},
		})
	}

	apiTokens.AddCommand(&cobra.Command{
		Use:   "delete <id>",
		Short: "Delete API token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := app.callAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionApiTokenDeleteWithBody(ctx, args[0], "application/json", nil)
			}); err != nil {
				return err
			}
			return app.printJSONValue(cmd, map[string]bool{"ok": true})
		},
	})

	return apiTokens
}

// tokenCreateOutput builds {"details": {...response.details, token:
// response.token}} like the TypeScript client, with JavaScript's spread and
// key order. A missing token removes the key, as JSON.stringify drops
// undefined values.
func tokenCreateOutput(raw []byte) ([]byte, error) {
	details := jsjson.NewObject()
	var token any
	hasToken := false

	// A response that is not an object has no details or token.
	if parsed, err := jsjson.Parse(raw); err == nil {
		if response, ok := parsed.(*jsjson.Object); ok {
			if value, ok := response.Get("details"); ok {
				spread(details, value)
			}
			token, hasToken = response.Get("token")
		}
	}

	if hasToken {
		details.Set("token", token)
	} else {
		details.Delete("token")
	}

	result := jsjson.NewObject()
	result.Set("details", details)
	return []byte(jsjson.Stringify(result, "")), nil
}

// spread copies value's own properties into target, as {...value} does.
func spread(target *jsjson.Object, value any) {
	switch v := value.(type) {
	case *jsjson.Object:
		for _, key := range v.Keys() {
			item, _ := v.Get(key)
			target.Set(key, item)
		}
	case []any:
		for i, item := range v {
			target.Set(strconv.Itoa(i), item)
		}
	case string:
		for i, unit := range utf16.Encode([]rune(v)) {
			target.Set(strconv.Itoa(i), string(rune(unit)))
		}
	}
}
