// Package jsjson parses and prints JSON with the semantics of JavaScript's
// JSON.parse and JSON.stringify, so the CLI reads and writes JSON exactly as
// the TypeScript CLI does.
//
// The differences from encoding/json that matter:
//   - Object keys that are array indexes ("0", "10") come first, in numeric
//     order, then the other keys in the order they were added.
//   - A repeated key keeps its first position and takes the last value.
//   - Strings keep lone UTF-16 surrogates (\ud800), which JSON.stringify
//     prints escaped. They are held as WTF-8 inside Go strings.
//   - Numbers are doubles, printed in JavaScript's shortest form.
//   - Anything after the value other than whitespace is an error.
package jsjson

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Object is a JSON object with JavaScript property order.
type Object struct {
	keys   []string
	values map[string]any
}

// NewObject returns an empty object.
func NewObject() *Object {
	return &Object{values: map[string]any{}}
}

// Keys returns the keys in JavaScript property order.
func (o *Object) Keys() []string {
	return append([]string(nil), o.keys...)
}

// Get returns the value for key.
func (o *Object) Get(key string) (any, bool) {
	v, ok := o.values[key]
	return v, ok
}

// Set adds or replaces key, keeping JavaScript property order.
func (o *Object) Set(key string, value any) {
	if _, exists := o.values[key]; exists {
		o.values[key] = value
		return
	}
	o.values[key] = value

	if index, ok := arrayIndex(key); ok {
		at := 0
		for at < len(o.keys) {
			other, isIndex := arrayIndex(o.keys[at])
			if !isIndex || other > index {
				break
			}
			at++
		}
		o.keys = append(o.keys, "")
		copy(o.keys[at+1:], o.keys[at:])
		o.keys[at] = key
		return
	}
	o.keys = append(o.keys, key)
}

// Delete removes key.
func (o *Object) Delete(key string) {
	if _, exists := o.values[key]; !exists {
		return
	}
	delete(o.values, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			return
		}
	}
}

// arrayIndex reports whether key is a canonical array index: "0", or digits
// without a leading zero, below 2^32 - 1.
func arrayIndex(key string) (uint64, bool) {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(key, 10, 64)
	if err != nil || n >= math.MaxUint32 {
		return 0, false
	}
	return n, true
}

// Number is a JSON number, held as a double like JavaScript.
type Number float64

// Parse parses data like JSON.parse. Objects are *Object, arrays []any,
// strings string, numbers Number, booleans bool and null nil.
func Parse(data []byte) (any, error) {
	p := &parser{data: data}
	p.skipSpace()
	value, err := p.value()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos != len(p.data) {
		return nil, p.errorf("unexpected data after the JSON value")
	}
	return value, nil
}

type parser struct {
	data []byte
	pos  int
}

func (p *parser) errorf(format string, args ...any) error {
	return fmt.Errorf("JSON position %d: %s", p.pos, fmt.Sprintf(format, args...))
}

func (p *parser) skipSpace() {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *parser) value() (any, error) {
	if p.pos >= len(p.data) {
		return nil, p.errorf("unexpected end of input")
	}
	switch c := p.data[p.pos]; {
	case c == '{':
		return p.object()
	case c == '[':
		return p.array()
	case c == '"':
		return p.string()
	case c == 't':
		return true, p.literal("true")
	case c == 'f':
		return false, p.literal("false")
	case c == 'n':
		return nil, p.literal("null")
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	default:
		return nil, p.errorf("unexpected character %q", c)
	}
}

func (p *parser) literal(word string) error {
	if !strings.HasPrefix(string(p.data[p.pos:]), word) {
		return p.errorf("expected %s", word)
	}
	p.pos += len(word)
	return nil
}

func (p *parser) object() (*Object, error) {
	p.pos++ // {
	obj := NewObject()
	p.skipSpace()
	if p.pos < len(p.data) && p.data[p.pos] == '}' {
		p.pos++
		return obj, nil
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.data) || p.data[p.pos] != '"' {
			return nil, p.errorf("expected a string key")
		}
		key, err := p.string()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.pos >= len(p.data) || p.data[p.pos] != ':' {
			return nil, p.errorf("expected ':'")
		}
		p.pos++
		p.skipSpace()
		value, err := p.value()
		if err != nil {
			return nil, err
		}
		obj.Set(key, value)
		p.skipSpace()
		if p.pos >= len(p.data) {
			return nil, p.errorf("unexpected end of input")
		}
		switch p.data[p.pos] {
		case ',':
			p.pos++
		case '}':
			p.pos++
			return obj, nil
		default:
			return nil, p.errorf("expected ',' or '}'")
		}
	}
}

func (p *parser) array() ([]any, error) {
	p.pos++ // [
	list := []any{}
	p.skipSpace()
	if p.pos < len(p.data) && p.data[p.pos] == ']' {
		p.pos++
		return list, nil
	}
	for {
		p.skipSpace()
		value, err := p.value()
		if err != nil {
			return nil, err
		}
		list = append(list, value)
		p.skipSpace()
		if p.pos >= len(p.data) {
			return nil, p.errorf("unexpected end of input")
		}
		switch p.data[p.pos] {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return list, nil
		default:
			return nil, p.errorf("expected ',' or ']'")
		}
	}
}

// string parses a JSON string into WTF-8: valid surrogate pairs become one
// code point, and lone surrogates are kept as their 3-byte encoding.
func (p *parser) string() (string, error) {
	p.pos++ // "
	var b strings.Builder
	for {
		if p.pos >= len(p.data) {
			return "", p.errorf("unterminated string")
		}
		c := p.data[p.pos]
		switch {
		case c == '"':
			p.pos++
			return b.String(), nil
		case c < 0x20:
			return "", p.errorf("control character in string")
		case c == '\\':
			p.pos++
			if p.pos >= len(p.data) {
				return "", p.errorf("unterminated string")
			}
			esc := p.data[p.pos]
			p.pos++
			switch esc {
			case '"', '\\', '/':
				b.WriteByte(esc)
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'u':
				unit, err := p.hex4()
				if err != nil {
					return "", err
				}
				if unit >= 0xD800 && unit <= 0xDBFF && strings.HasPrefix(string(p.data[p.pos:]), `\u`) {
					save := p.pos
					p.pos += 2
					low, err := p.hex4()
					if err == nil && low >= 0xDC00 && low <= 0xDFFF {
						b.WriteRune(rune(0x10000 + (unit-0xD800)<<10 + (low - 0xDC00)))
						continue
					}
					p.pos = save
				}
				writeCodeUnit(&b, unit)
			default:
				return "", p.errorf("bad escape \\%c", esc)
			}
		default:
			r, size := utf8.DecodeRune(p.data[p.pos:])
			if r == utf8.RuneError && size <= 1 {
				// JSON.parse reads the text as UTF-16 after decoding;
				// invalid bytes become U+FFFD.
				b.WriteRune(utf8.RuneError)
				p.pos++
				continue
			}
			b.Write(p.data[p.pos : p.pos+size])
			p.pos += size
		}
	}
}

// writeCodeUnit writes a UTF-16 code unit, encoding a lone surrogate as WTF-8.
func writeCodeUnit(b *strings.Builder, unit rune) {
	if unit >= 0xD800 && unit <= 0xDFFF {
		b.WriteByte(byte(0xE0 | unit>>12))
		b.WriteByte(byte(0x80 | (unit>>6)&0x3F))
		b.WriteByte(byte(0x80 | unit&0x3F))
		return
	}
	b.WriteRune(unit)
}

func (p *parser) hex4() (rune, error) {
	if p.pos+4 > len(p.data) {
		return 0, p.errorf("bad unicode escape")
	}
	n, err := strconv.ParseUint(string(p.data[p.pos:p.pos+4]), 16, 32)
	if err != nil {
		return 0, p.errorf("bad unicode escape")
	}
	p.pos += 4
	return rune(n), nil
}

func (p *parser) number() (Number, error) {
	start := p.pos
	if p.data[p.pos] == '-' {
		p.pos++
	}
	if p.pos >= len(p.data) {
		return 0, p.errorf("bad number")
	}
	if p.data[p.pos] == '0' {
		p.pos++
	} else if p.data[p.pos] >= '1' && p.data[p.pos] <= '9' {
		for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
			p.pos++
		}
	} else {
		return 0, p.errorf("bad number")
	}
	if p.pos < len(p.data) && p.data[p.pos] == '.' {
		p.pos++
		digits := p.pos
		for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
			p.pos++
		}
		if p.pos == digits {
			return 0, p.errorf("bad number")
		}
	}
	if p.pos < len(p.data) && (p.data[p.pos] == 'e' || p.data[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.data) && (p.data[p.pos] == '+' || p.data[p.pos] == '-') {
			p.pos++
		}
		digits := p.pos
		for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
			p.pos++
		}
		if p.pos == digits {
			return 0, p.errorf("bad number")
		}
	}

	f, err := strconv.ParseFloat(string(p.data[start:p.pos]), 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, p.errorf("bad number")
	}
	// Out-of-range numbers become ±Infinity (ParseFloat returns them with
	// ErrRange), as in JavaScript.
	return Number(f), nil
}

// Stringify prints value like JSON.stringify(value, null, indent). Indent
// "" gives the compact form.
func Stringify(value any, indent string) string {
	var b strings.Builder
	write(&b, value, indent, "")
	return b.String()
}

func write(b *strings.Builder, value any, indent, prefix string) {
	switch v := value.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case Number:
		b.WriteString(FormatNumber(float64(v)))
	case string:
		WriteString(b, v)
	case []any:
		if len(v) == 0 {
			b.WriteString("[]")
			return
		}
		inner := prefix + indent
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			if indent != "" {
				b.WriteString("\n" + inner)
			}
			write(b, item, indent, inner)
		}
		if indent != "" {
			b.WriteString("\n" + prefix)
		}
		b.WriteByte(']')
	case *Object:
		if len(v.keys) == 0 {
			b.WriteString("{}")
			return
		}
		inner := prefix + indent
		b.WriteByte('{')
		for i, key := range v.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			if indent != "" {
				b.WriteString("\n" + inner)
			}
			WriteString(b, key)
			b.WriteByte(':')
			if indent != "" {
				b.WriteByte(' ')
			}
			write(b, v.values[key], indent, inner)
		}
		if indent != "" {
			b.WriteString("\n" + prefix)
		}
		b.WriteByte('}')
	default:
		panic(fmt.Sprintf("jsjson: cannot print %T", value))
	}
}

// FormatNumber prints a double as JavaScript's Number#toString does in
// JSON.stringify: shortest round-trip digits, an exponent only below 1e-6 or
// from 1e21, and null for non-finite values.
func FormatNumber(f float64) string {
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

// WriteString writes s as JSON.stringify does: quote, backslash and control
// characters escaped (\b \f \n \r \t short forms, other control characters
// as lowercase \u00XX), lone surrogates as \udXXX, everything else as is.
func WriteString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		// A lone surrogate held as WTF-8: ED A0..BF xx.
		if s[i] == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF {
			unit := rune(s[i]&0x0F)<<12 | rune(s[i+1]&0x3F)<<6 | rune(s[i+2]&0x3F)
			fmt.Fprintf(b, `\u%04x`, unit)
			i += 3
			continue
		}

		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}
