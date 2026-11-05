package cli

import (
	"context"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

func newUtilsCommand(app *App) *cobra.Command {
	utils := &cobra.Command{Use: "utils", Short: "Check templates, schemas and payloads"}

	renderTemplate := &cobra.Command{
		Use:   "render-liquid-template",
		Short: "Render a Liquid template against sample payloads",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			template, err := readTextOption(stringFlag(cmd, "template"), "--template")
			if err != nil {
				return err
			}
			body := map[string]any{"template": template}
			if err := fieldsBody(cmd, body, "samples"); err != nil {
				return err
			}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				reader, err := jsonBody(body)
				if err != nil {
					return nil, err
				}
				return c.ActionUtilsRenderLiquidTemplateWithBody(ctx, "application/json", reader)
			})
		},
	}
	renderTemplate.Flags().String("template", "", "Liquid template, or @file")
	addJSONFlag(renderTemplate, "samples", `Samples to render: [{"payload":{...},"result_payload"?:{...}}] or @file`, "array")
	_ = renderTemplate.MarkFlagRequired("template")
	_ = renderTemplate.MarkFlagRequired("samples")
	utils.AddCommand(renderTemplate)

	validateSchema := &cobra.Command{
		Use:   "validate-schema",
		Short: "Validate a JSON schema, and a payload against it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body := map[string]any{}
			if err := fieldsBody(cmd, body, "schema", "payload"); err != nil {
				return err
			}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				reader, err := jsonBody(body)
				if err != nil {
					return nil, err
				}
				return c.ActionUtilsValidateSchemaWithBody(ctx, "application/json", reader)
			})
		},
	}
	addJSONFlag(validateSchema, "schema", "JSON Schema: JSON object or @file", "object")
	addJSONFlag(validateSchema, "payload", "Payload to check against the schema: JSON object, null or @file", "object or null")
	_ = validateSchema.MarkFlagRequired("schema")
	utils.AddCommand(validateSchema)

	processSensitive := &cobra.Command{
		Use:   "process-sensitive-payload",
		Short: "Process a payload with $sensitive markers and check it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body := map[string]any{}
			if err := fieldsBody(cmd, body, "payload", "schema"); err != nil {
				return err
			}
			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				reader, err := jsonBody(body)
				if err != nil {
					return nil, err
				}
				return c.ActionUtilsProcessSensitivePayloadWithBody(ctx, "application/json", reader)
			})
		},
	}
	addJSONFlag(processSensitive, "payload", "Payload with $sensitive markers: JSON object or @file", "object")
	addJSONFlag(processSensitive, "schema", "JSON Schema to check the payload against: JSON object or @file", "object")
	_ = processSensitive.MarkFlagRequired("payload")
	utils.AddCommand(processSensitive)

	return utils
}
