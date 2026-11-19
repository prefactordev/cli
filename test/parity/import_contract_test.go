//go:build !parity

package parity

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestImportLangfuseRequestsMatchTheOpenAPIDocument runs import langfuse
// against a fake Langfuse, and checks every request it sends to Prefactor
// against the document, including the raw observations it uploads.
func TestImportLangfuseRequestsMatchTheOpenAPIDocument(t *testing.T) {
	_, router := loadContract(t)

	start := time.Now().UTC().Add(-time.Hour)
	observation := func(id string, at time.Time, root bool) string {
		parent := `"root"`
		if root {
			parent = "null"
		}
		// Every field the Observations API v2 returns for the field
		// groups the import reads, as Langfuse Cloud sent them.
		return fmt.Sprintf(`{"id":%q,"traceId":"trace-1","projectId":"p1","type":"GENERATION","name":"ai.generateText",`+
			`"startTime":%q,"endTime":%q,"completionStartTime":null,"createdAt":%q,"updatedAt":%q,`+
			`"parentObservationId":%s,"isRootObservation":%v,"level":"DEFAULT","statusMessage":"","version":"","environment":"production",`+
			`"bookmarked":false,"public":false,"userId":"u1","sessionId":"","traceName":"support","tags":["beta"],"release":"",`+
			`"input":"{\"messages\":[]}","output":"\"ok\"","metadata":{"feature":"orders","resourceAttributes.process.pid":"1"},`+
			`"model":"claude-sonnet-4-5","internalModelId":"m1","modelId":"m1","modelParameters":{"maxTokens":1024},`+
			`"inputPrice":null,"outputPrice":null,"totalPrice":null,"usageDetails":{"input":10,"output":2,"total":12},`+
			`"inputUsage":10,"outputUsage":2,"totalUsage":12,"costDetails":{"total":0.001},"inputCost":0.0008,"outputCost":0.0002,"totalCost":0.001,`+
			`"usagePricingTierId":null,"usagePricingTierName":null,"latency":1.5,"timeToFirstToken":null}`,
			id, at.Format("2006-01-02T15:04:05.000Z"), at.Add(time.Second).Format("2006-01-02T15:04:05.000Z"),
			at.Format("2006-01-02T15:04:05.000Z"), at.Format("2006-01-02T15:04:05.000Z"), parent, root)
	}
	langfuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/public/projects":
			_, _ = io.WriteString(w, `{"data":[{"id":"p1","name":"Project","organization":{"id":"o1","name":"Org"}}]}`)
		case r.URL.Path == "/api/public/v2/metrics":
			_, _ = io.WriteString(w, `{"data":[{"traceName":"support","environment":"production","count_count":1}]}`)
		case r.URL.Path == "/api/public/v2/observations" && r.URL.Query().Get("limit") == "1":
			_, _ = io.WriteString(w, `{"data":[],"meta":{}}`)
		case r.URL.Path == "/api/public/v2/observations":
			_, _ = io.WriteString(w, `{"data":[`+observation("child", start.Add(time.Second), false)+`,`+observation("root", start, true)+`],"meta":{"cursor":null}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer langfuse.Close()

	var mu sync.Mutex
	var sent []sentRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		sent = append(sent, sentRequest{r.Method, r.URL.Path, r.URL.Query(), body})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/traces") {
			_, _ = io.WriteString(w, `{"status":"success","results":[{"trace_id":"trace-1","status":"written","agent_instance_id":"a1","reason":null}],"details":{"id":"01m6jcdypz3tq23qmke2ckg5jpt7k0fg"}}`)
			return
		}
		_, _ = io.WriteString(w, contractResponse)
	}))
	defer server.Close()

	stderr := runContractWithEnv(t, server.URL, []string{"import", "langfuse", "--yes"}, []string{
		"LANGFUSE_PUBLIC_KEY=pk-lf-contract", "LANGFUSE_SECRET_KEY=sk-lf-contract", "LANGFUSE_BASE_URL=" + langfuse.URL,
	})

	mu.Lock()
	requests := sent
	mu.Unlock()
	checked := map[string]bool{}
	for _, req := range requests {
		for _, problem := range checkRequest(router, req, true) {
			t.Errorf("%s %s: %s", req.method, req.path, problem)
		}
		if route, _, err := router.FindRoute(mustRequest(req)); err == nil {
			checked[route.Operation.OperationID] = true
		}
	}
	for op := range ownContractTests {
		if !checked[op] {
			t.Errorf("import langfuse sent no %s request: %s", op, stderr)
		}
	}
}
