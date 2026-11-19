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
		{Name: "busy", Environments: []EnvironmentRuns{{"production", 10}, {"development", 1}}, Runs: 11},
		{Name: "also-quiet", Environments: []EnvironmentRuns{{"production", 2}}, Runs: 2},
		{Name: "quiet", Environments: []EnvironmentRuns{{"production", 2}}, Runs: 2},
	}
	if !reflect.DeepEqual(agents, want) {
		t.Fatalf("agents = %+v", agents)
	}
}

func TestEstimateMinutes(t *testing.T) {
	if low, high := EstimateMinutes(101); low != 2 || high != 2 {
		t.Fatalf("estimate = %d to %d", low, high)
	}
	if low, high := EstimateMinutes(1000); low != 10 || high != 14 {
		t.Fatalf("estimate = %d to %d", low, high)
	}
	if low, high := EstimateMinutes(0); low != 0 || high != 0 {
		t.Fatalf("estimate for no runs = %d to %d", low, high)
	}
}
