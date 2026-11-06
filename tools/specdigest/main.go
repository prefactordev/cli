// Command specdigest prints an OpenAPI document as compact text: an
// operations index, each operation's parameters, body and responses, and
// a schema index. Diffing the digests of two documents shows what changed
// in the API without the noise of the raw JSON.
//
// Usage: specdigest <openapi.json>
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

// maxInlineDepth is how deep nested objects are spelled out.
const maxInlineDepth = 3

var methods = []string{"get", "post", "put", "patch", "delete"}

type object = map[string]any

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: specdigest <openapi.json>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var doc object
	if err := json.Unmarshal(raw, &doc); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
	fmt.Print(digest(doc))
}

func digest(doc object) string {
	paths, _ := doc["paths"].(object)
	components, _ := doc["components"].(object)
	schemas, _ := components["schemas"].(object)
	info, _ := doc["info"].(object)

	var b strings.Builder
	fmt.Fprintf(&b, "# %v %v: %d paths, %d schemas\n\n", info["title"], info["version"], len(paths), len(schemas))

	b.WriteString("## Operations index\n")
	eachOperation(paths, func(method, path string, op object) {
		fmt.Fprintf(&b, "- %s %s: %v\n", strings.ToUpper(method), path, op["operationId"])
	})

	b.WriteString("\n## Operations\n")
	eachOperation(paths, func(method, path string, op object) {
		deprecated := ""
		if op["deprecated"] == true {
			deprecated = " [deprecated]"
		}
		fmt.Fprintf(&b, "### %s %s%s\noperationId: %v\n", strings.ToUpper(method), path, deprecated, op["operationId"])
		params, _ := op["parameters"].([]any)
		for _, in := range []string{"path", "query", "body"} {
			if lines := describeParams(params, in); len(lines) > 0 {
				fmt.Fprintf(&b, "%s params:\n%s", in, strings.Join(lines, ""))
			}
		}
		if body := dig(op, "requestBody", "content", "application/json", "schema"); body != nil {
			fmt.Fprintf(&b, "request body: %s\n", describe(body, 0))
		}
		b.WriteString("responses:\n")
		responses, _ := op["responses"].(object)
		for _, status := range sortedKeys(responses) {
			response, _ := responses[status].(object)
			fmt.Fprintf(&b, "  - %s: %s\n", status, describe(dig(response, "content", "application/json", "schema"), 0))
		}
		b.WriteString("\n")
	})

	b.WriteString("## Schema index\n")
	for _, name := range sortedKeys(schemas) {
		fmt.Fprintf(&b, "- %s: %s\n", name, describe(schemas[name], 0))
	}
	return b.String()
}

func eachOperation(paths object, f func(method, path string, op object)) {
	for _, path := range sortedKeys(paths) {
		item, _ := paths[path].(object)
		for _, method := range methods {
			if op, ok := item[method].(object); ok {
				f(method, path, op)
			}
		}
	}
}

func describeParams(params []any, in string) []string {
	var lines []string
	for _, p := range params {
		param, _ := p.(object)
		if param["in"] != in {
			continue
		}
		required := ""
		if param["required"] == true {
			required = " (required)"
		}
		lines = append(lines, fmt.Sprintf("  - %v: %s%s\n", param["name"], describe(param["schema"], 0), required))
	}
	return lines
}

// describe writes a schema as a TypeScript-like type: references by name,
// T | null for nullable unions, enum[...] and { field?: T } for objects,
// with ? marking optional fields.
func describe(s any, depth int) string {
	schema, ok := s.(object)
	if !ok {
		return "unknown"
	}
	if ref, ok := schema["$ref"].(string); ok {
		return refName(ref)
	}
	combo, ok := schema["oneOf"].([]any)
	if !ok {
		combo, ok = schema["anyOf"].([]any)
	}
	if ok {
		var parts []string
		nullable := false
		for _, variant := range combo {
			if isNull(variant) {
				nullable = true
				continue
			}
			parts = append(parts, describe(variant, depth))
		}
		if len(parts) == 0 {
			return "null"
		}
		if nullable {
			parts = append(parts, "null")
		}
		return strings.Join(parts, " | ")
	}
	if values, ok := schema["enum"].([]any); ok {
		parts := make([]string, len(values))
		for i, v := range values {
			parts[i] = fmt.Sprint(v)
		}
		return "enum[" + strings.Join(parts, ", ") + "]"
	}
	if schema["type"] == "array" {
		return describe(schema["items"], depth) + "[]"
	}
	if props, ok := schema["properties"].(object); ok || schema["type"] == "object" {
		if len(props) == 0 {
			return "object"
		}
		if depth >= maxInlineDepth {
			return "{ ... }"
		}
		required, _ := schema["required"].([]any)
		var fields []string
		for _, name := range sortedKeys(props) {
			optional := "?"
			if slices.Contains(required, any(name)) {
				optional = ""
			}
			fields = append(fields, name+optional+": "+describe(props[name], depth+1))
		}
		return "{ " + strings.Join(fields, ", ") + " }"
	}
	if t, ok := schema["type"].(string); ok {
		return t
	}
	return "unknown"
}

func isNull(variant any) bool {
	v, _ := variant.(object)
	ref, _ := v["$ref"].(string)
	return v["type"] == "null" || (ref != "" && refName(ref) == "Null")
}

func refName(ref string) string {
	return ref[strings.LastIndex(ref, "/")+1:]
}

func dig(o object, keys ...string) any {
	var v any = o
	for _, key := range keys {
		m, ok := v.(object)
		if !ok {
			return nil
		}
		v = m[key]
	}
	return v
}

func sortedKeys(o object) []string {
	keys := make([]string, 0, len(o))
	for k := range o {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
