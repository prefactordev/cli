package parity

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The case files are shared by the parity tests, which run each case
// through both CLIs, and the contract tests, which check what this CLI
// sends against the OpenAPI document.

type parityCase struct {
	file string
	line int
	help bool
	// empty, set by an "empty:" prefix, makes the contract tests' server
	// answer lists with no items.
	empty bool
	words []string
	args  []string
}

func loadCases(t *testing.T) []parityCase {
	t.Helper()
	return loadCasesFrom(t, "cases")
}

// loadCasesFrom reads the case files in dir.
func loadCasesFrom(t *testing.T, dir string) []parityCase {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(dir, "*.txt"))
	if err != nil {
		t.Fatal(err)
	}

	var cases []parityCase
	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			t.Fatal(err)
		}

		scanner := bufio.NewScanner(f)
		line := 0
		for scanner.Scan() {
			line++
			text := strings.TrimSpace(scanner.Text())
			if text == "" || strings.HasPrefix(text, "#") {
				continue
			}

			tc := parityCase{file: filepath.Base(file), line: line}
			if rest, ok := strings.CutPrefix(text, "help:"); ok {
				tc.help = true
				text = strings.TrimSpace(rest)
			}
			if rest, ok := strings.CutPrefix(text, "empty:"); ok {
				tc.empty = true
				text = strings.TrimSpace(rest)
			}

			words, args, _ := strings.Cut(text, "|")
			tc.words = strings.Fields(words)
			parsed, err := splitArgs(args)
			if err != nil {
				t.Fatalf("%s:%d: %v", file, line, err)
			}
			tc.args = parsed
			cases = append(cases, tc)
		}
		_ = f.Close()
	}

	return cases
}

// splitArgs splits a line into arguments, honouring single and double quotes.
func splitArgs(s string) ([]string, error) {
	var args []string
	var current strings.Builder
	inArg := false
	var quote rune

	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inArg = true
		case r == ' ' || r == '\t':
			if inArg {
				args = append(args, current.String())
				current.Reset()
				inArg = false
			}
		default:
			current.WriteRune(r)
			inArg = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	if inArg {
		args = append(args, current.String())
	}

	return args, nil
}

func kebabWords(words []string) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = strings.ReplaceAll(w, "_", "-")
	}
	return out
}

var flagName = regexp.MustCompile(`^--[a-z0-9_]+`)

func kebabFlags(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = flagName.ReplaceAllStringFunc(a, func(f string) string { return strings.ReplaceAll(f, "_", "-") })
	}
	return out
}
