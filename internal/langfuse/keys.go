// Package langfuse reads agent runs from Langfuse: it finds a project's API
// keys, checks what they can read, and queries the public API. Keys are held
// as Secret values, which never print.
package langfuse

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Environment variables the keys are read from.
const (
	PublicKeyVar = "LANGFUSE_PUBLIC_KEY"
	SecretKeyVar = "LANGFUSE_SECRET_KEY"
	BaseURLVar   = "LANGFUSE_BASE_URL"
	// HostVar is the older name for BaseURLVar, read when it is not set.
	HostVar = "LANGFUSE_HOST"
	// DefaultBaseURL is Langfuse Cloud's EU region, which the Langfuse SDKs
	// use when no host is set.
	DefaultBaseURL = "https://cloud.langfuse.com"
)

// Secret is a key that must never be printed. Formatting it with fmt, or
// encoding it as JSON, gives a placeholder; only Reveal returns the value.
type Secret struct{ value string }

// NewSecret wraps a key.
func NewSecret(value string) Secret { return Secret{value: value} }

// Reveal returns the key, for the Authorization header only.
func (s Secret) Reveal() string { return s.value }

// Empty reports whether there is no key.
func (s Secret) Empty() bool { return s.value == "" }

func (s Secret) String() string   { return "[hidden]" }
func (s Secret) GoString() string { return "[hidden]" }

// MarshalJSON keeps the key out of JSON output.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"[hidden]"`), nil }

// SourceKind is where keys were found.
type SourceKind string

// The places keys come from.
const (
	SourceEnvironment SourceKind = "environment"
	SourceFile        SourceKind = "file"
	// SourceDefault is a host that nothing set, so DefaultBaseURL is used.
	SourceDefault SourceKind = "default"
)

// Source is where keys or the host were found: the shell environment, a
// file, or the default host.
type Source struct {
	Kind SourceKind `json:"kind"`
	Path string     `json:"path,omitempty"`
}

func (s Source) String() string {
	switch s.Kind {
	case SourceEnvironment:
		return "the shell environment"
	case SourceDefault:
		return "the default"
	}
	return s.Path
}

// Keys are a Langfuse project's API keys and the host they belong to.
type Keys struct {
	PublicKey Secret
	SecretKey Secret
	// BaseURL has no trailing slash.
	BaseURL string
	Source  Source
	// HostSource is where BaseURL came from: Source, the shell environment
	// when Source is a file without a host, or the default.
	HostSource Source
}

// FindOptions says where FindKeys looks.
type FindOptions struct {
	// LookupEnv reads the shell environment.
	LookupEnv func(string) (string, bool)
	// Dir is where the search for .env files starts.
	Dir string
	// File, when set, is the only place keys are read from, in .env
	// format.
	File string
}

// ErrNoKeys is returned when no source sets both keys.
var ErrNoKeys = errors.New("no Langfuse keys found")

// NoKeysError lists the places FindKeys looked.
type NoKeysError struct {
	Searched []string
}

func (e *NoKeysError) Error() string {
	return fmt.Sprintf("No Langfuse keys found. Set %s and %s in the shell environment or a .env file, or pass --env <file>, then run the command again. Looked in: %s.",
		PublicKeyVar, SecretKeyVar, strings.Join(e.Searched, ", "))
}

func (e *NoKeysError) Unwrap() error { return ErrNoKeys }

// FindKeys looks for LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY in order:
// the shell environment, then the nearest .env from Dir up to the root of
// its git repository. An active mise puts its [env] section in the shell
// environment, so mise files are not read. The first source that sets both
// keys is used, with its LANGFUSE_BASE_URL or LANGFUSE_HOST; when a file
// sets neither, the host comes from the shell environment or is the
// default. With File set, only that file is read for the keys.
func FindKeys(opts FindOptions) (Keys, error) {
	if opts.File != "" {
		values, err := readKeyFile(opts.File)
		if err != nil {
			return Keys{}, err
		}
		keys, ok, err := keysFrom(values, Source{Kind: SourceFile, Path: opts.File}, opts.LookupEnv)
		if err != nil {
			return Keys{}, err
		}
		if !ok {
			return Keys{}, fmt.Errorf("%s does not set both %s and %s.", opts.File, PublicKeyVar, SecretKeyVar)
		}
		return keys, nil
	}

	searched := []string{"the shell environment"}
	env := envValues(opts.LookupEnv, PublicKeyVar, SecretKeyVar, BaseURLVar, HostVar)
	if keys, ok, err := keysFrom(env, Source{Kind: SourceEnvironment}, opts.LookupEnv); ok || err != nil {
		return keys, err
	}

	dirs := searchDirs(opts.Dir)
	for _, dir := range dirs {
		path := filepath.Join(dir, ".env")
		values, err := readKeyFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		searched = append(searched, path)
		if err != nil {
			return Keys{}, err
		}
		if keys, ok, err := keysFrom(values, Source{Kind: SourceFile, Path: path}, opts.LookupEnv); ok || err != nil {
			return keys, err
		}
	}
	if len(searched) == 1 {
		searched = append(searched, ".env from "+opts.Dir+" up to the repository root")
	}
	return Keys{}, &NoKeysError{Searched: searched}
}

// keysFrom returns the keys in values, and false when either key is
// missing. Without a host in values, the host comes from shellHost.
func keysFrom(values map[string]string, source Source, lookupEnv func(string) (string, bool)) (Keys, bool, error) {
	public, secret := strings.TrimSpace(values[PublicKeyVar]), strings.TrimSpace(values[SecretKeyVar])
	if public == "" || secret == "" {
		return Keys{}, false, nil
	}
	keys := Keys{PublicKey: NewSecret(public), SecretKey: NewSecret(secret), Source: source, HostSource: source}
	base := hostIn(values)
	if base == "" {
		var err error
		if keys.BaseURL, keys.HostSource, err = shellHost(lookupEnv); err != nil {
			return Keys{}, false, err
		}
		return keys, true, nil
	}
	base, err := NormalizeBaseURL(base)
	if err != nil {
		return Keys{}, false, fmt.Errorf("%s in %s: %w", BaseURLVar, source, err)
	}
	keys.BaseURL = base
	return keys, true, nil
}

// shellHost is the host LANGFUSE_BASE_URL or LANGFUSE_HOST sets in the
// shell environment, or DefaultBaseURL, and where it came from.
func shellHost(lookupEnv func(string) (string, bool)) (string, Source, error) {
	from := Source{Kind: SourceEnvironment}
	base := hostIn(envValues(lookupEnv, BaseURLVar, HostVar))
	if base == "" {
		base, from = DefaultBaseURL, Source{Kind: SourceDefault}
	}
	normalized, err := NormalizeBaseURL(base)
	if err != nil {
		return "", from, fmt.Errorf("%s in %s: %w", BaseURLVar, from, err)
	}
	return normalized, from, nil
}

// hostIn is LANGFUSE_BASE_URL in values, or LANGFUSE_HOST.
func hostIn(values map[string]string) string {
	if base := strings.TrimSpace(values[BaseURLVar]); base != "" {
		return base
	}
	return strings.TrimSpace(values[HostVar])
}

// envValues reads names from the shell environment.
func envValues(lookupEnv func(string) (string, bool), names ...string) map[string]string {
	values := map[string]string{}
	if lookupEnv == nil {
		return values
	}
	for _, name := range names {
		if value, ok := lookupEnv(name); ok {
			values[name] = value
		}
	}
	return values
}

// hipaaHost is Langfuse Cloud's HIPAA region.
const hipaaHost = "hipaa.cloud.langfuse.com"

// CheckRegion refuses Langfuse's HIPAA region, which Prefactor cannot
// import from.
func CheckRegion(baseURL string) error {
	parsed, err := url.Parse(baseURL)
	if err == nil && strings.EqualFold(parsed.Hostname(), hipaaHost) {
		return fmt.Errorf("Prefactor cannot import from Langfuse's HIPAA region (%s).", hipaaHost)
	}
	return nil
}

// NormalizeBaseURL checks a Langfuse host is an http or https URL without
// credentials, and drops a trailing slash.
func NormalizeBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err == nil && parsed.User != nil {
		return "", errors.New("the URL must not contain a user name or password")
	}
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return "", fmt.Errorf("%q is not an http or https URL", raw)
	}
	parsed.RawQuery, parsed.Fragment = "", ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

// searchDirs lists dir and its parents up to the root of the git
// repository that contains it. Outside a repository it is dir alone.
func searchDirs(dir string) []string {
	if dir == "" {
		return nil
	}
	dir = filepath.Clean(dir)
	var dirs []string
	for current := dir; ; {
		dirs = append(dirs, current)
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return dirs
		}
		parent := filepath.Dir(current)
		if parent == current {
			return []string{dir}
		}
		current = parent
	}
}

// readKeyFile reads the variables a .env file sets.
func readKeyFile(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s does not exist: %w", path, os.ErrNotExist)
		}
		return nil, fmt.Errorf("Unable to read %s: %w", path, err)
	}
	return parseDotenv(string(raw)), nil
}

// parseDotenv reads KEY=value lines, with an optional export prefix.
// Values may be in double quotes, which take \n, \", \\ and similar
// escapes, or single quotes, which are literal; an unquoted value ends at
// " #". Later lines win.
func parseDotenv(contents string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		values[key] = unquoteValue(strings.TrimSpace(value))
	}
	return values
}

// unquoteValue returns a value without its quotes. An unquoted value ends
// at " #".
func unquoteValue(value string) string {
	if value == "" {
		return ""
	}
	switch value[0] {
	case '\'':
		if end := strings.IndexByte(value[1:], '\''); end >= 0 {
			return value[1 : end+1]
		}
		return value[1:]
	case '"':
		var b strings.Builder
		for i := 1; i < len(value); i++ {
			c := value[i]
			if c == '"' {
				return b.String()
			}
			if c == '\\' && i+1 < len(value) {
				i++
				switch value[i] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				case 'r':
					b.WriteByte('\r')
				default:
					b.WriteByte(value[i])
				}
				continue
			}
			b.WriteByte(c)
		}
		return b.String()
	}
	if i := strings.Index(value, " #"); i >= 0 {
		value = value[:i]
	}
	return strings.TrimSpace(value)
}
