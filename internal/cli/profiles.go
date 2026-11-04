package cli

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/profile"
)

func newProfilesCommand(app *App) *cobra.Command {
	profiles := &cobra.Command{
		Use:   "profiles",
		Short: "Manage CLI profiles",
	}

	profiles.AddCommand(
		newProfilesListCommand(app),
		newProfilesAddCommand(app),
		newProfilesRemoveCommand(app),
	)

	return profiles
}

func newProfilesListCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured profiles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := app.loadProfiles()
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			entries := store.Entries()
			if len(entries) == 0 {
				_, err := fmt.Fprintln(out, "No profiles configured. Use 'prefactor profiles add <name> [baseUrl] --api-token <apiToken>'.")
				return err
			}

			current, _ := app.resolveProfileName(cmd)
			for _, entry := range entries {
				marker := ""
				if entry.Name == current {
					marker = " (current)"
				}
				if _, err := fmt.Fprintf(out, "%s%s - %s\n", entry.Name, marker, entry.Profile.BaseURL); err != nil {
					return err
				}
			}

			return nil
		},
	}
}

func newProfilesAddCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <name> [baseUrl]",
		Short: "Add or update a profile",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			apiToken, _ := cmd.Flags().GetString("api-token")
			if strings.TrimSpace(apiToken) == "" {
				return errors.New("Missing API token. Specify --api-token <apiToken>.")
			}

			baseURL := profile.DefaultBaseURL
			if len(args) == 2 {
				if err := validateBaseURL(args[1]); err != nil {
					return err
				}
				baseURL = args[1]
			}

			store, err := app.loadProfiles()
			if err != nil {
				return err
			}

			if err := store.Set(args[0], profile.Profile{APIKey: apiToken, BaseURL: baseURL}); err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Profile '%s' saved.\n", args[0])
			return err
		},
	}

	cmd.Flags().String("api-token", "", "API token for this profile")
	_ = cmd.MarkFlagRequired("api-token")

	return cmd
}

func newProfilesRemoveCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := app.loadProfiles()
			if err != nil {
				return err
			}

			removed, err := store.Remove(args[0])
			if err != nil {
				return err
			}

			if removed {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Profile '%s' removed.\n", args[0])
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Profile '%s' not found.\n", args[0])
			return err
		},
	}
}

// validateBaseURL accepts what the WHATWG URL parser in the TypeScript CLI
// accepts for a base URL: an absolute URL with a scheme.
func validateBaseURL(baseURL string) error {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || (parsed.Host == "" && parsed.Opaque == "") {
		return errors.New("--baseUrl must be a valid URL.")
	}

	return nil
}
