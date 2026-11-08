package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

func newUtilsCommand(app *App) *cobra.Command {
	utils := &cobra.Command{Use: "utils", Short: "Check templates, schemas, and payloads"}

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
			if err := checkTemplateSamples(body["samples"]); err != nil {
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
	renderTemplate.Flags().String("template", "", "Liquid template string, or @path to a file")
	addJSONFlag(renderTemplate, "samples", "JSON array of { payload, result_payload? } objects, or @path to a JSON file", "array")
	_ = renderTemplate.MarkFlagRequired("template")
	_ = renderTemplate.MarkFlagRequired("samples")
	utils.AddCommand(renderTemplate)

	validateSchema := &cobra.Command{
		Use:   "validate-schema",
		Short: "Validate a JSON Schema and an optional payload",
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
	addJSONFlag(validateSchema, "schema", "JSON Schema object, or @path to a JSON file", "object")
	addJSONFlag(validateSchema, "payload", "JSON object, null to validate the schema alone, or @path to a JSON file", "object or null")
	_ = validateSchema.MarkFlagRequired("schema")
	utils.AddCommand(validateSchema)

	processSensitive := &cobra.Command{
		Use:   "process-sensitive-payload",
		Short: "Process a $sensitive payload and check its input and output",
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
	addJSONFlag(processSensitive, "payload", "JSON object in $sensitive wire form, or @path to a JSON file", "object")
	addJSONFlag(processSensitive, "schema", "JSON Schema object, null to skip the schema check, or @path to a JSON file", "object or null")
	_ = processSensitive.MarkFlagRequired("payload")
	utils.AddCommand(processSensitive)

	return utils
}

// checkTemplateSamples checks each sample as the TypeScript CLI does: an
// object whose payload is an object and whose result_payload, if present, is
// an object or null.
func checkTemplateSamples(samples any) error {
	raw, _ := samples.(json.RawMessage)
	var list []any
	if err := json.Unmarshal(raw, &list); err != nil {
		return err
	}
	for i, item := range list {
		sample, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("--samples[%d] must be a JSON object.", i)
		}
		if _, ok := sample["payload"].(map[string]any); !ok {
			return fmt.Errorf("--samples[%d].payload must be a JSON object.", i)
		}
		if result, present := sample["result_payload"]; present && result != nil {
			if _, ok := result.(map[string]any); !ok {
				return fmt.Errorf("--samples[%d].result_payload must be a JSON object or null.", i)
			}
		}
	}
	return nil
}
