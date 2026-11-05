// Package client connects the generated API client to a profile's base URL
// and token, with the same timeout, retry and error behaviour as the
// TypeScript CLI.
package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/prefactordev/cli/internal/api"
)

const (
	requestTimeout    = 10 * time.Second
	maxRetries        = 2
	initialRetryDelay = 250 * time.Millisecond
	maxRetryDelay     = 2 * time.Second
	retryMultiplier   = 2
)

// HTTPError is a failed API request. Message matches the TypeScript CLI's
// error text; Body is the raw response body.
type HTTPError struct {
	Message string
	Status  int
	Body    []byte
}

func (e *HTTPError) Error() string {
	return e.Message
}

// New returns a generated API client for baseURL that sends token as a bearer
// token. Like the TypeScript CLI, requests go to the origin of baseURL and any
// path in it is ignored.
func New(baseURL, token, version string) (*api.Client, error) {
	origin, err := originOf(baseURL)
	if err != nil {
		return nil, err
	}

	doer := &retryingDoer{
		client: &http.Client{Timeout: requestTimeout},
		sleep:  sleepContext,
		random: rand.Float64,
	}

	return api.NewClient(origin, api.WithHTTPClient(doer), api.WithRequestEditorFn(
		func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-Prefactor-SDK", SDKHeader(version))
			return nil
		},
	))
}

// SDKHeader is the X-Prefactor-SDK value for this CLI version.
func SDKHeader(version string) string {
	return "prefactor-cli@" + version
}

// ReadJSON returns the body of a successful response, or an *HTTPError for a
// failed one. A transport error becomes the TypeScript CLI's network error.
func ReadJSON(resp *http.Response, err error) ([]byte, error) {
	if err != nil {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) {
			return nil, httpErr
		}
		return nil, &HTTPError{Message: "HTTP request failed due to network error"}
	}
	defer func() { _ = resp.Body.Close() }()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, &HTTPError{Message: "HTTP request failed due to network error"}
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return body, nil
	}

	return nil, &HTTPError{
		Message: fmt.Sprintf("HTTP request failed with status %d", resp.StatusCode),
		Status:  resp.StatusCode,
		Body:    body,
	}
}

func originOf(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || !ValidPort(parsed.Port()) {
		return "", fmt.Errorf("invalid base URL %q", baseURL)
	}

	return parsed.Scheme + "://" + parsed.Host, nil
}

// ValidPort reports whether port, which may be empty, is one a URL can
// have, as the WHATWG URL parser the TypeScript CLI uses checks it.
func ValidPort(port string) bool {
	if port == "" {
		return true
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 0 && n <= 65535
}

// retryingDoer retries 429 and 5xx responses and network errors with
// jittered exponential backoff.
type retryingDoer struct {
	client *http.Client
	sleep  func(context.Context, time.Duration) error
	random func() float64
}

func (d *retryingDoer) Do(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
	}

	for attempt := 0; ; attempt++ {
		attemptReq := req.Clone(req.Context())
		if body != nil {
			attemptReq.Body = io.NopCloser(bytes.NewReader(body))
			attemptReq.ContentLength = int64(len(body))
		}

		resp, err := d.client.Do(attemptReq)
		if err != nil {
			if attempt < maxRetries && retryableNetworkError(req.Context(), err) {
				if sleepErr := d.sleep(req.Context(), retryDelay(attempt, d.random)); sleepErr != nil {
					return nil, err
				}
				continue
			}
			return nil, err
		}

		if attempt < maxRetries && retryableStatus(resp.StatusCode) {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if sleepErr := d.sleep(req.Context(), retryDelay(attempt, d.random)); sleepErr != nil {
				return nil, sleepErr
			}
			continue
		}

		return resp, nil
	}
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || (status >= 500 && status <= 599)
}

// retryableNetworkError reports whether a transport error is worth retrying:
// any failure except the caller cancelling the request.
func retryableNetworkError(ctx context.Context, _ error) bool {
	return ctx.Err() == nil
}

func retryDelay(attempt int, random func() float64) time.Duration {
	base := initialRetryDelay
	for i := 0; i < attempt; i++ {
		base *= retryMultiplier
	}
	if base > maxRetryDelay {
		base = maxRetryDelay
	}

	jitter := 0.5 + random()*0.5
	return time.Duration(float64(base) * jitter)
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
