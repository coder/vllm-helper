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

// sudo builds a privileged invocation. Without a terminal to answer a
// password prompt, fail fast rather than block on one forever.
func (s Service) sudo(args ...string) (string, []string) {
	if s.NonInteractive {
		return "sudo", append([]string{"-n"}, args...)
	}
	return "sudo", args
}

// run executes a command. Stderr always reaches the terminal: that is where
// sudo writes its password prompt and where anything that went wrong shows
// up, and swallowing either turns a failure into an unexplained hang. out
// may be nil to discard ordinary output.
func run(out *os.File, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stderr = os.Stdin, os.Stderr
	if out != nil {
		cmd.Stdout = out
	}
	return cmd.Run()
}
