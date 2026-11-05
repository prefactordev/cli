// Package installers runs install.sh and install.ps1 against a local
// release server: the URLs each channel downloads from, checksum checks,
// the arguments passed to the downloaded installer, PATH setup, and a real
// install of this CLI.
package installers

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var (
	installSh  = mustAbs("../../install.sh")
	installPs1 = mustAbs("../../install.ps1")
)

func mustAbs(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		panic(err)
	}
	return abs
}

// releases serves archives under /releases/download/<tag>/ and
// /releases/latest/download/, and records the paths requested.
type releases struct {
	*httptest.Server
	mu       sync.Mutex
	files    map[string][]byte
	requests []string
}

func newReleases(t *testing.T) *releases {
	t.Helper()
	r := &releases{files: map[string][]byte{}}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.requests = append(r.requests, req.URL.Path)
		body, ok := r.files[req.URL.Path]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(r.Close)
	return r
}

// publish serves archive as asset, with its checksum, under each prefix,
// such as "/releases/download/canary".
func (r *releases) publish(asset string, archive []byte, checksum string, prefixes ...string) {
	if checksum == "" {
		checksum = fmt.Sprintf("%x", sha256.Sum256(archive))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, prefix := range prefixes {
		r.files[prefix+"/"+asset] = archive
		r.files[prefix+"/SHA256SUMS"] = []byte(checksum + "  " + asset + "\n")
	}
}

func (r *releases) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = nil
}

func (r *releases) requested() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.requests, " ")
}

func (r *releases) env() []string {
	return []string{
		"PREFACTOR_RELEASE_BASE_URL=" + r.URL + "/releases/download",
		"PREFACTOR_RELEASE_LATEST_BASE_URL=" + r.URL + "/releases/latest/download",
	}
}

var allPrefixes = []string{"/releases/latest/download", "/releases/download/canary", "/releases/download/v0.0.4"}

func tarGz(t *testing.T, name string, contents []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write(contents)
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func zipped(t *testing.T, name string, contents []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(contents)
	_ = zw.Close()
	return buf.Bytes()
}

type result struct {
	code           int
	stdout, stderr string
}

func run(t *testing.T, env []string, name string, args ...string) result {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run %s: %v", name, err)
	}
	return result{code, stdout.String(), stderr.String()}
}

// linuxEnv makes install.sh act as on glibc Linux x64.
func linuxEnv(home, path string) []string {
	return []string{
		"HOME=" + home,
		"PATH=" + path,
		"SHELL=/bin/zsh",
		"PREFACTOR_INSTALL_TEST_UNAME_S=Linux",
		"PREFACTOR_INSTALL_TEST_UNAME_M=x86_64",
		"PREFACTOR_INSTALL_TEST_LIBC=glibc",
	}
}

const systemPath = "/usr/bin:/bin:/usr/sbin:/sbin"

func requireBash(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for macOS and Linux")
	}
}

func TestInstallShPrintsHelp(t *testing.T) {
	requireBash(t)
	r := run(t, os.Environ(), "bash", installSh, "--help")
	if r.code != 0 || !strings.Contains(r.stdout, "Install the Prefactor CLI from GitHub Releases.") || !strings.Contains(r.stdout, "prefactordev/cli/main/install.sh") {
		t.Fatalf("%+v", r)
	}
}

func TestInstallShFailsClearly(t *testing.T) {
	requireBash(t)
	cases := map[string][]string{
		"Either curl or wget is required.":       {"PREFACTOR_INSTALL_TEST_NO_DOWNLOADERS=1"},
		"Unsupported operating system: Plan9":    {"PREFACTOR_INSTALL_TEST_UNAME_S=Plan9"},
		"Unsupported architecture: mips":         {"PREFACTOR_INSTALL_TEST_UNAME_S=Linux", "PREFACTOR_INSTALL_TEST_UNAME_M=mips"},
		"Unsupported release selector 'nightly'": nil,
	}
	for want, env := range cases {
		args := []string{installSh}
		if strings.Contains(want, "nightly") {
			args = append(args, "nightly")
		}
		r := run(t, append(append(os.Environ(), "HOME="+t.TempDir()), env...), "bash", args...)
		if r.code == 0 || !strings.Contains(r.stderr, want) {
			t.Errorf("%s: %+v", want, r)
		}
	}
}

func TestInstallShRefusesAChecksumMismatch(t *testing.T) {
	requireBash(t)
	home := t.TempDir()
	log := filepath.Join(home, "child.log")
	server := newReleases(t)
	server.publish("prefactor-linux-x64.tar.gz", tarGz(t, "prefactor", []byte("#!/bin/sh\ntouch "+log+"\n")), strings.Repeat("0", 64), allPrefixes...)

	r := run(t, append(linuxEnv(home, systemPath), server.env()...), "bash", installSh)
	if r.code == 0 || !strings.Contains(r.stderr, "Checksum mismatch for prefactor-linux-x64.tar.gz.") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("the installer ran")
	}
}

func TestInstallShDownloadsEachChannelAndRunsTheInstaller(t *testing.T) {
	requireBash(t)
	home := t.TempDir()
	log := filepath.Join(home, "child.log")
	server := newReleases(t)
	child := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + log + "\n"
	server.publish("prefactor-linux-x64.tar.gz", tarGz(t, "prefactor", []byte(child)), "", allPrefixes...)

	cases := []struct {
		selector, request, args string
	}{
		{"stable", "/releases/latest/download/prefactor-linux-x64.tar.gz", "install --channel stable --asset-name prefactor-linux-x64.tar.gz"},
		{"latest", "/releases/download/canary/prefactor-linux-x64.tar.gz", "install --channel latest --resolved-tag canary --asset-name prefactor-linux-x64.tar.gz"},
		{"0.0.4", "/releases/download/v0.0.4/prefactor-linux-x64.tar.gz", "install --version v0.0.4 --resolved-tag v0.0.4 --asset-name prefactor-linux-x64.tar.gz"},
	}
	for _, tc := range cases {
		server.reset()
		r := run(t, append(linuxEnv(home, systemPath), server.env()...), "bash", installSh, tc.selector)
		if r.code != 0 {
			t.Fatalf("%s: %+v", tc.selector, r)
		}
		if !strings.HasPrefix(server.requested(), tc.request) {
			t.Errorf("%s requested %s", tc.selector, server.requested())
		}
		raw, _ := os.ReadFile(log)
		if got := strings.Join(strings.Fields(string(raw)), " "); got != tc.args {
			t.Errorf("%s: installer args %q, want %q", tc.selector, got, tc.args)
		}
	}
}

// TestInstallShInstallsThisCLI installs the real binary for this machine
// and checks it runs, records the install and is put on PATH once.
func TestInstallShInstallsThisCLI(t *testing.T) {
	requireBash(t)
	binary := filepath.Join(t.TempDir(), "prefactor")
	build := exec.Command("go", "build", "-ldflags", "-X main.version=0.7.0", "-o", binary, "../../cmd/prefactor")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	contents, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}

	osName := runtime.GOOS
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	asset := "prefactor-" + osName + "-" + arch + ".tar.gz"
	server := newReleases(t)
	server.publish(asset, tarGz(t, "prefactor", contents), "", "/releases/latest/download", "/releases/download/v0.7.0")

	home := t.TempDir()
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	path := localBin + ":" + systemPath
	env := append([]string{"HOME=" + home, "PATH=" + path, "SHELL=/bin/zsh", "PREFACTOR_INSTALL_TEST_ROSETTA=0", "PREFACTOR_INSTALL_TEST_LIBC=glibc"}, server.env()...)

	for range 2 {
		r := run(t, env, "bash", installSh)
		if r.code != 0 || !strings.Contains(r.stdout, "Installed Prefactor CLI to "+filepath.Join(home, ".prefactor", "bin", "prefactor")) {
			t.Fatalf("%+v", r)
		}
	}
	rc, _ := os.ReadFile(filepath.Join(home, ".zshrc"))
	if strings.Count(string(rc), ".prefactor/bin") != 1 {
		t.Fatalf(".zshrc = %q", rc)
	}

	// The link in ~/.local/bin makes prefactor runnable at once.
	r := run(t, []string{"HOME=" + home, "PATH=" + path}, filepath.Join(localBin, "prefactor"), "version")
	if r.code != 0 || r.stdout != "0.7.0\n" {
		t.Fatalf("version: %+v", r)
	}
	r = run(t, []string{"HOME=" + home, "PATH=" + path}, filepath.Join(home, ".prefactor", "bin", "prefactor"), "doctor")
	for _, want := range []string{"installState: present", "channel: stable", "resolvedTag: v0.7.0", "assetName: " + asset, "managedBinaryExists: yes"} {
		if !strings.Contains(r.stdout, want+"\n") {
			t.Errorf("doctor is missing %q:\n%s", want, r.stdout)
		}
	}
}

func TestInstallShLeavesAnExistingPathExportAlone(t *testing.T) {
	requireBash(t)
	home := t.TempDir()
	binDir := filepath.Join(home, ".prefactor", "bin")
	zshrc := filepath.Join(home, ".zshrc")
	existing := "export PATH=\"${HOME}/.prefactor/bin:$PATH\"\n"
	if err := os.WriteFile(zshrc, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	server := newReleases(t)
	child := "#!/bin/sh\nmkdir -p \"$HOME/.prefactor/bin\" && cp \"$0\" \"$HOME/.prefactor/bin/prefactor\"\n"
	server.publish("prefactor-linux-x64.tar.gz", tarGz(t, "prefactor", []byte(child)), "", allPrefixes...)

	r := run(t, append(linuxEnv(home, binDir+":"+systemPath), server.env()...), "bash", installSh)
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if rc, _ := os.ReadFile(zshrc); string(rc) != existing {
		t.Fatalf(".zshrc = %q", rc)
	}
}

func pwsh(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh is not installed")
	}
	return path
}

func runPs1(t *testing.T, env []string, args ...string) result {
	t.Helper()
	return run(t, env, pwsh(t), append([]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", installPs1}, args...)...)
}

// psEnv is this process's environment with a new home directory.
func psEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	env, _ := psHome(t, extra...)
	return env
}

func psHome(t *testing.T, extra ...string) ([]string, string) {
	t.Helper()
	home := t.TempDir()
	return append(append(os.Environ(), "USERPROFILE="+home, "HOME="+home), extra...), home
}

func TestInstallPs1PrintsHelp(t *testing.T) {
	r := runPs1(t, psEnv(t), "-Help")
	if r.code != 0 || !strings.Contains(r.stdout, "Install the Prefactor CLI from GitHub Releases.") || !strings.Contains(r.stdout, "prefactordev/cli/main/install.ps1") {
		t.Fatalf("%+v", r)
	}
}

func TestInstallPs1FailsForAnUnsupportedArchitecture(t *testing.T) {
	r := runPs1(t, psEnv(t, "PREFACTOR_INSTALL_TEST_ARCH=mips"))
	if r.code == 0 || !strings.Contains(r.stdout+r.stderr, "Unsupported architecture: mips") {
		t.Fatalf("%+v", r)
	}
}

func TestInstallPs1DownloadsEachChannelAndBuildsTheInstallerArguments(t *testing.T) {
	server := newReleases(t)
	server.publish("prefactor-windows-arm64.zip", zipped(t, "prefactor.exe", []byte("MZ")), "", allPrefixes...)
	capture := filepath.Join(t.TempDir(), "args.txt")

	cases := []struct {
		selector, request, args string
	}{
		{"stable", "/releases/latest/download/prefactor-windows-arm64.zip", "install --channel stable --asset-name prefactor-windows-arm64.zip"},
		{"latest", "/releases/download/canary/prefactor-windows-arm64.zip", "install --channel latest --resolved-tag canary --asset-name prefactor-windows-arm64.zip"},
		{"0.0.4", "/releases/download/v0.0.4/prefactor-windows-arm64.zip", "install --version v0.0.4 --resolved-tag v0.0.4 --asset-name prefactor-windows-arm64.zip"},
	}
	for _, tc := range cases {
		server.reset()
		// WOW64 reports the native architecture in PROCESSOR_ARCHITEW6432.
		env := psEnv(t, append(server.env(), "PROCESSOR_ARCHITECTURE=x86", "PROCESSOR_ARCHITEW6432=ARM64", "PREFACTOR_INSTALL_TEST_CAPTURE_ARGS="+capture)...)
		r := runPs1(t, env, tc.selector)
		if r.code != 0 {
			t.Fatalf("%s: %+v", tc.selector, r)
		}
		if !strings.HasPrefix(server.requested(), tc.request) {
			t.Errorf("%s requested %s", tc.selector, server.requested())
		}
		raw, _ := os.ReadFile(capture)
		if got := strings.Join(strings.Fields(string(raw)), " "); got != tc.args {
			t.Errorf("%s: installer args %q, want %q", tc.selector, got, tc.args)
		}
	}
}

func TestInstallPs1RefusesAChecksumMismatch(t *testing.T) {
	server := newReleases(t)
	server.publish("prefactor-windows-x64.zip", zipped(t, "prefactor.exe", []byte("MZ")), strings.Repeat("0", 64), allPrefixes...)
	capture := filepath.Join(t.TempDir(), "args.txt")

	r := runPs1(t, psEnv(t, append(server.env(), "PREFACTOR_INSTALL_TEST_ARCH=x64", "PREFACTOR_INSTALL_TEST_CAPTURE_ARGS="+capture)...))
	if r.code == 0 || !strings.Contains(r.stdout+r.stderr, "Checksum mismatch for prefactor-windows-x64.zip.") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(capture); err == nil {
		t.Fatal("the installer ran")
	}
}

// TestInstallPs1InstallsThisCLI installs the real Windows binary, puts it
// on the user's PATH, and passes on an installer failure's exit code.
func TestInstallPs1InstallsThisCLI(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("runs the Windows binary")
	}
	binary := filepath.Join(t.TempDir(), "prefactor.exe")
	build := exec.Command("go", "build", "-ldflags", "-X main.version=0.7.0", "-o", binary, "../../cmd/prefactor")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	contents, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	asset := "prefactor-windows-" + arch + ".zip"
	server := newReleases(t)
	server.publish(asset, zipped(t, "prefactor.exe", contents), "", "/releases/latest/download")

	env, home := psHome(t, server.env()...)
	r := runPs1(t, env)
	if r.code != 0 || !strings.Contains(r.stdout, "Installed Prefactor CLI to "+filepath.Join(home, ".prefactor", "bin", "prefactor.exe")) {
		t.Fatalf("%+v", r)
	}
	r = run(t, env, filepath.Join(home, ".prefactor", "bin", "prefactor.exe"), "version")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "0.7.0" {
		t.Fatalf("version: %+v", r)
	}

	// An installer that fails passes its exit code on.
	server.publish(asset, zipped(t, "prefactor.exe", []byte("not a program")), "", "/releases/latest/download")
	if r := runPs1(t, env); r.code == 0 {
		t.Fatalf("a failing installer succeeded: %+v", r)
	}
}
