package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prefactordev/cli/internal/lifecycle"
	"github.com/prefactordev/cli/internal/profile"
)

const testQRCode = "██ ██\n█ █ █"

type tokenReply struct {
	status     int
	body       string
	retryAfter string
}

// deviceServer fakes /cli/login/start and /cli/login/token.
type deviceServer struct {
	*httptest.Server
	startStatus   int
	startBody     string
	tokenReplies  []tokenReply
	startPayload  map[string]any
	tokenPayloads []map[string]any
}

func newDeviceServer(t *testing.T, replies ...tokenReply) *deviceServer {
	t.Helper()
	ds := &deviceServer{
		startStatus: http.StatusOK,
		startBody: `{"login_token":"lt","device_code":"dc","user_code":"ABCD-EFGH",` +
			`"verification_uri":"https://app.example/cli/connect",` +
			`"verification_uri_complete":"https://app.example/cli/connect?code=ABCD-EFGH",` +
			`"verification_qr_code":"` + strings.ReplaceAll(testQRCode, "\n", `\n`) + `","expires_in":600,"interval":5}`,
		tokenReplies: replies,
	}
	ds.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(raw, &payload)

		switch r.URL.Path {
		case "/cli/login/start":
			ds.startPayload = payload
			w.WriteHeader(ds.startStatus)
			_, _ = io.WriteString(w, ds.startBody)
		case "/cli/login/token":
			ds.tokenPayloads = append(ds.tokenPayloads, payload)
			reply := ds.tokenReplies[0]
			if len(ds.tokenReplies) > 1 {
				ds.tokenReplies = ds.tokenReplies[1:]
			}
			if reply.retryAfter != "" {
				w.Header().Set("Retry-After", reply.retryAfter)
			}
			w.WriteHeader(reply.status)
			_, _ = io.WriteString(w, reply.body)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(ds.Close)
	return ds
}

func pending() tokenReply {
	return tokenReply{status: http.StatusBadRequest, body: `{"error":"authorization_pending"}`}
}

func approved() tokenReply {
	return tokenReply{status: http.StatusOK, body: `{"access_token":"granted","account_id":"acc","account_name":"Acme"}`}
}

type loginHarness struct {
	*testApp
	clock    *fakeClock
	opened   []string
	columns  int
	prompts  []string
	pasted   string
	measured []io.Writer
}

func newLoginHarness(t *testing.T, server *deviceServer) *loginHarness {
	t.Helper()
	h := &loginHarness{testApp: newTestApp(t), clock: &fakeClock{now: time.Unix(1_700_000_000, 0)}, columns: 120}
	h.addProfile(t, profile.DefaultName, "old", server.URL)
	h.Login = LoginDeps{
		HTTPClient:      server.Client(),
		OpenBrowser:     func(url string) { h.opened = append(h.opened, url) },
		PromptForToken:  func(_ io.Writer, p string) (string, error) { h.prompts = append(h.prompts, p); return h.pasted, nil },
		Sleep:           h.clock.Sleep,
		Now:             h.clock.Now,
		TerminalColumns: func(out io.Writer) (int, bool) { h.measured = append(h.measured, out); return h.columns, h.columns > 0 },
		Hostname:        func() (string, error) { return "dev-box", nil },
		Platform:        "darwin-arm64",
	}
	return h
}

func (h *loginHarness) savedToken(t *testing.T) string {
	t.Helper()
	p, _ := profile.Load(h.configPath).Get(profile.DefaultName)
	return p.APIKey
}

func TestLoginDeviceFlowSavesTheGrantedToken(t *testing.T) {
	server := newDeviceServer(t, pending(), pending(), approved())
	h := newLoginHarness(t, server)

	out, err := h.run(t, "login")
	if err != nil {
		t.Fatal(err)
	}

	want := testQRCode + "\n\n" +
		"Opening your browser to approve this login: https://app.example/cli/connect?code=ABCD-EFGH\n" +
		"If the browser does not open automatically, visit the URL above or scan the QR code on another device.\n\n" +
		"Confirm this code in the browser: ABCD-EFGH\n" +
		"Waiting for approval.\n" +
		"Authentication successful. Signed in to Acme. Credentials saved to the 'default' profile.\n"
	if out != want {
		t.Fatalf("out =\n%s\nwant\n%s", out, want)
	}

	if got := h.savedToken(t); got != "granted" {
		t.Fatalf("saved token = %q", got)
	}
	if len(h.opened) != 1 || h.opened[0] != "https://app.example/cli/connect?code=ABCD-EFGH" {
		t.Fatalf("opened = %v", h.opened)
	}
	if len(server.tokenPayloads) != 3 || server.tokenPayloads[0]["login_token"] != "lt" || server.tokenPayloads[0]["device_code"] != "dc" {
		t.Fatalf("token payloads = %v", server.tokenPayloads)
	}
	for _, d := range h.clock.sleeps {
		if d != 5*time.Second {
			t.Fatalf("sleeps = %v, want 5s each", h.clock.sleeps)
		}
	}
	wantInfo := map[string]any{"client_version": "1.2.3", "platform": "darwin-arm64", "hostname": "dev-box"}
	if len(server.startPayload) != 3 {
		t.Fatalf("start payload = %v", server.startPayload)
	}
	for k, v := range wantInfo {
		if server.startPayload[k] != v {
			t.Fatalf("start payload = %v, want %v", server.startPayload, wantInfo)
		}
	}
}

func TestLoginHidesTheQRCodeWhenItDoesNotFit(t *testing.T) {
	for name, columns := range map[string]int{"narrow": 4, "not a terminal": 0} {
		t.Run(name, func(t *testing.T) {
			h := newLoginHarness(t, newDeviceServer(t, approved()))
			h.columns = columns

			out, err := h.run(t, "login")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, "█") || strings.Contains(out, "scan the QR code") {
				t.Fatalf("QR code shown:\n%s", out)
			}
			if !strings.Contains(out, "If the browser does not open automatically, visit the URL above.\n") {
				t.Fatalf("out = %s", out)
			}
		})
	}
}

func TestLoginSlowDownAddsFiveSecondsToTheInterval(t *testing.T) {
	h := newLoginHarness(t, newDeviceServer(t,
		tokenReply{status: http.StatusBadRequest, body: `{"error":"slow_down"}`},
		pending(),
		approved(),
	))

	if _, err := h.run(t, "login"); err != nil {
		t.Fatal(err)
	}

	want := []time.Duration{5 * time.Second, 10 * time.Second, 10 * time.Second}
	if len(h.clock.sleeps) != 3 || h.clock.sleeps[0] != want[0] || h.clock.sleeps[1] != want[1] || h.clock.sleeps[2] != want[2] {
		t.Fatalf("sleeps = %v, want %v", h.clock.sleeps, want)
	}
}

func TestLoginRateLimitWaitsForRetryAfterOnce(t *testing.T) {
	h := newLoginHarness(t, newDeviceServer(t,
		tokenReply{status: http.StatusTooManyRequests, body: `{"error":"rate_limited"}`, retryAfter: "30"},
		approved(),
	))

	if _, err := h.run(t, "login"); err != nil {
		t.Fatal(err)
	}

	if len(h.clock.sleeps) != 2 || h.clock.sleeps[0] != 5*time.Second || h.clock.sleeps[1] != 30*time.Second {
		t.Fatalf("sleeps = %v, want [5s 30s]", h.clock.sleeps)
	}
}

func TestLoginFailures(t *testing.T) {
	cases := map[string]struct {
		reply tokenReply
		want  string
	}{
		"denied":   {tokenReply{status: http.StatusBadRequest, body: `{"error":"access_denied"}`}, "The login was cancelled in the browser."},
		"expired":  {tokenReply{status: http.StatusBadRequest, body: `{"error":"expired_token"}`}, "Login expired or was already used. Run prefactor login again."},
		"unknown":  {tokenReply{status: http.StatusBadRequest, body: `{"error":"invalid_grant"}`}, "Login failed (invalid_grant)."},
		"server":   {tokenReply{status: http.StatusInternalServerError, body: ``}, "Login failed (HTTP 500)."},
		"no token": {tokenReply{status: http.StatusOK, body: `{"account_name":"Acme"}`}, "Login approval returned an unexpected response."},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newLoginHarness(t, newDeviceServer(t, tc.reply))
			_, err := h.run(t, "login")
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if got := h.savedToken(t); got != "old" {
				t.Fatalf("saved token changed to %q", got)
			}
		})
	}
}

func TestLoginStopsAtTheDeadline(t *testing.T) {
	server := newDeviceServer(t, pending())
	server.startBody = strings.Replace(server.startBody, `"expires_in":600`, `"expires_in":12`, 1)
	h := newLoginHarness(t, server)

	_, err := h.run(t, "login")
	if err == nil || err.Error() != "Login expired. Run prefactor login again." {
		t.Fatalf("err = %v", err)
	}
	if len(server.tokenPayloads) != 2 {
		t.Fatalf("polls = %d, want 2 within 12s at a 5s interval", len(server.tokenPayloads))
	}
}

func TestLoginStartFailures(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"rate limited": {http.StatusTooManyRequests, ``, "Login is rate limited. Wait and run prefactor login again."},
		"error code":   {http.StatusBadRequest, `{"error":"invalid_request"}`, "Login could not start (invalid_request)."},
		"status only":  {http.StatusBadGateway, ``, "Login could not start (HTTP 502)."},
		"bad body":     {http.StatusOK, `{"device_code":"dc"}`, "Login start returned an unexpected response."},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server := newDeviceServer(t, approved())
			server.startStatus, server.startBody = tc.status, tc.body
			_, err := newLoginHarness(t, server).run(t, "login")
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoginFallsBackToPastingATokenWhenDeviceLoginIsMissing(t *testing.T) {
	server := newDeviceServer(t, approved())
	server.startStatus, server.startBody = http.StatusNotFound, ``
	h := newLoginHarness(t, server)
	h.terminal = true
	h.pasted = "pasted-token"

	out, err := h.run(t, "login")
	if err != nil {
		t.Fatal(err)
	}

	want := "Opening your browser to: " + server.URL + "/cli/connect\n" +
		"If the browser does not open automatically, visit the URL above.\n\n" +
		"After authenticating, copy your API token from the browser.\n" +
		"Authentication successful. Credentials saved to the 'default' profile.\n"
	if out != want {
		t.Fatalf("out =\n%s\nwant\n%s", out, want)
	}
	if len(h.prompts) != 1 || h.prompts[0] != "Paste your API token here: " {
		t.Fatalf("prompts = %v", h.prompts)
	}
	if got := h.savedToken(t); got != "pasted-token" {
		t.Fatalf("saved token = %q", got)
	}

	h.pasted = "   "
	if _, err := h.run(t, "login"); err == nil || err.Error() != "No API token provided. Please paste a valid token." {
		t.Fatalf("blank paste err = %v", err)
	}
}

func TestLoginUsesTheDefaultBaseURLWithoutAProfile(t *testing.T) {
	ta := newTestApp(t)
	var started string
	ta.Login = LoginDeps{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			started = r.URL.String()
			return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{"error":"x"}`)), Header: http.Header{}}, nil
		})},
		Hostname: func() (string, error) { return "", nil },
	}

	_, _ = ta.run(t, "login")
	if started != "https://app.prefactorai.com/cli/login/start" {
		t.Fatalf("start URL = %q", started)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClientPlatformMatchesTheTypeScriptNames(t *testing.T) {
	detected := func(p lifecycle.Platform, err error) func() (lifecycle.Platform, error) {
		return func() (lifecycle.Platform, error) { return p, err }
	}
	if got := clientPlatform(detected(lifecycle.Platform{OS: "linux", Arch: "x64", Libc: "musl"}, nil)); got != "linux-x64" {
		t.Errorf("linux = %q", got)
	}
	// An x64 build under Rosetta reports the native architecture.
	if got := clientPlatform(detected(lifecycle.Platform{OS: "darwin", Arch: "arm64", Rosetta: true}, nil)); got != "darwin-arm64" {
		t.Errorf("rosetta = %q", got)
	}
	if got := clientPlatform(detected(lifecycle.Platform{}, errors.New("Unsupported platform: freebsd"))); got != "" {
		t.Errorf("unsupported = %q", got)
	}
}

func TestTruncateClientInfoCountsUTF16Units(t *testing.T) {
	if got := truncateClientInfo(strings.Repeat("a", 70)); len(got) != 64 {
		t.Fatalf("ascii length = %d", len(got))
	}
	// Each emoji is two UTF-16 units, so 32 fit.
	if got := truncateClientInfo(strings.Repeat("😀", 40)); got != strings.Repeat("😀", 32) {
		t.Fatalf("emoji truncation kept %d runes", len([]rune(got)))
	}
}

func TestBrowserOpenCommand(t *testing.T) {
	if got := browserOpenCommand("darwin", "https://x.example"); strings.Join(got, " ") != "open https://x.example" {
		t.Errorf("darwin = %v", got)
	}
	if got := browserOpenCommand("windows", "https://x.example"); strings.Join(got, " ") != "rundll32 url.dll,FileProtocolHandler https://x.example" {
		t.Errorf("windows = %v", got)
	}
	if got := browserOpenCommand("linux", "https://x.example"); strings.Join(got, " ") != "xdg-open https://x.example" {
		t.Errorf("linux = %v", got)
	}
	if got := browserOpenCommand("darwin", "file:///etc/passwd"); got != nil {
		t.Errorf("file URL = %v, want nil", got)
	}
}

func TestRetryAfterReadsTheHeaderAsTheTypeScriptCLIDoes(t *testing.T) {
	now := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	fallback := 2 * time.Second
	for header, want := range map[string]time.Duration{
		"":                              fallback,
		" 5 ":                           5 * time.Second,
		"0x10":                          16 * time.Second,
		"1":                             fallback,
		"soon":                          fallback,
		"2026-11-05T00:00:30Z":          30 * time.Second,
		"Thu, 05 Nov 2026 00:01:00 GMT": time.Minute,
	} {
		if got := retryAfter(header, fallback, now); got != want {
			t.Errorf("retryAfter(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestLoginStartKeysMatchExactly(t *testing.T) {
	valid := `{"login_token":"l","device_code":"d","user_code":"U","verification_uri_complete":"https://x/cli/connect","verification_qr_code":"","expires_in":600,"interval":5}`
	if _, ok := readDeviceLoginStart([]byte(valid)); !ok {
		t.Fatal("valid start rejected")
	}
	for _, body := range []string{
		strings.Replace(valid, `"login_token"`, `"LOGIN_TOKEN"`, 1),
		strings.Replace(valid, `"interval":5`, `"interval":"5"`, 1),
		strings.Replace(valid, `"verification_qr_code":""`, `"verification_qr_code":null`, 1),
		`[]`,
	} {
		if _, ok := readDeviceLoginStart([]byte(body)); ok {
			t.Errorf("accepted %s", body)
		}
	}
}
