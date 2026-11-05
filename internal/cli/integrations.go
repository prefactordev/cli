package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
	"github.com/prefactordev/cli/internal/js"
)

// findIntegration lists the account's integrations and returns the ID of the
// one with integrationType, or "" when none is configured.
func (a *App) findIntegration(cmd *cobra.Command, integrationType string) (string, error) {
	raw, err := a.callAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
		return c.QueryAccountIntegrationList(ctx, &api.QueryAccountIntegrationListParams{})
	})
	if err != nil {
		return "", err
	}

	// summaries.find(summary => summary.integration_type === type), with
	// the id used as the TypeScript CLI interpolates it into the path.
	summaries, ok := property(parseJSON(raw), "summaries").([]any)
	if !ok {
		return "", errors.New("Response is missing integration summaries.")
	}
	for _, summary := range summaries {
		if found, ok := property(summary, "integration_type").(string); ok && found == integrationType {
			return templateString(property(summary, "id")), nil
		}
	}

	return "", nil
}

// isFalsyJSON reports whether a JSON value is falsy in JavaScript.
func isFalsyJSON(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "false", "null", `""`:
		return true
	}

	var number float64
	if err := json.Unmarshal(raw, &number); err == nil {
		return number == 0
	}

	return false
}

func newIntegrationsCommand(app *App) *cobra.Command {
	integrations := &cobra.Command{Use: "integrations", Short: "Manage account integrations"}

	integrations.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List configured integrations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryAccountIntegrationList(ctx, &api.QueryAccountIntegrationListParams{})
			})
		},
	})

	for _, operation := range []struct {
		name, short string
		call        apiCallByID
	}{
		{"retrieve", "Retrieve integration config with secrets redacted", func(ctx context.Context, c *api.Client, id string) (*http.Response, error) {
			return c.QueryAccountIntegrationGetDetails(ctx, id, &api.QueryAccountIntegrationGetDetailsParams{})
		}},
		{"delete", "Remove an integration", func(ctx context.Context, c *api.Client, id string) (*http.Response, error) {
			return c.ActionAccountIntegrationDeleteWithBody(ctx, id, "application/json", nil)
		}},
		{"test", "Send a test notification", func(ctx context.Context, c *api.Client, id string) (*http.Response, error) {
			return c.ActionAccountIntegrationTestWithBody(ctx, id, "application/json", nil)
		}},
	} {
		integrations.AddCommand(&cobra.Command{
			Use:   operation.name + " <type>",
			Short: operation.short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				integrationType := args[0]
				id, err := app.findIntegration(cmd, integrationType)
				if err != nil {
					return err
				}
				if id == "" {
					return fmt.Errorf("Integration '%s' is not configured.", integrationType)
				}

				raw, err := app.callAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
					return operation.call(ctx, c, id)
				})
				if err != nil {
					return err
				}

				if operation.name == "test" {
					var result map[string]json.RawMessage
					if err := json.Unmarshal(raw, &result); err == nil {
						if delivered, ok := result["delivered"]; ok && isFalsyJSON(delivered) {
							return fmt.Errorf("Integration '%s' test notification was not delivered.", integrationType)
						}
					}
				}

				return app.printResult(cmd, raw)
			},
		})
	}

	update := &cobra.Command{
		Use:   "update <type>",
		Short: "Create or replace Slack integration config",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			integrationType := args[0]

			enabledEvents := []string{}
			if events := stringFlag(cmd, "enabled-events"); js.Trim(events) != "" {
				for _, event := range strings.Split(events, ",") {
					enabledEvents = append(enabledEvents, js.Trim(event))
				}
			}
			config := map[string]any{
				"_version":       1,
				"webhook_url":    stringFlag(cmd, "webhook-url"),
				"enabled_events": enabledEvents,
			}

			id, err := app.findIntegration(cmd, integrationType)
			if err != nil {
				return err
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				if id != "" {
					details := map[string]any{"config": config}
					optionalString(cmd, details, "status", "status")
					body, err := jsonBody(map[string]any{"details": details})
					if err != nil {
						return nil, err
					}
					return c.ActionAccountIntegrationUpdateWithBody(ctx, id, "application/json", body)
				}

				body, err := jsonBody(map[string]any{"details": map[string]any{
					"integration_type": integrationType,
					"status":           cmp.Or(stringFlag(cmd, "status"), "active"),
					"config":           config,
				}})
				if err != nil {
					return nil, err
				}
				return c.ActionAccountIntegrationCreateWithBody(ctx, "application/json", body)
			})
		},
	}
	update.Flags().String("webhook-url", "", "Slack incoming webhook URL (replaces the saved URL)")
	update.Flags().String("enabled-events", "", `Comma-separated event names; use "" for no events`)
	addChoiceFlag(update, "status", "Integration status (a new integration is active unless this is set)", "active", "paused")
	app.checkBeforeProfile(update, func(_ *cobra.Command, args []string) error {
		if len(args) > 0 && args[0] != "slack" {
			return errors.New("Only Slack configuration is supported.")
		}
		return nil
	})
	_ = update.MarkFlagRequired("webhook-url")
	_ = update.MarkFlagRequired("enabled-events")
	integrations.AddCommand(update)

	return integrations
}

// apiCallByID sends one request for the resource with id.
type apiCallByID func(ctx context.Context, c *api.Client, id string) (*http.Response, error)
