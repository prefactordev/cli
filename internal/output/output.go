// Package output prints command results the way the TypeScript CLI does.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/prefactordev/cli/internal/jsjson"
)

// JSON writes a raw JSON response body as JSON.stringify(JSON.parse(body),
// null, 2) would in the TypeScript CLI, followed by a newline. An empty body
// prints null and a body that is not JSON prints as a JSON string.
func JSON(w io.Writer, raw []byte) error {
	formatted, err := Format(raw)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(w, string(formatted))
	return err
}

// Format returns raw JSON re-encoded as JSON.stringify(JSON.parse(raw), null,
// 2). An empty body becomes null and a body that is not JSON becomes a JSON
// string.
func Format(raw []byte) ([]byte, error) {
	value, err := parse(raw)
	if err != nil {
		var b strings.Builder
		jsjson.WriteString(&b, string(raw))
		return []byte(b.String()), nil
	}
	return []byte(jsjson.Stringify(value, "  ")), nil
}

// parse reads a response body as the TypeScript CLI does: only an empty
// body is null, so one of only whitespace is not JSON.
func parse(raw []byte) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	return jsjson.Parse(raw)
}

// Value writes a Go value as two-space indented JSON, in the same form as
// JSON.
func Value(w io.Writer, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return JSON(w, raw)
}

// JSONFields writes raw JSON like JSON, keeping only the given dot-separated
// paths. A path continues through arrays, so "summaries.id" keeps the id of
// every summary. Paths that do not exist are left out; key order is kept.
func JSONFields(w io.Writer, raw []byte, fields []string) error {
	value, err := parse(raw)
	if err != nil {
		return JSON(w, raw)
	}

	paths := make([][]string, len(fields))
	for i, field := range fields {
		paths[i] = strings.Split(field, ".")
	}

	_, err = fmt.Fprintln(w, jsjson.Stringify(project(value, paths), "  "))
	return err
}

// project keeps the parts of value selected by paths. A value selected by an
// empty path is kept whole.
func project(value any, paths [][]string) any {
	for _, path := range paths {
		if len(path) == 0 {
			return value
		}
	}

	switch v := value.(type) {
	case []any:
		list := make([]any, 0, len(v))
		for _, item := range v {
			list = append(list, project(item, paths))
		}
		return list
	case *jsjson.Object:
		result := jsjson.NewObject()
		for _, key := range v.Keys() {
			var rest [][]string
			for _, path := range paths {
				if path[0] == key {
					rest = append(rest, path[1:])
				}
			}
			if len(rest) == 0 {
				continue
			}
			child, _ := v.Get(key)
			result.Set(key, project(child, rest))
		}
		return result
	default:
		// A path that goes past a scalar selects nothing more.
		return value
	}
}
