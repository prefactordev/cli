package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/prefactordev/cli/internal/api"
	"github.com/prefactordev/cli/internal/client"
	"github.com/prefactordev/cli/internal/profile"
)

// App holds what commands need from the process, so tests can replace it.
type App struct {
	Version string
	// ConfigPath returns the prefactor.json file to read and write.
	ConfigPath func() (string, error)
	// Getenv reads an environment variable.
	Getenv func(string) string
	Login  LoginDeps
	// StdinIsTerminal and StdoutIsTerminal report whether the streams are
	// terminals, which decides whether the CLI may prompt.
	StdinIsTerminal  func() bool
	StdoutIsTerminal func() bool

	session *session
}

// DefaultApp returns an App for the running process.
func DefaultApp(version string) *App {
	return &App{
		Version: version,
		ConfigPath: func() (string, error) {
			locator, err := profile.DefaultLocator()
			if err != nil {
				return "", err
			}
			return locator.ConfigPath(), nil
		},
		Getenv:           os.Getenv,
		Login:            defaultLoginDeps(),
		StdinIsTerminal:  func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
		StdoutIsTerminal: func() bool { return term.IsTerminal(int(os.Stdout.Fd())) },
	}
}

func (a *App) loadProfiles() (*profile.Store, error) {
	path, err := a.ConfigPath()
	if err != nil {
		return nil, err
	}

	return profile.Load(path), nil
}

// resolveProfileName returns the selected profile name and whether it fell
// back to the default, which is the only case that allows environment auth.
func (a *App) resolveProfileName(cmd *cobra.Command) (string, bool) {
	explicit, _ := cmd.Flags().GetString("profile")
	if explicit != "" {
		return explicit, false
	}

	if env := a.Getenv("PREFACTOR_PROFILE"); env != "" {
		return env, false
	}

	return profile.DefaultName, true
}

// authedClient returns an API client for the selected profile, or for
// PREFACTOR_API_TOKEN and PREFACTOR_API_URL when the default profile is
// selected and not configured.
func (a *App) authedClient(cmd *cobra.Command) (*api.Client, error) {
	store, err := a.loadProfiles()
	if err != nil {
		return nil, err
	}

	name, isDefault := a.resolveProfileName(cmd)
	if selected, ok := store.Get(name); ok {
		return client.New(selected.BaseURL, selected.APIKey, a.Version)
	}

	if token := a.Getenv("PREFACTOR_API_TOKEN"); token != "" && isDefault {
		apiURL := a.Getenv("PREFACTOR_API_URL")
		if apiURL == "" {
			apiURL = profile.DefaultBaseURL
		}
		return client.New(apiURL, token, a.Version)
	}

	return nil, fmt.Errorf(
		"No profile found for '%s'. Run 'prefactor profiles add <name> [baseUrl] --api-token <apiToken>' to configure one.",
		name,
	)
}
