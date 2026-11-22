package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

const (
	lfPublic       = "pk-lf-1111"
	lfSecret       = "sk-lf-2222"
	lfViewerPublic = "pk-lf-3333"
	lfViewerSecret = "sk-lf-4444"
)

// langfuseServer is a fake Langfuse. Keys in readers can read traces;
// other keys it knows get 403 from the observations API, and unknown keys
// get 401.
type langfuseServer struct {
	*httptest.Server
	mu      sync.Mutex
	readers map[string]bool
	metrics string
	// users is the public key of each request, in order.
	users []string
}

func newLangfuseServer(t *testing.T) *langfuseServer {
	t.Helper()
	ls := &langfuseServer{
		readers: map[string]bool{lfPublic + ":" + lfSecret: false, lfViewerPublic + ":" + lfViewerSecret: true},
		metrics: `{"data":[
			{"traceName":"linear-daily-report","environment":"development","count_count":1},
			{"traceName":"linear-daily-report","environment":"production","count_count":100},
			{"traceName":"triage","environment":"production","count_count":"4"},
			{"traceName":"triage-v2","environment":"production","count_count":2}
		]}`,
	}
	ls.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		ls.mu.Lock()
		ls.users = append(ls.users, user)
		canRead, known := ls.readers[user+":"+pass]
		metrics := ls.metrics
		ls.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case !known:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"message":"Invalid credentials. Confirm that you've configured the correct host."}`)
		case r.URL.Path == "/api/public/projects":
			_, _ = io.WriteString(w, `{"data":[{"id":"p1","name":"My Project","organization":{"id":"o1","name":"Acme"},"metadata":{}}]}`)
		case !canRead:
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"message":"Access denied. The \"traces:read\" permission is required to access this resource.","error":"ForbiddenError"}`)
		case r.URL.Path == "/api/public/v2/observations":
			_, _ = io.WriteString(w, `{"data":[],"meta":{}}`)
		case r.URL.Path == "/api/public/v2/metrics":
			_, _ = io.WriteString(w, metrics)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ls.Close)
	return ls
}

// langfuseApp is a test app whose shell environment holds keys for ls, run
// from an empty directory so no .env or mise file is found.
func langfuseApp(t *testing.T, ls *langfuseServer, public, secret string) *testApp {
	t.Helper()
	t.Chdir(t.TempDir())
	ta := newTestApp(t)
	ta.env["LANGFUSE_PUBLIC_KEY"] = public
	ta.env["LANGFUSE_SECRET_KEY"] = secret
	ta.env["LANGFUSE_BASE_URL"] = ls.URL
	return ta
}

func assertNoKeys(t *testing.T, texts ...string) {
	t.Helper()
	for _, text := range texts {
		for _, key := range []string{lfPublic, lfSecret, lfViewerPublic, lfViewerSecret} {
			if strings.Contains(text, key) {
				t.Fatalf("output contains a key:\n%s", text)
			}
		}
	}
}

func TestImportLangfuseDryRunPrintsThePlanAsJSON(t *testing.T) {
	ls := newLangfuseServer(t)
	ta := langfuseApp(t, ls, lfViewerPublic, lfViewerSecret)

	out, errOut, err := ta.runWithStderr(t, "import", "langfuse", "--dry-run", "--days", "7")
	if err != nil {
		t.Fatal(err)
	}
	assertNoKeys(t, out, errOut)
	if !strings.Contains(errOut, "Using Langfuse keys from the shell environment.") ||
		!strings.Contains(errOut, "Langfuse project: My Project (organisation: Acme) at "+ls.URL) {
		t.Fatalf("stderr = %s", errOut)
	}

	var plan langfusePlan
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if plan.KeysSource.Kind != "environment" || plan.Window.Days != 7 || plan.TotalRuns != 107 {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.Agents) != 3 || plan.Agents[0].Name != "linear-daily-report" || plan.Agents[0].Runs != 101 ||
		plan.Agents[0].Environments[0].Name != "production" {
		t.Fatalf("agents = %+v", plan.Agents)
	}
	if len(plan.Langfuse.Projects) != 1 || plan.Langfuse.Projects[0].Organization.Name != "Acme" {
		t.Fatalf("projects = %+v", plan.Langfuse.Projects)
	}
}

func TestImportLangfuseNeedsNoPrefactorProfile(t *testing.T) {
	ls := newLangfuseServer(t)
	ta := langfuseApp(t, ls, lfViewerPublic, lfViewerSecret)
	ta.env["PREFACTOR_PROFILE"] = "missing"

	if _, err := ta.run(t, "import", "langfuse", "--dry-run"); err != nil {
		t.Fatal(err)
	}
}

func TestImportLangfuseWithoutDryRunSendsNothing(t *testing.T) {
	ls := newLangfuseServer(t)
	ta := langfuseApp(t, ls, lfViewerPublic, lfViewerSecret)

	_, err := ta.run(t, "import", "langfuse")
	if err == nil || !strings.Contains(err.Error(), "not available yet") {
		t.Fatalf("err = %v", err)
	}
	if len(ls.users) != 0 {
		t.Fatalf("%d requests sent to Langfuse", len(ls.users))
	}
}

func TestImportLangfuseExplainsTheWrongHost(t *testing.T) {
	ls := newLangfuseServer(t)
	ta := langfuseApp(t, ls, "pk-lf-other", "sk-lf-other")

	_, errOut, err := ta.runWithStderr(t, "import", "langfuse", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "Invalid credentials. Confirm that you've configured the correct host.") ||
		!strings.Contains(err.Error(), "https://us.cloud.langfuse.com for the US region") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error()+errOut, "pk-lf-other") || strings.Contains(err.Error()+errOut, "sk-lf-other") {
		t.Fatalf("output contains a key: %v\n%s", err, errOut)
	}
}

func TestImportLangfuseSaysWhichVariablesToSetForAKeyThatCannotRead(t *testing.T) {
	ls := newLangfuseServer(t)
	ta := langfuseApp(t, ls, lfPublic, lfSecret)
	ta.terminal = true

	out, errOut, err := ta.runWithStderr(t, "import", "langfuse", "--dry-run")
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{`"traces:read" permission`, "Project Settings > API Keys", "Viewer", "Set LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY to the new key, or pass --env <file>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	assertNoKeys(t, out, errOut, err.Error())
}

func TestImportLangfuseLetsAPersonChooseAgents(t *testing.T) {
	ls := newLangfuseServer(t)
	ta := langfuseApp(t, ls, lfViewerPublic, lfViewerSecret)
	ta.terminal = true
	ta.Terminal = func(io.Writer) bool { return true }
	ta.env["NO_COLOR"] = "1"
	// Untick linear-daily-report.
	picker := &fakePicker{answers: []string{"triage  4,triage-v2"}}
	ta.Picker = picker

	out, errOut, err := ta.runWithStderr(t, "import", "langfuse", "--dry-run")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if len(picker.ticked[0]) != 3 {
		t.Fatalf("not every agent started ticked: %v", picker.ticked[0])
	}
	if !strings.HasPrefix(picker.offered[0][0].Label, "linear-daily-report  101 runs") {
		t.Fatalf("busiest agent is not first: %v", picker.offered[0])
	}
	for _, want := range []string{"Langfuse import plan", "Dry run: nothing was sent to Prefactor.", "triage  4 runs  (production 4)", "triage-v2  2 runs", "Total: 6 runs in 2 agents.", "about 1 minute at the Hobby plan's limit"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "linear-daily-report") {
		t.Errorf("unticked agent is in the plan:\n%s", out)
	}
	assertNoKeys(t, out, errOut)
}

func TestImportLangfuseRefusesABadDays(t *testing.T) {
	ls := newLangfuseServer(t)
	ta := langfuseApp(t, ls, lfViewerPublic, lfViewerSecret)

	if _, err := ta.run(t, "import", "langfuse", "--dry-run", "--days", "0"); err == nil || err.Error() != "--days must be a positive integer." {
		t.Fatalf("err = %v", err)
	}
}

func TestImportLangfuseSaysWhereTheHostCameFrom(t *testing.T) {
	ls := newLangfuseServer(t)
	ta := langfuseApp(t, ls, "", "")
	delete(ta.env, "LANGFUSE_PUBLIC_KEY")
	delete(ta.env, "LANGFUSE_SECRET_KEY")
	writeTestFile(t, ".env", "LANGFUSE_PUBLIC_KEY='"+lfViewerPublic+"'\nLANGFUSE_SECRET_KEY=\""+lfViewerSecret+"\"\n")

	out, errOut, err := ta.runWithStderr(t, "import", "langfuse", "--dry-run")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "Using the Langfuse host "+ls.URL+" from the shell environment.") {
		t.Fatalf("stderr = %s", errOut)
	}
	var plan langfusePlan
	if err := json.Unmarshal([]byte(out), &plan); err != nil || plan.KeysSource.Kind != "file" {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	assertNoKeys(t, out, errOut)
}

func TestImportLangfuseSaysWhichVariablesToSetWhenNoKeysAreFound(t *testing.T) {
	ls := newLangfuseServer(t)
	ta := langfuseApp(t, ls, "", "")
	delete(ta.env, "LANGFUSE_PUBLIC_KEY")
	delete(ta.env, "LANGFUSE_SECRET_KEY")
	ta.terminal = true

	_, err := ta.run(t, "import", "langfuse", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "No Langfuse keys found. Set LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY") ||
		!strings.Contains(err.Error(), "pass --env <file>") {
		t.Fatalf("err = %v", err)
	}
	if len(ls.users) != 0 {
		t.Fatalf("%d requests sent", len(ls.users))
	}
}

func TestImportLangfuseRefusesTheHIPAARegionBeforeAnyRequest(t *testing.T) {
	ls := newLangfuseServer(t)
	ta := langfuseApp(t, ls, lfViewerPublic, lfViewerSecret)
	ta.env["LANGFUSE_BASE_URL"] = "https://hipaa.cloud.langfuse.com"

	_, err := ta.run(t, "import", "langfuse", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "Prefactor cannot import from Langfuse's HIPAA region") {
		t.Fatalf("err = %v", err)
	}
	if len(ls.users) != 0 {
		t.Fatalf("%d requests sent", len(ls.users))
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
