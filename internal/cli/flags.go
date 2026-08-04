package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

// newFlagSet builds a flag set whose errors surface through our normal
// error path instead of os.Exit.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

// parseArgs parses fs against args with flags allowed anywhere on the
// command line (stdlib flag stops at the first positional, which makes
// "vllm-helper switch qwen --wait" fail). Returns the positional args.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if eq := strings.Index(name, "="); eq >= 0 {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			continue // let fs.Parse report the unknown flag
		}
		// Non-boolean flags consume the next arg as their value.
		if bv, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !bv.IsBoolFlag() {
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		}
	}
	if err := fs.Parse(flags); err != nil {
		return nil, err
	}
	return pos, nil
}

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
