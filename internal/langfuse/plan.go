package langfuse

import (
	"cmp"
	"slices"
)

// Assumptions behind EstimateMinutes.
const (
	// HobbyRequestsPerMinute is the Langfuse Hobby plan's rate limit for
	// the observations API.
	HobbyRequestsPerMinute = 30
	// ObservationsPerPage is how many observations one request reads.
	ObservationsPerPage = 50
	// ObservationsPerRunLow and ObservationsPerRunHigh bound how many
	// observations a typical run has.
	ObservationsPerRunLow  = 15
	ObservationsPerRunHigh = 20
)

// EnvironmentRuns is how many runs an agent has in one Langfuse
// environment.
type EnvironmentRuns struct {
	Name string `json:"name"`
	Runs int    `json:"runs"`
}

// Agent is one agent an import would create. Name is the Langfuse trace
// name, which Prefactor uses as the agent's name; it is "" for runs
// without a trace name.
type Agent struct {
	Name         string            `json:"name"`
	Environments []EnvironmentRuns `json:"environments"`
	Runs         int               `json:"runs"`
}

// Candidates turns trace counts into one agent per trace name, busiest
// first, then by name. Rows without a trace name are grouped under "".
func Candidates(counts []TraceCount) []Agent {
	var agents []Agent
	for _, count := range counts {
		i := slices.IndexFunc(agents, func(a Agent) bool { return a.Name == count.TraceName })
		if i < 0 {
			agents = append(agents, Agent{Name: count.TraceName})
			i = len(agents) - 1
		}
		agents[i].Environments = addRuns(agents[i].Environments, count.Environment, count.Count)
		agents[i].Runs += count.Count
	}
	for i := range agents {
		slices.SortStableFunc(agents[i].Environments, func(a, b EnvironmentRuns) int {
			return cmp.Or(cmp.Compare(b.Runs, a.Runs), cmp.Compare(a.Name, b.Name))
		})
	}
	slices.SortStableFunc(agents, func(a, b Agent) int {
		return cmp.Or(cmp.Compare(b.Runs, a.Runs), cmp.Compare(a.Name, b.Name))
	})
	return agents
}

func addRuns(envs []EnvironmentRuns, name string, runs int) []EnvironmentRuns {
	if i := slices.IndexFunc(envs, func(e EnvironmentRuns) bool { return e.Name == name }); i >= 0 {
		envs[i].Runs += runs
		return envs
	}
	return append(envs, EnvironmentRuns{Name: name, Runs: runs})
}

// EstimateMinutes is a rough range for how many minutes reading runs runs
// from Langfuse takes at the Hobby plan's rate limit.
func EstimateMinutes(runs int) (low, high int) {
	minutes := func(perRun int) int {
		requests := ceilDiv(runs*perRun, ObservationsPerPage)
		return ceilDiv(requests, HobbyRequestsPerMinute)
	}
	return minutes(ObservationsPerRunLow), minutes(ObservationsPerRunHigh)
}

func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}
