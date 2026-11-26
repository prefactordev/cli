package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/mcpconfig"
	"github.com/prefactordev/cli/internal/profile"
)

func newMCPCommand(app *App) *cobra.Command {
	group := &cobra.Command{
		Use:   "mcp",
		Short: "Connect AI assistants to Prefactor through its read-only MCP server",
		Long: "Connect AI assistants to Prefactor through its read-only MCP server, which Prefactor serves at <base URL>/mcp.\n\n" +
			"Use the MCP tools to ask about agents, runs, judge verdicts and alerts. " +
			"Use the CLI's other commands to make changes, and in scripts and CI.",
	}
	group.AddCommand(
		newMCPInstallCommand(app),
		newMCPConfigCommand(app),
		newMCPUninstallCommand(app),
		newMCPTokenCommand(app),
	)
	return group
}

// mcpInstall is one client file that install or uninstall looked at.
type mcpInstall struct {
	Client  string `json:"client"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
	Backup  string `json:"backup,omitempty"`
	Error   string `json:"error,omitempty"`
}

// addMCPClientFlags registers the flags install, config and uninstall share.
func addMCPClientFlags(cmd *cobra.Command, clientUsage, defaultClients string) {
	choices := "choices: " + strings.Join(mcpClientIDs(), ", ")
	if defaultClients != "" {
		choices += "; default: " + defaultClients
	}
	cmd.Flags().StringSlice("client", nil, clientUsage+" ("+choices+")")
	cmd.Flags().Lookup("client").DefValue = ""
	setPlaceholder(cmd, "client", "client")
	cmd.Flags().Bool("project", false, "Use the current project's configuration")
	cmd.Flags().Bool("global", false, "Use your own configuration in the home directory (the default)")
}

// The ways a client can sign in to the MCP server, as --auth takes them.
const (
	mcpAuthOAuth    = "oauth"
	mcpAuthAPIToken = "api-token"
)

// addMCPEntryFlags registers the flags that decide the entry written.
func addMCPEntryFlags(cmd *cobra.Command) {
	addChoiceFlag(cmd, "auth", "How the client signs in: oauth, in the browser (the default), or api-token, "+
		"with a read-only API token from the "+mcpconfig.TokenEnvVar+" environment variable", mcpAuthOAuth, mcpAuthAPIToken)
	setPlaceholder(cmd, "auth", "method")
}

// mcpAuth returns the --auth method, oauth when it is not given.
func mcpAuth(cmd *cobra.Command) string {
	if auth := stringFlag(cmd, "auth"); auth != "" {
		return auth
	}
	return mcpAuthOAuth
}

func mcpClientIDs() []string {
	ids := make([]string, len(mcpconfig.Clients))
	for i, c := range mcpconfig.Clients {
		ids[i] = string(c)
	}
	return ids
}

// mcpScope reads --project and --global.
func mcpScope(cmd *cobra.Command) (mcpconfig.Scope, error) {
	if boolFlag(cmd, "project") && boolFlag(cmd, "global") {
		return 0, errors.New("Pass --project or --global, not both.")
	}
	if boolFlag(cmd, "project") {
		return mcpconfig.Project, nil
	}
	return mcpconfig.Global, nil
}

// mcpClients returns the clients named by --client, or, when there are
// none, the result of fallback.
func mcpClients(cmd *cobra.Command, fallback func() ([]mcpconfig.Client, error)) ([]mcpconfig.Client, error) {
	ids, _ := cmd.Flags().GetStringSlice("client")
	var clients []mcpconfig.Client
	for _, id := range ids {
		c := mcpconfig.Client(id)
		if !slices.Contains(mcpconfig.Clients, c) {
			return nil, fmt.Errorf("--client must be one of: %s.", strings.Join(mcpClientIDs(), ", "))
		}
		if !slices.Contains(clients, c) {
			clients = append(clients, c)
		}
	}
	if len(clients) > 0 {
		return clients, nil
	}
	return fallback()
}

// mcpLocations returns the directories client configuration lives in.
func (a *App) mcpLocations() (mcpconfig.Locations, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return mcpconfig.Locations{}, err
	}
	wd, err := os.Getwd()
	if err != nil {
		return mcpconfig.Locations{}, err
	}
	return mcpconfig.Locations{Home: home, Project: profile.ProjectRoot(wd), GOOS: runtime.GOOS, Getenv: a.Getenv}, nil
}

// detectMCPClients returns the clients installed for this person, found as
// prefactor skills install finds coding agents: by their directory in the
// home directory. VS Code is found by its settings directory.
func detectMCPClients(loc mcpconfig.Locations) ([]mcpconfig.Client, error) {
	var found []mcpconfig.Client
	for _, c := range mcpconfig.Clients {
		var dir string
		if c == mcpconfig.VSCode {
			dir, _ = mcpconfig.VSCodeUserDir(loc)
		} else if i := slices.IndexFunc(skillAgents, func(a skillAgent) bool { return a.id == string(c) }); i >= 0 {
			dir = filepath.Join(loc.Home, filepath.FromSlash(skillAgents[i].detect))
		}
		if c == mcpconfig.Codex && loc.Getenv != nil && loc.Getenv("CODEX_HOME") != "" {
			dir = loc.Getenv("CODEX_HOME")
		}
		if dir == "" {
			continue
		}
		if _, err := os.Stat(dir); err == nil {
			found = append(found, c)
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("No MCP client found in %s. Pass --client with one of: %s.", loc.Home, strings.Join(mcpClientIDs(), ", "))
	}
	return found, nil
}

// mcpServerURL returns the URL of the MCP server for the selected profile:
// <base URL>/mcp. When the default profile is selected and not configured,
// it uses PREFACTOR_API_URL or Prefactor's usual URL, since OAuth needs no
// token. It returns the profile's name too, or empty without one.
func (a *App) mcpServerURL(cmd *cobra.Command) (serverURL, profileName string, err error) {
	path, err := a.mcpConfigPath(cmd)
	if err != nil {
		return "", "", err
	}
	name, isDefault := a.resolveProfileName(cmd)
	var baseURL string
	if p, ok := profile.Load(path).Get(name); ok {
		baseURL, profileName = p.BaseURL, name
	} else if isDefault {
		if baseURL = a.Getenv("PREFACTOR_API_URL"); baseURL == "" {
			baseURL = profile.DefaultBaseURL
		}
	} else {
		return "", "", fmt.Errorf("No profile found for '%s' in %s. Run 'prefactor login --profile %s' first, or 'prefactor profiles add <name> [baseUrl] --api-token <apiToken>'.", name, path, name)
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", "", fmt.Errorf("The base URL %q is not an http or https URL.", baseURL)
	}
	// The API client uses only the base URL's origin, and so does this.
	return u.Scheme + "://" + u.Host + "/mcp", profileName, nil
}

func newMCPInstallCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Add the Prefactor MCP server to Claude Code, Codex, Cursor or VS Code",
		Long: "Add the Prefactor MCP server to the MCP clients found on this machine, or those given with --client.\n\n" +
			"Each client connects to the MCP server Prefactor serves at the selected profile's base URL followed by /mcp. " +
			"By default the entry holds no credentials: the client signs you in to Prefactor in the browser the first " +
			"time it connects, and keeps its own sign-in.\n\n" +
			"With --auth api-token, for clients or CI that cannot sign in through a browser, the client sends a " +
			"read-only API token instead. The token is created now if the profile has none, lasts 90 days, and is " +
			"kept in the profile. Clients read it from the " + mcpconfig.TokenEnvVar + " environment variable " +
			"(VS Code asks for it once); 'prefactor mcp token' prints it. No token is written to any client's configuration.\n\n" +
			"Only the prefactor entry changes, and an entry written by an earlier version of this command is replaced. " +
			"Each file is backed up next to itself with a .prefactor.bak suffix before it changes, and running the " +
			"command again changes nothing.\n\n" +
			"Files changed, with --global (the default) and with --project:\n" +
			"  claude-code  ~/.claude.json; .mcp.json\n" +
			"  codex        ~/.codex/config.toml; .codex/config.toml (trusted projects)\n" +
			"  cursor       ~/.cursor/mcp.json; .cursor/mcp.json\n" +
			"  vscode       mcp.json in the VS Code user directory; .vscode/mcp.json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.installMCP(cmd)
		},
	}
	addMCPClientFlags(cmd, "MCP client to configure, repeatable", "the clients found on this machine")
	addMCPEntryFlags(cmd)
	return cmd
}

func (a *App) installMCP(cmd *cobra.Command) error {
	scope, err := mcpScope(cmd)
	if err != nil {
		return err
	}
	loc, err := a.mcpLocations()
	if err != nil {
		return err
	}
	clients, err := mcpClients(cmd, func() ([]mcpconfig.Client, error) { return detectMCPClients(loc) })
	if err != nil {
		return err
	}
	auth := mcpAuth(cmd)

	var creds mcpCredentials
	if auth == mcpAuthAPIToken {
		if creds, err = a.mcpToken(cmd); err != nil {
			return err
		}
	}
	serverURL, profileName, err := a.mcpServerURL(cmd)
	if err != nil {
		return err
	}
	entry := mcpconfig.Entry{URL: serverURL, Token: auth == mcpAuthAPIToken}

	installs, failed := applyMCP(clients, scope, loc, func(c mcpconfig.Client, path string) (mcpconfig.Result, error) {
		return mcpconfig.Install(c, path, entry)
	})

	if a.jsonOutput() {
		result := map[string]any{
			"profile":  profileName,
			"url":      serverURL,
			"auth":     auth,
			"installs": installs,
		}
		if entry.Token {
			result["token_id"] = creds.id
			result["token_expires_at"] = creds.expiresAt
			result["token_created"] = creds.created
			result["token_env_var"] = mcpconfig.TokenEnvVar
		}
		if err := a.printJSONValue(cmd, result); err != nil {
			return err
		}
		return failed
	}

	out := cmd.OutOrStdout()
	for _, in := range installs {
		switch {
		case in.Error != "":
			fmt.Fprintf(cmd.ErrOrStderr(), "Could not configure %s: %s\n", in.Name, in.Error)
		case !in.Changed:
			fmt.Fprintf(out, "%s already has the prefactor MCP server in %s\n", in.Name, in.Path)
		case in.Backup != "":
			fmt.Fprintf(out, "Added the prefactor MCP server to %s in %s (backup: %s)\n", in.Name, in.Path, in.Backup)
		default:
			fmt.Fprintf(out, "Added the prefactor MCP server to %s in %s\n", in.Name, in.Path)
		}
	}
	fmt.Fprintln(out)

	if !entry.Token {
		fmt.Fprintf(out, "It connects to %s and signs you in to Prefactor in the browser.\n", serverURL)
		for _, c := range clients {
			if hint := mcpSignInHint(c); hint != "" {
				fmt.Fprintf(out, "  %-12s %s\n", c.Name()+":", hint)
			}
		}
		fmt.Fprintln(out, "Restart the clients, or reload their MCP servers, to load it.")
		return failed
	}

	tokenNote := "the profile's own read-only token"
	if creds.id != "" {
		tokenNote = "read-only API token " + creds.id
		if creds.created {
			tokenNote = "a new " + tokenNote
		}
	}
	fmt.Fprintf(out, "It connects to %s for profile '%s' with %s.", serverURL, creds.profile, tokenNote)
	if creds.id != "" {
		fmt.Fprintf(out, " Revoke it with 'prefactor api-tokens revoke %s'.", creds.id)
	}
	fmt.Fprintln(out)
	if slices.ContainsFunc(clients, func(c mcpconfig.Client) bool { return c != mcpconfig.VSCode }) {
		fmt.Fprintf(out, "Set %s before starting the clients, for example in your shell profile:\n", mcpconfig.TokenEnvVar)
		fmt.Fprintf(out, "  export %s=\"$(prefactor mcp token%s)\"\n", mcpconfig.TokenEnvVar, profileFlagSuffix(cmd, a))
	}
	if slices.Contains(clients, mcpconfig.VSCode) {
		fmt.Fprintf(out, "VS Code asks for the token the first time it starts the server; 'prefactor mcp token%s' prints it.\n", profileFlagSuffix(cmd, a))
	}
	fmt.Fprintln(out, "Restart the clients, or reload their MCP servers, to load it.")
	return failed
}

// mcpSignInHint says how to start the browser sign-in in a client.
func mcpSignInHint(c mcpconfig.Client) string {
	switch c {
	case mcpconfig.ClaudeCode:
		return "run /mcp in Claude Code, choose prefactor, then Authenticate"
	case mcpconfig.Codex:
		return "run 'codex mcp login " + mcpconfig.ServerName + "'"
	case mcpconfig.Cursor:
		return "open Cursor Settings, then MCP, and choose Connect next to prefactor"
	case mcpconfig.VSCode:
		return "start the server, then accept the sign-in prompt"
	}
	return ""
}

// profileFlagSuffix returns " --profile <name>" when a profile other than
// the default is selected.
func profileFlagSuffix(cmd *cobra.Command, a *App) string {
	if name, isDefault := a.resolveProfileName(cmd); !isDefault {
		return " --profile " + name
	}
	return ""
}

// applyMCP runs change on each client's file and returns what happened,
// and an error when any client failed. One failure does not stop the
// others.
func applyMCP(clients []mcpconfig.Client, scope mcpconfig.Scope, loc mcpconfig.Locations, change func(mcpconfig.Client, string) (mcpconfig.Result, error)) ([]mcpInstall, error) {
	installs := make([]mcpInstall, 0, len(clients))
	failed := false
	for _, c := range clients {
		in := mcpInstall{Client: string(c), Name: c.Name()}
		path, err := mcpconfig.Path(c, scope, loc)
		if err == nil {
			in.Path = path
			var res mcpconfig.Result
			res, err = change(c, path)
			in.Changed, in.Backup = res.Changed, res.Backup
		}
		if err != nil {
			in.Error = err.Error()
			failed = true
		}
		installs = append(installs, in)
	}
	if failed {
		// Each failure has been reported; the run only needs to fail.
		return installs, &exitError{code: 1}
	}
	return installs, nil
}

func newMCPConfigCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Print an MCP client's configuration for the Prefactor MCP server",
		Long: "Print the configuration 'prefactor mcp install' would add for one client, without writing it, " +
			"for people who manage client configuration themselves. It makes no request to Prefactor. " +
			"With --auth api-token, run 'prefactor mcp token' to create the read-only token and print it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			scope, err := mcpScope(cmd)
			if err != nil {
				return err
			}
			clients, err := mcpClients(cmd, func() ([]mcpconfig.Client, error) {
				return nil, fmt.Errorf("Pass --client with one of: %s.", strings.Join(mcpClientIDs(), ", "))
			})
			if err != nil {
				return err
			}
			if len(clients) > 1 {
				return errors.New("Pass one --client.")
			}
			serverURL, _, err := app.mcpServerURL(cmd)
			if err != nil {
				return err
			}
			entry := mcpconfig.Entry{URL: serverURL, Token: mcpAuth(cmd) == mcpAuthAPIToken}
			snippet, err := mcpconfig.Snippet(clients[0], entry)
			if err != nil {
				return err
			}
			loc, err := app.mcpLocations()
			if err != nil {
				return err
			}
			path, err := mcpconfig.Path(clients[0], scope, loc)
			if err != nil {
				return err
			}
			if app.jsonOutput() {
				return app.printJSONValue(cmd, map[string]string{"client": string(clients[0]), "path": path, "config": snippet})
			}
			_, err = io.WriteString(cmd.OutOrStdout(), snippet)
			return err
		},
	}
	addMCPClientFlags(cmd, "MCP client to print the configuration for", "")
	addMCPEntryFlags(cmd)
	return cmd
}

func newMCPUninstallCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the Prefactor MCP server from MCP clients",
		Long: "Remove the prefactor entry from each client's configuration, leaving everything else as it is. " +
			"This includes entries written by earlier versions of 'prefactor mcp install', which ran a local " +
			"server. Each file is backed up next to itself with a .prefactor.bak suffix before it changes. " +
			"Without --client, every client is checked. A read-only token stays in the profile; " +
			"revoke it with 'prefactor api-tokens revoke <id>'. A client's own OAuth sign-in stays with the client.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			scope, err := mcpScope(cmd)
			if err != nil {
				return err
			}
			named := flagSet(cmd, "client")
			clients, err := mcpClients(cmd, func() ([]mcpconfig.Client, error) { return mcpconfig.Clients, nil })
			if err != nil {
				return err
			}
			loc, err := app.mcpLocations()
			if err != nil {
				return err
			}
			installs, failed := applyMCP(clients, scope, loc, mcpconfig.Uninstall)

			if app.jsonOutput() {
				if err := app.printJSONValue(cmd, map[string]any{"uninstalls": installs}); err != nil {
					return err
				}
				return failed
			}
			out := cmd.OutOrStdout()
			removed := false
			for _, in := range installs {
				switch {
				case in.Error != "":
					fmt.Fprintf(cmd.ErrOrStderr(), "Could not update %s: %s\n", in.Name, in.Error)
				case in.Changed:
					removed = true
					fmt.Fprintf(out, "Removed the prefactor MCP server from %s in %s (backup: %s)\n", in.Name, in.Path, in.Backup)
				case named:
					fmt.Fprintf(out, "%s has no prefactor MCP server in %s\n", in.Name, in.Path)
				}
			}
			if !removed && !named && failed == nil {
				fmt.Fprintln(out, "No client has the prefactor MCP server.")
			}
			return failed
		},
	}
	addMCPClientFlags(cmd, "MCP client to remove the server from, repeatable", "every client")
	return cmd
}

func newMCPTokenCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Print the profile's read-only token for MCP clients",
		Long: "Print the read-only API token that clients installed with 'prefactor mcp install --auth api-token' send, " +
			"creating it if the profile has none, or replacing it when it has been revoked or is within 7 days of expiring:\n\n" +
			"  export " + mcpconfig.TokenEnvVar + "=\"$(prefactor mcp token)\"\n\n" +
			"It never prints a token with full access, which the MCP server refuses.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			creds, err := app.mcpToken(cmd)
			if err != nil {
				return err
			}
			if app.jsonOutput() {
				return app.printJSONValue(cmd, map[string]any{
					"profile":    creds.profile,
					"token":      creds.token,
					"id":         creds.id,
					"expires_at": creds.expiresAt,
					"created":    creds.created,
				})
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), creds.token)
			return err
		},
	}
	cmd.Flags().String("config", "", "Profiles file to read instead of the usual prefactor.json")
	setPlaceholder(cmd, "config", "file")
	return cmd
}
