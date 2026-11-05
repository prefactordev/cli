package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/internal/jsjson"
	"github.com/prefactordev/cli/internal/lifecycle"
)

// parseWaitForPid is the TypeScript CLI's parser for --wait-for-pid.
func parseWaitForPid(value string) (int, error) {
	pid, err := parsePositiveInt(value)
	if err != nil {
		return 0, errors.New("--wait-for-pid must be a positive integer.")
	}
	return pid, nil
}

// lifecycleDeps returns the lifecycle dependencies for cmd. In JSON mode
// the messages go to stderr, so stdout holds only the JSON result.
func (a *App) lifecycleDeps(cmd *cobra.Command) lifecycle.Deps {
	out := cmd.OutOrStdout()
	if a.jsonOutput() {
		out = cmd.ErrOrStderr()
	}
	if a.Lifecycle != nil {
		return a.Lifecycle(out, cmd.ErrOrStderr())
	}
	d := lifecycle.DefaultDeps(out, cmd.ErrOrStderr())
	d.Getenv = a.Getenv
	d.LookupEnv = a.LookupEnv
	return d
}

// printLifecycleJSON prints o as the command's JSON result in JSON mode.
func (a *App) printLifecycleJSON(cmd *cobra.Command, o *jsjson.Object) error {
	if !a.jsonOutput() {
		return nil
	}
	return a.printResult(cmd, []byte(jsjson.Stringify(o, "")))
}

func addChannelFlags(cmd *cobra.Command) {
	addChoiceFlag(cmd, "channel", "Release channel to install", lifecycle.ChannelStable, lifecycle.ChannelLatest)
	cmd.Flags().String("version", "", "Pinned release version to install")
}

func newLifecycleCommands(app *App) []*cobra.Command {
	install := &cobra.Command{
		Use:   "install",
		Short: "Install the Prefactor CLI into the managed user-local location",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := lifecycle.InstallOptions{
				Channel:      stringFlag(cmd, "channel"),
				Version:      stringFlag(cmd, "version"),
				SourceBinary: stringFlag(cmd, "source-binary"),
				ResolvedTag:  stringFlag(cmd, "resolved-tag"),
				AssetName:    stringFlag(cmd, "asset-name"),
			}
			if flagSet(cmd, "wait-for-pid") {
				pid, err := parseWaitForPid(stringFlag(cmd, "wait-for-pid"))
				if err != nil {
					return err
				}
				opts.WaitForPID = pid
			}
			state, err := lifecycle.Install(opts, app.Version, app.lifecycleDeps(cmd))
			if err != nil {
				return err
			}
			return app.printLifecycleJSON(cmd, state.Object())
		},
	}
	addChannelFlags(install)
	for _, hidden := range []struct{ name, placeholder string }{
		{"source-binary", "path"},
		{"resolved-tag", "tag"},
		{"asset-name", "asset"},
		{"wait-for-pid", "pid"},
	} {
		install.Flags().String(hidden.name, "", "")
		setPlaceholder(install, hidden.name, hidden.placeholder)
		_ = install.Flags().MarkHidden(hidden.name)
	}
	_ = install.Flags().SetAnnotation("wait-for-pid", parserAnnotation, []string{"parseWaitForPid"})

	update := &cobra.Command{
		Use:   "update",
		Short: "Update the installed Prefactor CLI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tag, err := lifecycle.Update(lifecycle.UpdateOptions{
				Channel: stringFlag(cmd, "channel"),
				Version: stringFlag(cmd, "version"),
			}, app.lifecycleDeps(cmd))
			if err != nil {
				return err
			}
			o := jsjson.NewObject()
			o.Set("resolvedTag", tag)
			return app.printLifecycleJSON(cmd, o)
		},
	}
	addChannelFlags(update)

	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the managed Prefactor CLI installation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			binPath, err := lifecycle.Uninstall(app.lifecycleDeps(cmd))
			if err != nil {
				return err
			}
			o := jsjson.NewObject()
			o.Set("removed", binPath)
			return app.printLifecycleJSON(cmd, o)
		},
	}

	doctor := &cobra.Command{
		Use:   "doctor",
		Short: "Print diagnostic information about the managed Prefactor CLI installation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report, err := lifecycle.Doctor(app.lifecycleDeps(cmd))
			if err != nil {
				return err
			}
			if app.jsonOutput() {
				o := jsjson.NewObject()
				for _, line := range report.Lines {
					o.Set(line[0], line[1])
				}
				return app.printResult(cmd, []byte(jsjson.Stringify(o, "")))
			}
			for _, line := range report.Lines {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", line[0], line[1]); err != nil {
					return err
				}
			}
			return nil
		},
	}

	return []*cobra.Command{install, update, uninstall, doctor}
}
