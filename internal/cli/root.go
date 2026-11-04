// Package cli builds the prefactor command tree.
package cli

import (
	"fmt"
	"strings"

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
		Example: strings.Join([]string{
			"  prefactor profiles add default --api-token <api-token>",
			"  prefactor accounts list",
			"  prefactor agents list",
			"  prefactor agent-deployments create --agent-id <agent_id> --environment-id <environment_id>",
			`  prefactor agent-spans create --agent-instance-id <id> --schema-name <schema> --status active --payload '{"step":"tool"}'`,
		}, "\n"),
	}

	root.PersistentFlags().String("profile", "", "Profile name to use for commands")
	root.Flags().BoolP("version", "V", false, "output the version number")
	root.SetVersionTemplate("{{.Version}}\n")
	addSessionFlags(root)
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		return app.prepareRun(cmd, args)
	}

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
		newVersionCommand(app),
	)

	prepareCommandLine(root)
	useResolvedArgs(root, app)
	return root
}

// prepareRun checks the command line and fills in resource IDs before a
// command runs. In interactive mode a missing required resource ID opens a
// picker; otherwise the command fails with the argument or flag to pass.
func (a *App) prepareRun(cmd *cobra.Command, args []string) error {
	a.resolvedArgs = args

	if err := checkEarly(cmd, args, a.Argv); err != nil {
		return err
	}
	if err := a.startSession(cmd); err != nil {
		return err
	}

	if a.canPick() && missingPickable(cmd, args) {
		// Report a mistyped option before asking anything.
		if unknown := firstUnknownOption(cmd, a.Argv); unknown != "" {
			return unknownOptionError(cmd, unknown)
		}
		picked, err := a.pickMissing(cmd, args)
		if err != nil {
			return err
		}
		args = picked
	}

	if err := checkLate(cmd, args, a.Argv); err != nil {
		return err
	}

	resolved, err := a.resolveLatest(cmd, args)
	if err != nil {
		return err
	}
	a.resolvedArgs = resolved
	return nil
}

// useResolvedArgs makes every command run with the arguments prepareRun
// completed, since Cobra passes RunE the arguments as typed.
func useResolvedArgs(cmd *cobra.Command, app *App) {
	if run := cmd.RunE; run != nil {
		cmd.RunE = func(c *cobra.Command, _ []string) error {
			return run(c, app.resolvedArgs)
		}
	}
	for _, sub := range cmd.Commands() {
		useResolvedArgs(sub, app)
	}
}

func newVersionCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print CLI version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if app.jsonOutput() {
				return app.printJSONValue(cmd, map[string]string{"version": app.Version})
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), app.Version)
			return err
		},
	}
}
