package lifecycle

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fakeEnv struct {
	home   string
	vars   map[string]string
	stdout bytes.Buffer
	stderr bytes.Buffer
	ran    [][]string
	runErr error
}

func newFakeEnv(t *testing.T) *fakeEnv {
	t.Helper()
	home := t.TempDir()
	return &fakeEnv{home: home, vars: map[string]string{"HOME": home, "PATH": "/usr/bin:/bin"}}
}

func (e *fakeEnv) deps(platform Platform, executable string) Deps {
	return Deps{
		Getenv:     func(k string) string { return e.vars[k] },
		Stdout:     &e.stdout,
		Stderr:     &e.stderr,
		HTTPClient: http.DefaultClient,
		Now:        func() time.Time { return time.Date(2026, 11, 5, 1, 2, 3, 456_000_000, time.UTC) },
		Executable: func() (string, error) { return executable, nil },
		Platform:   func() (Platform, error) { return platform, nil },
		RunInstaller: func(path string, args []string, stdout, _ io.Writer) error {
			e.ran = append(e.ran, append([]string{path}, args...))
			_, _ = io.WriteString(stdout, "child installer ran\n")
			return e.runErr
		},
		StartInstaller: func(path string, args []string) error {
			e.ran = append(e.ran, append([]string{path}, args...))
			return nil
		},
		PID:        4242,
		WaitForPID: func(int) error { return nil },
	}
}

var macArm = Platform{OS: "darwin", Arch: "arm64"}

func writeBinary(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "prefactor")
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNormalizePinnedVersion(t *testing.T) {
	for input, want := range map[string]string{"0.0.4": "v0.0.4", "v1.2.3": "v1.2.3", " 2.0.0 ": "v2.0.0"} {
		if got, err := NormalizePinnedVersion(input); err != nil || got != want {
			t.Errorf("%q = %q, %v", input, got, err)
		}
	}
	for input, want := range map[string]string{
		"":       "Version must not be empty.",
		"1.2":    "Invalid version '1.2'. Expected semver like 0.0.4 or v0.0.4.",
		"v1.2.x": "Invalid version 'v1.2.x'. Expected semver like 0.0.4 or v0.0.4.",
	} {
		if _, err := NormalizePinnedVersion(input); err == nil || err.Error() != want {
			t.Errorf("%q: err = %v", input, err)
		}
	}
}

func TestAssetNamesMatchTheTypeScriptReleases(t *testing.T) {
	for p, want := range map[Platform]string{
		{OS: "darwin", Arch: "x64"}:                "prefactor-darwin-x64.tar.gz",
		{OS: "darwin", Arch: "arm64"}:              "prefactor-darwin-arm64.tar.gz",
		{OS: "linux", Arch: "x64", Libc: "glibc"}:  "prefactor-linux-x64.tar.gz",
		{OS: "linux", Arch: "arm64", Libc: "musl"}: "prefactor-linux-arm64-musl.tar.gz",
		{OS: "windows", Arch: "x64"}:               "prefactor-windows-x64.zip",
		{OS: "windows", Arch: "arm64"}:             "prefactor-windows-arm64.zip",
	} {
		if got := AssetName(p); got != want {
			t.Errorf("%+v = %s, want %s", p, got, want)
		}
	}
}

func TestBuildSpecReturnsStableLatestAndPinnedURLs(t *testing.T) {
	base, latest := ReleaseURLs(func(string) string { return "" })
	if base != "https://github.com/prefactordev/cli/releases/download" || latest != "https://github.com/prefactordev/cli/releases/latest/download" {
		t.Fatalf("urls = %s, %s", base, latest)
	}

	cases := []struct {
		channel, version string
		want             Spec
	}{
		{"", "", Spec{Channel: "stable", AssetName: "prefactor-darwin-arm64.tar.gz", AssetURL: latest + "/prefactor-darwin-arm64.tar.gz", ChecksumURL: latest + "/SHA256SUMS"}},
		{"latest", "", Spec{Channel: "latest", ResolvedTag: "canary", AssetName: "prefactor-darwin-arm64.tar.gz", AssetURL: base + "/canary/prefactor-darwin-arm64.tar.gz", ChecksumURL: base + "/canary/SHA256SUMS"}},
		{"latest", "0.7.1", Spec{Channel: "pinned", RequestedVersion: "v0.7.1", ResolvedTag: "v0.7.1", AssetName: "prefactor-darwin-arm64.tar.gz", AssetURL: base + "/v0.7.1/prefactor-darwin-arm64.tar.gz", ChecksumURL: base + "/v0.7.1/SHA256SUMS"}},
	}
	for _, tc := range cases {
		got, err := BuildSpec(macArm, tc.channel, tc.version, base, latest)
		if err != nil || got != tc.want {
			t.Errorf("%s %s = %+v, %v", tc.channel, tc.version, got, err)
		}
	}

	env := map[string]string{"PREFACTOR_RELEASE_BASE_URL": "http://127.0.0.1:9/releases/download"}
	if _, latest := ReleaseURLs(func(k string) string { return env[k] }); latest != "http://127.0.0.1:9/releases/latest/download" {
		t.Fatalf("latest = %s", latest)
	}
}

func TestDetectPlatformHonoursTheRosettaOverride(t *testing.T) {
	p, err := DetectPlatform(func(k string) string {
		if k == "PREFACTOR_INSTALL_TEST_ROSETTA" {
			return "0"
		}
		return ""
	})
	if err != nil || p.Rosetta {
		t.Fatalf("p = %+v, %v", p, err)
	}
}

func TestInstallWritesStateAndPrintsPathGuidance(t *testing.T) {
	e := newFakeEnv(t)
	source := writeBinary(t, "new build")

	state, err := Install(InstallOptions{}, "0.7.0", e.deps(macArm, source))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(e.home, ".prefactor")
	bin := filepath.Join(root, "bin", "prefactor")
	if got, _ := os.ReadFile(bin); string(got) != "new build" {
		t.Fatalf("binary = %q", got)
	}
	if info, _ := os.Stat(bin); runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", info.Mode())
	}
	want := `{
  "schemaVersion": 1,
  "channel": "stable",
  "requestedVersion": null,
  "resolvedTag": "v0.7.0",
  "assetName": "prefactor-darwin-arm64.tar.gz",
  "platform": "darwin",
  "arch": "arm64",
  "libc": null,
  "installRoot": "` + root + `",
  "binPath": "` + bin + `",
  "installedAt": "2026-11-05T01:02:03.456Z"
}`
	if raw, _ := os.ReadFile(filepath.Join(root, "install.json")); string(raw) != want {
		t.Fatalf("install.json =\n%s\nwant\n%s", raw, want)
	}
	if state.BinPath != bin {
		t.Fatalf("state = %+v", state)
	}
	if out := e.stdout.String(); out != "Installed Prefactor CLI to "+bin+"\nAdd this line to your shell profile: export PATH=\""+filepath.Dir(bin)+":$PATH\"\n" {
		t.Fatalf("stdout = %q", out)
	}

	e.stdout.Reset()
	e.vars["PATH"] = filepath.Dir(bin) + string(os.PathListSeparator) + "/usr/bin"
	if _, err := Install(InstallOptions{Version: "0.7.1", ResolvedTag: "v0.7.1", AssetName: "custom.tar.gz"}, "0.7.0", e.deps(macArm, source)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(e.stdout.String(), "The Prefactor bin directory is already on PATH.\n") {
		t.Fatalf("stdout = %q", e.stdout.String())
	}
	state, err = ReadState(root)
	if err != nil || state.Channel != "pinned" || state.RequestedVersion != "v0.7.1" || state.AssetName != "custom.tar.gz" {
		t.Fatalf("state = %+v, %v", state, err)
	}
}

func TestInstallNeedsTheSourceBinary(t *testing.T) {
	e := newFakeEnv(t)
	missing := filepath.Join(t.TempDir(), "nope")
	_, err := Install(InstallOptions{SourceBinary: missing}, "0.7.0", e.deps(macArm, ""))
	if err == nil || err.Error() != "Install source binary not found at "+missing+"." {
		t.Fatalf("err = %v", err)
	}
}

func TestUninstallKeepsProfiles(t *testing.T) {
	e := newFakeEnv(t)
	d := e.deps(macArm, writeBinary(t, "build"))
	if _, err := Install(InstallOptions{}, "0.7.0", d); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(e.home, ".prefactor")
	if err := os.WriteFile(filepath.Join(root, "prefactor.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	e.stdout.Reset()
	if _, err := Uninstall(d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "bin")); !os.IsNotExist(err) {
		t.Fatalf("bin dir remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "prefactor.json")); err != nil {
		t.Fatal("prefactor.json was removed")
	}
	if e.stdout.String() != "Removed Prefactor CLI from "+filepath.Join(root, "bin", "prefactor")+"\n" {
		t.Fatalf("stdout = %q", e.stdout.String())
	}

	// With nothing else left, the root goes too.
	_ = os.Remove(filepath.Join(root, "prefactor.json"))
	if _, err := Uninstall(d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("root remains: %v", err)
	}
}

func TestDoctorPrintsInstallMetadata(t *testing.T) {
	e := newFakeEnv(t)
	d := e.deps(Platform{OS: "linux", Arch: "x64", Libc: "musl"}, "/opt/prefactor")
	root := filepath.Join(e.home, ".prefactor")

	report, err := Doctor(d)
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]string{
		{"installRoot", root}, {"binPath", filepath.Join(root, "bin", "prefactor")}, {"currentExecutable", "/opt/prefactor"},
		{"pathStatus", "missing"}, {"platform", "linux"}, {"arch", "x64"}, {"libc", "musl"}, {"rosetta", "no"},
		{"installState", "missing"}, {"installStateError", "Install state not found at " + filepath.Join(root, "install.json") + "."},
	}
	if !equalLines(report.Lines, want) {
		t.Fatalf("report = %v", report.Lines)
	}

	if _, err := Install(InstallOptions{Channel: "latest", SourceBinary: writeBinary(t, "x")}, "0.7.0", d); err != nil {
		t.Fatal(err)
	}
	report, _ = Doctor(d)
	tail := report.Lines[8:]
	want = [][2]string{
		{"installState", "present"}, {"channel", "latest"}, {"requestedVersion", "null"}, {"resolvedTag", "canary"},
		{"assetName", "prefactor-linux-x64-musl.tar.gz"}, {"installedAt", "2026-11-05T01:02:03.456Z"}, {"managedBinaryExists", "yes"},
	}
	if !equalLines(tail, want) {
		t.Fatalf("report = %v", tail)
	}
}

func equalLines(a, b [][2]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestReadStateRejectsInvalidState(t *testing.T) {
	root := t.TempDir()
	for _, raw := range []string{
		`{"schemaVersion":1,"channel":"nightly","requestedVersion":null,"resolvedTag":"x","assetName":"a","platform":"linux","arch":"x64","libc":"glibc","installRoot":"r","binPath":"b","installedAt":"t"}`,
		`{"schemaVersion":2,"channel":"stable","requestedVersion":null,"resolvedTag":"x","assetName":"a","platform":"linux","arch":"x64","libc":"glibc","installRoot":"r","binPath":"b","installedAt":"t"}`,
		`{"schemaVersion":1,"channel":"stable","resolvedTag":"x","assetName":"a","platform":"linux","arch":"x64","libc":"glibc","installRoot":"r","binPath":"b","installedAt":"t"}`,
		`[]`,
	} {
		if err := os.WriteFile(filepath.Join(root, "install.json"), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadState(root); err == nil || err.Error() != "Install state at "+filepath.Join(root, "install.json")+" is invalid." {
			t.Errorf("%s: err = %v", raw, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "install.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadState(root); err == nil || !strings.HasPrefix(err.Error(), "Unable to read install state") {
		t.Fatalf("err = %v", err)
	}
}

// releaseServer serves archives and SHA256SUMS under
// /releases/download/<tag>/, and redirects /releases/latest/download/ to
// the stable tag, as GitHub does.
type releaseServer struct {
	*httptest.Server
	stable   string
	assets   map[string][]byte // "<tag>/<name>"
	requests []string
}

func newReleaseServer(t *testing.T, stable string) *releaseServer {
	t.Helper()
	s := &releaseServer{stable: stable, assets: map[string][]byte{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests = append(s.requests, r.URL.Path)
		if rest, ok := strings.CutPrefix(r.URL.Path, "/releases/latest/download/"); ok {
			http.Redirect(w, r, "/releases/download/"+s.stable+"/"+rest, http.StatusFound)
			return
		}
		body, ok := s.assets[strings.TrimPrefix(r.URL.Path, "/releases/download/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(s.Close)
	return s
}

// publish adds a release with the asset and its checksum.
func (s *releaseServer) publish(t *testing.T, tag, name string, archive []byte, checksum string) {
	t.Helper()
	if checksum == "" {
		sum := sha256.Sum256(archive)
		checksum = hex.EncodeToString(sum[:])
	}
	s.assets[tag+"/"+name] = archive
	s.assets[tag+"/SHA256SUMS"] = []byte(checksum + "  " + name + "\n" + strings.Repeat("0", 64) + "  other.tar.gz\n")
}

func tarGz(t *testing.T, name, contents string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte(contents))
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func zipped(t *testing.T, name, contents string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(contents))
	_ = zw.Close()
	return buf.Bytes()
}

func installedState(t *testing.T, e *fakeEnv, d Deps, opts InstallOptions) {
	t.Helper()
	opts.SourceBinary = writeBinary(t, "old build")
	if _, err := Install(opts, "0.7.0", d); err != nil {
		t.Fatal(err)
	}
	e.stdout.Reset()
}

func TestUpdateDownloadsVerifiesAndRunsTheNewInstaller(t *testing.T) {
	e := newFakeEnv(t)
	server := newReleaseServer(t, "v0.7.2")
	e.vars["PREFACTOR_RELEASE_BASE_URL"] = server.URL + "/releases/download"
	d := e.deps(macArm, "")
	installedState(t, e, d, InstallOptions{})
	server.publish(t, "v0.7.2", "prefactor-darwin-arm64.tar.gz", tarGz(t, "prefactor", "#!/bin/sh\n"), "")

	tag, err := Update(UpdateOptions{}, d)
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.7.2" {
		t.Fatalf("tag = %s", tag)
	}
	// The stable download redirects to its tag, whose checksums are used.
	if got := strings.Join(server.requests, " "); got != "/releases/latest/download/prefactor-darwin-arm64.tar.gz /releases/download/v0.7.2/prefactor-darwin-arm64.tar.gz /releases/download/v0.7.2/SHA256SUMS" {
		t.Fatalf("requests = %s", got)
	}
	if len(e.ran) != 1 {
		t.Fatalf("ran = %v", e.ran)
	}
	child := e.ran[0]
	if filepath.Base(child[0]) != "prefactor" || strings.Join(child[1:], " ") != "install --source-binary "+child[0]+" --resolved-tag v0.7.2 --asset-name prefactor-darwin-arm64.tar.gz --channel stable" {
		t.Fatalf("child = %v", child)
	}
	if e.stdout.String() != "child installer ran\nUpdated Prefactor CLI to v0.7.2.\n" {
		t.Fatalf("stdout = %q", e.stdout.String())
	}
	if _, err := os.Stat(child[0]); !os.IsNotExist(err) {
		t.Fatal("the download was not cleaned up")
	}
}

func TestUpdateFollowsThePinnedVersionUnlessAChannelIsGiven(t *testing.T) {
	e := newFakeEnv(t)
	server := newReleaseServer(t, "v0.7.2")
	e.vars["PREFACTOR_RELEASE_BASE_URL"] = server.URL + "/releases/download"
	d := e.deps(macArm, "")
	installedState(t, e, d, InstallOptions{Version: "0.7.1"})
	archive := tarGz(t, "prefactor", "x")
	server.publish(t, "v0.7.1", "prefactor-darwin-arm64.tar.gz", archive, "")
	server.publish(t, "canary", "prefactor-darwin-arm64.tar.gz", archive, "")

	if tag, err := Update(UpdateOptions{}, d); err != nil || tag != "v0.7.1" {
		t.Fatalf("pinned update = %s, %v", tag, err)
	}
	if args := strings.Join(e.ran[0][1:], " "); !strings.HasSuffix(args, "--version v0.7.1") {
		t.Fatalf("args = %s", args)
	}
	if tag, err := Update(UpdateOptions{Channel: "latest"}, d); err != nil || tag != "canary" {
		t.Fatalf("latest update = %s, %v", tag, err)
	}
	if args := strings.Join(e.ran[1][1:], " "); !strings.HasSuffix(args, "--channel latest") {
		t.Fatalf("args = %s", args)
	}
}

func TestUpdateRefusesABadDownload(t *testing.T) {
	e := newFakeEnv(t)
	server := newReleaseServer(t, "v0.7.2")
	e.vars["PREFACTOR_RELEASE_BASE_URL"] = server.URL + "/releases/download"
	d := e.deps(macArm, "")

	if _, err := Update(UpdateOptions{}, d); err == nil || err.Error() != "Install state not found at "+filepath.Join(e.home, ".prefactor", "install.json")+"." {
		t.Fatalf("err = %v", err)
	}
	installedState(t, e, d, InstallOptions{})

	if _, err := Update(UpdateOptions{}, d); err == nil || !strings.HasPrefix(err.Error(), "Download failed for "+server.URL+"/releases/latest/download/prefactor-darwin-arm64.tar.gz: 404 Not Found") {
		t.Fatalf("err = %v", err)
	}
	server.publish(t, "v0.7.2", "prefactor-darwin-arm64.tar.gz", tarGz(t, "prefactor", "x"), strings.Repeat("a", 64))
	if _, err := Update(UpdateOptions{}, d); err == nil || err.Error() != "Checksum mismatch for prefactor-darwin-arm64.tar.gz." {
		t.Fatalf("err = %v", err)
	}
	server.publish(t, "v0.7.2", "prefactor-darwin-arm64.tar.gz", tarGz(t, "other", "x"), "")
	if _, err := Update(UpdateOptions{}, d); err == nil || err.Error() != "Failed to extract prefactor-darwin-arm64.tar.gz." {
		t.Fatalf("err = %v", err)
	}
	server.assets["v0.7.2/SHA256SUMS"] = []byte(strings.Repeat("0", 64) + "  other.tar.gz\n")
	if _, err := Update(UpdateOptions{}, d); err == nil || err.Error() != "No checksum entry found for prefactor-darwin-arm64.tar.gz." {
		t.Fatalf("err = %v", err)
	}
	if len(e.ran) != 0 {
		t.Fatalf("ran an installer: %v", e.ran)
	}
}

func TestUpdateOnWindowsStartsTheInstallerAfterThisProcessExits(t *testing.T) {
	e := newFakeEnv(t)
	server := newReleaseServer(t, "v0.7.2")
	e.vars["PREFACTOR_RELEASE_BASE_URL"] = server.URL + "/releases/download"
	d := e.deps(Platform{OS: "windows", Arch: "x64"}, "")
	installedState(t, e, d, InstallOptions{})
	server.publish(t, "v0.7.2", "prefactor-windows-x64.zip", zipped(t, "prefactor.exe", "MZ"), "")

	if _, err := Update(UpdateOptions{}, d); err != nil {
		t.Fatal(err)
	}
	child := e.ran[0]
	if got, _ := os.ReadFile(child[0]); string(got) != "MZ" || filepath.Base(child[0]) != "prefactor.exe" {
		t.Fatalf("child binary %s = %q", child[0], got)
	}
	if args := strings.Join(child[1:], " "); args != "install --source-binary "+child[0]+" --resolved-tag v0.7.2 --asset-name prefactor-windows-x64.zip --wait-for-pid 4242 --channel stable" {
		t.Fatalf("args = %s", args)
	}
	if e.stdout.String() != "Update started. The installed binary will be replaced after this process exits.\n" {
		t.Fatalf("stdout = %q", e.stdout.String())
	}
	_ = os.RemoveAll(filepath.Dir(filepath.Dir(child[0])))
}

func TestUpdateReportsAFailedInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs a shell script as the installer")
	}
	e := newFakeEnv(t)
	server := newReleaseServer(t, "v0.7.2")
	e.vars["PREFACTOR_RELEASE_BASE_URL"] = server.URL + "/releases/download"
	d := e.deps(macArm, "")
	installedState(t, e, d, InstallOptions{})
	server.publish(t, "v0.7.2", "prefactor-darwin-arm64.tar.gz", tarGz(t, "prefactor", "#!/bin/sh\nexit 3\n"), "")
	d.RunInstaller = runInstaller

	if _, err := Update(UpdateOptions{}, d); err == nil || err.Error() != "The downloaded installer failed." {
		t.Fatalf("err = %v", err)
	}
}
