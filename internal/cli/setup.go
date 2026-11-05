package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/api"
	"github.com/prefactordev/cli/internal/client"
	"github.com/prefactordev/cli/internal/js"
	"github.com/prefactordev/cli/internal/jsjson"
)

const (
	defaultAgentIdentifier = "1.0.0"
	setupTokenType         = "api_session_agent_deployment_scope"
	// createAgentChoice is the agent picker's choice to create an agent.
	createAgentChoice = "\x00create"
)

// setupValues are what setup prints, in the TypeScript CLI's key order.
type setupValues struct {
	APIURL          string `json:"api_url"`
	APIToken        string `json:"api_token"`
	AgentID         string `json:"agent_id"`
	AgentIdentifier string `json:"agent_identifier"`
}

func newSetupCommand(app *App) *cobra.Command {
	setup := &cobra.Command{
		Use:   "setup [agent_id]",
		Short: "Create setup values for an agent (optionally create the agent first)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			baseURL, token, err := app.credentials(cmd)
			if err != nil {
				return err
			}
			c, err := client.New(baseURL, token, app.Version)
			if err != nil {
				return err
			}
			agentArg := ""
			if len(args) > 0 {
				agentArg = args[0]
			}

			s := &setupRun{app: app, cmd: cmd, client: c}
			agentID, err := s.resolveAgent(agentArg)
			if err != nil {
				return err
			}
			if _, err := s.call(func(ctx context.Context) (*http.Response, error) {
				return c.QueryAgentGetDetailsById(ctx, agentID, &api.QueryAgentGetDetailsByIdParams{})
			}); err != nil {
				return err
			}

			environmentID, err := s.resolveEnvironment(agentID)
			if err != nil {
				return err
			}
			apiToken, err := s.createToken(agentID, environmentID)
			if err != nil {
				return err
			}
			if err := s.checkToken(baseURL, apiToken, agentID); err != nil {
				return err
			}

			values := setupValues{APIURL: baseURL, APIToken: apiToken, AgentID: agentID, AgentIdentifier: defaultAgentIdentifier}
			if app.jsonOutput() {
				return app.printJSONValue(cmd, values)
			}
			// The deployment token is printed on purpose, for the caller
			// to capture.
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "PREFACTOR_API_URL=%s\nPREFACTOR_API_TOKEN=%s\nPREFACTOR_AGENT_ID=%s\nPREFACTOR_AGENT_IDENTIFIER=%s\n",
				values.APIURL, values.APIToken, values.AgentID, values.AgentIdentifier)
			return err
		},
	}
	setup.Flags().Bool("create", false, "Create a new agent before printing setup values")
	setup.Flags().String("name", "", "Agent name (required with --create)")
	setup.Flags().String("description", "", "Agent description (optional with --create)")
	// The TypeScript CLI's own --json; it does what the global flag does.
	setup.Flags().Bool("json", false, "Print setup values as JSON")
	return setup
}

// setupRun is one run of setup.
type setupRun struct {
	app    *App
	cmd    *cobra.Command
	client *api.Client
}

func (s *setupRun) call(send func(ctx context.Context) (*http.Response, error)) ([]byte, error) {
	return client.ReadJSON(send(s.cmd.Context()))
}

// resolveAgent returns the agent to set up: the argument, a new agent with
// --create, or, in interactive mode, one picked or created on the spot.
func (s *setupRun) resolveAgent(agentArg string) (string, error) {
	cmd := s.cmd
	if boolFlag(cmd, "create") {
		name := js.Trim(stringFlag(cmd, "name"))
		if name == "" && s.app.canPick() {
			var err error
			if name, err = s.app.Picker.Input("Agent name", true); err != nil {
				return "", err
			}
		}
		if name == "" {
			return "", errors.New("prefactor setup --create requires --name '<agent-name>'.")
		}
		if agentArg != "" {
			return "", errors.New("prefactor setup --create does not accept an agent_id argument.")
		}
		return s.createAgent(name, js.Trim(stringFlag(cmd, "description")))
	}

	if agentArg == "" && s.app.canPick() && stringFlag(cmd, "name") == "" && stringFlag(cmd, "description") == "" {
		return s.pickAgent()
	}
	if agentArg == "" {
		return "", errors.New("prefactor setup requires <agent_id>, or --create --name '<agent-name>' to create one.")
	}
	if stringFlag(cmd, "name") != "" {
		return "", errors.New("--name is only valid with --create.")
	}
	if stringFlag(cmd, "description") != "" {
		return "", errors.New("--description is only valid with --create.")
	}
	return agentArg, nil
}

// pickAgent asks for an agent, offering to create one first.
func (s *setupRun) pickAgent() (string, error) {
	choices, more, err := listChoices(s.cmd.Context(), s.client, agentKind, "")
	if err != nil {
		return "", err
	}

	id := createAgentChoice
	if len(choices) > 0 {
		note := ""
		if more {
			note = fmt.Sprintf("Showing the newest %d. To use another, pass its ID.", pickerMaxItems)
		}
		choices = append([]Choice{{Label: "Create a new agent", Value: createAgentChoice}}, choices...)
		if id, err = s.app.Picker.Pick("Choose an agent to set up", note, choices); err != nil {
			return "", err
		}
	}
	if id != createAgentChoice {
		return id, nil
	}

	name, err := s.app.Picker.Input("Agent name", true)
	if err != nil {
		return "", err
	}
	description, err := s.app.Picker.Input("Agent description (optional)", false)
	if err != nil {
		return "", err
	}
	return s.createAgent(name, description)
}

func (s *setupRun) createAgent(name, description string) (string, error) {
	details := map[string]any{"name": name}
	if description != "" {
		details["description"] = description
	}
	raw, err := s.call(func(ctx context.Context) (*http.Response, error) {
		body, err := jsonBody(map[string]any{"details": details})
		if err != nil {
			return nil, err
		}
		return s.client.ActionAgentCreateWithBody(ctx, "application/json", body)
	})
	if err != nil {
		return "", err
	}

	id, ok := stringProperty(property(parseJSON(raw), "details"), "id")
	if !ok {
		return "", errors.New("Agent create response is missing details.id.")
	}
	return id, nil
}

// deployment is the part of an agent deployment summary setup reads.
type deployment struct {
	environmentID     string
	hasCurrentVersion bool
}

// resolveEnvironment chooses the environment for the token as the
// TypeScript CLI does: the agent's only deployment, or the only one with a
// current version, or the first environment of the first account when the
// agent has no deployment. In interactive mode it asks instead of choosing
// between several deployments, accounts or environments.
func (s *setupRun) resolveEnvironment(agentID string) (string, error) {
	raw, err := s.call(func(ctx context.Context) (*http.Response, error) {
		return s.client.QueryAgentDeploymentList(ctx, &api.QueryAgentDeploymentListParams{AgentId: &agentID})
	})
	if err != nil {
		return "", err
	}
	var deployments []deployment
	for _, item := range listItems(raw) {
		environmentID, _ := stringProperty(item, "environment_id")
		current := property(item, "current_version_id")
		_, missing := current.(undefined)
		deployments = append(deployments, deployment{environmentID: environmentID, hasCurrentVersion: !missing && current != nil})
	}

	switch {
	case len(deployments) == 0 && s.app.canPick():
		r := &resolver{app: s.app, cmd: s.cmd, client: s.client, known: map[*resourceKind]string{}}
		return r.pick(environmentKind, false)
	case len(deployments) == 0:
		return s.firstEnvironment()
	case len(deployments) == 1:
		return requireEnvironmentID(deployments[0].environmentID, agentID)
	case s.app.canPick():
		return s.pickDeployment(deployments, agentID)
	}

	var current []deployment
	for _, d := range deployments {
		if d.hasCurrentVersion {
			current = append(current, d)
		}
	}
	if len(current) == 1 {
		return requireEnvironmentID(current[0].environmentID, agentID)
	}
	return "", fmt.Errorf("Unable to choose an agent deployment for '%s'. Expected one deployment, or one deployment with current_version_id.", agentID)
}

// pickDeployment asks which of the agent's deployments to use, by
// environment name.
func (s *setupRun) pickDeployment(deployments []deployment, agentID string) (string, error) {
	var choices []Choice
	for _, d := range deployments {
		if _, err := requireEnvironmentID(d.environmentID, agentID); err != nil {
			return "", err
		}
		raw, err := s.call(func(ctx context.Context) (*http.Response, error) {
			return s.client.QueryEnvironmentGetDetailsById(ctx, d.environmentID, &api.QueryEnvironmentGetDetailsByIdParams{})
		})
		if err != nil {
			return "", err
		}
		var environment struct {
			Details map[string]any `json:"details"`
		}
		_ = json.Unmarshal(raw, &environment)
		label := choiceLabel(environment.Details, d.environmentID)
		if d.hasCurrentVersion {
			label += "  (has a current version)"
		}
		choices = append(choices, Choice{Label: label, Value: d.environmentID})
	}
	return s.app.Picker.Pick("Choose the environment to set up", "", choices)
}

// firstEnvironment is the first environment of the first account.
func (s *setupRun) firstEnvironment() (string, error) {
	raw, err := s.call(func(ctx context.Context) (*http.Response, error) {
		return s.client.QueryAccountList(ctx, &api.QueryAccountListParams{})
	})
	if err != nil {
		return "", err
	}
	accounts := listItems(raw)
	if len(accounts) == 0 {
		return "", errors.New("No accounts accessible to this profile; cannot create a deployment token.")
	}
	accountID, ok := stringProperty(accounts[0], "id")
	if !ok {
		return "", errors.New("Account list response is missing id.")
	}

	raw, err = s.call(func(ctx context.Context) (*http.Response, error) {
		return s.client.QueryEnvironmentList(ctx, &api.QueryEnvironmentListParams{AccountId: &accountID})
	})
	if err != nil {
		return "", err
	}
	environments := listItems(raw)
	if len(environments) == 0 {
		return "", fmt.Errorf("No environments found for account %s; create one first.", accountID)
	}
	environmentID, ok := stringProperty(environments[0], "id")
	if !ok {
		return "", fmt.Errorf("Environment list response is missing id for account %s.", accountID)
	}
	return environmentID, nil
}

// listItems is a list response's summaries, or its details when there are
// no summaries (summaries ?? details ?? []).
func listItems(raw []byte) []any {
	response := parseJSON(raw)
	for _, key := range []string{"summaries", "details"} {
		switch value := property(response, key).(type) {
		case undefined, nil:
			continue
		case []any:
			return value
		default:
			return nil
		}
	}
	return nil
}

func requireEnvironmentID(environmentID, agentID string) (string, error) {
	if environmentID == "" {
		return "", fmt.Errorf("Agent deployment for '%s' is missing environment_id.", agentID)
	}
	return environmentID, nil
}

// createToken mints a deployment token for the agent in the environment.
func (s *setupRun) createToken(agentID, environmentID string) (string, error) {
	raw, err := s.call(func(ctx context.Context) (*http.Response, error) {
		body, err := jsonBody(map[string]any{"details": map[string]any{
			"token_scope":    "agent_deployment",
			"agent_id":       agentID,
			"environment_id": environmentID,
		}})
		if err != nil {
			return nil, err
		}
		return s.client.ActionApiTokenCreateWithBody(ctx, "application/json", body)
	})
	if err != nil {
		return "", err
	}

	token, ok := property(parseJSON(raw), "token").(string)
	if !ok {
		return "", errors.New("API token create response is missing token.")
	}
	return token, nil
}

// checkToken pings with the new token and checks it is a deployment token
// for the agent.
func (s *setupRun) checkToken(baseURL, apiToken, agentID string) error {
	pingClient, err := client.New(baseURL, apiToken, s.app.Version)
	if err != nil {
		return err
	}
	raw, err := s.call(func(ctx context.Context) (*http.Response, error) {
		return pingClient.QueryPingGet(ctx, &api.QueryPingGetParams{})
	})
	if err != nil {
		return err
	}

	ping := parseJSON(raw)
	status := property(ping, "status")
	details := property(ping, "details")
	if value, ok := status.(string); !ok || value != "success" {
		return fmt.Errorf("Setup token ping failed: expected status \"success\", got %s.", jsValue(status))
	}
	if value, ok := property(details, "token_type").(string); !ok || value != setupTokenType {
		return fmt.Errorf("Setup token ping failed: expected token_type \"%s\", got %s.", setupTokenType, jsValue(property(details, "token_type")))
	}
	if value, ok := property(details, "agent_id").(string); !ok || value != agentID {
		return fmt.Errorf("Setup token ping failed: expected agent_id \"%s\", got %s.", agentID, jsValue(property(details, "agent_id")))
	}
	return nil
}

// undefined stands for a missing value, as JavaScript's undefined.
type undefined struct{}

// property returns value[key] as JavaScript's optional chaining does.
func property(value any, key string) any {
	if object, ok := value.(*jsjson.Object); ok {
		if v, ok := object.Get(key); ok {
			return v
		}
	}
	return undefined{}
}

// parseJSON parses a response, or returns undefined when it is not JSON.
func parseJSON(raw []byte) any {
	value, err := jsjson.Parse(raw)
	if err != nil {
		return undefined{}
	}
	return value
}

// stringProperty returns value[key] when it is a non-empty string.
func stringProperty(value any, key string) (string, bool) {
	s, ok := property(value, key).(string)
	return s, ok && s != ""
}

// templateString is value in a JavaScript template string, as `${value}`.
func templateString(value any) string {
	switch v := value.(type) {
	case undefined:
		return "undefined"
	case nil:
		return "null"
	case string:
		return v
	case bool:
		return fmt.Sprint(v)
	case jsjson.Number:
		return jsjson.FormatNumber(float64(v))
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			if item != nil {
				parts[i] = templateString(item)
			}
		}
		return strings.Join(parts, ",")
	default:
		return "[object Object]"
	}
}

// jsValue is JSON.stringify(value) in a template string.
func jsValue(value any) string {
	if _, ok := value.(undefined); ok {
		return "undefined"
	}
	return jsjson.Stringify(value, "")
}
