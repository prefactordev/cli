// Package profile reads and writes CLI profiles in prefactor.json.
//
// The file format and lookup order match the TypeScript CLI, so both CLIs can
// share one file during the move.
package profile

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/prefactordev/cli/internal/jsjson"
)

const (
	// DefaultName is the profile used when no profile is selected.
	DefaultName = "default"
	// DefaultBaseURL is the Prefactor API used when a profile or the
	// environment does not set one.
	DefaultBaseURL = "https://app.prefactorai.com"

	configFilename = "prefactor.json"
)

// Profile is one entry in prefactor.json.
type Profile struct {
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url"`
}

// Entry is a named profile.
type Entry struct {
	Name    string
	Profile Profile
}

// Store holds the profiles of one prefactor.json file in JavaScript property
// order, as the TypeScript CLI keeps them.
type Store struct {
	path     string
	profiles *jsjson.Object
}

// Locator finds the prefactor.json file to use.
type Locator struct {
	// WorkingDir is the directory the search starts from.
	WorkingDir string
	// ExecutablePath is the path of the running binary.
	ExecutablePath string
}

// DefaultLocator returns a Locator for the current process. PREFACTOR_CLI_SELF_PATH
// overrides the executable path, as in the TypeScript CLI, and is used as
// given; the running binary's path has its symlinks resolved.
func DefaultLocator() (Locator, error) {
	wd, err := os.Getwd()
	if err != nil {
		return Locator{}, err
	}

	exe := os.Getenv("PREFACTOR_CLI_SELF_PATH")
	if exe == "" {
		exe, err = os.Executable()
		if err != nil {
			return Locator{}, err
		}
		exe = resolveExecutable(exe)
	}

	return Locator{WorkingDir: wd, ExecutablePath: exe}, nil
}

// resolveExecutable follows symlinks to the real binary, as Node's
// process.execPath does, so a symlinked install finds the same prefactor.json
// as the TypeScript CLI. A path that cannot be resolved is used as given.
func resolveExecutable(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// ConfigPath returns prefactor.json at the root of the git repository that
// contains WorkingDir when that file exists, and otherwise prefactor.json in
// the install root: the parent of the binary's directory when that directory
// is named bin, or the binary's directory.
func (l Locator) ConfigPath() string {
	repoConfig := filepath.Join(findDirectoryRoot(l.WorkingDir), configFilename)
	if fileExists(repoConfig) {
		return repoConfig
	}

	exeDir := filepath.Dir(filepath.Clean(l.ExecutablePath))
	if filepath.Base(exeDir) == "bin" {
		exeDir = filepath.Dir(exeDir)
	}

	return filepath.Join(exeDir, configFilename)
}

// Load reads the profiles at path like the TypeScript CLI's JSON.parse: a
// missing or unreadable file, invalid JSON (including anything after the
// value) or a value that is not an object loads as no profiles; a repeated
// name keeps its last value; entries without a string api_key and base_url
// are skipped.
func Load(path string) *Store {
	store := &Store{path: path, profiles: jsjson.NewObject()}

	raw, err := os.ReadFile(path)
	if err != nil {
		return store
	}

	parsed, err := jsjson.Parse(raw)
	if err != nil {
		return store
	}

	for name, value := range objectEntries(parsed) {
		entry, ok := value.(*jsjson.Object)
		if !ok {
			continue
		}
		apiKey, apiKeyOK := field(entry, "api_key")
		baseURL, baseURLOK := field(entry, "base_url")
		if apiKeyOK && baseURLOK {
			store.profiles.Set(name, profileObject(Profile{APIKey: apiKey, BaseURL: baseURL}))
		}
	}
	return store
}

// objectEntries yields Object.entries(value) for an object or array, in
// JavaScript order.
func objectEntries(value any) func(yield func(string, any) bool) {
	return func(yield func(string, any) bool) {
		switch v := value.(type) {
		case *jsjson.Object:
			for _, key := range v.Keys() {
				item, _ := v.Get(key)
				if !yield(key, item) {
					return
				}
			}
		case []any:
			for i, item := range v {
				if !yield(strconv.Itoa(i), item) {
					return
				}
			}
		}
	}
}

func field(obj *jsjson.Object, key string) (string, bool) {
	value, ok := obj.Get(key)
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

func profileObject(p Profile) *jsjson.Object {
	obj := jsjson.NewObject()
	obj.Set("api_key", p.APIKey)
	obj.Set("base_url", p.BaseURL)
	return obj
}

// Path returns the file the store reads and writes.
func (s *Store) Path() string {
	return s.path
}

// Entries returns the profiles in file order.
func (s *Store) Entries() []Entry {
	var entries []Entry
	for _, name := range s.profiles.Keys() {
		profile, _ := s.Get(name)
		entries = append(entries, Entry{Name: name, Profile: profile})
	}
	return entries
}

// Get returns the named profile.
func (s *Store) Get(name string) (Profile, bool) {
	value, ok := s.profiles.Get(name)
	if !ok {
		return Profile{}, false
	}
	obj := value.(*jsjson.Object)
	apiKey, _ := field(obj, "api_key")
	baseURL, _ := field(obj, "base_url")
	return Profile{APIKey: apiKey, BaseURL: baseURL}, true
}

// Set adds or replaces the named profile and saves the file. A replaced
// profile keeps its position.
func (s *Store) Set(name string, profile Profile) error {
	s.profiles.Set(name, profileObject(profile))
	return s.save()
}

// Remove deletes the named profile and saves the file. It reports whether the
// profile existed.
func (s *Store) Remove(name string) (bool, error) {
	if _, ok := s.profiles.Get(name); !ok {
		return false, nil
	}
	s.profiles.Delete(name)
	return true, s.save()
}

// save writes JSON.stringify(profiles, null, 2), as the TypeScript CLI does.
func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	if err := os.WriteFile(s.path, []byte(jsjson.Stringify(s.profiles, "  ")), 0o600); err != nil {
		return err
	}

	return ensureGitignoreEntry(s.path)
}

// ensureGitignoreEntry adds prefactor.json to the .gitignore next to the file
// when that directory is the root of a git repository.
func ensureGitignoreEntry(configPath string) error {
	if filepath.Base(configPath) != configFilename {
		return nil
	}

	dir := filepath.Dir(configPath)
	if !fileExists(filepath.Join(dir, ".git")) {
		return nil
	}

	gitignorePath := filepath.Join(dir, ".gitignore")
	contents, err := os.ReadFile(gitignorePath)
	if errors.Is(err, fs.ErrNotExist) {
		return os.WriteFile(gitignorePath, []byte(configFilename+"\n"), 0o644)
	}
	if err != nil {
		return err
	}

	for _, line := range strings.Split(string(contents), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == configFilename || trimmed == "/"+configFilename {
			return nil
		}
	}

	separator := ""
	if len(contents) > 0 && !bytes.HasSuffix(contents, []byte("\n")) {
		separator = "\n"
	}

	return os.WriteFile(gitignorePath, append(contents, []byte(separator+configFilename+"\n")...), 0o644)
}

// findDirectoryRoot returns the nearest directory at or above start that
// contains .git, or start when there is none.
func findDirectoryRoot(start string) string {
	current := filepath.Clean(start)
	for {
		if fileExists(filepath.Join(current, ".git")) {
			return current
		}

		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(start)
		}
		current = parent
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
