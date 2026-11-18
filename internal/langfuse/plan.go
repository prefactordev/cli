package langfuse

import (
	"cmp"
	"slices"
)

// Assumptions behind Estimate.
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

// Agent is one agent an import would create: the runs of one or more
// Langfuse trace names.
type Agent struct {
	Name         string            `json:"name"`
	TraceNames   []string          `json:"trace_names"`
	Environments []EnvironmentRuns `json:"environments"`
	Runs         int               `json:"runs"`
}

// Candidates turns trace counts into one agent per trace name, named after
// it, busiest first. Rows without a trace name are grouped under "".
func Candidates(counts []TraceCount) []Agent {
	var agents []Agent
	for _, count := range counts {
		i := slices.IndexFunc(agents, func(a Agent) bool { return a.TraceNames[0] == count.TraceName })
		if i < 0 {
			agents = append(agents, Agent{Name: count.TraceName, TraceNames: []string{count.TraceName}})
			i = len(agents) - 1
		}
		agents[i].Environments = addRuns(agents[i].Environments, count.Environment, count.Count)
		agents[i].Runs += count.Count
	}
	for i := range agents {
		sortEnvironments(agents[i].Environments)
	}
	SortAgents(agents)
	return agents
}

// Merge combines two agents into one called name.
func Merge(a, b Agent, name string) Agent {
	merged := Agent{Name: name, Runs: a.Runs + b.Runs}
	merged.TraceNames = append(slices.Clone(a.TraceNames), b.TraceNames...)
	merged.Environments = slices.Clone(a.Environments)
	for _, env := range b.Environments {
		merged.Environments = addRuns(merged.Environments, env.Name, env.Runs)
	}
	sortEnvironments(merged.Environments)
	return merged
}

// SortAgents orders agents busiest first, then by name.
func SortAgents(agents []Agent) {
	slices.SortStableFunc(agents, func(a, b Agent) int {
		return cmp.Or(cmp.Compare(b.Runs, a.Runs), cmp.Compare(a.Name, b.Name))
	})
}

func addRuns(envs []EnvironmentRuns, name string, runs int) []EnvironmentRuns {
	if i := slices.IndexFunc(envs, func(e EnvironmentRuns) bool { return e.Name == name }); i >= 0 {
		envs[i].Runs += runs
		return envs
	}
	return append(envs, EnvironmentRuns{Name: name, Runs: runs})
}

func sortEnvironments(envs []EnvironmentRuns) {
	slices.SortStableFunc(envs, func(a, b EnvironmentRuns) int {
		return cmp.Or(cmp.Compare(b.Runs, a.Runs), cmp.Compare(a.Name, b.Name))
	})
}

// Estimate is a rough range for how long reading runs from Langfuse takes
// at the Hobby plan's rate limit.
type Estimate struct {
	RequestsPerMinute      int `json:"requests_per_minute"`
	ObservationsPerPage    int `json:"observations_per_page"`
	ObservationsPerRunLow  int `json:"observations_per_run_low"`
	ObservationsPerRunHigh int `json:"observations_per_run_high"`
	RequestsLow            int `json:"requests_low"`
	RequestsHigh           int `json:"requests_high"`
	MinutesLow             int `json:"minutes_low"`
	MinutesHigh            int `json:"minutes_high"`
}

// EstimateFor estimates reading runs runs.
func EstimateFor(runs int) Estimate {
	e := Estimate{
		RequestsPerMinute:      HobbyRequestsPerMinute,
		ObservationsPerPage:    ObservationsPerPage,
		ObservationsPerRunLow:  ObservationsPerRunLow,
		ObservationsPerRunHigh: ObservationsPerRunHigh,
	}
	e.RequestsLow = ceilDiv(runs*ObservationsPerRunLow, ObservationsPerPage)
	e.RequestsHigh = ceilDiv(runs*ObservationsPerRunHigh, ObservationsPerPage)
	e.MinutesLow = ceilDiv(e.RequestsLow, HobbyRequestsPerMinute)
	e.MinutesHigh = ceilDiv(e.RequestsHigh, HobbyRequestsPerMinute)
	return e
}

func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}
