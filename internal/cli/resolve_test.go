package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/prefactordev/cli/internal/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// fakePicker records each prompt and answers with the choice whose label
// contains the next scripted text.
type fakePicker struct {
	answers []string
	prompts []string
	notes   []string
	offered [][]Choice
}

func (p *fakePicker) Pick(title, note string, choices []Choice) (string, error) {
	p.prompts = append(p.prompts, title)
	p.notes = append(p.notes, note)
	p.offered = append(p.offered, choices)
	if len(p.answers) == 0 {
		return "", errPickCancelled
	}
	want := p.answers[0]
	p.answers = p.answers[1:]
	for _, c := range choices {
		if strings.Contains(c.Label, want) {
			return c.Value, nil
		}
	}
	return "", errPickCancelled
}

// listServer answers list requests by path and records every request.
type listServer struct {
	*httptest.Server
	mu       sync.Mutex
	lists    map[string]string
	requests []string
}

func newListServer(t *testing.T, lists map[string]string) *listServer {
	t.Helper()
	ls := &listServer{lists: lists}
	ls.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ls.mu.Lock()
		ls.requests = append(ls.requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		ls.mu.Unlock()

		key := r.URL.Path
		for _, param := range []string{"agent_id", "agent_instance_id", "knowledgebase_document_id", "knowledgebase_id"} {
			if v := r.URL.Query().Get(param); v != "" {
				key += "?" + param + "=" + v
				break
			}
		}
		body, ok := ls.lists[key]
		if !ok {
			body = `{"status":"success","details":{"id":"done"}}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(ls.Close)
	return ls
}

func (ls *listServer) last() string {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return ls.requests[len(ls.requests)-1]
}

func summaries(items ...string) string {
	return `{"status":"success","summaries":[` + strings.Join(items, ",") + `],"pagination":{"next_page_offset":null}}`
}

const (
	agentA = "01m6aaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	agentB = "01m6bbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	inst1  = "01m6iiiiiiiiiiiiiiiiiiiiiiiiiii1"
	inst2  = "01m6iiiiiiiiiiiiiiiiiiiiiiiiiii2"
)

func pickerTestApp(t *testing.T, lists map[string]string) (*testApp, *listServer, *fakePicker) {
	t.Helper()
	ta := newTestApp(t)
	ta.terminal = true
	server := newListServer(t, lists)
	ta.addProfile(t, "default", "tok", server.URL)
	picker := &fakePicker{}
	ta.Picker = picker
	return ta, server, picker
}

var twoAgents = map[string]string{
	"/api/v1/agent": summaries(`{"id":"`+agentA+`","name":"Support bot","status":"active"}`, `{"id":"`+agentB+`","name":"Billing bot","status":"active"}`),
	"/api/v1/agent_instance?agent_id=" + agentB: summaries(`{"id":"`+inst2+`","status":"complete","started_at":"2026-11-02T00:00:00Z"}`, `{"id":"`+inst1+`","status":"failed","started_at":"2026-11-01T00:00:00Z"}`),
	"/api/v1/agent_instance?agent_id=" + agentA: summaries(),
}

func TestPickerFillsAMissingRequiredFlag(t *testing.T) {
	ta, server, picker := pickerTestApp(t, twoAgents)
	picker.answers = []string{"Billing"}

	if _, err := ta.run(t, "agent-instances", "list"); err != nil {
		t.Fatal(err)
	}

	if len(picker.prompts) != 1 || picker.prompts[0] != "Choose an agent" {
		t.Fatalf("prompts = %v", picker.prompts)
	}
	if got := picker.offered[0][0].Label; got != "Support bot  active  "+agentA {
		t.Fatalf("label = %q", got)
	}
	if last := server.last(); !strings.Contains(last, "GET /api/v1/agent_instance?") || !strings.Contains(last, "agent_id="+agentB) {
		t.Fatalf("request = %s", last)
	}
}

func TestPickerPicksTheParentFirstForAMissingArgument(t *testing.T) {
	ta, server, picker := pickerTestApp(t, twoAgents)
	picker.answers = []string{"Billing", "failed"}

	if _, err := ta.run(t, "agent-instances", "retrieve"); err != nil {
		t.Fatal(err)
	}

	if strings.Join(picker.prompts, " | ") != "Choose an agent | Choose an agent instance" {
		t.Fatalf("prompts = %v", picker.prompts)
	}
	if last := server.last(); !strings.HasPrefix(last, "GET /api/v1/agent_instance/"+inst1+"?") {
		t.Fatalf("request = %s", last)
	}
}

func TestPickerUsesTheParentTheCommandAlreadyHas(t *testing.T) {
	doc := "01m6dddddddddddddddddddddddddddd"
	ta, server, picker := pickerTestApp(t, map[string]string{
		"/api/v1/knowledgebase_document_revision?knowledgebase_document_id=" + doc: summaries(`{"id":"rev2","inserted_at":"2026-11-02T00:00:00Z"}`, `{"id":"rev1","inserted_at":"2026-11-01T00:00:00Z"}`),
	})
	picker.answers = []string{"rev2"}

	if _, err := ta.run(t, "knowledgebase-documents", "rename", doc, "--path", "b.md"); err != nil {
		t.Fatal(err)
	}

	if len(picker.prompts) != 1 || picker.prompts[0] != "Choose a document revision" {
		t.Fatalf("prompts = %v (the document came from the argument, so no knowledgebase picker)", picker.prompts)
	}
	if last := server.last(); !strings.HasPrefix(last, "POST /api/v1/knowledgebase_document/"+doc+"/rename") {
		t.Fatalf("request = %s", last)
	}
}

func TestPickerUsesASingleChoiceWithoutAsking(t *testing.T) {
	ta, server, picker := pickerTestApp(t, map[string]string{
		"/api/v1/agent": summaries(`{"id":"` + agentA + `","name":"Only bot","status":"active"}`),
	})

	_, stderr, err := ta.runWithStderr(t, "agent-versions", "list")
	if err != nil {
		t.Fatal(err)
	}
	if len(picker.prompts) != 0 {
		t.Fatalf("asked %v", picker.prompts)
	}
	if stderr != "Using agent Only bot  active  "+agentA+"\n" {
		t.Fatalf("stderr = %q", stderr)
	}
	if last := server.last(); !strings.Contains(last, "agent_id="+agentA) {
		t.Fatalf("request = %s", last)
	}
}

func TestPickerFailsWhenThereIsNothingToPick(t *testing.T) {
	ta, _, _ := pickerTestApp(t, map[string]string{"/api/v1/team": summaries()})

	if _, err := ta.run(t, "teams", "retrieve"); err == nil || err.Error() != "No teams found to pick from." {
		t.Fatalf("err = %v", err)
	}
}

func TestPickerCancelStopsTheCommand(t *testing.T) {
	ta, server, picker := pickerTestApp(t, twoAgents)
	picker.answers = nil

	if _, err := ta.run(t, "agents", "retrieve"); err == nil || err.Error() != "Cancelled." {
		t.Fatalf("err = %v", err)
	}
	if last := server.last(); !strings.HasPrefix(last, "GET /api/v1/agent?") {
		t.Fatalf("request after cancel = %s", last)
	}
}

func TestNoPickerOutsideInteractiveModeOrWithYes(t *testing.T) {
	for name, setup := range map[string]func(ta *testApp) []string{
		"not a terminal": func(ta *testApp) []string { ta.terminal = false; return nil },
		"agent detected": func(ta *testApp) []string { ta.env["AI_AGENT"] = "claude-code"; return nil },
		"--no-input":     func(ta *testApp) []string { return []string{"--no-input"} },
		"--yes":          func(ta *testApp) []string { return []string{"--yes"} },
	} {
		t.Run(name, func(t *testing.T) {
			ta, _, picker := pickerTestApp(t, twoAgents)
			extra := setup(ta)

			_, err := ta.run(t, append([]string{"agent-instances", "list"}, extra...)...)
			if err == nil || err.Error() != "error: required option '--agent-id <agent-id>' not specified" {
				t.Fatalf("err = %v", err)
			}
			_, err = ta.run(t, append([]string{"agent-instances", "retrieve"}, extra...)...)
			if err == nil || err.Error() != "error: missing required argument 'id'" {
				t.Fatalf("err = %v", err)
			}
			if len(picker.prompts) != 0 {
				t.Fatalf("asked %v", picker.prompts)
			}
		})
	}
}

func TestLatestUsesTheNewestInstanceOfThePickedAgent(t *testing.T) {
	ta, server, picker := pickerTestApp(t, twoAgents)
	picker.answers = []string{"Billing"}

	if _, err := ta.run(t, "agent-instances", "retrieve", "@latest"); err != nil {
		t.Fatal(err)
	}
	if last := server.last(); !strings.HasPrefix(last, "GET /api/v1/agent_instance/"+inst2+"?") {
		t.Fatalf("request = %s", last)
	}

	found := false
	for _, r := range server.requests {
		if strings.Contains(r, "agent_instance?") && strings.Contains(r, "sorting%5Bdirection%5D=desc") && strings.Contains(r, "sorting%5Bfield%5D=id") && strings.Contains(r, "pagination%5Bpage_size%5D=1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no newest-first lookup in %v", server.requests)
	}
}

func TestLatestUsesTheAgentFlagWithoutAsking(t *testing.T) {
	ta, server, picker := pickerTestApp(t, twoAgents)
	ta.terminal = false

	if _, err := ta.run(t, "alerts", "list", "--agent-id", agentB, "--agent-instance-id", "@latest"); err != nil {
		t.Fatal(err)
	}
	if len(picker.prompts) != 0 {
		t.Fatalf("asked %v", picker.prompts)
	}
	if last := server.last(); !strings.Contains(last, "agent_instance_id="+inst2) {
		t.Fatalf("request = %s", last)
	}
}

func TestLatestErrors(t *testing.T) {
	ta, _, _ := pickerTestApp(t, twoAgents)
	ta.terminal = false

	_, err := ta.run(t, "agent-instances", "retrieve", "@latest")
	want := "@latest needs an agent: use <agent_id>/@latest or run it in a terminal to choose the agent."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v", err)
	}

	_, err = ta.run(t, "alerts", "list", "--agent-instance-id", "@latest")
	want = "@latest needs an agent: use <agent_id>/@latest, pass --agent-id or run it in a terminal to choose the agent."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v", err)
	}

	_, err = ta.run(t, "agent-instance-associations", "list-from", "--agent-instance-ids", "@latest")
	want = "@latest needs a judge: use <judge_id>/@latest or run it in a terminal to choose the judge."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v", err)
	}

	_, err = ta.run(t, "alerts", "list", "--agent-id", agentA, "--agent-instance-id", "@latest")
	if err == nil || err.Error() != "Agent "+agentA+" has no instances, so @latest has nothing to use." {
		t.Fatalf("err = %v", err)
	}
}

func TestLatestErrorOffersATerminalOnlyWhenThatWouldPick(t *testing.T) {
	for name, setup := range map[string]func(ta *testApp) []string{
		"--json":         func(*testApp) []string { return []string{"--json"} },
		"--yes":          func(*testApp) []string { return []string{"--yes"} },
		"--no-input":     func(*testApp) []string { return []string{"--no-input"} },
		"agent detected": func(ta *testApp) []string { ta.env["CLAUDECODE"] = "1"; return nil },
	} {
		t.Run(name, func(t *testing.T) {
			ta, _, picker := pickerTestApp(t, twoAgents)
			_, err := ta.run(t, append([]string{"alerts", "list", "--agent-instance-id", "@latest"}, setup(ta)...)...)
			want := "@latest needs an agent: use <agent_id>/@latest or pass --agent-id."
			if err == nil || err.Error() != want {
				t.Fatalf("err = %v", err)
			}
			if len(picker.prompts) != 0 {
				t.Fatalf("asked %v", picker.prompts)
			}
		})
	}
}

func TestJSONOutputTurnsPickersOff(t *testing.T) {
	for name, setup := range map[string]func(ta *testApp) []string{
		"--json":                       func(*testApp) []string { return []string{"--json"} },
		"--fields":                     func(*testApp) []string { return []string{"--fields", "details.id"} },
		"PREFACTOR_OUTPUT_FORMAT=json": func(ta *testApp) []string { ta.env["PREFACTOR_OUTPUT_FORMAT"] = "json"; return nil },
	} {
		t.Run(name, func(t *testing.T) {
			ta, _, picker := pickerTestApp(t, twoAgents)
			_, err := ta.run(t, append([]string{"agents", "retrieve"}, setup(ta)...)...)
			if err == nil || err.Error() != "error: missing required argument 'id'" {
				t.Fatalf("err = %v", err)
			}
			if len(picker.prompts) != 0 {
				t.Fatalf("asked %v", picker.prompts)
			}
		})
	}
}

func TestNoPickerWhenStderrIsNotATerminal(t *testing.T) {
	ta, _, picker := pickerTestApp(t, twoAgents)
	ta.StderrIsTerminal = func() bool { return false }

	if _, err := ta.run(t, "agents", "retrieve"); err == nil || err.Error() != "error: missing required argument 'id'" {
		t.Fatalf("err = %v", err)
	}
	if len(picker.prompts) != 0 {
		t.Fatalf("asked %v", picker.prompts)
	}
}

// TestEveryOtherCheckRunsBeforeAnyPicker covers each kind of check: each
// case must fail with the error it gives when the missing ID is passed, and
// without asking anything or listing anything first.
func TestEveryOtherCheckRunsBeforeAnyPicker(t *testing.T) {
	cases := []struct {
		name    string
		args    []string // the command line, missing an ID a picker can supply
		idFlags []string // the flags that supply that ID
	}{
		{"required option", []string{"agent-schema-versions", "create"}, []string{"--agent-id", agentA}},
		{"unknown option", []string{"agents", "retrieve", "--bogus"}, []string{agentA}},
		{"choice", []string{"agent-spans", "create", "--status", "bogus", "--payload", "{}", "--schema-name", "s"}, []string{"--agent-instance-id", inst1}},
		{"argument parser", []string{"knowledgebase-documents", "list", "--pagination-offset", "x"}, []string{"--knowledgebase-id", "kb"}},
		{"JSON option", []string{"agent-spans", "create", "--status", "active", "--payload", "[1]", "--schema-name", "s"}, []string{"--agent-instance-id", inst1}},
		{"ID option", []string{"agent-spans", "create", "--status", "active", "--payload", "{}", "--schema-name", "s", "--parent-span-id", "bad"}, []string{"--agent-instance-id", inst1}},
		{"status list", []string{"agent-instances", "list", "--statuses", "bogus"}, []string{"--agent-id", agentA}},
		{"sorting", []string{"knowledgebase-documents", "list", "--sort-direction", "asc"}, []string{"--knowledgebase-id", "kb"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ta, server, picker := pickerTestApp(t, twoAgents)

			_, want := ta.run(t, append(slices.Clone(tc.args), tc.idFlags...)...)
			if want == nil {
				t.Fatal("the command succeeded with the ID passed")
			}
			server.requests = nil

			_, err := ta.run(t, tc.args...)
			if err == nil || err.Error() != want.Error() {
				t.Fatalf("err = %v, want %v", err, want)
			}
			if len(picker.prompts) != 0 || len(server.requests) != 0 {
				t.Fatalf("asked %v and sent %v before reporting the error", picker.prompts, server.requests)
			}
		})
	}
}

func TestDestructiveCommandsAskEvenForOneChoice(t *testing.T) {
	ta, server, picker := pickerTestApp(t, map[string]string{
		"/api/v1/agent": summaries(`{"id":"` + agentA + `","name":"Only bot","status":"active"}`),
	})
	picker.answers = []string{"Only bot"}

	if _, err := ta.run(t, "agents", "delete"); err != nil {
		t.Fatal(err)
	}
	if len(picker.prompts) != 1 || picker.prompts[0] != "Choose an agent" {
		t.Fatalf("prompts = %v; a delete must not pick its target by itself", picker.prompts)
	}
	if last := server.last(); !strings.HasPrefix(last, "DELETE /api/v1/agent/"+agentA) {
		t.Fatalf("request = %s", last)
	}

	// A non-destructive command uses the single choice without asking.
	picker.prompts = nil
	if _, err := ta.run(t, "agents", "retrieve"); err != nil {
		t.Fatal(err)
	}
	if len(picker.prompts) != 0 {
		t.Fatalf("asked %v", picker.prompts)
	}
}

// TestEveryRequiredIDHasAPicker fails when a required ID argument or flag
// has no resource to pick from, so new commands cannot skip pickers.
func TestEveryRequiredIDHasAPicker(t *testing.T) {
	root := newRootCommand(&App{Version: "test"})
	idName := func(name string) bool {
		return name == "id" || strings.HasSuffix(name, "_id") || strings.HasSuffix(name, "-id") || strings.HasSuffix(name, "-ids")
	}

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		bound := map[string]bool{}
		for _, b := range bindings(cmd) {
			if b.argIndex >= 0 {
				bound[declaredArguments(cmd)[b.argIndex].name] = true
			} else {
				bound["--"+b.flag.Name] = true
			}
		}
		for _, arg := range declaredArguments(cmd) {
			if arg.required && idName(arg.name) && !bound[arg.name] {
				t.Errorf("%s: required argument <%s> has no picker", cmd.CommandPath(), arg.name)
			}
		}
		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if _, required := f.Annotations[requiredAnnotation]; required && idName(f.Name) && !bound["--"+f.Name] {
				t.Errorf("%s: required --%s has no picker", cmd.CommandPath(), f.Name)
			}
		})
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

func TestInstanceChoicesShowStatusAndStartTime(t *testing.T) {
	ta, _, picker := pickerTestApp(t, twoAgents)
	picker.answers = []string{"Billing", "failed"}

	if _, err := ta.run(t, "agent-instances", "retrieve"); err != nil {
		t.Fatal(err)
	}
	if got := picker.offered[1][0].Label; got != "complete  2026-11-02 00:00 UTC  "+inst2 {
		t.Fatalf("label = %q", got)
	}
}

func TestPickerSaysWhenTheListIsCutOff(t *testing.T) {
	ta := newTestApp(t)
	ta.terminal = true
	picker := &fakePicker{answers: []string{"team-0005"}}
	ta.Picker = picker

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset := 0
		_, _ = fmt.Sscan(r.URL.Query().Get("pagination[offset]"), &offset)
		var items []string
		for i := offset; i < offset+100 && i < 1500; i++ {
			items = append(items, fmt.Sprintf(`{"id":"01m6tttttttttttttttttttttttt%04d","name":"team-%04d"}`, i, i))
		}
		next := "null"
		if offset+100 < 1500 {
			next = fmt.Sprint(offset + 100)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"success","summaries":[`+strings.Join(items, ",")+`],"pagination":{"next_page_offset":`+next+`}}`)
	}))
	defer server.Close()
	ta.addProfile(t, "default", "tok", server.URL)

	if _, err := ta.run(t, "teams", "retrieve"); err != nil {
		t.Fatal(err)
	}
	if len(picker.offered[0]) != pickerMaxItems {
		t.Fatalf("offered %d choices, want %d", len(picker.offered[0]), pickerMaxItems)
	}
	if picker.notes[0] != "Showing the newest 1000. To use another, pass its ID." {
		t.Fatalf("note = %q", picker.notes[0])
	}

	// Revisions cannot be sorted, so their note does not claim the newest.
	picker.answers = []string{"team-0005", "team-0005"}
	if _, err := ta.run(t, "knowledgebase-revisions", "retrieve"); err != nil {
		t.Fatal(err)
	}
	if got := picker.notes[len(picker.notes)-1]; got != "Showing the first 1000. To use another, pass its ID." {
		t.Fatalf("revision note = %q", got)
	}
}

func TestAssociationLabelsNameWhatIsScored(t *testing.T) {
	cases := map[string]string{
		`{"id":"as1","to_agent_id":"ag9","trigger":"on_completion","inserted_at":"2026-10-22T08:11:02Z"}`: "scores ag9  on_completion  2026-10-22 08:11 UTC  as1",
		`{"id":"ia1","to_agent_instance_id":"in9","inserted_at":"2026-10-23T04:17:26Z"}`:                  "scores in9  2026-10-23 04:17 UTC  ia1",
	}
	for raw, want := range cases {
		var summary map[string]any
		if err := json.Unmarshal([]byte(raw), &summary); err != nil {
			t.Fatal(err)
		}
		if got := choiceLabel(summary, summary["id"].(string)); got != want {
			t.Errorf("label = %q, want %q", got, want)
		}
	}
}

func TestScopedLatestWorksWithoutATerminal(t *testing.T) {
	ta, server, picker := pickerTestApp(t, twoAgents)
	ta.terminal = false

	if _, err := ta.run(t, "agent-instances", "retrieve", agentB+"/@LATEST"); err != nil {
		t.Fatal(err)
	}
	if last := server.last(); !strings.HasPrefix(last, "GET /api/v1/agent_instance/"+inst2+"?") {
		t.Fatalf("request = %s", last)
	}
	if len(picker.prompts) != 0 {
		t.Fatalf("asked %v", picker.prompts)
	}
}

func TestLatestInsideAnIDList(t *testing.T) {
	ta, server, _ := pickerTestApp(t, twoAgents)
	ta.terminal = false

	if _, err := ta.run(t, "agent-instance-associations", "list-from", "--agent-instance-ids", inst1+","+agentB+"/@latest"); err != nil {
		t.Fatal(err)
	}
	last := server.last()
	if !strings.Contains(last, "agent_instance_ids%5B%5D="+inst1) || !strings.Contains(last, "agent_instance_ids%5B%5D="+inst2) {
		t.Fatalf("request = %s", last)
	}
}

// TestPickersListTheRightResource pins each resource kind to the list
// request it sends and the parent it needs, so a binding cannot quietly
// list the wrong kind of resource (as associations once listed all agents
// instead of judges).
func TestPickersListTheRightResource(t *testing.T) {
	cases := []struct {
		kind   *resourceKind
		parent *resourceKind
		want   string // method, path and the query keys that identify the list
	}{
		{accountKind, nil, "GET /api/v1/account"},
		{environmentKind, accountKind, "GET /api/v1/environment account_id=P"},
		{agentKind, nil, "GET /api/v1/agent"},
		{judgeKind, nil, "GET /api/v1/agent role=judge"},
		{agentInstanceKind, agentKind, "GET /api/v1/agent_instance agent_id=P"},
		{agentSpanKind, agentInstanceKind, "GET /api/v1/agent_spans agent_instance_id=P"},
		{alertKind, nil, "GET /api/v1/alerts"},
		{apiTokenKind, nil, "GET /api/v1/api_token"},
		{adminUserKind, nil, "GET /api/v1/admin_user"},
		{adminUserInviteKind, nil, "GET /api/v1/admin_user_invite"},
		{agentVersionKind, agentKind, "GET /api/v1/agent_version agent_id=P"},
		{agentSchemaVersionKind, agentKind, "GET /api/v1/agent_schema_version agent_id=P"},
		{agentDeploymentKind, agentKind, "GET /api/v1/agent_deployment agent_id=P"},
		{agentAssociationKind, judgeKind, "GET /api/v1/agent_association/from agent_ids[]=P"},
		{judgeRunKind, judgeKind, "GET /api/v1/agent_instance agent_id=P"},
		{agentInstanceAssociationKind, judgeRunKind, "GET /api/v1/agent_instance_association/from agent_instance_ids[]=P"},
		{teamKind, nil, "GET /api/v1/team"},
		{personKind, nil, "GET /api/v1/person"},
		{riskProfileKind, nil, "GET /api/v1/risk_profile"},
		{llmProviderKind, nil, "GET /api/v1/llm_provider"},
		{llmProviderModelKind, llmProviderKind, "GET /api/v1/llm_provider_model llm_provider_id=P"},
		{knowledgebaseKind, nil, "GET /api/v1/knowledgebase"},
		{knowledgebaseDocumentKind, knowledgebaseKind, "GET /api/v1/knowledgebase_document knowledgebase_id=P"},
		{knowledgebaseDocumentRevisionKind, knowledgebaseDocumentKind, "GET /api/v1/knowledgebase_document_revision knowledgebase_document_id=P"},
		{knowledgebaseRevisionKind, knowledgebaseKind, "GET /api/v1/knowledgebase_revision knowledgebase_id=P"},
		{agentKnowledgebaseLinkKind, agentKind, "GET /api/v1/agent_knowledgebase_link agent_id=P"},
		{agentRiskVersionKind, agentKind, "GET /api/v1/agent_risk_version agent_id=P"},
	}

	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Method + " " + r.URL.Path
		for _, key := range []string{"role", "account_id", "agent_id", "agent_instance_id", "agent_ids[]", "agent_instance_ids[]", "llm_provider_id", "knowledgebase_id", "knowledgebase_document_id"} {
			if v := r.URL.Query().Get(key); v != "" {
				got += " " + key + "=" + v
			}
		}
		_, _ = io.WriteString(w, `{"summaries":[]}`)
	}))
	defer server.Close()

	c, err := client.New(server.URL, "tok", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range cases {
		if tc.kind.parent != tc.parent {
			t.Errorf("%s: parent is %v, want %v", tc.kind.name, kindName(tc.kind.parent), kindName(tc.parent))
		}
		if _, _, err := listChoices(context.Background(), c, tc.kind, "P"); err != nil {
			t.Fatalf("%s: %v", tc.kind.name, err)
		}
		if got != tc.want {
			t.Errorf("%s lists %q, want %q", tc.kind.name, got, tc.want)
		}
	}

	tested := map[*resourceKind]bool{}
	for _, tc := range cases {
		tested[tc.kind] = true
	}
	var used []*resourceKind
	for _, k := range groupKinds {
		used = append(used, k)
	}
	for _, k := range flagKinds {
		used = append(used, k)
	}
	for _, k := range argumentKinds {
		used = append(used, k)
	}
	for _, k := range used {
		for ; k != nil; k = k.parent {
			if !tested[k] {
				t.Errorf("%s has a picker but no case here", k.name)
			}
		}
	}
}

// TestIDFlagsNameTheRightResource pins the flags and arguments whose meaning
// depends on the command, such as the judge runs that
// agent-instance-associations list-from takes.
func TestIDFlagsNameTheRightResource(t *testing.T) {
	root := newRootCommand(newTestApp(t).App)
	cases := []struct {
		command []string
		input   string // a flag name, or "<arg>" for an argument
		want    *resourceKind
	}{
		{[]string{"agent-instance-associations", "list-from"}, "agent-instance-ids", judgeRunKind},
		{[]string{"agent-instance-associations", "list-to"}, "agent-instance-id", agentInstanceKind},
		{[]string{"agent-instance-associations", "retrieve"}, "<id>", agentInstanceAssociationKind},
		{[]string{"agent-associations", "list-from"}, "agent-ids", judgeKind},
		{[]string{"agent-associations", "list-to"}, "agent-id", agentKind},
		{[]string{"agent-associations", "create"}, "from-agent-id", judgeKind},
		{[]string{"agent-associations", "create"}, "to-agent-id", agentKind},
		{[]string{"judges", "retrieve"}, "<id>", judgeKind},
		{[]string{"agent-instances", "list"}, "agent-id", agentKind},
	}
	for _, tc := range cases {
		cmd, _, err := root.Find(tc.command)
		if err != nil {
			t.Fatal(err)
		}
		var got *resourceKind
		for _, b := range bindings(cmd) {
			if (b.argIndex >= 0 && tc.input == "<id>") || (b.argIndex < 0 && b.flag.Name == tc.input) {
				got = b.kind
			}
		}
		if got != tc.want {
			t.Errorf("%v %s names %s, want %s", tc.command, tc.input, kindName(got), kindName(tc.want))
		}
	}
}

func kindName(k *resourceKind) string {
	if k == nil {
		return "none"
	}
	return k.name
}

func TestLatestForAnUnknownAgent(t *testing.T) {
	ta := newTestApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":"validation_errors","status":"error","errors":{"agent_ref":{"code":"invalid_value","message":"Invalid value: \"nope\""}}}`)
	}))
	defer server.Close()
	ta.addProfile(t, "default", "tok", server.URL)

	_, err := ta.run(t, "agent-instances", "retrieve", "nope/@latest")
	if err == nil || err.Error() != "Agent nope was not found, so @latest has nothing to use." {
		t.Fatalf("err = %v", err)
	}
}
