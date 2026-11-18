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
	DryRun     bool              `json:"dry_run"`
	KeysSource langfuse.Source   `json:"keys_source"`
	HostSource langfuse.Source   `json:"host_source"`
	Langfuse   langfuseTarget    `json:"langfuse"`
	Window     langfuseWindow    `json:"window"`
	Agents     []langfuse.Agent  `json:"agents"`
	TotalRuns  int               `json:"total_runs"`
	Estimate   langfuse.Estimate `json:"estimate"`
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
	importLangfuse.Flags().String("env", "", "Read the Langfuse keys from this file only (.env format, or a mise TOML file); by default they come from the shell environment, the nearest .env, then mise.local.toml and mise.toml")
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
	var noKeys *langfuse.NoKeysError
	if errors.As(err, &noKeys) {
		keys, err = a.pasteMissingKeys(cmd, noKeys)
	}
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	status := cmd.ErrOrStderr()
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -days)

	var client *langfuse.Client
	var projects []langfuse.Project
	for {
		fmt.Fprintf(status, "Using Langfuse keys from %s.\n", keys.Source)
		if keys.HostSource.Kind == langfuse.SourceDefault {
			fmt.Fprintf(status, "Using the default Langfuse host %s, as %s and %s are not set.\n", keys.BaseURL, langfuse.BaseURLVar, langfuse.HostVar)
		} else {
			fmt.Fprintf(status, "Using the Langfuse host %s from %s.\n", keys.BaseURL, keys.HostSource)
		}
		if err := langfuse.CheckRegion(keys.BaseURL); err != nil {
			return err
		}
		client = langfuse.NewClient(keys, a.Version)
		if projects, err = client.Projects(ctx); err != nil {
			return langfuseKeyError(err, keys)
		}
		for _, p := range projects {
			fmt.Fprintf(status, "Langfuse project: %s at %s.\n", projectLabel(p), keys.BaseURL)
		}

		err = client.CheckTracesRead(ctx, from, to)
		var apiErr *langfuse.APIError
		if err == nil {
			break
		}
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
			return err
		}
		if keys, err = a.pasteReadableKeys(cmd, apiErr, keys); err != nil {
			return err
		}
	}
	fmt.Fprintln(status, "The key can read traces.")

	counts, err := client.TraceCounts(ctx, from, to)
	if err != nil {
		return err
	}
	agents := langfuse.Candidates(counts)
	if len(agents) > 0 && a.canPick() {
		if agents, err = a.editLangfusePlan(cmd, agents); err != nil {
			return err
		}
	}

	plan := langfusePlan{
		DryRun:     true,
		KeysSource: keys.Source,
		HostSource: keys.HostSource,
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
	plan.Estimate = langfuse.EstimateFor(plan.TotalRuns)

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

// langfuseReadHelp explains a 403 from the read check and how to get a key
// that can read traces.
func langfuseReadHelp(apiErr *langfuse.APIError) string {
	return apiErr.Error() + "\n" +
		"These keys cannot read traces. Create a Viewer key in Langfuse: open the project, go to Project Settings > API Keys, " +
		`choose "Create new API keys" and pick the Viewer role. Full access and Developer keys also work.`
}

// pasteReadableKeys asks for keys that can read traces, for this run only,
// on the same host.
func (a *App) pasteReadableKeys(cmd *cobra.Command, apiErr *langfuse.APIError, keys langfuse.Keys) (langfuse.Keys, error) {
	help := langfuseReadHelp(apiErr)
	notInteractive := fmt.Errorf("%s\nSet %s and %s to the new key, or pass --env <file>, then run the command again.",
		help, langfuse.PublicKeyVar, langfuse.SecretKeyVar)
	return a.pasteKeys(cmd, help, notInteractive, keys)
}

// pasteMissingKeys asks for keys when none were found, for this run only,
// on the host the shell environment sets or the default.
func (a *App) pasteMissingKeys(cmd *cobra.Command, noKeys *langfuse.NoKeysError) (langfuse.Keys, error) {
	host, from, err := langfuse.ShellHost(a.LookupEnv)
	if err != nil {
		return langfuse.Keys{}, err
	}
	if err := langfuse.CheckRegion(host); err != nil {
		return langfuse.Keys{}, err
	}
	return a.pasteKeys(cmd, noKeys.Error(), noKeys, langfuse.Keys{BaseURL: host, HostSource: from})
}

// pasteKeys explains problem and asks for a public and secret key with
// hidden input, used for this run only and never saved. A coding agent is
// never asked, because keys pasted into its chat go to its model provider;
// other sessions that cannot prompt get notInteractive.
func (a *App) pasteKeys(cmd *cobra.Command, problem string, notInteractive error, keys langfuse.Keys) (langfuse.Keys, error) {
	if agent := a.detectAgent(); agent != "" {
		return keys, fmt.Errorf("%s\nA coding agent is running this command (%s is set), so the CLI will not ask for a key. Set %s and %s in your shell or the project's .env yourself, then run the command again. Do not paste the keys into the agent's chat: that sends them to the model provider.",
			problem, agent, langfuse.PublicKeyVar, langfuse.SecretKeyVar)
	}
	if !a.canPick() {
		return keys, notInteractive
	}

	fmt.Fprintln(cmd.ErrOrStderr(), problem+"\nPaste a Langfuse key below. It is used for this run only and is not saved.")
	public, err := a.Picker.Secret("Langfuse public key (pk-lf-...)")
	if err != nil {
		return keys, err
	}
	secret, err := a.Picker.Secret("Langfuse secret key (sk-lf-...)")
	if err != nil {
		return keys, err
	}
	return langfuse.Keys{
		PublicKey:  langfuse.NewSecret(public),
		SecretKey:  langfuse.NewSecret(secret),
		BaseURL:    keys.BaseURL,
		Source:     langfuse.Source{Kind: langfuse.SourcePasted},
		HostSource: keys.HostSource,
	}, nil
}

// Choices in the plan review.
const (
	planContinue = "continue"
	planRename   = "rename"
	planMerge    = "merge"
)

// editLangfusePlan lets the user choose which agents to import, then rename
// them or merge two into one.
func (a *App) editLangfusePlan(cmd *cobra.Command, candidates []langfuse.Agent) ([]langfuse.Agent, error) {
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

	for {
		options := []Choice{{Label: "Continue with this plan", Value: planContinue}, {Label: "Rename an agent", Value: planRename}}
		if len(plan) > 1 {
			options = append(options, Choice{Label: "Merge two agents into one", Value: planMerge})
		}
		action, err := a.Picker.Pick("Review the import plan", planSummary(plan), options)
		if err != nil {
			return nil, err
		}

		switch action {
		case planContinue:
			return plan, nil
		case planRename:
			i, err := a.pickPlannedAgent("Choose the agent to rename", plan, -1)
			if err != nil {
				return nil, err
			}
			name, err := a.Picker.Input("New name for "+displayName(plan[i].Name), true)
			if err != nil {
				return nil, err
			}
			if nameTaken(plan, name, i, -1) {
				fmt.Fprintf(cmd.ErrOrStderr(), "There is already an agent called %s. Merge the two instead.\n", name)
				continue
			}
			plan[i].Name = name
		case planMerge:
			i, err := a.pickPlannedAgent("Choose the first agent to merge", plan, -1)
			if err != nil {
				return nil, err
			}
			j, err := a.pickPlannedAgent("Choose the agent to merge into "+displayName(plan[i].Name), plan, i)
			if err != nil {
				return nil, err
			}
			name, err := a.Picker.Input("Name for the merged agent (leave blank for "+displayName(plan[i].Name)+")", false)
			if err != nil {
				return nil, err
			}
			if name == "" {
				name = plan[i].Name
			}
			if nameTaken(plan, name, i, j) {
				fmt.Fprintf(cmd.ErrOrStderr(), "There is already an agent called %s. Choose another name.\n", name)
				continue
			}
			rest := []langfuse.Agent{langfuse.Merge(plan[i], plan[j], name)}
			for k, agent := range plan {
				if k != i && k != j {
					rest = append(rest, agent)
				}
			}
			plan = rest
			langfuse.SortAgents(plan)
		}
	}
}

// pickPlannedAgent asks for one agent in plan other than skip and returns
// its index.
func (a *App) pickPlannedAgent(title string, plan []langfuse.Agent, skip int) (int, error) {
	var choices []Choice
	for i, agent := range plan {
		if i != skip {
			choices = append(choices, Choice{Label: agentLabel(agent), Value: strconv.Itoa(i)})
		}
	}
	value, err := a.Picker.Pick(title, "", choices)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(value)
}

// nameTaken reports whether an agent in plan, other than those at skip1
// and skip2, is called name.
func nameTaken(plan []langfuse.Agent, name string, skip1, skip2 int) bool {
	for i, agent := range plan {
		if i != skip1 && i != skip2 && agent.Name == name {
			return true
		}
	}
	return false
}

// displayName shows runs without a trace name, which Langfuse allows.
func displayName(name string) string {
	if name == "" {
		return "(no trace name)"
	}
	return name
}

// agentLabel is one line about an agent: its name, runs, environments and,
// when it was renamed or merged, the trace names it imports.
func agentLabel(agent langfuse.Agent) string {
	return strings.TrimRight(fmt.Sprintf("%s  %s  (%s)  %s", displayName(agent.Name), plural(agent.Runs, "run"), environmentsText(agent), traceNamesText(agent)), " ")
}

// environmentsText lists an agent's runs in each environment.
func environmentsText(agent langfuse.Agent) string {
	envs := make([]string, len(agent.Environments))
	for i, env := range agent.Environments {
		envs[i] = fmt.Sprintf("%s %d", displayName(env.Name), env.Runs)
	}
	return strings.Join(envs, ", ")
}

// traceNamesText lists the trace names an agent imports when they differ
// from its name, because it was renamed or merged.
func traceNamesText(agent langfuse.Agent) string {
	if len(agent.TraceNames) == 1 && agent.TraceNames[0] == agent.Name {
		return ""
	}
	names := make([]string, len(agent.TraceNames))
	for i, name := range agent.TraceNames {
		names[i] = displayName(name)
	}
	return "from " + strings.Join(names, ", ")
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

// estimateText describes an estimate in a sentence.
func estimateText(e langfuse.Estimate) string {
	return fmt.Sprintf("Reading these runs from Langfuse needs %s, which take %s at the Hobby plan's limit of %d requests a minute (assuming %d to %d observations a run and %d a request).",
		rangeText(e.RequestsLow, e.RequestsHigh, "request"), rangeText(e.MinutesLow, e.MinutesHigh, "minute"),
		e.RequestsPerMinute, e.ObservationsPerRunLow, e.ObservationsPerRunHigh, e.ObservationsPerPage)
}

func rangeText(low, high int, noun string) string {
	if low == high {
		return "about " + plural(low, noun)
	}
	return fmt.Sprintf("about %d to %s", low, plural(high, noun))
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

	width, runsWidth := 0, 0
	for _, agent := range plan.Agents {
		width = max(width, len(displayName(agent.Name)))
		runsWidth = max(runsWidth, len(plural(agent.Runs, "run")))
	}
	for _, agent := range plan.Agents {
		name := displayName(agent.Name)
		line := "  " + t.Command.Render(name) + strings.Repeat(" ", width-len(name)) + "  " +
			fmt.Sprintf("%-*s  ", runsWidth, plural(agent.Runs, "run")) + t.Muted.Render(environmentsText(agent))
		if from := traceNamesText(agent); from != "" {
			line += t.Muted.Render("  " + from)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\nTotal: " + plural(plan.TotalRuns, "run") + " in " + plural(len(plan.Agents), "agent") + ".\n")
	b.WriteString(estimateText(plan.Estimate) + "\n")
	_, err := io.WriteString(w, b.String())
	return err
}
