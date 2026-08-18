//go:build linux

package service

import "testing"

func TestParseShowProperties(t *testing.T) {
	t.Parallel()

	// systemd does not promise the order it prints properties in, and
	// values can be empty or contain '='.
	props := parseShowProperties("NRestarts=3\nResult=exit-code\nActiveState=activating\nSubState=auto-restart\nEnv=A=B\nBlank=\n\n")
	for key, want := range map[string]string{
		"NRestarts":   "3",
		"Result":      "exit-code",
		"ActiveState": "activating",
		"SubState":    "auto-restart",
		"Env":         "A=B",
		"Blank":       "",
	} {
		if got := props[key]; got != want {
			t.Errorf("props[%q] = %q, want %q", key, got, want)
		}
	}
}

func TestHealthFromProperties(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		props    map[string]string
		state    string
		restarts int
		crashed  bool
	}{
		{
			name:     "serving",
			props:    map[string]string{"ActiveState": "active", "SubState": "running", "Result": "success", "NRestarts": "0"},
			state:    "active",
			restarts: 0,
		},
		{
			// The case issue #13 is about: a long model load looks like
			// this, and must not be called a crash.
			name:     "still loading",
			props:    map[string]string{"ActiveState": "activating", "SubState": "start", "Result": "success", "NRestarts": "0"},
			state:    "activating",
			restarts: 0,
		},
		{
			// ...and so does this, which is a crash loop.
			name:     "crash looping between restarts",
			props:    map[string]string{"ActiveState": "activating", "SubState": "auto-restart", "Result": "exit-code", "NRestarts": "4"},
			state:    "activating",
			restarts: 4,
			crashed:  true,
		},
		{
			name:     "gave up",
			props:    map[string]string{"ActiveState": "failed", "SubState": "failed", "Result": "exit-code", "NRestarts": "5"},
			state:    "failed",
			restarts: 5,
			crashed:  true,
		},
		{
			// A unit restarted by hand still carries the old verdict for a
			// moment; that alone must not read as a crash.
			name:     "stale result while starting again",
			props:    map[string]string{"ActiveState": "activating", "SubState": "start", "Result": "exit-code", "NRestarts": "2"},
			state:    "activating",
			restarts: 2,
		},
		{
			name:     "stopped cleanly",
			props:    map[string]string{"ActiveState": "inactive", "SubState": "dead", "Result": "success", "NRestarts": "0"},
			state:    "inactive",
			restarts: 0,
		},
		{
			name:     "nothing readable",
			props:    map[string]string{},
			state:    "unknown",
			restarts: -1,
		},
		{
			name:     "unparseable restart count",
			props:    map[string]string{"ActiveState": "active", "NRestarts": "[not set]"},
			state:    "active",
			restarts: -1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := healthFromProperties(tt.props)
			if h.State != tt.state {
				t.Errorf("State = %q, want %q", h.State, tt.state)
			}
			if h.NRestarts != tt.restarts {
				t.Errorf("NRestarts = %d, want %d", h.NRestarts, tt.restarts)
			}
			if h.Crashed != tt.crashed {
				t.Errorf("Crashed = %v, want %v", h.Crashed, tt.crashed)
			}
		})
	}
}
