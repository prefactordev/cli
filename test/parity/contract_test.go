//go:build !parity

package parity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
)

// The contract tests check the requests this CLI sends against
// api/openapi.json. contract-cases/ holds a valid invocation of every
// operation in api/coverage.yaml, in this CLI's names, and each of its
// requests must match the document: the operation, every parameter and body
// field, and their types. The parity cases in cases/ pass unusual values
// through on purpose, so their requests are only checked for what the CLI
// chooses itself: the operation and the query parameter names.

var contractBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "contract-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	contractBinary = filepath.Join(dir, "prefactor")
	build := exec.Command("go", "build", "-o", contractBinary, "../../cmd/prefactor")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// contractResponse lets multi-step commands, such as setup and the
// integrations commands, run on: it has an ID, a token, a list with a Slack
// integration and a successful ping. emptyResponse is a list with no items.
const (
	contractResponse = `{"status":"success","details":{"id":"01m6jcdypz3tq23qmke2ckg5jpt7k0fg","content":"x","environment_id":"01m6jcdypz3tq23qmke2ckg5jpt7k0fg","token_type":"api_session_agent_deployment_scope","agent_id":"a1"},"token":"tok","summaries":[{"id":"01m6jcdypz3tq23qmke2ckg5jpt7k0fg","integration_type":"slack","environment_id":"01m6jcdypz3tq23qmke2ckg5jpt7k0fg"}]}`
	emptyResponse    = `{"status":"success","summaries":[]}`
)

type sentRequest struct {
	method, path string
	query        url.Values
	body         []byte
}

func loadContract(t *testing.T) (*openapi3.T, routers.Router) {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile("../../api/openapi.normalized.json")
	if err != nil {
		t.Fatal(err)
	}
	doc.Servers = openapi3.Servers{{URL: "/"}}
	router, err := legacy.NewRouter(doc, openapi3.DisableSchemaFormatValidation(), openapi3.DisableExamplesValidation(), openapi3.AllowExtraSiblingFields("description"))
	if err != nil {
		t.Fatal(err)
	}
	return doc, router
}

func TestRequestsMatchTheOpenAPIDocument(t *testing.T) {
	_, router := loadContract(t)
	full := map[string]bool{}
	contract := loadCasesFrom(t, "contract-cases")
	for _, tc := range contract {
		full[tc.file+fmt.Sprint(tc.line)] = true
	}
	cases := append(loadCases(t), contract...)

	var mu sync.Mutex
	var sent []sentRequest
	empty := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		sent = append(sent, sentRequest{r.Method, r.URL.Path, r.URL.Query(), body})
		respond := contractResponse
		if empty && r.Method == http.MethodGet {
			respond = emptyResponse
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respond)
	}))
	defer server.Close()

	checked := map[string]bool{}
	for _, tc := range cases {
		if tc.help {
			continue
		}
		mu.Lock()
		sent = nil
		empty = tc.empty
		mu.Unlock()
		args := append(kebabWords(tc.words), kebabFlags(tc.args)...)
		stderr := runContract(t, server.URL, args)

		mu.Lock()
		requests := sent
		mu.Unlock()
		name := fmt.Sprintf("%s:%d %s", tc.file, tc.line, strings.Join(tc.words, " "))
		isFull := full[tc.file+fmt.Sprint(tc.line)]
		if isFull && len(requests) == 0 {
			t.Errorf("%s sent no request: %s", name, stderr)
		}
		for _, req := range requests {
			for _, problem := range checkRequest(router, req, isFull) {
				t.Errorf("%s: %s %s: %s", name, req.method, req.path, problem)
			}
			if route, _, err := router.FindRoute(mustRequest(req)); err == nil && isFull {
				checked[route.Operation.OperationID] = true
			}
		}
	}

	for _, op := range coveredOperations(t) {
		if !checked[op] {
			t.Errorf("%s has a command in api/coverage.yaml but no case in contract-cases/", op)
		}
	}
}

// coveredOperations lists the operations api/coverage.yaml maps to a
// command.
func coveredOperations(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../../api/coverage.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var ops []string
	for _, line := range strings.Split(string(raw), "\n") {
		op, command, ok := strings.Cut(line, ": ")
		if ok && !strings.HasPrefix(op, "#") && !strings.HasPrefix(command, "{") {
			ops = append(ops, op)
		}
	}
	return ops
}

// runContract runs this CLI and returns its stderr.
func runContract(t *testing.T, serverURL string, args []string) string {
	t.Helper()
	work := t.TempDir()
	if err := os.Mkdir(filepath.Join(work, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	profile := fmt.Sprintf(`{"default":{"api_key":"tok","base_url":%q}}`, serverURL)
	if err := os.WriteFile(filepath.Join(work, "prefactor.json"), []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtures, _ := filepath.Abs("fixtures")
	if err := os.Symlink(fixtures, filepath.Join(work, "fixtures")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(contractBinary, args...)
	cmd.Dir = work
	cmd.Env = append(filteredEnvironment(), "HOME="+work)
	cmd.Stdin = strings.NewReader("")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return strings.TrimSpace(stderr.String())
}

func filteredEnvironment() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PREFACTOR_") {
			env = append(env, kv)
		}
	}
	return env
}

// specForm rewrites the query forms this CLI sends because p2 accepts them
// and not the documented ones (see the README): sorting[field] and
// sorting[direction] become sorting, and name[]=value becomes name=value.
func specForm(query url.Values) url.Values {
	out := url.Values{}
	for key, values := range query {
		switch {
		case key == "sorting[field]":
			field := values[0]
			if query.Get("sorting[direction]") == "desc" {
				field = "-" + field
			}
			out.Set("sorting", field)
		case key == "sorting[direction]":
		case strings.HasSuffix(key, "[]"):
			out[strings.TrimSuffix(key, "[]")] = values
		default:
			out[key] = values
		}
	}
	return out
}

func mustRequest(req sentRequest) *http.Request {
	u := url.URL{Path: req.path, RawQuery: specForm(req.query).Encode()}
	r, _ := http.NewRequest(req.method, u.String(), bytes.NewReader(req.body))
	if len(req.body) > 0 {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

// checkRequest lists how req departs from the document. Unless full, only
// the operation and query parameter names are checked.
func checkRequest(router routers.Router, req sentRequest, full bool) []string {
	r := mustRequest(req)
	route, pathParams, err := router.FindRoute(r)
	if err != nil {
		return []string{"no such operation"}
	}
	var problems []string

	declared := map[string]bool{}
	for _, p := range route.Operation.Parameters {
		if p.Value.In == openapi3.ParameterInQuery {
			declared[p.Value.Name] = true
		}
	}
	var unknown []string
	for key := range r.URL.Query() {
		if !declared[key] {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	for _, key := range unknown {
		problems = append(problems, "query parameter "+key+" is not declared")
	}
	if !full {
		return problems
	}

	input := &openapi3filter.RequestValidationInput{
		Request:    r,
		PathParams: pathParams,
		Route:      route,
		Options: &openapi3filter.Options{
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
			MultiError:         true,
		},
	}
	if err := openapi3filter.ValidateRequest(context.Background(), input); err != nil {
		problems = append(problems, strings.ReplaceAll(err.Error(), "\n", " | "))
	}

	if len(req.body) > 0 && route.Operation.RequestBody != nil {
		if media := route.Operation.RequestBody.Value.Content.Get("application/json"); media != nil {
			var body any
			if json.Unmarshal(req.body, &body) == nil {
				problems = append(problems, undeclaredFields(media.Schema, body, "body")...)
			}
		}
	}
	return problems
}

// undeclaredFields lists object fields in value that schema does not
// declare, where the schema declares its fields and does not allow others.
func undeclaredFields(ref *openapi3.SchemaRef, value any, path string) []string {
	if ref == nil || ref.Value == nil {
		return nil
	}
	s := ref.Value
	for _, alternative := range append(append([]*openapi3.SchemaRef{}, s.OneOf...), s.AnyOf...) {
		if alternative.Value != nil && matchesType(alternative.Value, value) {
			return undeclaredFields(alternative, value, path)
		}
	}
	switch v := value.(type) {
	case map[string]any:
		if len(s.Properties) == 0 {
			return nil
		}
		var problems []string
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			prop, ok := s.Properties[key]
			if !ok {
				if s.AdditionalProperties.Has == nil || !*s.AdditionalProperties.Has {
					problems = append(problems, path+"."+key+" is not declared")
				}
				continue
			}
			problems = append(problems, undeclaredFields(prop, v[key], path+"."+key)...)
		}
		return problems
	case []any:
		var problems []string
		for i, item := range v {
			problems = append(problems, undeclaredFields(s.Items, item, fmt.Sprintf("%s[%d]", path, i))...)
		}
		return problems
	}
	return nil
}

func matchesType(s *openapi3.Schema, value any) bool {
	switch value.(type) {
	case map[string]any:
		return s.Type.Is("object") || len(s.Properties) > 0
	case []any:
		return s.Type.Is("array")
	case nil:
		return s.Type.Is("null")
	default:
		return !s.Type.Is("object") && !s.Type.Is("array") && !s.Type.Is("null")
	}
}
