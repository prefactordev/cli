package lifecycle

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/prefactordev/cli/internal/js"
)

// DefaultReleaseBaseURL is where release assets are downloaded from:
// <base>/<tag>/<asset>. PREFACTOR_RELEASE_BASE_URL overrides it.
const DefaultReleaseBaseURL = "https://github.com/prefactordev/cli/releases/download"

// Channels an install can follow. pinned stays on one version.
const (
	ChannelStable = "stable"
	ChannelLatest = "latest"
	ChannelPinned = "pinned"
	// canaryTag is the pre-release the latest channel installs from.
	canaryTag = "canary"
)

// Spec says which release asset to download and from where.
type Spec struct {
	Channel          string
	RequestedVersion string
	// ResolvedTag is empty for stable, whose tag is only known once the
	// download redirects to it.
	ResolvedTag string
	AssetName   string
	AssetURL    string
	ChecksumURL string
}

var semver = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// NormalizePinnedVersion accepts 1.2.3 or v1.2.3 and returns v1.2.3.
func NormalizePinnedVersion(version string) (string, error) {
	trimmed := js.Trim(version)
	if trimmed == "" {
		return "", errors.New("Version must not be empty.")
	}
	if !strings.HasPrefix(trimmed, "v") {
		trimmed = "v" + trimmed
	}
	if !semver.MatchString(trimmed) {
		return "", fmt.Errorf("Invalid version '%s'. Expected semver like 0.0.4 or v0.0.4.", version)
	}
	return trimmed, nil
}

// AssetName is the release archive for the platform, such as
// prefactor-linux-x64-musl.tar.gz or prefactor-windows-arm64.zip.
func AssetName(p Platform) string {
	suffix := p.OS + "-" + p.Arch
	if p.OS == "linux" && p.Libc == "musl" {
		suffix += "-musl"
	}
	if p.OS == "windows" {
		return "prefactor-" + suffix + ".zip"
	}
	return "prefactor-" + suffix + ".tar.gz"
}

// ReleaseURLs returns the base URL for tagged downloads and the one for the
// newest stable release, from PREFACTOR_RELEASE_BASE_URL and
// PREFACTOR_RELEASE_LATEST_BASE_URL when set, even to "".
func ReleaseURLs(lookupEnv func(string) (string, bool)) (base, latest string) {
	base, set := lookupEnv("PREFACTOR_RELEASE_BASE_URL")
	if !set {
		base = DefaultReleaseBaseURL
	}
	if latest, set = lookupEnv("PREFACTOR_RELEASE_LATEST_BASE_URL"); !set {
		latest = strings.TrimSuffix(base, "/download") + "/latest/download"
	}
	return base, latest
}

// BuildSpec chooses the release: a pinned version when version is set,
// otherwise channel, which defaults to stable.
func BuildSpec(p Platform, channel, version, base, latest string) (Spec, error) {
	spec := Spec{Channel: channel, AssetName: AssetName(p)}
	if version != "" {
		pinned, err := NormalizePinnedVersion(version)
		if err != nil {
			return spec, err
		}
		spec.Channel, spec.RequestedVersion, spec.ResolvedTag = ChannelPinned, pinned, pinned
	}

	switch spec.Channel {
	case "", ChannelStable:
		spec.Channel = ChannelStable
		spec.AssetURL = latest + "/" + spec.AssetName
		spec.ChecksumURL = latest + "/SHA256SUMS"
	case ChannelLatest:
		spec.ResolvedTag = canaryTag
		spec.AssetURL = base + "/" + canaryTag + "/" + spec.AssetName
		spec.ChecksumURL = base + "/" + canaryTag + "/SHA256SUMS"
	case ChannelPinned:
		spec.AssetURL = base + "/" + spec.RequestedVersion + "/" + spec.AssetName
		spec.ChecksumURL = base + "/" + spec.RequestedVersion + "/SHA256SUMS"
	default:
		return spec, fmt.Errorf("Unknown release channel '%s'.", channel)
	}
	return spec, nil
}

var downloadTag = regexp.MustCompile(`/releases/download/([^/]+)/`)

// TagFromURL returns the release tag in a download URL, or "".
func TagFromURL(url string) string {
	if m := downloadTag.FindStringSubmatch(url); m != nil {
		return m[1]
	}
	return ""
}
