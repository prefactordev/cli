package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
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

// missingPickableInputs lists the required resource IDs the command lacks
// that a picker can supply.
func missingPickableInputs(cmd *cobra.Command, args []string) *pickableInputs {
	inputs := &pickableInputs{args: map[int]bool{}, flags: map[string]bool{}}
	for _, b := range bindings(cmd) {
		if _, given := b.value(args); !b.required || given {
			continue
		}
		if b.argIndex >= 0 {
			inputs.args[b.argIndex] = true
		} else {
			inputs.flags[b.flag.Name] = true
		}
	}
	return inputs
}

// destructiveCommands never act on a target chosen without the person
// choosing it: their picker shows even when there is one choice.
var destructiveCommands = map[string]bool{
	"delete": true, "revoke": true, "terminate": true, "archive": true,
	"discard-sensitive": true, "retire": true, "suspend": true,
}

func (a *App) newResolver(cmd *cobra.Command, args []string) (*resolver, error) {
	c, err := a.authedClient(cmd)
	if err != nil {
		return nil, err
	}

	r := &resolver{app: a, cmd: cmd, client: c, known: map[*resourceKind]string{}}
	for _, b := range bindings(cmd) {
		if value, given := b.value(args); given && value != "" && !strings.Contains(strings.ToLower(value), latestInstance) {
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

		id, err := r.pick(b.kind, b.argIndex >= 0 && destructiveCommands[cmd.Name()])
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
// does not already have it. A single choice is used without asking, unless
// explicit is set because the command destroys what is picked.
func (r *resolver) pick(kind *resourceKind, explicit bool) (string, error) {
	if id, ok := r.known[kind]; ok {
		return id, nil
	}

	parentID := ""
	if kind.parent != nil {
		var err error
		if parentID, err = r.pick(kind.parent, false); err != nil {
			return "", err
		}
	}

	choices, more, err := listChoices(r.cmd.Context(), r.client, kind, parentID)
	if err != nil {
		return "", err
	}
	title := "Choose " + article(kind.name) + " " + kind.name
	note := ""
	if more {
		which := "newest"
		if kind.unsorted {
			which = "first"
		}
		note = fmt.Sprintf("Showing the %s %d. To use another, pass its ID.", which, pickerMaxItems)
	}

	var id string
	switch len(choices) {
	case 0:
		return "", fmt.Errorf("No %s found to pick from.", kind.plural)
	case 1:
		if explicit {
			if id, err = r.app.Picker.Pick(title, note, choices); err != nil {
				return "", err
			}
			break
		}
		id = choices[0].Value
		fmt.Fprintf(r.cmd.ErrOrStderr(), "Using %s %s\n", kind.name, choices[0].Label)
	default:
		if id, err = r.app.Picker.Pick(title, note, choices); err != nil {
			return "", err
		}
	}

	r.known[kind] = id
	return id, nil
}

// rejectsAgentRef reports whether p2 refused a list because its agent_id is
// not an agent, which it answers with a 400 naming agent_ref.
func rejectsAgentRef(err error) bool {
	var httpErr *client.HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusBadRequest {
		return false
	}
	var body struct {
		Errors map[string]json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(httpErr.Body, &body) != nil {
		return false
	}
	_, ok := body.Errors["agent_ref"]
	return ok
}

// ownedPlural names the instances @latest picks from, by owner.
var ownedPlural = map[*resourceKind]string{agentKind: "instances", judgeKind: "runs"}

func capitalize(s string) string {
	return strings.ToUpper(s[:1]) + s[1:]
}

func article(noun string) string {
	if strings.ContainsRune("aeiouAEIOU", rune(noun[0])) {
		return "an"
	}
	return "a"
}

// latestSelector matches @latest, optionally scoped to an agent as
// <agent_id>/@latest, ignoring case, as Sentry's CLI does for its selectors.
var latestSelector = regexp.MustCompile(`(?i)^(?:([^/\s]+)/)?@latest$`)

// resolveLatest replaces @latest in agent instance and judge run arguments
// and flags, including inside comma-separated ID lists, with the most recent
// instance of an agent or judge. The agent or judge comes from
// <id>/@latest, the command's own flag for it, or a picker in interactive
// mode.
func (a *App) resolveLatest(cmd *cobra.Command, args []string) ([]string, error) {
	type use struct {
		b      binding
		tokens []string
	}
	var uses []use
	for _, b := range bindings(cmd) {
		value, given := b.value(args)
		if !given || (b.kind != agentInstanceKind && b.kind != judgeRunKind) {
			continue
		}
		tokens := []string{value}
		if b.argIndex < 0 && strings.HasSuffix(b.flag.Name, "-ids") {
			tokens = strings.Split(value, ",")
		}
		for _, token := range tokens {
			if latestSelector.MatchString(strings.TrimSpace(token)) {
				uses = append(uses, use{b: b, tokens: tokens})
				break
			}
		}
	}
	if len(uses) == 0 {
		return args, nil
	}

	r, err := a.newResolver(cmd, args)
	if err != nil {
		return nil, err
	}

	newest := map[string]string{}
	resolve := func(owner *resourceKind, scope string) (string, error) {
		ownerID := scope
		if ownerID == "" {
			if known, ok := r.known[owner]; ok {
				ownerID = known
			} else if a.canPick() {
				if ownerID, err = r.pick(owner, false); err != nil {
					return "", err
				}
			} else {
				return "", latestNeedsOwner(cmd, owner, a.onlyTerminalsStopPickers())
			}
		}
		if id, ok := newest[ownerID]; ok {
			return id, nil
		}
		id, err := newestInstance(cmd.Context(), r.client, owner, ownerID)
		if err != nil {
			return "", err
		}
		newest[ownerID] = id
		return id, nil
	}

	for _, u := range uses {
		resolved := make([]string, len(u.tokens))
		for i, token := range u.tokens {
			m := latestSelector.FindStringSubmatch(strings.TrimSpace(token))
			if m == nil {
				resolved[i] = token
				continue
			}
			id, err := resolve(u.b.kind.parent, m[1])
			if err != nil {
				return nil, err
			}
			resolved[i] = id
		}

		value := strings.Join(resolved, ",")
		if u.b.argIndex >= 0 {
			args[u.b.argIndex] = value
		} else if err := cmd.Flags().Set(u.b.flag.Name, value); err != nil {
			return nil, err
		}
	}
	return args, nil
}

// latestNeedsOwner explains the ways to give @latest the agent or judge it
// needs on this command. A terminal is offered only when it would bring up
// the picker.
func latestNeedsOwner(cmd *cobra.Command, owner *resourceKind, offerTerminal bool) error {
	ways := []string{fmt.Sprintf("use <%s_id>/%s", owner.name, latestInstance)}
	for _, b := range bindings(cmd) {
		if b.kind == owner && b.argIndex < 0 {
			ways = append(ways, "pass --"+b.flag.Name)
		}
	}
	if offerTerminal {
		ways = append(ways, "run it in a terminal to choose the "+owner.name)
	}
	return fmt.Errorf("%s needs %s %s: %s.", latestInstance, article(owner.name), owner.name, joinOr(ways))
}

func joinOr(items []string) string {
	if len(items) == 1 {
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// newestInstance returns the most recent instance of an agent or judge.
func newestInstance(ctx context.Context, c *api.Client, owner *resourceKind, ownerID string) (string, error) {
	raw, err := client.ReadJSON(agentInstanceKind.list(ctx, c, ownerID, pageRequest{offset: 0, size: 1}))
	if rejectsAgentRef(err) {
		return "", fmt.Errorf("%s %s was not found, so %s has nothing to use.", capitalize(owner.name), ownerID, latestInstance)
	}
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
		return "", fmt.Errorf("%s %s has no %s, so %s has nothing to use.", capitalize(owner.name), ownerID, ownedPlural[owner], latestInstance)
	}
	return page.Summaries[0].ID, nil
}

// canPick reports whether pickers may run: interactive mode, stderr a
// terminal (the picker draws there), and neither JSON output, which must
// never wait on a prompt, nor --yes, because a picker has no default answer.
// onlyTerminalsStopPickers reports whether pickers are off only because a
// stream is not a terminal, rather than by --json, --yes, --no-input or a
// detected coding agent.
func (a *App) onlyTerminalsStopPickers() bool {
	return a.session != nil && !a.session.noInput && !a.session.yes && !a.jsonOutput() && a.detectAgent() == ""
}

func (a *App) canPick() bool {
	return a.interactive() &&
		a.Picker != nil &&
		a.StderrIsTerminal != nil && a.StderrIsTerminal() &&
		!a.jsonOutput() &&
		(a.session == nil || !a.session.yes)
}
