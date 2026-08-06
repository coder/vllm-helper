package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"golang.org/x/term"
)

// stdoutIsTTY reports whether stdout is a terminal. When it isn't (pipes,
// cron, a UI backend), commands default to non-interactive behavior.
//
// This asks the kernel rather than reading the file mode: /dev/null is a
// character device too, so a mode check calls it a terminal and sends
// 'logs > /dev/null' into a tail that never returns.
func stdoutIsTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// stdinIsTTY reports whether stdin can actually answer a prompt.
func stdinIsTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// printJSON renders v as indented JSON on stdout.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	return nil
}
