package cli

import (
	"encoding/json"
	"fmt"
	"os"
)

// stdoutIsTTY reports whether stdout is a terminal. When it isn't (pipes,
// cron, a UI backend), commands default to non-interactive behavior.
func stdoutIsTTY() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// stdinIsTTY reports whether stdin can actually answer a prompt.
func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
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
