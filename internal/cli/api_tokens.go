package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
	"github.com/prefactordev/cli/internal/output"
)

func newAPITokensCommand(app *App) *cobra.Command {
	apiTokens := &cobra.Command{Use: "api-tokens", Short: "Manage API tokens"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List API tokens",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			params := &api.QueryApiTokenListParams{}
			if accountID := stringFlag(cmd, "account-id"); accountID != "" {
				params.AccountId = &accountID
			}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryApiTokenList(ctx, params)
			})
		},
	}
	list.Flags().String("account-id", "", "Account ID")
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
			agentID := strings.TrimSpace(stringFlag(cmd, "agent-id"))
			environmentID := strings.TrimSpace(stringFlag(cmd, "environment-id"))

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
			return output.JSON(cmd.OutOrStdout(), merged)
		},
	}
	create.Flags().String("token-scope", "", "Token scope")
	create.Flags().String("account-id", "", "Account ID")
	create.Flags().String("agent-id", "", "Agent ID (required when token_scope is agent_deployment)")
	create.Flags().String("environment-id", "", "Environment ID (required when token_scope is agent_deployment)")
	create.Flags().String("expires-at", "", "Expiration timestamp")
	_ = create.MarkFlagRequired("token-scope")
	apiTokens.AddCommand(create)

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
			return printValue(cmd, map[string]bool{"ok": true})
		},
	})

	return apiTokens
}

// tokenCreateOutput builds {"details": {...response.details, "token": response.token}}
// like the TypeScript client, keeping the server's key order. A missing token
// is dropped, as JSON.stringify drops undefined.
func tokenCreateOutput(raw []byte) ([]byte, error) {
	// A response that is not an object has no details or token, as in the
	// TypeScript CLI.
	var response map[string]json.RawMessage
	_ = json.Unmarshal(raw, &response)

	var entries []jsonEntry
	if details, ok := response["details"]; ok {
		entries = objectEntries(details)
	}

	token, hasToken := response["token"]
	replaced := false
	kept := entries[:0]
	for _, entry := range entries {
		if entry.key == "token" {
			if !hasToken {
				continue
			}
			entry.value = token
			replaced = true
		}
		kept = append(kept, entry)
	}
	if hasToken && !replaced {
		kept = append(kept, jsonEntry{key: "token", value: token})
	}

	var buf bytes.Buffer
	buf.WriteString(`{"details":{`)
	for i, entry := range kept {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(entry.key)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(entry.value)
	}
	buf.WriteString(`}}`)
	return buf.Bytes(), nil
}

type jsonEntry struct {
	key   string
	value json.RawMessage
}

// objectEntries returns a JSON object's members in order, or nil when raw is
// not an object.
func objectEntries(raw json.RawMessage) []jsonEntry {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil
	}

	var entries []jsonEntry
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil
		}
		key, _ := token.(string)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil
		}
		entries = append(entries, jsonEntry{key: key, value: value})
	}

	return entries
}
