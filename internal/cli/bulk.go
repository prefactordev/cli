package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"unicode/utf16"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
)

// parseBulkItems parses --items (JSON array or @file) and checks every item
// is an object with a string _type and a unique 8–128 character
// idempotency_key. Items are returned as written.
func parseBulkItems(value string) ([]json.RawMessage, error) {
	parsed, err := parseJSONOption(value, "--items", "array")
	if err != nil {
		return nil, err
	}

	var items []json.RawMessage
	if err := json.Unmarshal(parsed, &items); err != nil {
		return nil, err
	}

	if len(items) == 0 {
		return nil, errors.New("--items must contain at least one item.")
	}

	seenKeys := map[string]bool{}
	for index, item := range items {
		var entry map[string]json.RawMessage
		if trimmed := bytes.TrimSpace(item); len(trimmed) == 0 || trimmed[0] != '{' || json.Unmarshal(trimmed, &entry) != nil {
			return nil, fmt.Errorf("--items[%d] must be a JSON object.", index)
		}

		if _, ok := jsonString(entry["_type"]); !ok {
			return nil, fmt.Errorf("--items[%d]._type must be a string.", index)
		}

		key, ok := jsonString(entry["idempotency_key"])
		if !ok {
			return nil, fmt.Errorf("--items[%d].idempotency_key must be a string.", index)
		}

		// JavaScript string length counts UTF-16 code units.
		if length := len(utf16.Encode([]rune(key))); length < 8 || length > 128 {
			return nil, fmt.Errorf("--items[%d].idempotency_key must be 8–128 characters.", index)
		}

		if seenKeys[key] {
			return nil, fmt.Errorf("--items[%d].idempotency_key must be unique within the request.", index)
		}
		seenKeys[key] = true
	}

	return items, nil
}

// jsonString decodes raw when it is a JSON string.
func jsonString(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return "", false
	}

	var value string
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return "", false
	}
	return value, true
}

func newBulkCommand(app *App) *cobra.Command {
	bulk := &cobra.Command{Use: "bulk", Short: "Execute bulk query and action operations"}

	execute := &cobra.Command{
		Use:   "execute",
		Short: "Execute bulk requests",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			items, err := parseBulkItems(stringFlag(cmd, "items"))
			if err != nil {
				return err
			}

			return app.runAPI(cmd, func(ctx context.Context, c *api.Client) (*http.Response, error) {
				body, err := jsonBody(map[string]any{"items": items})
				if err != nil {
					return nil, err
				}
				return c.BulkExecuteWithBody(ctx, "application/json", body)
			})
		},
	}
	execute.Flags().String("items", "", "JSON array or @file")
	_ = execute.MarkFlagRequired("items")
	bulk.AddCommand(execute)

	return bulk
}
