// Package service manages the long-running vLLM server through whatever init
// system the host provides: systemd on Linux, launchd on macOS. Every
// implementation honors the configured scope, "system" or "user".
package service

import (
	"os"
	"os/exec"
)

// Service identifies the unit (systemd) or job (launchd) running the server.
type Service struct {
	Name  string // systemd unit name, or launchd label
	Scope string // "system" or "user"

	// NonInteractive makes privileged calls fail fast (sudo -n) instead of
	// prompting for a password nothing can answer (scripts, the UI daemon).
	NonInteractive bool
}

func (s Service) userScope() bool { return s.Scope == "user" }

// run executes a command, streaming any sudo password prompt to the terminal.
// out may be nil to discard output.
func run(out *os.File, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	if out != nil {
		cmd.Stdout, cmd.Stderr = out, out
	}
	return cmd.Run()
}
