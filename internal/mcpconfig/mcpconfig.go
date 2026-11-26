// Package mcpconfig adds the Prefactor MCP server, which Prefactor serves
// over Streamable HTTP, to the configuration of MCP clients (Claude Code,
// Codex, Cursor and VS Code), and removes it.
//
// It edits only the prefactor entry. Other servers, settings, key order and
// number spelling are kept, the file is backed up before it changes, and an
// install that would change nothing leaves the file alone. A file the
// package cannot read safely, such as JSON with comments, is refused rather
// than rewritten.
package mcpconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ServerName is the name of the entry in every client's configuration.
const ServerName = "prefactor"

// TokenEnvVar is the environment variable that holds the read-only token,
// in clients that read it from the environment.
const TokenEnvVar = "PREFACTOR_MCP_TOKEN"

// VSCodeInputID is the id of the VS Code input that asks for the token.
// VS Code stores the answer in its secret storage.
const VSCodeInputID = "prefactor-mcp-token"

// BackupSuffix is added to a file's name for the copy made before it
// changes. Each change replaces the previous copy.
const BackupSuffix = ".prefactor.bak"

// Client is an MCP client this package can configure.
type Client string

// The clients, named as prefactor mcp install --client takes them.
const (
	ClaudeCode Client = "claude-code"
	Codex      Client = "codex"
	Cursor     Client = "cursor"
	VSCode     Client = "vscode"
)

// Clients lists every client in the order commands report them.
var Clients = []Client{ClaudeCode, Codex, Cursor, VSCode}

// Name returns the client's product name.
func (c Client) Name() string {
	switch c {
	case ClaudeCode:
		return "Claude Code"
	case Codex:
		return "Codex"
	case Cursor:
		return "Cursor"
	case VSCode:
		return "VS Code"
	}
	return string(c)
}

// Scope says whether the configuration is the person's own or a project's.
type Scope int

// The scopes.
const (
	Global Scope = iota
	Project
)

// Entry is the server entry to write: the URL of the Prefactor MCP server.
//
// Without Token, the entry has no credentials, so the client signs the
// person in through Prefactor with OAuth. With Token, the client sends a
// read-only API token as a bearer token, read from TokenEnvVar (or, in VS
// Code, from a prompt). The token itself is never written.
type Entry struct {
	URL   string
	Token bool
}

func (e Entry) check() error {
	if e.URL == "" {
		return errors.New("the MCP server entry needs a URL")
	}
	return nil
}

// Locations are the directories configuration paths are built from.
type Locations struct {
	// Home is the person's home directory.
	Home string
	// Project is the root of the current project.
	Project string
	// GOOS is the operating system, as runtime.GOOS names it.
	GOOS string
	// Getenv reads CODEX_HOME, CLAUDE_CONFIG_DIR, XDG_CONFIG_HOME and
	// APPDATA. Nil reads nothing.
	Getenv func(string) string
}

func (l Locations) env(name string) string {
	if l.Getenv == nil {
		return ""
	}
	return l.Getenv(name)
}

// Path returns the file that holds client's MCP servers for scope.
//
//   - Codex: config.toml in CODEX_HOME (default ~/.codex), or
//     .codex/config.toml in a project, which Codex reads for trusted
//     projects.
//   - Cursor: ~/.cursor/mcp.json, or .cursor/mcp.json in a project.
//   - Claude Code: ~/.claude.json (in CLAUDE_CONFIG_DIR when set), which
//     holds user-scope servers, or .mcp.json in a project.
//   - VS Code: mcp.json in the user settings directory, or .vscode/mcp.json
//     in a project.
func Path(client Client, scope Scope, loc Locations) (string, error) {
	if scope == Project {
		switch client {
		case Codex:
			return filepath.Join(loc.Project, ".codex", "config.toml"), nil
		case Cursor:
			return filepath.Join(loc.Project, ".cursor", "mcp.json"), nil
		case ClaudeCode:
			return filepath.Join(loc.Project, ".mcp.json"), nil
		case VSCode:
			return filepath.Join(loc.Project, ".vscode", "mcp.json"), nil
		}
		return "", unknownClient(client)
	}

	switch client {
	case Codex:
		dir := loc.env("CODEX_HOME")
		if dir == "" {
			dir = filepath.Join(loc.Home, ".codex")
		}
		return filepath.Join(dir, "config.toml"), nil
	case Cursor:
		return filepath.Join(loc.Home, ".cursor", "mcp.json"), nil
	case ClaudeCode:
		if dir := loc.env("CLAUDE_CONFIG_DIR"); dir != "" {
			return filepath.Join(dir, ".claude.json"), nil
		}
		return filepath.Join(loc.Home, ".claude.json"), nil
	case VSCode:
		dir, err := VSCodeUserDir(loc)
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "mcp.json"), nil
	}
	return "", unknownClient(client)
}

// VSCodeUserDir returns VS Code's user settings directory.
func VSCodeUserDir(loc Locations) (string, error) {
	switch loc.GOOS {
	case "darwin":
		return filepath.Join(loc.Home, "Library", "Application Support", "Code", "User"), nil
	case "windows":
		appData := loc.env("APPDATA")
		if appData == "" {
			return "", errors.New("APPDATA is not set, so the VS Code settings directory is unknown")
		}
		return filepath.Join(appData, "Code", "User"), nil
	default:
		dir := loc.env("XDG_CONFIG_HOME")
		if dir == "" {
			dir = filepath.Join(loc.Home, ".config")
		}
		return filepath.Join(dir, "Code", "User"), nil
	}
}

func unknownClient(client Client) error {
	return fmt.Errorf("unknown MCP client %q", client)
}

// Result says what Install or Uninstall did to a file.
type Result struct {
	// Changed is false when the file already had the wanted content.
	Changed bool
	// Backup is the copy of the file before it changed, or empty when the
	// file did not exist or did not change.
	Backup string
}

// Snippet returns the configuration for entry as it would appear in a new
// file for client.
func Snippet(client Client, entry Entry) (string, error) {
	if err := entry.check(); err != nil {
		return "", err
	}
	if client == Codex {
		return codexBlock(entry), nil
	}
	f, err := jsonFormatFor(client)
	if err != nil {
		return "", err
	}
	out, err := f.apply(nil, &entry)
	return string(out), err
}

// Install adds entry to client's file at path, replacing an earlier
// prefactor entry.
func Install(client Client, path string, entry Entry) (Result, error) {
	if err := entry.check(); err != nil {
		return Result{}, err
	}
	return edit(client, path, &entry)
}

// Uninstall removes the prefactor entry from client's file at path. A
// missing file or entry is not an error.
func Uninstall(client Client, path string) (Result, error) {
	return edit(client, path, nil)
}

// edit applies entry (or removes the entry, when nil) to the file and
// writes it when its content changes.
func edit(client Client, path string, entry *Entry) (Result, error) {
	old, mode, exists, err := readFile(path)
	if err != nil {
		return Result{}, err
	}
	if !exists && entry == nil {
		return Result{}, nil
	}

	var updated []byte
	if client == Codex {
		updated, err = editCodex(old, entry)
	} else {
		var f jsonFormat
		if f, err = jsonFormatFor(client); err == nil {
			updated, err = f.apply(old, entry)
		}
	}
	if err != nil {
		return Result{}, fmt.Errorf("could not update %s: %w", path, err)
	}
	if exists && bytes.Equal(old, updated) {
		return Result{}, nil
	}

	res := Result{Changed: true}
	if exists {
		res.Backup = path + BackupSuffix
		if err := writeAtomic(res.Backup, old, mode); err != nil {
			return Result{}, fmt.Errorf("could not back up %s: %w", path, err)
		}
	} else {
		mode = 0o644
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{}, err
	}
	if err := writeAtomic(path, updated, mode); err != nil {
		return Result{}, fmt.Errorf("could not write %s: %w", path, err)
	}
	return res, nil
}

func readFile(path string) ([]byte, fs.FileMode, bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, fmt.Errorf("%s is not a regular file", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false, err
	}
	return raw, info.Mode().Perm(), true, nil
}

// writeAtomic writes data to a temporary file next to path and renames it
// over path, so a client never reads a half-written file. A symlink at path
// is followed, so the file it points to is the one replaced.
func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
