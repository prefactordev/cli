package langfuse

import (
	"reflect"
	"testing"
)

func TestCandidatesGroupByTraceNameBusiestFirst(t *testing.T) {
	agents := Candidates([]TraceCount{
		{"quiet", "production", 2},
		{"busy", "development", 1},
		{"busy", "production", 10},
		{"also-quiet", "production", 2},
	})
	want := []Agent{
		{Name: "busy", TraceNames: []string{"busy"}, Environments: []EnvironmentRuns{{"production", 10}, {"development", 1}}, Runs: 11},
		{Name: "also-quiet", TraceNames: []string{"also-quiet"}, Environments: []EnvironmentRuns{{"production", 2}}, Runs: 2},
		{Name: "quiet", TraceNames: []string{"quiet"}, Environments: []EnvironmentRuns{{"production", 2}}, Runs: 2},
	}
	if !reflect.DeepEqual(agents, want) {
		t.Fatalf("agents = %+v", agents)
	}
}

func TestMergeAddsRunsByEnvironment(t *testing.T) {
	a := Agent{Name: "a", TraceNames: []string{"a"}, Environments: []EnvironmentRuns{{"production", 3}}, Runs: 3}
	b := Agent{Name: "b", TraceNames: []string{"b"}, Environments: []EnvironmentRuns{{"development", 5}, {"production", 1}}, Runs: 6}

	merged := Merge(a, b, "support")
	want := Agent{Name: "support", TraceNames: []string{"a", "b"}, Environments: []EnvironmentRuns{{"development", 5}, {"production", 4}}, Runs: 9}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged = %+v", merged)
	}
	if len(a.Environments) != 1 || a.Environments[0].Runs != 3 {
		t.Fatalf("Merge changed its input: %+v", a)
	}
}

func TestEstimateFor(t *testing.T) {
	e := EstimateFor(101)
	if e.RequestsLow != 31 || e.RequestsHigh != 41 || e.MinutesLow != 2 || e.MinutesHigh != 2 {
		t.Fatalf("estimate = %+v", e)
	}
	if e := EstimateFor(0); e.RequestsHigh != 0 || e.MinutesHigh != 0 {
		t.Fatalf("estimate for no runs = %+v", e)
	}
}
