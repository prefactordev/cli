package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// fakePicker records each prompt and answers with the choice whose label
// contains the next scripted text.
type fakePicker struct {
	answers []string
	prompts []string
	offered [][]Choice
}

func (p *fakePicker) Pick(title string, choices []Choice) (string, error) {
	p.prompts = append(p.prompts, title)
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
	want := "@latest needs an agent to choose from. Pass the agent instance ID instead; 'prefactor agent-instances list --agent-id <agent_id>' lists them."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v", err)
	}

	_, err = ta.run(t, "alerts", "list", "--agent-id", agentA, "--agent-instance-id", "@latest")
	if err == nil || err.Error() != "Agent "+agentA+" has no instances, so @latest has nothing to use." {
		t.Fatalf("err = %v", err)
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

func TestEveryOtherCheckRunsBeforeAnyPicker(t *testing.T) {
	ta, _, picker := pickerTestApp(t, twoAgents)

	_, err := ta.run(t, "agent-schema-versions", "create")
	if err == nil || err.Error() != "error: required option '--external-identifier <external-identifier>' not specified" {
		t.Fatalf("err = %v", err)
	}
	_, err = ta.run(t, "agents", "retrieve", "--bogus")
	if err == nil || err.Error() != "error: unknown option '--bogus'" {
		t.Fatalf("err = %v", err)
	}
	if len(picker.prompts) != 0 {
		t.Fatalf("asked %v before reporting the error", picker.prompts)
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
