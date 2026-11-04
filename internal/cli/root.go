// Package cli builds the prefactor command tree.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewRootCommand returns the root prefactor command with every subcommand registered.
func NewRootCommand(version string) *cobra.Command {
	root := &cobra.Command{
		Use:           "prefactor",
		Short:         "Prefactor CLI for managing Prefactor resources",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().String("profile", "", "Profile name to use for commands")

	root.AddCommand(newVersionCommand(version))

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
