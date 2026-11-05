// Package js reproduces JavaScript string behaviour the TypeScript CLI
// relies on.
package js

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// isSpace reports whether String.prototype.trim removes r: the Unicode
// space separators, the line terminators, tab, vertical tab, form feed and
// U+FEFF, but not U+0085, which Go's unicode.IsSpace includes.
func isSpace(r rune) bool {
	return r == '\uFEFF' || (unicode.IsSpace(r) && r != '\u0085')
}

// Trim is String.prototype.trim.
func Trim(s string) string {
	return strings.TrimFunc(s, isSpace)
}

// TrimStart is String.prototype.trimStart, which Number.parseInt also
// applies.
func TrimStart(s string) string {
	return strings.TrimLeftFunc(s, isSpace)
}

var decimal = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)

// Number is the Number() conversion of a string: surrounding whitespace is
// ignored, a blank string is 0, 0x, 0o and 0b prefix integers, and anything
// else that is not a decimal literal or Infinity is NaN.
func Number(s string) float64 {
	s = Trim(s)
	if s == "" {
		return 0
	}
	if len(s) > 2 && s[0] == '0' {
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[s[1]]
		if base != 0 {
			n, err := strconv.ParseUint(s[2:], base, 64)
			if err != nil || strings.Contains(s[2:], "_") {
				return math.NaN()
			}
			return float64(n)
		}
	}
	switch strings.TrimLeft(s, "+-") {
	case "Infinity":
		if strings.HasPrefix(s, "-") {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	if !decimal.MatchString(s) {
		return math.NaN()
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return math.NaN()
	}
	return f
}
