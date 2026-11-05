// Package cli builds the prefactor command tree.
package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

// NewRootCommand returns the root prefactor command for the running process.
func NewRootCommand(version string) *cobra.Command {
	return newRootCommand(DefaultApp(version))
}

func newRootCommand(app *App) *cobra.Command {
	// Help lists commands in the order they are added, as Commander does.
	cobra.EnableCommandSorting = false

	root := &cobra.Command{
		Use:           "prefactor",
		Short:         "Prefactor CLI for managing Prefactor resources",
		Version:       app.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Example: strings.Join([]string{
			"  prefactor profiles add default --api-token <api-token>",
			"  prefactor setup <agent_id>",
			`  prefactor setup --create --name "<agent-name>" --description "<description>"`,
			"  prefactor setup <agent_id> --json",
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
	var help bool
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		return app.prepareRun(cmd, args, help)
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
		newAgentKnowledgebaseLinksCommand(app),
		newAgentRiskVersionsCommand(app),
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
		newSetupCommand(app),
		newBulkCommand(app),
		newUtilsCommand(app),
		newPfidCommand(app),
		newPingCommand(app),
		newVersionCommand(app),
	)
	root.AddCommand(newLifecycleCommands(app)...)

	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) { app.writeHelp(cmd) })
	argv := func() []string { return app.Argv }
	prepareCommandLine(root, &help, argv)
	useResolvedArgs(root, app)
	addHelpCommands(root, argv)
	return root
}

// prepareRun checks the command line and fills in resource IDs before a
// command runs, in this order: --version, option values, help and command
// groups, session settings, every other Commander check (allowing only the
// missing IDs a picker will supply), pickers, then @latest. When pickers may
// not run, a missing resource ID fails with the argument or flag to pass.
// profileFreeCommands are the top-level commands that do not call the API
// with the selected profile.
var profileFreeCommands = map[string]bool{
	"login": true, "profiles": true, "version": true, "help": true,
	"install": true, "update": true, "uninstall": true, "doctor": true,
}

// needsProfile reports whether cmd calls the API with the selected profile.
// ping with --api-token uses that token instead.
func needsProfile(cmd *cobra.Command) bool {
	if !cmd.Runnable() || !cmd.HasParent() {
		return false
	}
	top := cmd
	for top.Parent().HasParent() {
		top = top.Parent()
	}
	if profileFreeCommands[top.Name()] || cmd.Name() == "help" {
		return false
	}
	return top.Name() != "ping" || stringFlag(cmd, "api-token") == ""
}

func (a *App) prepareRun(cmd *cobra.Command, args []string, help bool) error {
	a.resolvedArgs = args

	if err := checkEarly(cmd, args, a.Argv, help); err != nil {
		return err
	}
	if err := a.startSession(cmd); err != nil {
		return err
	}

	// Every other check runs before any picker, so a person is never asked
	// to pick for a command that would fail anyway.
	var pickable *pickableInputs
	if a.canPick() {
		pickable = missingPickableInputs(cmd, args)
	}
	if err := checkLate(cmd, args, a.Argv, pickable); err != nil {
		return err
	}
	// The TypeScript CLI loads the profile before a command's own checks,
	// except for the few it makes first.
	if check := a.checksBeforeProfile[cmd]; check != nil {
		if err := check(cmd, args); err != nil {
			return err
		}
	}
	if needsProfile(cmd) {
		if _, _, err := a.credentials(cmd); err != nil {
			return err
		}
	}

	if pickable.any() {
		if err := a.checkCommandOptions(cmd, args, pickable); err != nil {
			return err
		}
		picked, err := a.pickMissing(cmd, args)
		if err != nil {
			return err
		}
		args = picked
	}

	resolved, err := a.resolveLatest(cmd, args)
	if err != nil {
		return err
	}
	a.resolvedArgs = resolved
	return nil
}

// errDryRun stops a command when it asks for an API client while
// checkCommandOptions runs it.
var errDryRun = errors.New("stopped before the first API call")

// checkCommandOptions runs the command up to its first API call, with the
// IDs a picker will supply left empty, so that its own checks of the other
// options fail before anyone is asked to pick.
func (a *App) checkCommandOptions(cmd *cobra.Command, args []string, pickable *pickableInputs) error {
	placeholders := slices.Clone(args)
	for pickable.args[len(placeholders)] {
		placeholders = append(placeholders, "")
	}

	a.resolvedArgs = placeholders
	a.dryRun = true
	defer func() { a.dryRun = false }()

	if err := cmd.RunE(cmd, placeholders); err != nil && !errors.Is(err, errDryRun) {
		return err
	}
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
