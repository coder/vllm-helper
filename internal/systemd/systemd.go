// Package systemd wraps service management for the vLLM unit, honoring the
// configured scope: "system" (sudo systemctl) or "user" (systemctl --user).
package systemd

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type Service struct {
	Name  string // e.g. "vllm.service"
	Scope string // "system" or "user"

	// NonInteractive makes privileged calls fail fast (sudo -n) instead of
	// prompting for a password nothing can answer (scripts, the UI daemon).
	NonInteractive bool
}

func (s Service) systemctlArgs(args ...string) (string, []string) {
	if s.Scope == "user" {
		return "systemctl", append([]string{"--user"}, args...)
	}
	if s.NonInteractive {
		return "sudo", append([]string{"-n", "systemctl"}, args...)
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

// NRestarts returns the unit's restart counter, or -1 if it can't be read.
// A counter that climbs right after a restart means a crash loop.
func (s Service) NRestarts() int {
	args := []string{"show", "-p", "NRestarts", "--value", s.Name}
	if s.Scope == "user" {
		args = append([]string{"--user"}, args...)
	}
	out, err := exec.Command("systemctl", args...).Output()
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return -1
	}
	return n
}

// Restart restarts the unit, streaming any sudo prompt to the terminal.
func (s Service) Restart() error {
	return s.runPrivileged("restart")
}

// Stop stops the unit.
func (s Service) Stop() error {
	return s.runPrivileged("stop")
}

// Start starts the unit.
func (s Service) Start() error {
	return s.runPrivileged("start")
}

func (s Service) runPrivileged(verb string) error {
	bin, args := s.systemctlArgs(verb, s.Name)
	cmd := exec.Command(bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func (s Service) journalArgs(extra ...string) []string {
	args := []string{"-u", s.Name, "-o", "cat"}
	if s.Scope == "user" {
		args = append([]string{"--user"}, args...)
	}
	return append(args, extra...)
}

// StartTail spawns journalctl following the unit's log (last n lines of
// context first), writing to this process's stdout. The caller stops
// watching by killing the returned process; the service itself is
// unaffected.
func (s Service) StartTail(n int) (*exec.Cmd, error) {
	cmd := exec.Command("journalctl", s.journalArgs("-n", strconv.Itoa(n), "-f")...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// PrintLogs prints the last n journal lines and returns.
func (s Service) PrintLogs(n int) error {
	cmd := exec.Command("journalctl", s.journalArgs("-n", strconv.Itoa(n))...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// LastLogs returns the last n journal lines as a string (for failure
// reports); best-effort, "" if the journal isn't readable.
func (s Service) LastLogs(n int) string {
	out, err := exec.Command("journalctl", s.journalArgs("-n", strconv.Itoa(n))...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}
