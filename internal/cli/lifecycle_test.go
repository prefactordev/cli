package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallVersionPinsTheRelease checks install's own --version names the
// release to pin, where the root's --version prints the CLI's version. The
// TypeScript CLI prints its version for both, so a pinned install there
// installs nothing.
func TestInstallVersionPinsTheRelease(t *testing.T) {
	ta := newTestApp(t)
	home := t.TempDir()
	ta.env["HOME"] = home
	source := filepath.Join(t.TempDir(), "prefactor")
	if err := os.WriteFile(source, []byte("build"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := ta.run(t, "install", "--version", "0.7.1", "--source-binary", source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Installed Prefactor CLI to "+filepath.Join(home, ".prefactor", "bin")) {
		t.Fatalf("out = %q", out)
	}
	raw, _ := os.ReadFile(filepath.Join(home, ".prefactor", "install.json"))
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil || state["channel"] != "pinned" || state["requestedVersion"] != "v0.7.1" {
		t.Fatalf("install.json = %s", raw)
	}

	if out, _ := ta.run(t, "install", "-V"); out != "1.2.3\n" {
		t.Fatalf("install -V = %q", out)
	}
	if out, _ := ta.run(t, "agents", "--version"); out != "1.2.3\n" {
		t.Fatalf("agents --version = %q", out)
	}
}

func TestLifecycleCommandsPrintJSON(t *testing.T) {
	ta := newTestApp(t)
	home := t.TempDir()
	ta.env["HOME"] = home
	source := filepath.Join(t.TempDir(), "prefactor")
	if err := os.WriteFile(source, []byte("build"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := ta.runWithStderr(t, "install", "--source-binary", source, "--channel", "latest", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal([]byte(out), &state); err != nil || state["resolvedTag"] != "canary" {
		t.Fatalf("stdout = %q", out)
	}
	if !strings.HasPrefix(errOut, "Installed Prefactor CLI to ") {
		t.Fatalf("stderr = %q", errOut)
	}

	out, err = ta.run(t, "doctor", "--fields", "installState,channel")
	if err != nil || out != "{\n  \"installState\": \"present\",\n  \"channel\": \"latest\"\n}\n" {
		t.Fatalf("doctor = %q, %v", out, err)
	}

	out, err = ta.run(t, "uninstall", "--json")
	if err != nil || out != "{\n  \"removed\": \""+filepath.Join(home, ".prefactor", "bin", "prefactor")+"\"\n}\n" {
		t.Fatalf("uninstall = %q, %v", out, err)
	}
}
