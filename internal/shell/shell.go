// Package shell holds small POSIX-shell helpers shared by several packages.
package shell

import "strings"

// Quote returns s as a single-quoted POSIX shell word.
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// InitSnippet is the shell function that routes `tiup backup ...` to the
// tiup-backup binary (tiup only runs components listed in its signed mirror).
const InitSnippet = `# add to ~/.bashrc or ~/.zshrc
tiup() {
  if [ "$1" = "backup" ]; then
    shift
    command tiup-backup "$@"
  else
    command tiup "$@"
  fi
}
`
