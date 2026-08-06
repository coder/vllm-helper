package service

import (
	"strings"
	"testing"
)

func TestUserScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		scope string
		want  bool
	}{
		{"user", true},
		{"system", false},
		{"", false},
		{"User", false},
	}
	for _, tt := range tests {
		t.Run(tt.scope, func(t *testing.T) {
			t.Parallel()
			if got := (Service{Scope: tt.scope}).userScope(); got != tt.want {
				t.Errorf("userScope() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Without a terminal to type a password into, sudo has to fail instead of
// blocking on a prompt forever.
func TestSudoFailsFastWithoutATerminal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		nonInteractive bool
		want           []string
	}{
		{"interactive", false, []string{"cp", "/a", "/b"}},
		{"non-interactive", true, []string{"-n", "cp", "/a", "/b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			bin, args := (Service{NonInteractive: tt.nonInteractive}).sudo("cp", "/a", "/b")
			if bin != "sudo" {
				t.Errorf("bin = %q, want sudo", bin)
			}
			if strings.Join(args, " ") != strings.Join(tt.want, " ") {
				t.Errorf("args = %v, want %v", args, tt.want)
			}
		})
	}
}
