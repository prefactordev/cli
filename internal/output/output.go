// Package output prints command results the way the TypeScript CLI does.
package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
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
// 2): server key order kept, numbers in JavaScript form (1.0 becomes 1) and
// strings escaped the way JavaScript escapes them. An empty body becomes null
// and a body that is not JSON becomes a JSON string.
func Format(raw []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return []byte("null"), nil
	}

	value, err := parse(trimmed)
	if err != nil {
		var buf bytes.Buffer
		writeString(&buf, string(raw))
		return buf.Bytes(), nil
	}

	var buf bytes.Buffer
	writeValue(&buf, value, "")
	return buf.Bytes(), nil
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

// object keeps keys in the order JSON.parse would: first appearance, with a
// repeated key's value replacing the earlier one in place.
type object struct {
	keys   []string
	values map[string]any
}

func parse(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	value, err := parseValue(decoder)
	if err != nil {
		return nil, err
	}

	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after JSON value")
	}

	return value, nil
}

func parseValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}

	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := &object{values: map[string]any{}}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("object key is not a string")
				}
				value, err := parseValue(decoder)
				if err != nil {
					return nil, err
				}
				if _, seen := obj.values[key]; !seen {
					obj.keys = append(obj.keys, key)
				}
				obj.values[key] = value
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return obj, nil
		case '[':
			list := []any{}
			for decoder.More() {
				value, err := parseValue(decoder)
				if err != nil {
					return nil, err
				}
				list = append(list, value)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return list, nil
		}
		return nil, fmt.Errorf("unexpected delimiter %v", t)
	default:
		return t, nil
	}
}

func writeValue(buf *bytes.Buffer, value any, indent string) {
	switch v := value.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		buf.WriteString(strconv.FormatBool(v))
	case json.Number:
		buf.WriteString(formatNumber(v))
	case string:
		writeString(buf, v)
	case []any:
		if len(v) == 0 {
			buf.WriteString("[]")
			return
		}
		inner := indent + "  "
		buf.WriteString("[\n")
		for i, item := range v {
			buf.WriteString(inner)
			writeValue(buf, item, inner)
			if i < len(v)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		buf.WriteString(indent)
		buf.WriteByte(']')
	case *object:
		if len(v.keys) == 0 {
			buf.WriteString("{}")
			return
		}
		inner := indent + "  "
		buf.WriteString("{\n")
		for i, key := range v.keys {
			buf.WriteString(inner)
			writeString(buf, key)
			buf.WriteString(": ")
			writeValue(buf, v.values[key], inner)
			if i < len(v.keys)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		buf.WriteString(indent)
		buf.WriteByte('}')
	}
}

// formatNumber prints a JSON number the way JavaScript's Number#toString
// does after JSON.parse: as a double, shortest round-trip digits, with an
// exponent only below 1e-6 or from 1e21.
func formatNumber(n json.Number) string {
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil {
		// Out of range for a double; JSON.parse gives Infinity, which
		// JSON.stringify prints as null.
		return "null"
	}
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return "null"
	}
	if f == 0 {
		return "0"
	}

	abs := math.Abs(f)
	if abs >= 1e-6 && abs < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}

	formatted := strconv.FormatFloat(f, 'e', -1, 64)
	mantissa, exponent, _ := strings.Cut(formatted, "e")
	sign := exponent[0]
	digits := strings.TrimLeft(exponent[1:], "0")
	if digits == "" {
		digits = "0"
	}
	return mantissa + "e" + string(sign) + digits
}

// writeString escapes a string as JSON.stringify does: quote, backslash and
// control characters only, with \b \f \n \r \t short forms and lowercase hex
// for other control characters. Invalid UTF-8 becomes U+FFFD.
func writeString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size

		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(buf, `\u%04x`, r)
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
}
