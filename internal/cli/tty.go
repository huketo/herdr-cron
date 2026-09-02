package cli

import (
	"os"

	"golang.org/x/term"
)

// isTerminal reports whether the file is an interactive terminal.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}
