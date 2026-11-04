// Command specnormalize rewrites the p2 OpenAPI document into a form that
// oapi-codegen accepts.
//
// The p2 document describes most request bodies as parameters with
// "in": "body", which OpenAPI 3.0 does not allow, and has query parameters
// without a schema. This command moves body parameters into a JSON
// requestBody and gives schemaless parameters a string schema. Everything
// else is copied unchanged.
//
// Usage: specnormalize <input.json> <output.json>
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: specnormalize <input.json> <output.json>")
		os.Exit(2)
	}

	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(inputPath, outputPath string) error {
	raw, err := os.ReadFile(inputPath)
	if err != nil {
		return err
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", inputPath, err)
	}

	if err := normalize(doc); err != nil {
		return err
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(outputPath, append(out, '\n'), 0o644)
}

func normalize(doc map[string]any) error {
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		return fmt.Errorf("document has no paths object")
	}

	for path, item := range paths {
		pathItem, ok := item.(map[string]any)
		if !ok {
			continue
		}

		for _, method := range httpMethods {
			operation, ok := pathItem[method].(map[string]any)
			if !ok {
				continue
			}

			if err := normalizeOperation(operation); err != nil {
				return fmt.Errorf("%s %s: %w", method, path, err)
			}
		}
	}

	return nil
}

func normalizeOperation(operation map[string]any) error {
	params, _ := operation["parameters"].([]any)

	kept := make([]any, 0, len(params))
	properties := map[string]any{}
	required := []any{}

	for _, entry := range params {
		param, ok := entry.(map[string]any)
		if !ok {
			kept = append(kept, entry)
			continue
		}

		if param["in"] != "body" {
			if _, hasSchema := param["schema"]; !hasSchema {
				if _, hasContent := param["content"]; !hasContent {
					param["schema"] = map[string]any{"type": "string"}
				}
			}
			kept = append(kept, param)
			continue
		}

		name, _ := param["name"].(string)
		if name == "" {
			return fmt.Errorf("body parameter without a name")
		}

		schema, ok := param["schema"]
		if !ok {
			schema = map[string]any{}
		}
		properties[name] = schema

		if isRequired, _ := param["required"].(bool); isRequired {
			required = append(required, name)
		}
	}

	if len(properties) == 0 {
		return nil
	}

	if _, exists := operation["requestBody"]; exists {
		return fmt.Errorf("operation has both a requestBody and body parameters")
	}

	bodySchema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		bodySchema["required"] = required
	}

	operation["parameters"] = kept
	operation["requestBody"] = map[string]any{
		"required": len(required) > 0,
		"content": map[string]any{
			"application/json": map[string]any{"schema": bodySchema},
		},
	}

	return nil
}
