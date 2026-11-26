package mcpconfig

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// codexTable is the TOML table that holds the entry.
var codexTable = []string{"mcp_servers", ServerName}

// codexBlock returns the TOML table for entry.
func codexBlock(e Entry) string {
	var b strings.Builder
	b.WriteString("[mcp_servers." + ServerName + "]\n")
	b.WriteString("url = " + tomlString(e.URL) + "\n")
	if e.Token {
		b.WriteString("bearer_token_env_var = " + tomlString(TokenEnvVar) + "\n")
	}
	return b.String()
}

// codexWant is the entry as Codex reads it back, to check an edit.
func codexWant(e Entry) map[string]any {
	want := map[string]any{"url": e.URL}
	if e.Token {
		want["bearer_token_env_var"] = TokenEnvVar
	}
	return want
}

// editCodex replaces the [mcp_servers.prefactor] table, and its sub-tables,
// with entry, or removes them when entry is nil. The rest of the file is
// kept line for line, comments included. The result is parsed to check it
// is valid TOML with exactly the wanted entry, so a definition this edit
// cannot see, such as an inline table, is refused rather than duplicated.
func editCodex(old []byte, entry *Entry) ([]byte, error) {
	if _, err := decodeCodex(old); err != nil {
		return nil, fmt.Errorf("it is not valid TOML (%w)", err)
	}

	lines := strings.SplitAfter(string(old), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	var kept, removed []string
	insertAt := -1
	ours := false
	inString := ""
	for _, line := range lines {
		if inString == "" {
			if key, ok := tableHeader(line); ok {
				if ours && !hasPrefix(key, codexTable) {
					// Comments just above the next table belong to it.
					kept = append(kept, leadingComments(removed)...)
				}
				ours = hasPrefix(key, codexTable)
				removed = nil
				if ours && insertAt < 0 {
					insertAt = len(kept)
				}
			}
		}
		inString = multilineState(line, inString)
		if ours {
			removed = append(removed, line)
		} else {
			kept = append(kept, line)
		}
	}

	if entry != nil {
		block := codexBlock(*entry)
		if insertAt >= 0 {
			if insertAt < len(kept) {
				block += "\n"
			}
			kept = append(kept[:insertAt], append([]string{block}, kept[insertAt:]...)...)
		} else {
			if n := len(kept); n > 0 {
				if !strings.HasSuffix(kept[n-1], "\n") {
					kept[n-1] += "\n"
				}
				if strings.TrimSpace(kept[n-1]) != "" {
					kept = append(kept, "\n")
				}
			}
			kept = append(kept, block)
		}
	}
	updated := trimTrailingBlankLines(strings.Join(kept, ""))

	parsed, err := decodeCodex([]byte(updated))
	if err != nil {
		return nil, fmt.Errorf("the prefactor entry is defined in a way this command cannot replace (%w); remove it by hand and try again", err)
	}
	got, present := lookup(parsed, codexTable)
	switch {
	case entry == nil && present:
		return nil, fmt.Errorf("the prefactor entry is defined in a way this command cannot remove; remove [mcp_servers.%s] by hand", ServerName)
	case entry != nil && !reflect.DeepEqual(got, codexWant(*entry)):
		return nil, fmt.Errorf("the prefactor entry is defined in a way this command cannot replace; remove it by hand and try again")
	}
	if entry == nil && insertAt < 0 {
		return old, nil
	}
	return []byte(updated), nil
}

func decodeCodex(raw []byte) (map[string]any, error) {
	parsed := map[string]any{}
	_, err := toml.Decode(string(raw), &parsed)
	return parsed, err
}

func lookup(m map[string]any, path []string) (any, bool) {
	var cur any = m
	for _, key := range path {
		table, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = table[key]; !ok {
			return nil, false
		}
	}
	return cur, true
}

var headerLine = regexp.MustCompile(`^\s*\[\[?([^\[\]]*)\]\]?\s*(#.*)?$`)

// tableHeader returns the key of a [table] or [[array]] header line.
func tableHeader(line string) ([]string, bool) {
	m := headerLine.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
	if m == nil {
		return nil, false
	}
	return splitKey(m[1]), true
}

// splitKey splits a dotted TOML key, removing quotes and spaces.
func splitKey(key string) []string {
	var parts []string
	var b strings.Builder
	quote := rune(0)
	for _, r := range key {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			b.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
		case r == '.':
			parts = append(parts, strings.TrimSpace(b.String()))
			b.Reset()
		case r == ' ' || r == '\t':
		default:
			b.WriteRune(r)
		}
	}
	return append(parts, strings.TrimSpace(b.String()))
}

func hasPrefix(key, prefix []string) bool {
	if len(key) < len(prefix) {
		return false
	}
	for i := range prefix {
		if key[i] != prefix[i] {
			return false
		}
	}
	return true
}

// multilineState follows multi-line strings across lines, so a line inside
// one is never taken for a table header. It returns the delimiter of the
// string still open after line, or "".
func multilineState(line, open string) string {
	for {
		if open != "" {
			i := strings.Index(line, open)
			if i < 0 {
				return open
			}
			line, open = line[i+3:], ""
			continue
		}
		if i := strings.IndexByte(line, '#'); i >= 0 && !strings.Contains(line[:i], `"`) && !strings.Contains(line[:i], "'") {
			return ""
		}
		basic, literal := strings.Index(line, `"""`), strings.Index(line, "'''")
		switch {
		case basic < 0 && literal < 0:
			return ""
		case literal < 0 || (basic >= 0 && basic < literal):
			line, open = line[basic+3:], `"""`
		default:
			line, open = line[literal+3:], "'''"
		}
	}
}

// leadingComments returns the comment lines at the end of a removed table,
// from the first of them, which describe the table that follows it.
func leadingComments(lines []string) []string {
	start := len(lines)
	for start > 0 {
		trimmed := strings.TrimSpace(lines[start-1])
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			break
		}
		start--
	}
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	return lines[start:]
}

// trimTrailingBlankLines ends the file with exactly one newline, so a
// table removed from the end leaves no blank lines behind.
func trimTrailingBlankLines(s string) string {
	s = strings.TrimRight(s, "\r\n\t ")
	if s == "" {
		return ""
	}
	return s + "\n"
}

// tomlString writes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
