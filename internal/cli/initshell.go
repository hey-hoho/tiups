package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"tiup-backup/internal/shell"
)

func newInitShellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init-shell",
		Short: "Print a shell snippet that makes `tiup backup ...` run tiup-backup",
		Args:  cobra.NoArgs,
		Run: func(*cobra.Command, []string) {
			fmt.Print(shell.InitSnippet)
		},
	}
}
