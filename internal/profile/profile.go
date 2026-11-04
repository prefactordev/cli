// Package profile reads and writes CLI profiles in prefactor.json.
//
// The file format and lookup order match the TypeScript CLI, so both CLIs can
// share one file during the move.
package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

// Store holds the profiles of one prefactor.json file, in file order.
type Store struct {
	path    string
	entries []Entry
}

// Locator finds the prefactor.json file to use.
type Locator struct {
	// WorkingDir is the directory the search starts from.
	WorkingDir string
	// ExecutablePath is the path of the running binary.
	ExecutablePath string
}

// DefaultLocator returns a Locator for the current process. PREFACTOR_CLI_SELF_PATH
// overrides the executable path, as in the TypeScript CLI.
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
	}

	return Locator{WorkingDir: wd, ExecutablePath: exe}, nil
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

// Load reads the profiles at path. A missing or unreadable file, or entries
// without a string api_key and base_url, load as no profiles, as in the
// TypeScript CLI.
func Load(path string) *Store {
	store := &Store{path: path}

	raw, err := os.ReadFile(path)
	if err != nil {
		return store
	}

	entries, err := parseEntries(raw)
	if err != nil {
		return store
	}

	store.entries = entries
	return store
}

// Path returns the file the store reads and writes.
func (s *Store) Path() string {
	return s.path
}

// Entries returns the profiles in file order.
func (s *Store) Entries() []Entry {
	return append([]Entry(nil), s.entries...)
}

// Get returns the named profile.
func (s *Store) Get(name string) (Profile, bool) {
	for _, entry := range s.entries {
		if entry.Name == name {
			return entry.Profile, true
		}
	}

	return Profile{}, false
}

// Set adds or replaces the named profile and saves the file. A new profile
// goes at the end; a replaced one keeps its position.
func (s *Store) Set(name string, profile Profile) error {
	replaced := false
	for i := range s.entries {
		if s.entries[i].Name == name {
			s.entries[i].Profile = profile
			replaced = true
			break
		}
	}

	if !replaced {
		s.entries = append(s.entries, Entry{Name: name, Profile: profile})
	}

	return s.save()
}

// Remove deletes the named profile and saves the file. It reports whether the
// profile existed.
func (s *Store) Remove(name string) (bool, error) {
	for i := range s.entries {
		if s.entries[i].Name == name {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return true, s.save()
		}
	}

	return false, nil
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	data, err := encodeEntries(s.entries)
	if err != nil {
		return err
	}

	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return err
	}

	return ensureGitignoreEntry(s.path)
}

// parseEntries decodes the top-level object in file order and keeps the
// entries that have a string api_key and base_url.
func parseEntries(raw []byte) ([]Entry, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))

	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("profiles file is not a JSON object")
	}

	var entries []Entry
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, _ := keyToken.(string)

		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}

		var fields map[string]any
		if err := json.Unmarshal(value, &fields); err != nil {
			continue
		}

		apiKey, apiKeyOK := fields["api_key"].(string)
		baseURL, baseURLOK := fields["base_url"].(string)
		if !apiKeyOK || !baseURLOK {
			continue
		}

		entries = append(entries, Entry{Name: name, Profile: Profile{APIKey: apiKey, BaseURL: baseURL}})
	}

	return entries, nil
}

// encodeEntries writes the profiles as a two-space indented JSON object in
// order, matching JSON.stringify(profiles, null, 2) in the TypeScript CLI.
func encodeEntries(entries []Entry) ([]byte, error) {
	if len(entries) == 0 {
		return []byte("{}"), nil
	}

	var buf bytes.Buffer
	buf.WriteString("{\n")
	for i, entry := range entries {
		name, err := marshalJSON(entry.Name, "")
		if err != nil {
			return nil, err
		}
		value, err := marshalJSON(entry.Profile, "  ")
		if err != nil {
			return nil, err
		}

		fmt.Fprintf(&buf, "  %s: %s", name, value)
		if i < len(entries)-1 {
			buf.WriteString(",")
		}
		buf.WriteString("\n")
	}
	buf.WriteString("}")

	return buf.Bytes(), nil
}

// marshalJSON encodes value like JSON.stringify: no HTML escaping, and with
// two-space indentation after prefix when prefix is set.
func marshalJSON(value any, prefix string) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if prefix != "" {
		encoder.SetIndent(prefix, "  ")
	}
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}

	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
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
