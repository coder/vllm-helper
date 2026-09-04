//go:build !darwin && !linux

package cli

// fitMemUtilization is a no-op wherever we can't tell what the machine has:
// with no memory probe there is nothing to clamp against.
func fitMemUtilization(want string, _ bool) string { return want }
