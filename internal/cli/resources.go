package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/prefactordev/cli/internal/api"
	"github.com/prefactordev/cli/internal/client"
)

// resourceKind is a kind of resource a picker can list.
type resourceKind struct {
	// name is the singular name used in prompts and messages.
	name string
	// plural is used in "No ... found" messages.
	plural string
	// parent is the resource this one belongs to, picked first when the
	// command does not already have it.
	parent *resourceKind
	// list fetches one page, newest first where the API can sort.
	list func(ctx context.Context, c *api.Client, parentID string, page pageRequest) (*http.Response, error)
}

type pageRequest struct {
	offset int
	size   int
}

func (p pageRequest) params() (*int, *int) {
	return &p.offset, &p.size
}

// newestFirst sorts a list by id, newest first. It uses the
// sorting[field] and sorting[direction] form, because p2 rejects the
// documented "-id" string form.
func newestFirst() api.RequestEditorFn {
	return withQuery(url.Values{"sorting[field]": {"id"}, "sorting[direction]": {"desc"}})
}

const (
	pickerPageSize = 100
	pickerMaxItems = 1000
)

var (
	accountKind = &resourceKind{name: "account", plural: "accounts", list: func(ctx context.Context, c *api.Client, _ string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryAccountList(ctx, &api.QueryAccountListParams{PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	environmentKind = &resourceKind{name: "environment", plural: "environments", parent: accountKind, list: func(ctx context.Context, c *api.Client, accountID string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryEnvironmentList(ctx, &api.QueryEnvironmentListParams{AccountId: &accountID, PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	agentKind = &resourceKind{name: "agent", plural: "agents", list: func(ctx context.Context, c *api.Client, _ string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryAgentList(ctx, &api.QueryAgentListParams{PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	judgeKind = &resourceKind{name: "judge", plural: "judges", list: func(ctx context.Context, c *api.Client, _ string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		role := api.QueryAgentListParamsRoleJudge
		return c.QueryAgentList(ctx, &api.QueryAgentListParams{Role: &role, PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	agentInstanceKind = &resourceKind{name: "agent instance", plural: "agent instances", parent: agentKind, list: func(ctx context.Context, c *api.Client, agentID string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryAgentInstanceList(ctx, &api.QueryAgentInstanceListParams{AgentId: &agentID, PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	agentSpanKind = &resourceKind{name: "agent span", plural: "agent spans", parent: agentInstanceKind, list: func(ctx context.Context, c *api.Client, instanceID string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryAgentSpanList(ctx, &api.QueryAgentSpanListParams{AgentInstanceId: &instanceID, PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	alertKind = &resourceKind{name: "alert", plural: "alerts", list: func(ctx context.Context, c *api.Client, _ string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryAlertList(ctx, &api.QueryAlertListParams{PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	apiTokenKind = &resourceKind{name: "API token", plural: "API tokens", list: func(ctx context.Context, c *api.Client, _ string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryApiTokenList(ctx, &api.QueryApiTokenListParams{PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	adminUserKind = &resourceKind{name: "admin user", plural: "admin users", list: func(ctx context.Context, c *api.Client, _ string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryAdminUserList(ctx, &api.QueryAdminUserListParams{PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	adminUserInviteKind = &resourceKind{name: "admin user invite", plural: "admin user invites", list: func(ctx context.Context, c *api.Client, _ string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryAdminUserInviteList(ctx, &api.QueryAdminUserInviteListParams{PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	agentVersionKind = &resourceKind{name: "agent version", plural: "agent versions", parent: agentKind, list: func(ctx context.Context, c *api.Client, agentID string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryAgentVersionList(ctx, &api.QueryAgentVersionListParams{AgentId: agentID, PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	agentSchemaVersionKind = &resourceKind{name: "agent schema version", plural: "agent schema versions", parent: agentKind, list: func(ctx context.Context, c *api.Client, agentID string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryAgentSchemaVersionList(ctx, &api.QueryAgentSchemaVersionListParams{AgentId: &agentID, PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	agentDeploymentKind = &resourceKind{name: "agent deployment", plural: "agent deployments", parent: agentKind, list: func(ctx context.Context, c *api.Client, agentID string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryAgentDeploymentList(ctx, &api.QueryAgentDeploymentListParams{AgentId: &agentID, PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	agentAssociationKind = &resourceKind{name: "agent association", plural: "agent associations", parent: agentKind, list: func(ctx context.Context, c *api.Client, agentID string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryAgentAssociationListFrom(ctx, &api.QueryAgentAssociationListFromParams{AgentIds: []api.AgentId{agentID}, PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	agentInstanceAssociationKind = &resourceKind{name: "agent instance association", plural: "agent instance associations", parent: agentInstanceKind, list: func(ctx context.Context, c *api.Client, instanceID string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryAgentInstanceAssociationListFrom(ctx, &api.QueryAgentInstanceAssociationListFromParams{AgentInstanceIds: []api.AgentInstanceId{instanceID}, PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	teamKind = &resourceKind{name: "team", plural: "teams", list: func(ctx context.Context, c *api.Client, _ string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryTeamList(ctx, &api.QueryTeamListParams{PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	personKind = &resourceKind{name: "person", plural: "people", list: func(ctx context.Context, c *api.Client, _ string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryPersonList(ctx, &api.QueryPersonListParams{PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	riskProfileKind = &resourceKind{name: "risk profile", plural: "risk profiles", list: func(ctx context.Context, c *api.Client, _ string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryRiskProfileList(ctx, &api.QueryRiskProfileListParams{PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	llmProviderKind = &resourceKind{name: "LLM provider", plural: "LLM providers", list: func(ctx context.Context, c *api.Client, _ string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryLlmProviderList(ctx, &api.QueryLlmProviderListParams{PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	llmProviderModelKind = &resourceKind{name: "LLM provider model", plural: "LLM provider models", parent: llmProviderKind, list: func(ctx context.Context, c *api.Client, providerID string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryLlmProviderModelList(ctx, &api.QueryLlmProviderModelListParams{LlmProviderId: &providerID, PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	knowledgebaseKind = &resourceKind{name: "knowledgebase", plural: "knowledgebases", list: func(ctx context.Context, c *api.Client, _ string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryKnowledgebaseList(ctx, &api.QueryKnowledgebaseListParams{PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	knowledgebaseDocumentKind = &resourceKind{name: "knowledgebase document", plural: "knowledgebase documents", parent: knowledgebaseKind, list: func(ctx context.Context, c *api.Client, knowledgebaseID string, p pageRequest) (*http.Response, error) {
		o, n := p.params()
		return c.QueryKnowledgebaseDocumentList(ctx, &api.QueryKnowledgebaseDocumentListParams{KnowledgebaseId: knowledgebaseID, PaginationOffset: o, PaginationPageSize: n}, newestFirst())
	}}
	knowledgebaseDocumentRevisionKind = &resourceKind{name: "document revision", plural: "document revisions", parent: knowledgebaseDocumentKind, list: func(ctx context.Context, c *api.Client, documentID string, p pageRequest) (*http.Response, error) {
		return c.QueryKnowledgebaseDocumentRevisionList(ctx, &api.QueryKnowledgebaseDocumentRevisionListParams{KnowledgebaseDocumentId: documentID, PaginationOffset: &p.offset, PaginationPageSize: &p.size})
	}}
	knowledgebaseRevisionKind = &resourceKind{name: "knowledgebase revision", plural: "knowledgebase revisions", parent: knowledgebaseKind, list: func(ctx context.Context, c *api.Client, knowledgebaseID string, p pageRequest) (*http.Response, error) {
		return c.QueryKnowledgebaseRevisionList(ctx, &api.QueryKnowledgebaseRevisionListParams{KnowledgebaseId: knowledgebaseID, PaginationOffset: &p.offset, PaginationPageSize: &p.size})
	}}
)

// groupKinds maps a command group to the resource its <id> argument names.
var groupKinds = map[string]*resourceKind{
	"accounts":                         accountKind,
	"environments":                     environmentKind,
	"agents":                           agentKind,
	"judges":                           judgeKind,
	"agent-instances":                  agentInstanceKind,
	"agent-spans":                      agentSpanKind,
	"alerts":                           alertKind,
	"api-tokens":                       apiTokenKind,
	"admin-users":                      adminUserKind,
	"admin-user-invites":               adminUserInviteKind,
	"agent-versions":                   agentVersionKind,
	"agent-schema-versions":            agentSchemaVersionKind,
	"agent-deployments":                agentDeploymentKind,
	"agent-associations":               agentAssociationKind,
	"agent-instance-associations":      agentInstanceAssociationKind,
	"teams":                            teamKind,
	"people":                           personKind,
	"risk-profiles":                    riskProfileKind,
	"llm-providers":                    llmProviderKind,
	"llm-provider-models":              llmProviderModelKind,
	"knowledgebases":                   knowledgebaseKind,
	"knowledgebase-documents":          knowledgebaseDocumentKind,
	"knowledgebase-document-revisions": knowledgebaseDocumentRevisionKind,
	"knowledgebase-revisions":          knowledgebaseRevisionKind,
}

// flagKinds maps an ID flag to the resource it names.
var flagKinds = map[string]*resourceKind{
	"account-id":                   accountKind,
	"environment-id":               environmentKind,
	"agent-id":                     agentKind,
	"agent-ids":                    agentKind,
	"to-agent-id":                  agentKind,
	"from-agent-id":                judgeKind,
	"judge-agent-id":               judgeKind,
	"agent-instance-id":            agentInstanceKind,
	"agent-instance-ids":           agentInstanceKind,
	"knowledgebase-id":             knowledgebaseKind,
	"knowledgebase-document-id":    knowledgebaseDocumentKind,
	"base-revision-id":             knowledgebaseDocumentRevisionKind,
	"llm-provider-id":              llmProviderKind,
	"hosted-llm-provider-model-id": llmProviderModelKind,
}

// argumentKinds maps an argument name that is not "id" to its resource.
var argumentKinds = map[string]*resourceKind{
	"agent_instance_id": agentInstanceKind,
}

// flagKind returns the resource an ID flag names on a command. In the judges
// group, --agent-id names a judge.
func flagKind(cmd commandRef, flag string) *resourceKind {
	if flag == "agent-id" && cmd.group == "judges" {
		return judgeKind
	}
	return flagKinds[flag]
}

// commandRef is the part of a command the bindings depend on.
type commandRef struct {
	group string
}

// listChoices fetches the choices for a resource, newest first.
func listChoices(ctx context.Context, c *api.Client, kind *resourceKind, parentID string) ([]Choice, error) {
	var choices []Choice
	offset := 0
	for len(choices) < pickerMaxItems {
		raw, err := client.ReadJSON(kind.list(ctx, c, parentID, pageRequest{offset: offset, size: pickerPageSize}))
		if err != nil {
			return nil, err
		}

		var page struct {
			Summaries  []map[string]any `json:"summaries"`
			Pagination *struct {
				NextPageOffset *int `json:"next_page_offset"`
			} `json:"pagination"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("Could not read the %s list: %w", kind.plural, err)
		}

		for _, summary := range page.Summaries {
			id, _ := summary["id"].(string)
			if id == "" {
				continue
			}
			choices = append(choices, Choice{Label: choiceLabel(summary, id), Value: id})
		}

		if page.Pagination == nil || page.Pagination.NextPageOffset == nil || len(page.Summaries) == 0 {
			break
		}
		offset = *page.Pagination.NextPageOffset
	}
	return choices, nil
}

// choiceLabel describes a summary by its name (or path, email or external
// identifier), its status and its ID, so the list can be searched by any of
// them.
func choiceLabel(summary map[string]any, id string) string {
	var parts []string
	for _, key := range []string{"name", "path", "email", "external_identifier", "schema_name"} {
		if value, ok := summary[key].(string); ok && value != "" {
			parts = append(parts, value)
			break
		}
	}
	for _, key := range []string{"status", "started_at", "inserted_at"} {
		if value, ok := summary[key].(string); ok && value != "" {
			parts = append(parts, value)
			break
		}
	}
	parts = append(parts, id)
	return strings.Join(parts, "  ")
}
