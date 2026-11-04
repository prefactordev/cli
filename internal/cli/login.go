package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/prefactordev/cli/internal/profile"
)

const (
	slowDownExtra = 5 * time.Second
	// The server rejects longer client details with invalid_request.
	maxClientInfoLength = 64
)

// LoginDeps holds the parts of login that touch the outside world.
type LoginDeps struct {
	HTTPClient      *http.Client
	OpenBrowser     func(url string)
	PromptForToken  func(prompt string) (string, error)
	Sleep           func(ctx context.Context, d time.Duration) error
	Now             func() time.Time
	TerminalColumns func() (int, bool)
	Hostname        func() (string, error)
	Platform        string
}

func defaultLoginDeps() LoginDeps {
	return LoginDeps{
		HTTPClient:      &http.Client{Timeout: 30 * time.Second},
		OpenBrowser:     openBrowser,
		PromptForToken:  promptForToken,
		Sleep:           sleepContext,
		Now:             time.Now,
		TerminalColumns: terminalColumns,
		Hostname:        os.Hostname,
		Platform:        clientPlatform(runtime.GOOS, runtime.GOARCH),
	}
}

type deviceLoginStart struct {
	LoginToken              string   `json:"login_token"`
	DeviceCode              string   `json:"device_code"`
	UserCode                string   `json:"user_code"`
	VerificationURIComplete string   `json:"verification_uri_complete"`
	VerificationQRCode      *string  `json:"verification_qr_code"`
	ExpiresIn               *float64 `json:"expires_in"`
	Interval                *float64 `json:"interval"`
}

type deviceLoginGrant struct {
	AccessToken string
	AccountName string
}

type loginClientInfo struct {
	ClientVersion string `json:"client_version"`
	Platform      string `json:"platform,omitempty"`
	Hostname      string `json:"hostname,omitempty"`
}

func newLoginCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Authenticate the CLI with your Prefactor account",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := app.loadProfiles()
			if err != nil {
				return err
			}

			baseURL := profile.DefaultBaseURL
			if existing, ok := store.Get(profile.DefaultName); ok {
				baseURL = existing.BaseURL
			}

			deps := app.Login
			// Instructions go to stderr in JSON mode, so stdout holds only
			// the result.
			info := cmd.OutOrStdout()
			if app.jsonOutput() {
				info = cmd.ErrOrStderr()
			}

			started, err := startDeviceLogin(cmd.Context(), baseURL, deps, app.Version)
			if err != nil {
				return err
			}

			var grant deviceLoginGrant
			if started == nil {
				if !app.interactive() {
					return errors.New("Cannot ask for an API token because the CLI is not interactive. Run 'prefactor profiles add default <baseUrl> --api-token <apiToken>' instead.")
				}
				grant, err = loginByPastingToken(info, baseURL, deps)
			} else {
				grant, err = loginWithDeviceCode(cmd.Context(), info, baseURL, *started, deps)
			}
			if err != nil {
				return err
			}

			if err := store.Set(profile.DefaultName, profile.Profile{APIKey: grant.AccessToken, BaseURL: baseURL}); err != nil {
				return err
			}

			if app.jsonOutput() {
				return app.printJSONValue(cmd, loginResult{
					Profile:     profile.DefaultName,
					BaseURL:     baseURL,
					AccountName: grant.AccountName,
				})
			}

			if grant.AccountName != "" {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Authentication successful. Signed in to %s. Credentials saved to the '%s' profile.\n", grant.AccountName, profile.DefaultName)
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Authentication successful. Credentials saved to the '%s' profile.\n", profile.DefaultName)
			return err
		},
	}
}

// loginResult is the login --json output. It never includes the token.
type loginResult struct {
	Profile     string `json:"profile"`
	BaseURL     string `json:"base_url"`
	AccountName string `json:"account_name,omitempty"`
}

func loginByPastingToken(out io.Writer, baseURL string, deps LoginDeps) (deviceLoginGrant, error) {
	loginURL := stripTrailingSlashes(baseURL) + "/cli/connect"

	fmt.Fprintf(out, "Opening your browser to: %s\n", loginURL)
	fmt.Fprintln(out, "If the browser does not open automatically, visit the URL above.")
	deps.OpenBrowser(loginURL)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "After authenticating, copy your API token from the browser.")

	token, err := deps.PromptForToken("Paste your API token here: ")
	if err != nil {
		return deviceLoginGrant{}, err
	}
	if strings.TrimSpace(token) == "" {
		return deviceLoginGrant{}, errors.New("No API token provided. Please paste a valid token.")
	}

	return deviceLoginGrant{AccessToken: token}, nil
}

func loginWithDeviceCode(
	ctx context.Context,
	out io.Writer,
	baseURL string,
	started deviceLoginStart,
	deps LoginDeps,
) (deviceLoginGrant, error) {
	columns, known := deps.TerminalColumns()
	showQRCode := known && qrCodeFits(*started.VerificationQRCode, columns)
	if showQRCode {
		fmt.Fprintln(out, *started.VerificationQRCode)
		fmt.Fprintln(out)
	}

	fmt.Fprintf(out, "Opening your browser to approve this login: %s\n", started.VerificationURIComplete)
	if showQRCode {
		fmt.Fprintln(out, "If the browser does not open automatically, visit the URL above or scan the QR code on another device.")
	} else {
		fmt.Fprintln(out, "If the browser does not open automatically, visit the URL above.")
	}
	deps.OpenBrowser(started.VerificationURIComplete)
	fmt.Fprintln(out)
	fmt.Fprintf(out, "Confirm this code in the browser: %s\n", started.UserCode)
	fmt.Fprintln(out, "Waiting for approval.")

	return pollForDeviceGrant(ctx, baseURL, started, deps)
}

// startDeviceLogin returns nil when the server has no device login endpoint.
func startDeviceLogin(ctx context.Context, baseURL string, deps LoginDeps, version string) (*deviceLoginStart, error) {
	origin := stripTrailingSlashes(baseURL)

	payload, err := json.Marshal(resolveClientInfo(deps, version))
	if err != nil {
		return nil, err
	}

	resp, err := postJSON(ctx, deps.HTTPClient, origin+"/cli/login/start", payload)
	if err != nil {
		return nil, fmt.Errorf("Could not reach %s to start login. %w", origin, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New(startFailureMessage(errorCode(body), resp.StatusCode))
	}

	var started deviceLoginStart
	if err := json.Unmarshal(body, &started); err != nil || !validDeviceLoginStart(started) {
		return nil, errors.New("Login start returned an unexpected response.")
	}

	return &started, nil
}

func pollForDeviceGrant(ctx context.Context, baseURL string, started deviceLoginStart, deps LoginDeps) (deviceLoginGrant, error) {
	origin := stripTrailingSlashes(baseURL)
	deadline := deps.Now().Add(time.Duration(*started.ExpiresIn * float64(time.Second)))
	interval := time.Duration(max(1, *started.Interval) * float64(time.Second))
	// A rate-limited poll waits for retry-after instead of the interval, once.
	wait := interval

	payload, err := json.Marshal(map[string]string{
		"login_token": started.LoginToken,
		"device_code": started.DeviceCode,
	})
	if err != nil {
		return deviceLoginGrant{}, err
	}

	for deps.Now().Before(deadline) {
		if err := deps.Sleep(ctx, wait); err != nil {
			return deviceLoginGrant{}, err
		}
		wait = interval
		if !deps.Now().Before(deadline) {
			break
		}

		resp, err := postJSON(ctx, deps.HTTPClient, origin+"/cli/login/token", payload)
		if err != nil {
			return deviceLoginGrant{}, fmt.Errorf("Could not reach %s while waiting for approval. %w", origin, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return readDeviceGrant(body)
		}

		code := errorCode(body)
		switch {
		case resp.StatusCode == http.StatusBadRequest && code == "authorization_pending":
			continue
		case resp.StatusCode == http.StatusBadRequest && code == "slow_down":
			interval += slowDownExtra
			wait = interval
			continue
		case resp.StatusCode == http.StatusBadRequest && code == "access_denied":
			return deviceLoginGrant{}, errors.New("The login was cancelled in the browser.")
		case resp.StatusCode == http.StatusBadRequest && code == "expired_token":
			return deviceLoginGrant{}, errors.New("Login expired or was already used. Run prefactor login again.")
		case resp.StatusCode == http.StatusTooManyRequests || code == "rate_limited":
			now := deps.Now()
			wait = min(retryAfter(resp.Header.Get("Retry-After"), interval, now), max(0, deadline.Sub(now)))
			continue
		}

		return deviceLoginGrant{}, errors.New(tokenFailureMessage(code, resp.StatusCode))
	}

	return deviceLoginGrant{}, errors.New("Login expired. Run prefactor login again.")
}

func readDeviceGrant(body []byte) (deviceLoginGrant, error) {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return deviceLoginGrant{}, errors.New("Login approval returned an unexpected response.")
	}

	accessToken, _ := decoded["access_token"].(string)
	if accessToken == "" {
		return deviceLoginGrant{}, errors.New("Login approval returned an unexpected response.")
	}

	accountName, _ := decoded["account_name"].(string)
	return deviceLoginGrant{AccessToken: accessToken, AccountName: accountName}, nil
}

func validDeviceLoginStart(s deviceLoginStart) bool {
	return s.LoginToken != "" &&
		s.DeviceCode != "" &&
		s.UserCode != "" &&
		s.VerificationURIComplete != "" &&
		s.VerificationQRCode != nil &&
		s.ExpiresIn != nil &&
		s.Interval != nil
}

func resolveClientInfo(deps LoginDeps, version string) loginClientInfo {
	info := loginClientInfo{ClientVersion: truncateClientInfo(version)}
	if deps.Platform != "" {
		info.Platform = truncateClientInfo(deps.Platform)
	}
	if host, err := deps.Hostname(); err == nil && strings.TrimSpace(host) != "" {
		info.Hostname = truncateClientInfo(strings.TrimSpace(host))
	}
	return info
}

// truncateClientInfo keeps the first 64 UTF-16 code units, as String.slice
// does in the TypeScript CLI, without splitting a character.
func truncateClientInfo(value string) string {
	units := 0
	for i, r := range value {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if units+width > maxClientInfoLength {
			return value[:i]
		}
		units += width
	}
	return value
}

// clientPlatform names the platform the way the TypeScript CLI does, for
// example darwin-arm64 or linux-x64.
func clientPlatform(goos, goarch string) string {
	platform := goos
	arch := goarch
	if goarch == "amd64" {
		arch = "x64"
	}

	switch platform {
	case "darwin", "linux", "windows":
	default:
		return ""
	}
	switch arch {
	case "x64", "arm64":
	default:
		return ""
	}

	return platform + "-" + arch
}

// qrCodeFits reports whether every line of the QR code fits in the terminal.
// A wrapped QR code cannot be scanned.
func qrCodeFits(qrCode string, columns int) bool {
	width := 0
	for _, line := range strings.Split(qrCode, "\n") {
		width = max(width, utf8.RuneCountInString(line))
	}
	return width <= columns
}

func errorCode(body []byte) string {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ""
	}
	code, _ := decoded["error"].(string)
	return code
}

func startFailureMessage(code string, status int) string {
	if code == "rate_limited" || status == http.StatusTooManyRequests {
		return "Login is rate limited. Wait and run prefactor login again."
	}
	if code != "" {
		return fmt.Sprintf("Login could not start (%s).", code)
	}
	return fmt.Sprintf("Login could not start (HTTP %d).", status)
}

func tokenFailureMessage(code string, status int) string {
	if code != "" {
		return fmt.Sprintf("Login failed (%s).", code)
	}
	return fmt.Sprintf("Login failed (HTTP %d).", status)
}

// retryAfter reads Retry-After as seconds or an HTTP date, and never waits
// less than fallback.
func retryAfter(header string, fallback time.Duration, now time.Time) time.Duration {
	if header == "" {
		return fallback
	}

	if seconds, err := strconv.ParseFloat(header, 64); err == nil && seconds >= 0 {
		return max(fallback, time.Duration(seconds*float64(time.Second)))
	}

	if date, err := http.ParseTime(header); err == nil {
		return max(fallback, date.Sub(now))
	}

	return fallback
}

func postJSON(ctx context.Context, httpClient *http.Client, target string, payload []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	return httpClient.Do(req)
}

func stripTrailingSlashes(baseURL string) string {
	return strings.TrimRight(baseURL, "/")
}

// browserOpenCommand returns the command that opens url in the default
// browser, or nil for anything other than an http or https URL.
func browserOpenCommand(goos, target string) []string {
	parsed, err := url.Parse(target)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil
	}

	switch goos {
	case "darwin":
		return []string{"open", target}
	case "windows":
		// rundll32 takes the URL as an argument. cmd.exe would interpret it as a shell command.
		return []string{"rundll32", "url.dll,FileProtocolHandler", target}
	default:
		return []string{"xdg-open", target}
	}
}

// openBrowser starts the browser and does not wait. A failure to start it is
// ignored, because the URL is always printed for the user to open by hand.
func openBrowser(target string) {
	args := browserOpenCommand(runtime.GOOS, target)
	if args == nil {
		return
	}

	cmd := exec.Command(args[0], args[1:]...)
	if err := cmd.Start(); err != nil {
		return
	}
	go func() { _ = cmd.Wait() }()
}

// promptForToken reads a token without echoing it when stdin is a terminal.
func promptForToken(prompt string) (string, error) {
	fmt.Fprint(os.Stdout, prompt)

	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		token, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stdout)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(token)), nil
	}

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Fprintln(os.Stdout)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func terminalColumns() (int, bool) {
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return 0, false
	}

	width, _, err := term.GetSize(fd)
	if err != nil {
		return 0, false
	}
	return width, true
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
