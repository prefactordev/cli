package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/prefactordev/cli/internal/api"
	"github.com/prefactordev/cli/internal/client"
)

// latestInstance stands for the most recent instance of the selected agent
// wherever an agent instance ID is accepted.
const latestInstance = "@latest"

// binding is an argument or flag of a command that names a resource.
type binding struct {
	kind     *resourceKind
	argIndex int // position of the argument, or -1 for a flag
	flag     *pflag.Flag
	required bool
}

// bindings lists the arguments and flags of cmd that name resources.
func bindings(cmd *cobra.Command) []binding {
	ref := commandRef{group: commandGroup(cmd)}

	var result []binding
	for i, arg := range declaredArguments(cmd) {
		kind := argumentKinds[arg.name]
		if arg.name == "id" {
			kind = groupKinds[ref.group]
		}
		if kind != nil {
			result = append(result, binding{kind: kind, argIndex: i, required: arg.required})
		}
	}

	local := cmd.LocalFlags()
	local.SortFlags = false
	local.VisitAll(func(f *pflag.Flag) {
		if kind := flagKind(ref, f.Name); kind != nil && takesValue(f) {
			_, required := f.Annotations[requiredAnnotation]
			result = append(result, binding{kind: kind, argIndex: -1, flag: cmd.Flags().Lookup(f.Name), required: required})
		}
	})

	return result
}

// commandGroup returns the first command word after prefactor.
func commandGroup(cmd *cobra.Command) string {
	words := strings.Fields(cmd.CommandPath())
	if len(words) < 2 {
		return ""
	}
	return words[1]
}

func (b binding) value(args []string) (string, bool) {
	if b.argIndex >= 0 {
		if b.argIndex < len(args) {
			return args[b.argIndex], true
		}
		return "", false
	}
	return b.flag.Value.String(), b.flag.Changed
}

// resolver fills in resource IDs for one run.
type resolver struct {
	app    *App
	cmd    *cobra.Command
	client *api.Client
	// known holds IDs already given or picked, by resource kind.
	known map[*resourceKind]string
}

// missingPickable reports whether the command lacks a required resource ID
// that a picker could supply.
func missingPickable(cmd *cobra.Command, args []string) bool {
	for _, b := range bindings(cmd) {
		if _, given := b.value(args); b.required && !given {
			return true
		}
	}
	return false
}

func (a *App) newResolver(cmd *cobra.Command, args []string) (*resolver, error) {
	c, err := a.authedClient(cmd)
	if err != nil {
		return nil, err
	}

	r := &resolver{app: a, cmd: cmd, client: c, known: map[*resourceKind]string{}}
	for _, b := range bindings(cmd) {
		if value, given := b.value(args); given && value != "" && value != latestInstance {
			r.known[b.kind] = value
		}
	}
	return r, nil
}

// pickMissing asks for every required resource ID the command is missing,
// in argument then flag order, and returns the completed arguments.
func (a *App) pickMissing(cmd *cobra.Command, args []string) ([]string, error) {
	r, err := a.newResolver(cmd, args)
	if err != nil {
		return nil, err
	}

	for _, b := range bindings(cmd) {
		if _, given := b.value(args); !b.required || given {
			continue
		}
		if b.argIndex >= 0 && b.argIndex != len(args) {
			continue
		}

		id, err := r.pick(b.kind)
		if err != nil {
			return nil, err
		}

		if b.argIndex >= 0 {
			args = append(args, id)
		} else if err := cmd.Flags().Set(b.flag.Name, id); err != nil {
			return nil, err
		}
	}

	return args, nil
}

// pick returns an ID of kind, picking its parent first when the command
// does not already have it. A single choice is used without asking.
func (r *resolver) pick(kind *resourceKind) (string, error) {
	if id, ok := r.known[kind]; ok {
		return id, nil
	}

	parentID := ""
	if kind.parent != nil {
		var err error
		if parentID, err = r.pick(kind.parent); err != nil {
			return "", err
		}
	}

	choices, err := listChoices(r.cmd.Context(), r.client, kind, parentID)
	if err != nil {
		return "", err
	}

	var id string
	switch len(choices) {
	case 0:
		return "", fmt.Errorf("No %s found to pick from.", kind.plural)
	case 1:
		id = choices[0].Value
		fmt.Fprintf(r.cmd.ErrOrStderr(), "Using %s %s\n", kind.name, choices[0].Label)
	default:
		if id, err = r.app.Picker.Pick("Choose "+article(kind.name)+" "+kind.name, choices); err != nil {
			return "", err
		}
	}

	r.known[kind] = id
	return id, nil
}

func article(noun string) string {
	if strings.ContainsRune("aeiouAEIOU", rune(noun[0])) {
		return "an"
	}
	return "a"
}

// resolveLatest replaces @latest in agent instance arguments and flags with
// the most recent instance of the selected agent. The agent comes from the
// command's --agent-id, or a picker in interactive mode.
func (a *App) resolveLatest(cmd *cobra.Command, args []string) ([]string, error) {
	var latest []binding
	for _, b := range bindings(cmd) {
		if value, given := b.value(args); given && value == latestInstance && b.kind == agentInstanceKind {
			latest = append(latest, b)
		}
	}
	if len(latest) == 0 {
		return args, nil
	}

	r, err := a.newResolver(cmd, args)
	if err != nil {
		return nil, err
	}

	if _, ok := r.known[agentKind]; !ok && !a.canPick() {
		return nil, fmt.Errorf("%s needs an agent to choose from. Pass the agent instance ID instead; 'prefactor agent-instances list --agent-id <agent_id>' lists them.", latestInstance)
	}

	agentID, err := r.pick(agentKind)
	if err != nil {
		return nil, err
	}

	id, err := newestInstance(cmd.Context(), r.client, agentID)
	if err != nil {
		return nil, err
	}

	for _, b := range latest {
		if b.argIndex >= 0 {
			args[b.argIndex] = id
		} else if err := cmd.Flags().Set(b.flag.Name, id); err != nil {
			return nil, err
		}
	}
	return args, nil
}

func newestInstance(ctx context.Context, c *api.Client, agentID string) (string, error) {
	raw, err := client.ReadJSON(agentInstanceKind.list(ctx, c, agentID, pageRequest{offset: 0, size: 1}))
	if err != nil {
		return "", err
	}

	var page struct {
		Summaries []struct {
			ID string `json:"id"`
		} `json:"summaries"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return "", fmt.Errorf("Could not read the agent instance list: %w", err)
	}
	if len(page.Summaries) == 0 || page.Summaries[0].ID == "" {
		return "", fmt.Errorf("Agent %s has no instances, so %s has nothing to use.", agentID, latestInstance)
	}
	return page.Summaries[0].ID, nil
}

// canPick reports whether pickers may run: interactive mode without --yes,
// because a picker has no default answer.
func (a *App) canPick() bool {
	return a.interactive() && a.Picker != nil && (a.session == nil || !a.session.yes)
}
