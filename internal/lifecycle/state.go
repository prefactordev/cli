package lifecycle

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/prefactordev/cli/internal/jsjson"
)

const stateFilename = "install.json"

// State is install.json, which records what is installed. Its fields and
// key order are the TypeScript CLI's, so either CLI reads the other's.
type State struct {
	Channel string
	// RequestedVersion is the pinned version, or nil (null in the file).
	RequestedVersion *string
	ResolvedTag      string
	AssetName        string
	Platform         string
	Arch             string
	// Libc is "" (null in the file) off Linux.
	Libc        string
	InstallRoot string
	BinPath     string
	InstalledAt string
}

// nullable is a pointer to s, or nil when s is empty.
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Object is the state in the file's key order.
func (s State) Object() *jsjson.Object {
	o := jsjson.NewObject()
	o.Set("schemaVersion", jsjson.Number(1))
	o.Set("channel", s.Channel)
	if s.RequestedVersion != nil {
		o.Set("requestedVersion", *s.RequestedVersion)
	} else {
		o.Set("requestedVersion", nil)
	}
	o.Set("resolvedTag", s.ResolvedTag)
	o.Set("assetName", s.AssetName)
	o.Set("platform", s.Platform)
	o.Set("arch", s.Arch)
	if s.Libc != "" {
		o.Set("libc", s.Libc)
	} else {
		o.Set("libc", nil)
	}
	o.Set("installRoot", s.InstallRoot)
	o.Set("binPath", s.BinPath)
	o.Set("installedAt", s.InstalledAt)
	return o
}

func statePath(root string) string {
	return filepath.Join(root, stateFilename)
}

// writeState writes install.json as JSON.stringify(state, null, 2) does.
func writeState(root string, s State) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return os.WriteFile(statePath(root), []byte(jsjson.Stringify(s.Object(), "  ")), 0o644)
}

var (
	validChannels  = []string{ChannelStable, ChannelLatest, ChannelPinned}
	validPlatforms = []string{"darwin", "linux", "windows"}
	validArchs     = []string{"x64", "arm64"}
	validLibcs     = []string{"glibc", "musl", ""}
)

// ReadState reads install.json from root. The error says why there is no
// usable state, in the TypeScript CLI's words.
func ReadState(root string) (State, error) {
	path := statePath(root)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, fmt.Errorf("Install state not found at %s.", path)
	}
	if err != nil {
		return State{}, fmt.Errorf("Unable to read install state at %s.", path)
	}
	parsed, err := jsjson.Parse(raw)
	// The TypeScript CLI fails to read a null state's fields, and reports
	// that as unreadable.
	if err != nil || parsed == nil {
		return State{}, fmt.Errorf("Unable to read install state at %s.", path)
	}

	o, _ := parsed.(*jsjson.Object)
	valid := o != nil
	field := func(key string, nullOK bool) string {
		if o == nil {
			return ""
		}
		v, present := o.Get(key)
		if s, ok := v.(string); ok {
			return s
		}
		if !nullOK || !present || v != nil {
			valid = false
		}
		return ""
	}

	s := State{
		Channel:          field("channel", false),
		RequestedVersion: nil,
		ResolvedTag:      field("resolvedTag", false),
		AssetName:        field("assetName", false),
		Platform:         field("platform", false),
		Arch:             field("arch", false),
		Libc:             field("libc", true),
		InstallRoot:      field("installRoot", false),
		BinPath:          field("binPath", false),
		InstalledAt:      field("installedAt", false),
	}
	if o != nil {
		if requested, ok := o.Get("requestedVersion"); ok && requested != nil {
			if text, isText := requested.(string); isText {
				s.RequestedVersion = &text
			} else {
				valid = false
			}
		} else if !ok {
			valid = false
		}
		// libc is glibc, musl or null; an empty string is invalid.
		if libc, _ := o.Get("libc"); libc == "" {
			valid = false
		}
		version, _ := o.Get("schemaVersion")
		if n, ok := version.(jsjson.Number); !ok || n != 1 {
			valid = false
		}
	}
	valid = valid &&
		slices.Contains(validChannels, s.Channel) &&
		slices.Contains(validPlatforms, s.Platform) &&
		slices.Contains(validArchs, s.Arch) &&
		slices.Contains(validLibcs, s.Libc)
	if !valid {
		return State{}, fmt.Errorf("Install state at %s is invalid.", path)
	}
	return s, nil
}
