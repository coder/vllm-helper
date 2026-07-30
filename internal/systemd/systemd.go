// Package systemd wraps service management for the vLLM unit, honoring the
// configured scope: "system" (sudo systemctl) or "user" (systemctl --user).
package systemd

import (
	"os"
	"os/exec"
	"strings"
)

type Service struct {
	Name  string // e.g. "vllm.service"
	Scope string // "system" or "user"
}

func (s Service) systemctlArgs(args ...string) (string, []string) {
	if s.Scope == "user" {
		return "systemctl", append([]string{"--user"}, args...)
	}
	return "sudo", append([]string{"systemctl"}, args...)
}

// IsActive returns systemd's activity string ("active", "inactive",
// "failed", ...) without needing privileges.
func (s Service) IsActive() string {
	args := []string{"is-active", s.Name}
	if s.Scope == "user" {
		args = append([]string{"--user"}, args...)
	}
	out, _ := exec.Command("systemctl", args...).CombinedOutput()
	state := strings.TrimSpace(string(out))
	if state == "" {
		state = "unknown"
	}
	return state
}

// Restart restarts the unit, streaming any sudo prompt to the terminal.
func (s Service) Restart() error {
	bin, args := s.systemctlArgs("restart", s.Name)
	cmd := exec.Command(bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// TailLogs follows the unit's journal until the user interrupts (Ctrl+C).
// The Ctrl+C kills only the journalctl process, not the service.
func (s Service) TailLogs() error {
	args := []string{"-u", s.Name, "-f", "-o", "cat"}
	if s.Scope == "user" {
		args = append([]string{"--user"}, args...)
	}
	cmd := exec.Command("journalctl", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
