package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSymlinkedBinaryReadsProfilesFromTheRealInstallRoot runs the built CLI
// through a symlink, as a managed install on PATH is run, and checks it uses
// the prefactor.json next to the real binary.
func TestSymlinkedBinaryReadsProfilesFromTheRealInstallRoot(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "real", "bin", "prefactor")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	profiles := `{"installed":{"api_key":"tok","base_url":"https://installed.example"}}`
	if err := os.WriteFile(filepath.Join(dir, "real", "prefactor.json"), []byte(profiles), 0o600); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(dir, "link", "bin", "prefactor")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(binary, link); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(link, "profiles", "list")
	cmd.Dir = t.TempDir()
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PREFACTOR_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("profiles list: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "installed") {
		t.Fatalf("profiles list = %q, want the profile from the real install root", out)
	}
}
