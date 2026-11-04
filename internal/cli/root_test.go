package cli

import (
	"bytes"
	"testing"
)

func TestVersionCommandPrintsVersion(t *testing.T) {
	root := NewRootCommand("1.2.3")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if got, want := out.String(), "1.2.3\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
