package langfuse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	requestTimeout = 60 * time.Second
	// maxBodySize is the largest response Langfuse Cloud sends.
	maxBodySize = 5 * 1024 * 1024
	// maxRateLimitRetries is how many 429 responses a request waits out.
	maxRateLimitRetries = 5
	// maxRetries is how many network errors and 5xx responses a request
	// retries.
	maxRetries     = 2
	retryBaseDelay = time.Second
	// maxRetryAfter caps a Retry-After wait.
	maxRetryAfter = 2 * time.Minute
	// metricsRowLimit is the most rows the Metrics API returns for one
	// query. It has no pagination.
	metricsRowLimit = 1000
	// minCountWindow is the shortest window TraceCounts splits.
	minCountWindow = time.Second
	// maxSplitDepth is how many times TraceCounts halves a window, which
	// caps it at 2^(maxSplitDepth+1)-1 Metrics queries.
	maxSplitDepth = 5
	// maxRedirects is how many same-host redirects a request follows.
	maxRedirects = 10
)

// ErrResponseTooLarge is returned for a response over maxBodySize. A
// smaller page of observations can fit.
var ErrResponseTooLarge = errors.New("Langfuse sent a response larger than 5 MB.")

// errRedirect refuses a redirect that would send the keys to another host
// or over another scheme.
var errRedirect = errors.New("Langfuse redirected the request to another host or scheme, so the keys were not sent there")

// APIError is a response from Langfuse other than 2xx. Message is
// Langfuse's own message when the body has one.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Langfuse returned %d: %s", e.Status, e.Message)
}

// Client calls the Langfuse public API with a project's keys.
type Client struct {
	keys      Keys
	userAgent string
	http      *http.Client
	// sleep waits between retries; tests replace it.
	sleep func(ctx context.Context, d time.Duration) error
}

// NewClient returns a client for keys. version goes in the User-Agent.
func NewClient(keys Keys, version string) *Client {
	return &Client{
		keys:      keys,
		userAgent: "prefactor-cli/" + version,
		http:      &http.Client{Timeout: requestTimeout, CheckRedirect: sameOriginRedirect},
		sleep:     sleepContext,
	}
}

// sameOriginRedirect follows a redirect only to the scheme and host of
// the first request. Go keeps the Authorization header on a redirect to
// the same host name over http, or to another port on it.
func sameOriginRedirect(req *http.Request, via []*http.Request) error {
	first := via[0].URL
	if req.URL.Scheme != first.Scheme || req.URL.Host != first.Host {
		return errRedirect
	}
	if len(via) >= maxRedirects {
		return fmt.Errorf("Langfuse redirected the request more than %d times", maxRedirects)
	}
	return nil
}

// Organization is the organisation a project belongs to.
type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Project is a Langfuse project. An API key belongs to one project.
type Project struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Organization Organization `json:"organization"`
}

// Projects returns the projects the keys belong to. It succeeds for any
// valid key, including keys that cannot read traces.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var response struct {
		Data []Project `json:"data"`
	}
	if err := c.get(ctx, "/api/public/projects", nil, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

// CheckTracesRead asks for one observation, which fails with 403 when the
// key lacks the traces:read permission.
func (c *Client) CheckTracesRead(ctx context.Context, from, to time.Time) error {
	query := url.Values{
		"fromStartTime": {Timestamp(from)},
		"toStartTime":   {Timestamp(to)},
		"limit":         {"1"},
		"fields":        {"core"},
	}
	var response json.RawMessage
	return c.get(ctx, "/api/public/v2/observations", query, &response)
}

// TraceCount is how many root observations, which are runs, a trace name
// has in one environment.
type TraceCount struct {
	TraceName   string
	Environment string
	Count       int
}

// TraceCounts counts root observations by trace name and environment
// between from and to, both inclusive, with the Metrics API. That API
// returns at most metricsRowLimit rows and cannot page, so a window that
// fills them is split in two and each half counted on its own.
//
// It halves a window at most maxSplitDepth times. Needing more usually
// means trace names contain an ID, which would make every run its own
// agent.
func (c *Client) TraceCounts(ctx context.Context, from, to time.Time) ([]TraceCount, error) {
	return c.traceCounts(ctx, from, to, 0)
}

func (c *Client) traceCounts(ctx context.Context, from, to time.Time, depth int) ([]TraceCount, error) {
	counts, err := c.traceCountsQuery(ctx, from, to)
	if err != nil || len(counts) < metricsRowLimit {
		return counts, err
	}
	if depth >= maxSplitDepth {
		return nil, fmt.Errorf("Langfuse has more than %d trace name and environment pairs between %s and %s, which is too many to import. Each trace name becomes an agent, and trace names often contain an ID such as a session or user ID.",
			metricsRowLimit, Timestamp(from), Timestamp(to))
	}
	if to.Sub(from) <= minCountWindow {
		return nil, fmt.Errorf("Langfuse has more than %d trace name and environment pairs between %s and %s, which is too many to count.",
			metricsRowLimit, Timestamp(from), Timestamp(to))
	}
	mid := from.Add(to.Sub(from) / 2).Truncate(time.Millisecond)
	first, err := c.traceCounts(ctx, from, mid, depth+1)
	if err != nil {
		return nil, err
	}
	second, err := c.traceCounts(ctx, mid.Add(time.Millisecond), to, depth+1)
	if err != nil {
		return nil, err
	}
	return addCounts(first, second), nil
}

// addCounts adds b's counts to a's, by trace name and environment, keeping
// a's order and appending pairs only b has.
func addCounts(a, b []TraceCount) []TraceCount {
	type pair struct{ traceName, environment string }
	index := make(map[pair]int, len(a))
	for i, tc := range a {
		index[pair{tc.TraceName, tc.Environment}] = i
	}
	for _, count := range b {
		key := pair{count.TraceName, count.Environment}
		if i, ok := index[key]; ok {
			a[i].Count += count.Count
			continue
		}
		index[key] = len(a)
		a = append(a, count)
	}
	return a
}

// traceCountsQuery sends one Metrics API query for TraceCounts.
func (c *Client) traceCountsQuery(ctx context.Context, from, to time.Time) ([]TraceCount, error) {
	metricsQuery := map[string]any{
		"view":       "observations",
		"metrics":    []map[string]string{{"measure": "count", "aggregation": "count"}},
		"dimensions": []map[string]string{{"field": "traceName"}, {"field": "environment"}},
		"filters": []map[string]any{
			{"column": "isRootObservation", "operator": "=", "value": true, "type": "boolean"},
		},
		"fromTimestamp": Timestamp(from),
		"toTimestamp":   Timestamp(to),
		"config":        map[string]int{"row_limit": metricsRowLimit},
	}
	encoded, err := json.Marshal(metricsQuery)
	if err != nil {
		return nil, err
	}
	var response struct {
		Data []struct {
			TraceName   *string     `json:"traceName"`
			Environment *string     `json:"environment"`
			Count       json.Number `json:"count_count"`
		} `json:"data"`
	}
	if err := c.get(ctx, "/api/public/v2/metrics", url.Values{"query": {string(encoded)}}, &response); err != nil {
		return nil, err
	}

	counts := make([]TraceCount, 0, len(response.Data))
	for _, row := range response.Data {
		// ClickHouse may send counts as strings or as numbers, which
		// json.Number accepts alike.
		if row.Count == "" {
			return nil, errors.New("Langfuse metrics response has a row with no count.")
		}
		count, err := row.Count.Float64()
		if err != nil {
			return nil, fmt.Errorf("Langfuse metrics response has a count that is not a number: %q", row.Count)
		}
		tc := TraceCount{Count: int(count)}
		if row.TraceName != nil {
			tc.TraceName = *row.TraceName
		}
		if row.Environment != nil {
			tc.Environment = *row.Environment
		}
		counts = append(counts, tc)
	}
	return counts, nil
}

// get sends a GET request and decodes the JSON response into out. It
// waits out 429 responses as Retry-After asks, and retries network errors
// and 5xx responses a few times.
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	target := c.keys.BaseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	rateLimited, failed := 0, 0
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return err
		}
		req.SetBasicAuth(c.keys.PublicKey.Reveal(), c.keys.SecretKey.Reveal())
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)

		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() == nil && failed < maxRetries && !errors.Is(err, errRedirect) {
				failed++
				if err := c.sleep(ctx, retryBaseDelay*time.Duration(failed)); err != nil {
					return err
				}
				continue
			}
			return c.networkError(err)
		}
		body, err := readBody(resp)
		if err != nil {
			return err
		}

		switch {
		case resp.StatusCode == http.StatusTooManyRequests && rateLimited < maxRateLimitRetries:
			rateLimited++
			if err := c.sleep(ctx, retryAfter(resp.Header.Get("Retry-After"), rateLimited)); err != nil {
				return err
			}
			continue
		case resp.StatusCode >= 500 && failed < maxRetries:
			failed++
			if err := c.sleep(ctx, retryBaseDelay*time.Duration(failed)); err != nil {
				return err
			}
			continue
		case resp.StatusCode < 200 || resp.StatusCode > 299:
			return &APIError{Status: resp.StatusCode, Message: c.redact(errorMessage(resp.StatusCode, body))}
		}

		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("Langfuse sent a response to %s that is not valid JSON.", path)
		}
		return nil
	}
}

// networkError describes a failed request without its URL or headers.
func (c *Client) networkError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	return fmt.Errorf("Unable to reach Langfuse at %s: %s", c.keys.BaseURL, c.redact(err.Error()))
}

// redact replaces the keys in text, in case a server or proxy echoes them.
func (c *Client) redact(text string) string {
	for _, key := range []Secret{c.keys.PublicKey, c.keys.SecretKey} {
		if !key.Empty() {
			text = strings.ReplaceAll(text, key.Reveal(), key.String())
		}
	}
	return text
}

// readBody reads at most maxBodySize bytes of a response.
func readBody(resp *http.Response) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("Unable to read the response from Langfuse: %w", err)
	}
	if len(body) > maxBodySize {
		return nil, ErrResponseTooLarge
	}
	return body, nil
}

// errorMessage is the message in a Langfuse error body, or the start of
// the body when it has none.
func errorMessage(status int, body []byte) string {
	var parsed struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		if parsed.Message != "" {
			return parsed.Message
		}
		if parsed.Error != "" {
			return parsed.Error
		}
	}
	text := strings.TrimSpace(string(bytes.ToValidUTF8(body, nil)))
	if len(text) > 300 {
		text = text[:300] + "..."
	}
	if text == "" {
		text = http.StatusText(status)
	}
	return text
}

// retryAfter is how long a 429 response asks to wait: Retry-After in
// seconds, or a growing delay without it.
func retryAfter(header string, attempt int) time.Duration {
	wait := time.Duration(attempt) * 5 * time.Second
	if seconds, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && seconds >= 0 {
		wait = time.Duration(seconds) * time.Second
	}
	return min(wait, maxRetryAfter)
}

// Timestamp formats a time as Langfuse's ISO 8601 parameters expect.
func Timestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
