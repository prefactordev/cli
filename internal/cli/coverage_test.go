package cli

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// coverageEntry is one operation in api/coverage.yaml: the command that calls
// it, or a reason it has no command.
type coverageEntry struct {
	Command  string
	Excluded string
	Pending  string
}

func (e *coverageEntry) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		e.Command = node.Value
		return nil
	}

	var fields struct {
		Excluded string `yaml:"excluded"`
		Pending  string `yaml:"pending"`
	}
	if err := node.Decode(&fields); err != nil {
		return err
	}
	e.Excluded, e.Pending = fields.Excluded, fields.Pending
	return nil
}

func loadCoverage(t *testing.T) map[string]coverageEntry {
	t.Helper()

	raw, err := os.ReadFile("../../api/coverage.yaml")
	if err != nil {
		t.Fatal(err)
	}

	var coverage map[string]coverageEntry
	if err := yaml.Unmarshal(raw, &coverage); err != nil {
		t.Fatalf("parse api/coverage.yaml: %v", err)
	}
	return coverage
}

func specOperations(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile("../../api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}

	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}

	var ops []string
	for _, item := range doc.Paths {
		for _, op := range item {
			if op.OperationID != "" {
				ops = append(ops, op.OperationID)
			}
		}
	}
	sort.Strings(ops)
	return ops
}

// TestEveryOperationHasACommand fails when the OpenAPI document gains an
// operation that api/coverage.yaml does not list, so API changes cannot slip
// past the CLI unnoticed.
func TestEveryOperationHasACommand(t *testing.T) {
	coverage := loadCoverage(t)

	for _, op := range specOperations(t) {
		if _, ok := coverage[op]; !ok {
			t.Errorf("%s is in api/openapi.json but not in api/coverage.yaml", op)
		}
	}
}

func TestCoverageListsOnlyOperationsInTheDocument(t *testing.T) {
	known := map[string]bool{}
	for _, op := range specOperations(t) {
		known[op] = true
	}

	for op := range loadCoverage(t) {
		if !known[op] {
			t.Errorf("%s is in api/coverage.yaml but not in api/openapi.json", op)
		}
	}
}

func TestCoverageCommandsExist(t *testing.T) {
	root := newRootCommand(&App{Version: "test"})

	for op, entry := range loadCoverage(t) {
		set := 0
		for _, value := range []string{entry.Command, entry.Excluded, entry.Pending} {
			if value != "" {
				set++
			}
		}
		if set != 1 {
			t.Errorf("%s must have exactly one of a command, excluded or pending", op)
			continue
		}

		if entry.Command == "" {
			continue
		}

		words := strings.Fields(entry.Command)
		found, rest, err := root.Find(words)
		if err != nil || len(rest) != 0 || found.CommandPath() != "prefactor "+entry.Command {
			t.Errorf("%s maps to %q, which is not a command", op, entry.Command)
		}
	}
}
