// Command tiup-backup keeps copies of the local TiUP home on backup nodes.
// It is reached as `tiup backup ...` through the shell function printed by `init-shell`.
package main

import (
	"fmt"
	"os"

	"tiup-backup/internal/cli"
)

func main() {
	if err := cli.NewRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
