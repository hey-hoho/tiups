// Package cli defines the `tiup backup` command tree.
package cli

import "github.com/spf13/cobra"

// NewRoot builds the root command. It is named "backup" because users reach it as `tiup backup ...`.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "backup",
		Short:         "Keep copies of ~/.tiup on backup nodes",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newAddCmd(), newRemoveCmd(), newListCmd())
	root.AddCommand(newSyncCmd(), newJobCmd(), newInitShellCmd())
	return root
}
