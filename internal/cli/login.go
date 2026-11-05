package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/prefactordev/cli/internal/js"
	"github.com/prefactordev/cli/internal/jsjson"
	"github.com/prefactordev/cli/internal/lifecycle"
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
	PromptForToken  func(out io.Writer, prompt string) (string, error)
	Sleep           func(ctx context.Context, d time.Duration) error
	Now             func() time.Time
	TerminalColumns func(out io.Writer) (int, bool)
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
		Platform: clientPlatform(func() (lifecycle.Platform, error) {
			return lifecycle.DetectPlatform(os.Getenv)
		}),
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

	token, err := deps.PromptForToken(out, "Paste your API token here: ")
	if err != nil {
		return deviceLoginGrant{}, err
	}
	if js.Trim(token) == "" {
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
	columns, known := deps.TerminalColumns(out)
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

	started, ok := readDeviceLoginStart(body)
	if !ok {
		return nil, errors.New("Login start returned an unexpected response.")
	}

	return &started, nil
}

// readDeviceLoginStart reads a login start response as the TypeScript
// CLI's isDeviceLoginStart checks it: exact keys, non-empty strings, a
// string QR code and finite numbers.
func readDeviceLoginStart(body []byte) (deviceLoginStart, bool) {
	parsed := parseJSON(body)
	if _, isObject := parsed.(*jsjson.Object); !isObject {
		return deviceLoginStart{}, false
	}
	text := func(key string) string {
		s, _ := property(parsed, key).(string)
		return s
	}
	number := func(key string) *float64 {
		n, ok := property(parsed, key).(jsjson.Number)
		if !ok || math.IsInf(float64(n), 0) || math.IsNaN(float64(n)) {
			return nil
		}
		f := float64(n)
		return &f
	}
	started := deviceLoginStart{
		LoginToken:              text("login_token"),
		DeviceCode:              text("device_code"),
		UserCode:                text("user_code"),
		VerificationURIComplete: text("verification_uri_complete"),
		ExpiresIn:               number("expires_in"),
		Interval:                number("interval"),
	}
	if qr, ok := property(parsed, "verification_qr_code").(string); ok {
		started.VerificationQRCode = &qr
	}
	return started, validDeviceLoginStart(started)
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
	if host, err := deps.Hostname(); err == nil && js.Trim(host) != "" {
		info.Hostname = truncateClientInfo(js.Trim(host))
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

// clientPlatform names the platform as the TypeScript CLI does, from the
// same detection the installer uses, so an x64 build under Rosetta reports
// darwin-arm64. It is empty when the platform is not one Prefactor builds
// for.
func clientPlatform(detect func() (lifecycle.Platform, error)) string {
	p, err := detect()
	if err != nil {
		return ""
	}
	return p.OS + "-" + p.Arch
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

	// As Number() and Date.parse read it in the TypeScript CLI.
	if seconds := js.Number(header); !math.IsInf(seconds, 0) && seconds >= 0 {
		return max(fallback, time.Duration(seconds*float64(time.Second)))
	}

	if date, ok := parseDate(header); ok {
		return max(fallback, date.Sub(now))
	}

	return fallback
}

// parseDate reads the date forms Date.parse accepts that a Retry-After
// header may hold: HTTP dates and ISO 8601 dates, which without a time zone
// are UTC for a date alone and local time otherwise.
func parseDate(value string) (time.Time, bool) {
	value = js.Trim(value)
	if date, err := http.ParseTime(value); err == nil {
		return date, true
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04Z07:00"} {
		if date, err := time.Parse(layout, value); err == nil {
			return date, true
		}
	}
	if date, err := time.Parse("2006-01-02", value); err == nil {
		return date, true
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04"} {
		if date, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return date, true
		}
	}
	return time.Time{}, false
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

// promptForToken reads a token the way the TypeScript CLI does: the prompt
// goes to out, a terminal is put in raw mode so nothing is echoed, Enter
// finishes, Backspace deletes and Ctrl+C restores the terminal and exits 1.
// Any signal that ends the CLI while it waits also restores the terminal.
func promptForToken(out io.Writer, prompt string) (string, error) {
	fmt.Fprint(out, prompt)

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		fmt.Fprintln(out)
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return js.Trim(line), nil
	}

	state, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	restore := func() { _ = term.Restore(fd, state) }

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	defer signal.Stop(signals)
	go func() {
		if _, ok := <-signals; ok {
			restore()
			fmt.Fprintln(out)
			os.Exit(1)
		}
	}()

	var input []rune
	reader := bufio.NewReader(os.Stdin)
	for {
		r, _, err := reader.ReadRune()
		if err != nil {
			restore()
			fmt.Fprintln(out)
			if errors.Is(err, io.EOF) {
				return js.Trim(string(input)), nil
			}
			return "", err
		}

		switch r {
		case '\r', '\n':
			restore()
			fmt.Fprintln(out)
			return js.Trim(string(input)), nil
		case 3: // Ctrl+C
			restore()
			fmt.Fprintln(out)
			return "", &exitError{code: 1}
		case 127, 8: // Backspace
			if len(input) > 0 {
				input = input[:len(input)-1]
			}
		default:
			input = append(input, r)
		}
	}
}

// terminalColumns returns the width of the terminal out writes to.
func terminalColumns(out io.Writer) (int, bool) {
	file, ok := out.(*os.File)
	if !ok {
		return 0, false
	}
	fd := int(file.Fd())
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
