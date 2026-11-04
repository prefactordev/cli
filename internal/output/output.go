// Package output prints command results the way the TypeScript CLI does.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// JSON writes a raw JSON response body with two-space indentation and the
// server's key order, matching JSON.stringify(value, null, 2). An empty body
// prints null and a body that is not JSON prints as a JSON string.
func JSON(w io.Writer, raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		_, err := fmt.Fprintln(w, "null")
		return err
	}

	var buf bytes.Buffer
	if err := json.Indent(&buf, trimmed, "", "  "); err != nil {
		return Value(w, string(raw))
	}

	_, err := fmt.Fprintln(w, buf.String())
	return err
}

// Value writes a Go value as two-space indented JSON without HTML escaping.
func Value(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
