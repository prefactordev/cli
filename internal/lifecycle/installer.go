package lifecycle

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/prefactordev/cli/internal/js"
)

// Deps are what the lifecycle commands use from the process, so tests can
// replace them.
type Deps struct {
	Getenv func(string) string
	// LookupEnv also says whether a variable is set: an empty release URL
	// is kept, and fails, as in the TypeScript CLI.
	LookupEnv func(string) (string, bool)
	// Stdout receives the messages the TypeScript CLI prints with
	// console.log. Stderr receives the child installer's errors.
	Stdout, Stderr io.Writer
	HTTPClient     *http.Client
	Now            func() time.Time
	// Executable is the running binary.
	Executable func() (string, error)
	Platform   func() (Platform, error)
	// RunInstaller runs a downloaded binary and waits for it. On Windows
	// StartInstaller starts it detached instead, because the running
	// binary cannot be replaced until this process exits.
	RunInstaller   func(path string, args []string, stdout, stderr io.Writer) error
	StartInstaller func(path string, args []string) error
	PID            int
	// WaitForPID waits for a process to exit.
	WaitForPID func(pid int) error
}

// DefaultDeps returns Deps for the running process.
func DefaultDeps(stdout, stderr io.Writer) Deps {
	return Deps{
		Getenv:         os.Getenv,
		LookupEnv:      os.LookupEnv,
		Stdout:         stdout,
		Stderr:         stderr,
		HTTPClient:     &http.Client{Timeout: 5 * time.Minute},
		Now:            time.Now,
		Executable:     currentExecutable,
		Platform:       func() (Platform, error) { return DetectPlatform(os.Getenv) },
		RunInstaller:   runInstaller,
		StartInstaller: startDetached,
		PID:            os.Getpid(),
		WaitForPID:     waitForPID,
	}
}

// currentExecutable is PREFACTOR_CLI_SELF_PATH, which tests set as they do
// for the TypeScript CLI, or the running binary with symlinks resolved.
func currentExecutable() (string, error) {
	if path := os.Getenv("PREFACTOR_CLI_SELF_PATH"); path != "" {
		return path, nil
	}
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	}
	return path, nil
}

// InstallRoot is ~/.prefactor, from HOME or USERPROFILE.
func InstallRoot(getenv func(string) string) (string, error) {
	home := getenv("HOME")
	if home == "" {
		home = getenv("USERPROFILE")
	}
	if home == "" {
		return "", errors.New("Unable to determine the user home directory for installation.")
	}
	return filepath.Abs(filepath.Join(home, ".prefactor"))
}

// BinPath is the managed binary in root.
func BinPath(root, osName string) string {
	name := "prefactor"
	if osName == "windows" {
		name = "prefactor.exe"
	}
	return filepath.Join(root, "bin", name)
}

// OnPath reports whether dir is one of the PATH entries.
func OnPath(dir string, getenv func(string) string, osName string) bool {
	normalize := func(p string) string {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if osName == "windows" {
			return strings.ToLower(abs)
		}
		return abs
	}
	want := normalize(dir)
	for _, entry := range filepath.SplitList(getenv("PATH")) {
		if entry != "" && normalize(entry) == want {
			return true
		}
	}
	return false
}

func pathInstruction(binDir, osName string) string {
	if osName == "windows" {
		return fmt.Sprintf("Add %s to your PATH, then open a new terminal.", binDir)
	}
	return fmt.Sprintf(`Add this line to your shell profile: export PATH="%s:$PATH"`, binDir)
}

// InstallOptions are the install command's options. SourceBinary,
// ResolvedTag, AssetName and WaitForPID are hidden: update, the install
// scripts and the TypeScript CLI's update pass them to the binary they
// downloaded.
type InstallOptions struct {
	Channel, Version          string
	SourceBinary, ResolvedTag string
	AssetName                 string
	WaitForPID                int
}

// Install copies the source binary, by default the running one, to the
// managed location and records what was installed.
func Install(opts InstallOptions, version string, d Deps) (State, error) {
	platform, err := d.Platform()
	if err != nil {
		return State{}, err
	}
	root, err := InstallRoot(d.Getenv)
	if err != nil {
		return State{}, err
	}
	base, latest := ReleaseURLs(d.LookupEnv)
	spec, err := BuildSpec(platform, opts.Channel, opts.Version, base, latest)
	if err != nil {
		return State{}, err
	}

	source := opts.SourceBinary
	if source == "" {
		if source, err = d.Executable(); err != nil {
			return State{}, err
		}
	}
	if source, err = filepath.Abs(source); err != nil {
		return State{}, err
	}
	if _, err := os.Stat(source); err != nil {
		return State{}, fmt.Errorf("Install source binary not found at %s.", source)
	}

	if opts.WaitForPID != 0 {
		if err := d.WaitForPID(opts.WaitForPID); err != nil {
			return State{}, err
		}
	}

	binPath := BinPath(root, platform.OS)
	if err := replaceBinary(source, binPath, platform.OS, d.PID); err != nil {
		return State{}, err
	}

	resolvedTag := opts.ResolvedTag
	switch {
	case resolvedTag != "":
	case spec.Channel == ChannelLatest:
		resolvedTag = canaryTag
	case spec.Channel == ChannelPinned:
		resolvedTag = spec.RequestedVersion
	default:
		resolvedTag = "v" + version
	}
	assetName := opts.AssetName
	if assetName == "" {
		assetName = spec.AssetName
	}
	state := State{
		Channel:          spec.Channel,
		RequestedVersion: nullable(spec.RequestedVersion),
		ResolvedTag:      resolvedTag,
		AssetName:        assetName,
		Platform:         platform.OS,
		Arch:             platform.Arch,
		Libc:             platform.Libc,
		InstallRoot:      root,
		BinPath:          binPath,
		InstalledAt:      d.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	if err := writeState(root, state); err != nil {
		return State{}, err
	}

	fmt.Fprintf(d.Stdout, "Installed Prefactor CLI to %s\n", binPath)
	if OnPath(filepath.Dir(binPath), d.Getenv, platform.OS) {
		fmt.Fprintln(d.Stdout, "The Prefactor bin directory is already on PATH.")
	} else {
		fmt.Fprintln(d.Stdout, pathInstruction(filepath.Dir(binPath), platform.OS))
	}
	return state, nil
}

// replaceBinary copies source next to target, then renames it into place.
func replaceBinary(source, target, osName string, pid int) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	staged := target + ".tmp-" + strconv.Itoa(pid)
	_ = os.Remove(staged)
	if err := copyFile(source, staged); err != nil {
		return err
	}
	if osName != "windows" {
		if err := os.Chmod(staged, 0o755); err != nil {
			return err
		}
		return os.Rename(staged, target)
	}

	// Windows keeps the old binary locked until its process exits.
	var err error
	for range retryAttempts {
		if err = os.Remove(target); err == nil || errors.Is(err, os.ErrNotExist) {
			if err = os.Rename(staged, target); err == nil {
				return nil
			}
		}
		time.Sleep(retryDelay)
	}
	return err
}

const (
	retryAttempts = 100
	retryDelay    = 250 * time.Millisecond
)

func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// UpdateOptions are the update command's options.
type UpdateOptions struct {
	Channel, Version string
}

// Update downloads the release the install follows, or the one the options
// ask for, checks it against SHA256SUMS and runs its install command. It
// returns the release tag installed.
func Update(opts UpdateOptions, d Deps) (string, error) {
	platform, err := d.Platform()
	if err != nil {
		return "", err
	}
	root, err := InstallRoot(d.Getenv)
	if err != nil {
		return "", err
	}
	state, err := ReadState(root)
	if err != nil {
		return "", err
	}

	// Without options, follow the installed channel or pinned version. A
	// channel option replaces a pinned version.
	channel, version := opts.Channel, opts.Version
	if channel == "" && version == "" {
		if state.Channel == ChannelPinned {
			if state.RequestedVersion != nil {
				version = *state.RequestedVersion
			}
		} else {
			channel = state.Channel
		}
	}
	base, latest := ReleaseURLs(d.LookupEnv)
	spec, err := BuildSpec(platform, channel, version, base, latest)
	if err != nil {
		return "", err
	}

	tempRoot, err := os.MkdirTemp("", "prefactor-update-")
	if err != nil {
		return "", err
	}
	// On Windows the detached installer still needs the download.
	if platform.OS != "windows" {
		defer func() { _ = os.RemoveAll(tempRoot) }()
	}

	archivePath := filepath.Join(tempRoot, spec.AssetName)
	finalURL, err := download(d.HTTPClient, spec.AssetURL, archivePath)
	if err != nil {
		return "", err
	}
	resolvedTag := TagFromURL(finalURL)
	if resolvedTag == "" {
		resolvedTag = spec.ResolvedTag
	}
	checksumURL := spec.ChecksumURL
	if resolvedTag != "" {
		checksumURL = base + "/" + resolvedTag + "/SHA256SUMS"
	}
	if tag, err := verifyChecksum(d.HTTPClient, archivePath, spec.AssetName, checksumURL); err != nil {
		return "", err
	} else if tag != "" {
		resolvedTag = tag
	}

	extracted := filepath.Join(tempRoot, "extracted")
	binary, err := extract(archivePath, extracted, platform.OS)
	if err != nil {
		return "", err
	}

	args := []string{"install", "--source-binary", binary, "--resolved-tag", resolvedTag, "--asset-name", spec.AssetName}
	if platform.OS == "windows" {
		args = append(args, "--wait-for-pid", strconv.Itoa(d.PID))
	}
	if spec.Channel == ChannelPinned {
		args = append(args, "--version", spec.RequestedVersion)
	} else {
		args = append(args, "--channel", spec.Channel)
	}

	if platform.OS == "windows" {
		if err := d.StartInstaller(binary, args); err != nil {
			return "", err
		}
		fmt.Fprintln(d.Stdout, "Update started. The installed binary will be replaced after this process exits.")
		return resolvedTag, nil
	}

	if err := d.RunInstaller(binary, args, d.Stdout, d.Stderr); err != nil {
		var exitErr interface{ ExitCode() int }
		if errors.As(err, &exitErr) {
			return "", errors.New("The downloaded installer failed.")
		}
		return "", err
	}
	fmt.Fprintf(d.Stdout, "Updated Prefactor CLI to %s.\n", resolvedTag)
	return resolvedTag, nil
}

// download saves url to path and returns the URL it was served from after
// redirects, which names the release tag of a stable download.
func download(client *http.Client, url, path string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("Download failed for %s: %d %s", url, resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		_ = out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return resp.Request.URL.String(), nil
}

var checksumLine = regexp.MustCompile(`^([a-fA-F0-9]{64})\s+\*?(.+)$`)

// verifyChecksum checks path against the asset's entry in the SHA256SUMS
// at url, and returns the release tag the manifest was served from.
func verifyChecksum(client *http.Client, path, assetName, url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("Checksum download failed for %s: %d %s", url, resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	manifest, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	expected := ""
	for _, line := range regexp.MustCompile(`\r?\n`).Split(string(manifest), -1) {
		if m := checksumLine.FindStringSubmatch(js.Trim(line)); m != nil && m[2] == assetName {
			expected = strings.ToLower(m[1])
			break
		}
	}
	if expected == "" {
		return "", fmt.Errorf("No checksum entry found for %s.", assetName)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return "", fmt.Errorf("Checksum mismatch for %s.", assetName)
	}
	return TagFromURL(resp.Request.URL.String()), nil
}

// extract unpacks the CLI binary from a release archive into dir.
func extract(archive, dir, osName string) (string, error) {
	name := "prefactor"
	if osName == "windows" {
		name = "prefactor.exe"
	}
	failed := fmt.Errorf("Failed to extract %s.", filepath.Base(archive))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(dir, name)

	var contents io.Reader
	if osName == "windows" {
		r, err := zip.OpenReader(archive)
		if err != nil {
			return "", failed
		}
		defer func() { _ = r.Close() }()
		for _, f := range r.File {
			if f.Name == name {
				rc, err := f.Open()
				if err != nil {
					return "", failed
				}
				defer func() { _ = rc.Close() }()
				contents = rc
				break
			}
		}
	} else {
		f, err := os.Open(archive)
		if err != nil {
			return "", failed
		}
		defer func() { _ = f.Close() }()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return "", failed
		}
		tr := tar.NewReader(gz)
		for {
			header, err := tr.Next()
			if err != nil {
				return "", failed
			}
			if strings.TrimPrefix(header.Name, "./") == name && header.Typeflag == tar.TypeReg {
				contents = tr
				break
			}
		}
	}
	if contents == nil {
		return "", failed
	}

	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, contents); err != nil {
		_ = out.Close()
		return "", failed
	}
	return target, out.Close()
}

// Uninstall removes the managed binary and install.json, and the bin
// directory and install root when nothing else is left in them, so
// prefactor.json survives.
func Uninstall(d Deps) (string, error) {
	platform, err := d.Platform()
	if err != nil {
		return "", err
	}
	root, err := InstallRoot(d.Getenv)
	if err != nil {
		return "", err
	}
	binPath := BinPath(root, platform.OS)

	if err := removeBinary(binPath, platform.OS); err != nil {
		return "", err
	}
	if err := os.Remove(statePath(root)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	_ = os.Remove(filepath.Dir(binPath))
	_ = os.Remove(root)

	fmt.Fprintf(d.Stdout, "Removed Prefactor CLI from %s\n", binPath)
	return binPath, nil
}

// removeBinary deletes the managed binary. Windows does not let a running
// binary be deleted, but does let it be moved, so uninstalling with the
// managed binary moves it to the temporary directory instead.
func removeBinary(binPath, osName string) error {
	err := os.Remove(binPath)
	if err == nil || errors.Is(err, os.ErrNotExist) || osName != "windows" {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	moved := filepath.Join(os.TempDir(), fmt.Sprintf("prefactor-uninstalled-%d.exe", time.Now().UnixNano()))
	if renameErr := os.Rename(binPath, moved); renameErr != nil {
		return err
	}
	return nil
}

// Report is what doctor prints, in its order.
type Report struct {
	Lines [][2]string
}

func (r *Report) add(key, value string) {
	r.Lines = append(r.Lines, [2]string{key, value})
}

// Doctor describes the managed installation and the running binary.
func Doctor(d Deps) (Report, error) {
	var r Report
	platform, err := d.Platform()
	if err != nil {
		return r, err
	}
	root, err := InstallRoot(d.Getenv)
	if err != nil {
		return r, err
	}
	binPath := BinPath(root, platform.OS)
	current, err := d.Executable()
	if err != nil {
		return r, err
	}

	pathStatus := "missing"
	if OnPath(filepath.Dir(binPath), d.Getenv, platform.OS) {
		pathStatus = "present"
	}
	libc := platform.Libc
	if libc == "" {
		libc = "n/a"
	}
	rosetta := "no"
	if platform.Rosetta {
		rosetta = "yes"
	}
	r.add("installRoot", root)
	r.add("binPath", binPath)
	r.add("currentExecutable", current)
	r.add("pathStatus", pathStatus)
	r.add("platform", platform.OS)
	r.add("arch", platform.Arch)
	r.add("libc", libc)
	r.add("rosetta", rosetta)

	state, err := ReadState(root)
	if err != nil {
		r.add("installState", "missing")
		r.add("installStateError", err.Error())
		return r, nil
	}
	exists := "no"
	if _, err := os.Stat(state.BinPath); err == nil {
		exists = "yes"
	}
	requested := "null"
	if state.RequestedVersion != nil {
		requested = *state.RequestedVersion
	}
	r.add("installState", "present")
	r.add("channel", state.Channel)
	r.add("requestedVersion", requested)
	r.add("resolvedTag", state.ResolvedTag)
	r.add("assetName", state.AssetName)
	r.add("installedAt", state.InstalledAt)
	r.add("managedBinaryExists", exists)
	return r, nil
}
