// Package cli builds the prefactor command tree.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewRootCommand returns the root prefactor command for the running process.
func NewRootCommand(version string) *cobra.Command {
	return newRootCommand(DefaultApp(version))
}

func newRootCommand(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:           "prefactor",
		Short:         "Prefactor CLI for managing Prefactor resources",
		Version:       app.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().String("profile", "", "Profile name to use for commands")

	root.AddCommand(
		newLoginCommand(app),
		newProfilesCommand(app),
		newAgentsCommand(app),
		newKnowledgebasesCommand(app),
		newKnowledgebaseDocumentsCommand(app),
		newKnowledgebaseDocumentRevisionsCommand(app),
		newKnowledgebaseRevisionsCommand(app),
		newAgentVersionsCommand(app),
		newAgentSchemaVersionsCommand(app),
		newAgentDeploymentsCommand(app),
		newAgentAssociationsCommand(app),
		newAgentInstanceAssociationsCommand(app),
		newJudgesCommand(app),
		newLlmProvidersCommand(app),
		newLlmProviderModelsCommand(app),
		newAccountsCommand(app),
		newEnvironmentsCommand(app),
		newTeamsCommand(app),
		newPeopleCommand(app),
		newRiskProfilesCommand(app),
		newIntegrationsCommand(app),
		newAPITokensCommand(app),
		newAdminUsersCommand(app),
		newAdminUserInvitesCommand(app),
		newAgentInstancesCommand(app),
		newAgentSpansCommand(app),
		newAlertsCommand(app),
		newBulkCommand(app),
		newPfidCommand(app),
		newPingCommand(app),
		newVersionCommand(app.Version),
	)

	return root
}

func newVersionCommand(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print CLI version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version)
			return err
		},
	}
}
