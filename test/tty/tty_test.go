//go:build !windows

// Package tty runs the CLI in a pseudo-terminal, which unit tests cannot do:
// start-up behaviour, the token prompt and the real picker.
package tty

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tty-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// TTY_TEST_BINARY runs the tests against another build, for example an
	// older commit, to check they catch a regression.
	binary = os.Getenv("TTY_TEST_BINARY")
	if binary == "" {
		binary = filepath.Join(dir, "prefactor")
		build := exec.Command("go", "build", "-o", binary, "../../cmd/prefactor")
		build.Stdout, build.Stderr = os.Stdout, os.Stderr
		if err := build.Run(); err != nil {
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// session is the CLI running in a pseudo-terminal that answers nothing, like
// a terminal without query support.
type session struct {
	t    *testing.T
	cmd  *exec.Cmd
	work string
	tty  *os.File
	// slave stays open so the terminal's settings can be read after exit.
	slave *os.File
	mu    sync.Mutex
	out   bytes.Buffer
	done  chan struct{}
}

// start runs the CLI in a work directory with a default profile for
// serverURL. stdin, stdout and stderr are all the terminal unless stderr is
// given.
func start(t *testing.T, serverURL string, stderr io.Writer, args ...string) *session {
	t.Helper()

	work := t.TempDir()
	if err := os.Mkdir(filepath.Join(work, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	profile := fmt.Sprintf(`{"default":{"api_key":"tok","base_url":%q}}`, serverURL)
	if err := os.WriteFile(filepath.Join(work, "prefactor.json"), []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binary, args...)
	cmd.Dir = work
	cmd.Env = cleanEnv()

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120}); err != nil {
		t.Fatal(err)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	// Make the terminal the controlling terminal of a new session, as a
	// shell does, so the CLI runs in the terminal's foreground.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if stderr != nil {
		cmd.Stderr = stderr
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	s := &session{t: t, cmd: cmd, work: work, tty: ptmx, slave: tty, done: make(chan struct{})}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			s.mu.Lock()
			s.out.Write(buf[:n])
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	go func() {
		_ = cmd.Wait()
		close(s.done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = ptmx.Close()
		_ = tty.Close()
	})
	return s
}

// cleanEnv drops coding agent and Prefactor variables, so the CLI sees a
// person at a terminal.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "CLAUDECODE", "CURSOR_AGENT", "GEMINI_CLI", "CODEX_SANDBOX", "CODEX_CI", "AGENT", "AI_AGENT":
			continue
		}
		if strings.HasPrefix(name, "PREFACTOR_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "TERM=xterm-256color")
}

func (s *session) output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.String()
}

// waitFor waits until the terminal shows text.
func (s *session) waitFor(text string) {
	s.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(s.output(), text) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.t.Fatalf("terminal never showed %q; it showed:\n%q", text, s.output())
}

func (s *session) send(keys string) {
	s.t.Helper()
	if _, err := s.tty.WriteString(keys); err != nil {
		s.t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
}

// wait waits for the CLI to exit and returns its exit code.
func (s *session) wait(limit time.Duration) int {
	s.t.Helper()
	select {
	case <-s.done:
		time.Sleep(50 * time.Millisecond)
		return s.cmd.ProcessState.ExitCode()
	case <-time.After(limit):
		s.t.Fatalf("CLI still running after %v; terminal showed:\n%q", limit, s.output())
		return -1
	}
}

func jsonServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"not_found"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

// TestCommandsDoNotQueryTheTerminal fails if the CLI writes escape sequences
// or waits for terminal replies on a run that shows no picker. Terminals that
// do not answer queries would otherwise stall every command.
func TestCommandsDoNotQueryTheTerminal(t *testing.T) {
	server := jsonServer(t, map[string]string{"GET /api/v1/agent": `{"status":"success","summaries":[]}`})

	for _, args := range [][]string{{"version"}, {"agents", "list"}, {"--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			began := time.Now()
			s := start(t, server.URL, nil, args...)
			if code := s.wait(3 * time.Second); code != 0 {
				t.Fatalf("exit %d: %q", code, s.output())
			}
			if took := time.Since(began); took > 2*time.Second {
				t.Fatalf("took %v", took)
			}
			if strings.ContainsRune(s.output(), '\x1b') {
				t.Fatalf("wrote escape sequences: %q", s.output())
			}
		})
	}
}

// echoOn reports whether the terminal echoes input. It reads the settings
// through the master side, which shares them and stays usable after the
// session ends.
func (s *session) echoOn() bool {
	s.t.Helper()
	settings, err := unix.IoctlGetTermios(int(s.tty.Fd()), ioctlGetTermios)
	if err != nil {
		s.t.Fatal(err)
	}
	return settings.Lflag&unix.ECHO != 0
}

func (s *session) savedToken() string {
	raw, err := os.ReadFile(filepath.Join(s.work, "prefactor.json"))
	if err != nil {
		s.t.Fatal(err)
	}
	return string(raw)
}

// A server without the device login endpoints makes login ask for a token.
var noDeviceLogin = map[string]string{}

func TestTokenPromptSavesTheTypedToken(t *testing.T) {
	server := jsonServer(t, noDeviceLogin)
	s := start(t, server.URL, nil, "login")

	s.waitFor("Paste your API token here: ")
	s.send("secret-token\r")
	if code := s.wait(5 * time.Second); code != 0 {
		t.Fatalf("exit %d: %q", code, s.output())
	}
	if strings.Contains(s.output(), "secret-token") {
		t.Fatalf("the token was echoed: %q", s.output())
	}
	if !strings.Contains(s.savedToken(), `"api_key": "secret-token"`) {
		t.Fatalf("profile = %s", s.savedToken())
	}
	if !s.echoOn() {
		t.Fatal("echo is off after login")
	}
}

func TestCtrlCAtTheTokenPromptRestoresTheTerminal(t *testing.T) {
	server := jsonServer(t, noDeviceLogin)
	s := start(t, server.URL, nil, "login")

	s.waitFor("Paste your API token here: ")
	s.send("ab")
	s.send("\x03")
	if code := s.wait(5 * time.Second); code != 1 {
		t.Fatalf("exit %d, want 1 as in the TypeScript CLI", code)
	}
	if !s.echoOn() {
		t.Fatal("echo is still off after Ctrl+C")
	}
	if !strings.Contains(s.savedToken(), `"api_key":"tok"`) {
		t.Fatalf("profile changed: %s", s.savedToken())
	}
}

func TestLoginJSONKeepsThePromptOffStdout(t *testing.T) {
	server := jsonServer(t, noDeviceLogin)
	var stderr bytes.Buffer
	s := start(t, server.URL, &stderr, "login", "--json")

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(stderr.String(), "Paste your API token here: ") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	s.send("secret-token\r")
	if code := s.wait(5 * time.Second); code != 0 {
		t.Fatalf("exit %d: stdout %q stderr %q", code, s.output(), stderr.String())
	}

	stdout := strings.TrimSpace(strings.ReplaceAll(s.output(), "\r", ""))
	if !strings.HasPrefix(stdout, "{") || !strings.HasSuffix(stdout, "}") || strings.Contains(stdout, "Paste") {
		t.Fatalf("stdout is not only JSON: %q", stdout)
	}
	if !strings.Contains(stderr.String(), "Paste your API token here: ") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
