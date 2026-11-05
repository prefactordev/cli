package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

// newLlmProvidersCommand registers commands for the language model providers
// that hosted judges run on.
func newLlmProvidersCommand(app *App) *cobra.Command {
	providers := &cobra.Command{
		Use:   "llm-providers",
		Short: "Manage language model providers for hosted judges",
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List LLM providers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q, err := parseListFlags(cmd)
			if err != nil {
				return err
			}
			params := &api.QueryLlmProviderListParams{PaginationOffset: q.Offset, PaginationPageSize: q.PageSize}
			if accountID := stringFlag(cmd, "account-id"); accountID != "" {
				params.AccountId = &accountID
			}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryLlmProviderList(ctx, params, sortingQuery(q.Sorting))
			})
		},
	}
	list.Flags().String("account-id", "", "Account ID")
	addListFlags(list, "id", "name")
	providers.AddCommand(list)

	providers.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve an LLM provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryLlmProviderGetDetails(ctx, args[0], &api.QueryLlmProviderGetDetailsParams{})
			})
		},
	})

	create := &cobra.Command{
		Use:   "create",
		Short: "Create an LLM provider",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			provider := stringFlag(cmd, "provider")
			id := stringFlag(cmd, "id")
			if err := validateOptionalPfid(id, "--id"); err != nil {
				return err
			}
			apiKey, err := readTextOption(stringFlag(cmd, "api-key"), "--api-key")
			if err != nil {
				return err
			}

			details := map[string]any{
				"name":     stringFlag(cmd, "name"),
				"provider": provider,
				"api_key":  strings.TrimSpace(apiKey),
			}
			if baseURL := stringFlag(cmd, "api-base-url"); baseURL != "" {
				details["api_base_url"] = baseURL
			}
			if accountID := stringFlag(cmd, "account-id"); accountID != "" {
				details["account_id"] = accountID
			}
			if id != "" {
				details["id"] = id
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return jsonRequest(map[string]any{"details": details}, func(body io.Reader) (*http.Response, error) {
					return c.ActionLlmProviderCreateWithBody(ctx, "application/json", body)
				})
			})
		},
	}
	create.Flags().String("name", "", "Provider name, unique in the account")
	create.Flags().String("provider", "", "Provider type")
	create.Flags().String("api-key", "", "API key, or @path to read it from a file")
	create.Flags().String("api-base-url", "", "Endpoint URL; required for custom_openai")
	create.Flags().String("account-id", "", "Account ID")
	create.Flags().String("id", "", "LLM provider ID")
	_ = create.MarkFlagRequired("name")
	_ = create.MarkFlagRequired("provider")
	_ = create.MarkFlagRequired("api-key")
	providers.AddCommand(create)

	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an LLM provider; the provider type cannot change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !flagSet(cmd, "name") && !flagSet(cmd, "api-key") && !flagSet(cmd, "api-base-url") {
				return errors.New("No fields provided to update. Specify --name, --api-key, or --api-base-url.")
			}

			details := map[string]any{}
			if flagSet(cmd, "name") {
				details["name"] = stringFlag(cmd, "name")
			}
			if flagSet(cmd, "api-key") {
				apiKey, err := readTextOption(stringFlag(cmd, "api-key"), "--api-key")
				if err != nil {
					return err
				}
				details["api_key"] = strings.TrimSpace(apiKey)
			}
			if flagSet(cmd, "api-base-url") {
				details["api_base_url"] = stringFlag(cmd, "api-base-url")
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return jsonRequest(map[string]any{"details": details}, func(body io.Reader) (*http.Response, error) {
					return c.ActionLlmProviderUpdateWithBody(ctx, args[0], "application/json", body)
				})
			})
		},
	}
	update.Flags().String("name", "", "Provider name")
	update.Flags().String("api-key", "", "New API key, or @path to read it from a file")
	update.Flags().String("api-base-url", "", "Endpoint URL of a custom_openai provider")
	providers.AddCommand(update)

	providers.AddCommand(&cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an LLM provider and its models",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionLlmProviderDeleteWithBody(ctx, args[0], "application/json", nil)
			})
		},
	})

	return providers
}

// newLlmProviderModelsCommand registers commands for the models available
// through an LLM provider.
func newLlmProviderModelsCommand(app *App) *cobra.Command {
	models := &cobra.Command{
		Use:   "llm-provider-models",
		Short: "Manage the models available through an LLM provider",
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List the models of an LLM provider",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			providerID := stringFlag(cmd, "llm-provider-id")
			q, err := parseListFlags(cmd)
			if err != nil {
				return err
			}
			params := &api.QueryLlmProviderModelListParams{LlmProviderId: &providerID, PaginationOffset: q.Offset, PaginationPageSize: q.PageSize}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryLlmProviderModelList(ctx, params, sortingQuery(q.Sorting))
			})
		},
	}
	list.Flags().String("llm-provider-id", "", "LLM provider ID")
	addListFlags(list, "id", "name")
	_ = list.MarkFlagRequired("llm-provider-id")
	models.AddCommand(list)

	models.AddCommand(&cobra.Command{
		Use:   "retrieve <id>",
		Short: "Retrieve an LLM provider model",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.QueryLlmProviderModelGetDetails(ctx, args[0], &api.QueryLlmProviderModelGetDetailsParams{})
			})
		},
	})

	create := &cobra.Command{
		Use:   "create",
		Short: "Add a model. For named providers, Prefactor fills in the name and pricing from models.dev",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id := stringFlag(cmd, "id")
			if err := validateOptionalPfid(id, "--id"); err != nil {
				return err
			}

			details := map[string]any{
				"llm_provider_id": stringFlag(cmd, "llm-provider-id"),
				"api_name":        stringFlag(cmd, "api-name"),
			}
			for _, option := range []struct{ flag, field string }{
				{"name", "name"},
				{"models-dev-provider", "models_dev_provider"},
				{"models-dev-model-name", "models_dev_model_name"},
				{"id", "id"},
			} {
				if value := stringFlag(cmd, option.flag); value != "" {
					details[option.field] = value
				}
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return jsonRequest(map[string]any{"details": details}, func(body io.Reader) (*http.Response, error) {
					return c.ActionLlmProviderModelCreateWithBody(ctx, "application/json", body)
				})
			})
		},
	}
	create.Flags().String("llm-provider-id", "", "LLM provider ID")
	create.Flags().String("api-name", "", "Model name the provider API expects")
	create.Flags().String("name", "", "Display name; required for custom_openai")
	create.Flags().String("models-dev-provider", "", "models.dev provider, for custom_openai pricing")
	create.Flags().String("models-dev-model-name", "", "models.dev model name, for custom_openai pricing")
	create.Flags().String("id", "", "LLM provider model ID")
	_ = create.MarkFlagRequired("llm-provider-id")
	_ = create.MarkFlagRequired("api-name")
	models.AddCommand(create)

	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an LLM provider model",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			details := map[string]any{}
			for _, option := range []struct{ flag, field string }{
				{"api-name", "api_name"},
				{"name", "name"},
				{"models-dev-provider", "models_dev_provider"},
				{"models-dev-model-name", "models_dev_model_name"},
			} {
				if flagSet(cmd, option.flag) {
					details[option.field] = stringFlag(cmd, option.flag)
				}
			}
			if len(details) == 0 {
				return errors.New("No fields provided to update. Specify --api-name, --name, --models-dev-provider, or --models-dev-model-name.")
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return jsonRequest(map[string]any{"details": details}, func(body io.Reader) (*http.Response, error) {
					return c.ActionLlmProviderModelUpdateWithBody(ctx, args[0], "application/json", body)
				})
			})
		},
	}
	update.Flags().String("api-name", "", "Model name the provider API expects")
	update.Flags().String("name", "", "Display name")
	update.Flags().String("models-dev-provider", "", "models.dev provider")
	update.Flags().String("models-dev-model-name", "", "models.dev model name")
	models.AddCommand(update)

	models.AddCommand(&cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an LLM provider model",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				return c.ActionLlmProviderModelDeleteWithBody(ctx, args[0], "application/json", nil)
			})
		},
	})

	return models
}
