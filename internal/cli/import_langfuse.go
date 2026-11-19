package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/human"
	"github.com/prefactordev/cli/internal/langfuse"
)

const langfuseDefaultDays = "30"

// langfusePlan is what import langfuse --dry-run prints.
type langfusePlan struct {
	KeysSource langfuse.Source  `json:"keys_source"`
	Langfuse   langfuseTarget   `json:"langfuse"`
	Window     langfuseWindow   `json:"window"`
	Agents     []langfuse.Agent `json:"agents"`
	TotalRuns  int              `json:"total_runs"`
}

type langfuseTarget struct {
	BaseURL  string             `json:"base_url"`
	Projects []langfuse.Project `json:"projects"`
}

type langfuseWindow struct {
	Days int    `json:"days"`
	From string `json:"from"`
	To   string `json:"to"`
}

func newImportCommand(app *App) *cobra.Command {
	group := &cobra.Command{
		Use:   "import",
		Short: "Import agents and their runs from other tools",
	}
	importLangfuse := &cobra.Command{
		Use:   "langfuse",
		Short: "Import agents and runs from Langfuse (only --dry-run works so far)",
		Args:  cobra.NoArgs,
		Example: strings.Join([]string{
			"  prefactor import langfuse --dry-run",
			"  prefactor import langfuse --dry-run --env .env.langfuse --days 7",
		}, "\n"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.importLangfuse(cmd)
		},
	}
	importLangfuse.Flags().Bool("dry-run", false, "Show what an import would bring in, sending nothing to Prefactor")
	importLangfuse.Flags().String("env", "", "Read the Langfuse keys from this file only, in .env format; by default they come from the shell environment, then the nearest .env")
	setPlaceholder(importLangfuse, "env", "file")
	importLangfuse.Flags().String("days", langfuseDefaultDays, "Look for runs from this many days back")
	setPlaceholder(importLangfuse, "days", "n")
	group.AddCommand(importLangfuse)
	return group
}

func (a *App) importLangfuse(cmd *cobra.Command) error {
	days, err := parsePositiveInt(stringFlag(cmd, "days"))
	if err != nil {
		return errors.New("--days must be a positive integer.")
	}
	if !boolFlag(cmd, "dry-run") {
		return errors.New("Uploading Langfuse runs to Prefactor is not available yet. Run 'prefactor import langfuse --dry-run' to see what an import would bring in.")
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	keys, err := langfuse.FindKeys(langfuse.FindOptions{LookupEnv: a.LookupEnv, Dir: dir, File: stringFlag(cmd, "env")})
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	status := cmd.ErrOrStderr()
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -days)

	fmt.Fprintf(status, "Using Langfuse keys from %s.\n", keys.Source)
	if keys.HostSource.Kind == langfuse.SourceDefault {
		fmt.Fprintf(status, "Using the default Langfuse host %s, as %s and %s are not set.\n", keys.BaseURL, langfuse.BaseURLVar, langfuse.HostVar)
	} else {
		fmt.Fprintf(status, "Using the Langfuse host %s from %s.\n", keys.BaseURL, keys.HostSource)
	}
	if err := langfuse.CheckRegion(keys.BaseURL); err != nil {
		return err
	}
	client := langfuse.NewClient(keys, a.Version)
	projects, err := client.Projects(ctx)
	if err != nil {
		return langfuseKeyError(err, keys)
	}
	for _, p := range projects {
		fmt.Fprintf(status, "Langfuse project: %s at %s.\n", projectLabel(p), keys.BaseURL)
	}
	if err := client.CheckTracesRead(ctx, from, to); err != nil {
		return langfuseReadError(err)
	}
	fmt.Fprintln(status, "The key can read traces.")

	counts, err := client.TraceCounts(ctx, from, to)
	if err != nil {
		return err
	}
	agents := langfuse.Candidates(counts)
	if len(agents) > 0 && a.canPick() {
		if agents, err = a.pickLangfuseAgents(agents); err != nil {
			return err
		}
	}

	plan := langfusePlan{
		KeysSource: keys.Source,
		Langfuse:   langfuseTarget{BaseURL: keys.BaseURL, Projects: projects},
		Window:     langfuseWindow{Days: days, From: langfuse.Timestamp(from), To: langfuse.Timestamp(to)},
		Agents:     agents,
	}
	if plan.Langfuse.Projects == nil {
		plan.Langfuse.Projects = []langfuse.Project{}
	}
	if plan.Agents == nil {
		plan.Agents = []langfuse.Agent{}
	}
	for _, agent := range plan.Agents {
		plan.TotalRuns += agent.Runs
	}

	out := cmd.OutOrStdout()
	if a.styled(out) {
		return writeLangfusePlan(human.Writer(out, os.Environ()), plan, human.DefaultTheme())
	}
	return a.printJSONValue(cmd, plan)
}

// langfuseKeyError explains a 401 from Langfuse, which usually means the
// keys belong to another region or host.
func langfuseKeyError(err error, keys langfuse.Keys) error {
	var apiErr *langfuse.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		return err
	}
	return fmt.Errorf("%s\nLangfuse at %s rejected the keys from %s. Keys work only with the region or host they were created in: set %s to https://cloud.langfuse.com for the EU region, https://us.cloud.langfuse.com for the US region, or the address of your own Langfuse.",
		apiErr.Error(), keys.BaseURL, keys.Source, langfuse.BaseURLVar)
}

// langfuseReadError explains a 403 from the read check: how to create a key
// that can read traces, and which variables to set to it.
func langfuseReadError(err error) error {
	var apiErr *langfuse.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		return err
	}
	return fmt.Errorf("%s\nThese keys cannot read traces. Create a Viewer key in Langfuse: open the project, go to Project Settings > API Keys, "+
		`choose "Create new API keys" and pick the Viewer role. Full access and Developer keys also work. `+
		"Set %s and %s to the new key, or pass --env <file>, then run the command again.",
		apiErr.Error(), langfuse.PublicKeyVar, langfuse.SecretKeyVar)
}

// pickLangfuseAgents lets the user choose which agents to import.
func (a *App) pickLangfuseAgents(candidates []langfuse.Agent) ([]langfuse.Agent, error) {
	choices := make([]Choice, len(candidates))
	all := make([]string, len(candidates))
	for i, agent := range candidates {
		all[i] = strconv.Itoa(i)
		choices[i] = Choice{Label: agentLabel(agent), Value: all[i]}
	}
	picked, err := a.Picker.MultiPick("Choose the agents to import", "Busiest first. Space ticks or unticks one; enter confirms.", choices, all)
	if err != nil {
		return nil, err
	}
	var plan []langfuse.Agent
	for i, agent := range candidates {
		if slices.Contains(picked, strconv.Itoa(i)) {
			plan = append(plan, agent)
		}
	}
	if len(plan) == 0 {
		return nil, errors.New("No agents chosen, so there is nothing to import.")
	}
	return plan, nil
}

// displayName shows runs without a trace name, which Langfuse allows.
func displayName(name string) string {
	if name == "" {
		return "(no trace name)"
	}
	return name
}

// agentLabel is one line about an agent: its name, runs and
// environments.
func agentLabel(agent langfuse.Agent) string {
	return fmt.Sprintf("%s  %s  (%s)", displayName(agent.Name), plural(agent.Runs, "run"), environmentsText(agent))
}

// environmentsText lists an agent's runs in each environment.
func environmentsText(agent langfuse.Agent) string {
	envs := make([]string, len(agent.Environments))
	for i, env := range agent.Environments {
		envs[i] = fmt.Sprintf("%s %d", displayName(env.Name), env.Runs)
	}
	return strings.Join(envs, ", ")
}

// planSummary lists the agents in a plan and the totals.
func planSummary(plan []langfuse.Agent) string {
	lines := make([]string, 0, len(plan)+1)
	total := 0
	for _, agent := range plan {
		lines = append(lines, agentLabel(agent))
		total += agent.Runs
	}
	return strings.Join(append(lines, "Total: "+plural(total, "run")+" in "+plural(len(plan), "agent")+"."), "\n")
}

// estimateText says roughly how long reading runs from Langfuse takes.
func estimateText(runs int) string {
	low, high := langfuse.EstimateMinutes(runs)
	text := fmt.Sprintf("about %d to %s", low, plural(high, "minute"))
	if low == high {
		text = "about " + plural(low, "minute")
	}
	return fmt.Sprintf("Reading these runs from Langfuse takes %s at the Hobby plan's limit of %d requests a minute.", text, langfuse.HobbyRequestsPerMinute)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func projectLabel(p langfuse.Project) string {
	if p.Organization.Name == "" {
		return p.Name
	}
	return fmt.Sprintf("%s (organisation: %s)", p.Name, p.Organization.Name)
}

// writeLangfusePlan prints a plan for a person at a terminal.
func writeLangfusePlan(w io.Writer, plan langfusePlan, t human.Theme) error {
	var b strings.Builder
	b.WriteString(t.Title.Render("Langfuse import plan") + "  " + t.Muted.Render("Dry run: nothing was sent to Prefactor.") + "\n\n")

	projects := make([]string, len(plan.Langfuse.Projects))
	for i, p := range plan.Langfuse.Projects {
		projects[i] = projectLabel(p)
	}
	details := [][2]string{
		{"Langfuse", strings.TrimPrefix(strings.Join(projects, ", ")+" at "+plan.Langfuse.BaseURL, " ")},
		{"Keys from", plan.KeysSource.String()},
		{"Runs since", plan.Window.From[:10] + " (the last " + plural(plan.Window.Days, "day") + ")"},
	}
	for _, d := range details {
		b.WriteString(t.Muted.Render(fmt.Sprintf("%-12s", d[0])) + d[1] + "\n")
	}
	b.WriteString("\n")

	if len(plan.Agents) == 0 {
		b.WriteString("No runs found in the last " + plural(plan.Window.Days, "day") + ".\n")
		_, err := io.WriteString(w, b.String())
		return err
	}

	b.WriteString(planSummary(plan.Agents) + "\n")
	b.WriteString(estimateText(plan.TotalRuns) + "\n")
	_, err := io.WriteString(w, b.String())
	return err
}
