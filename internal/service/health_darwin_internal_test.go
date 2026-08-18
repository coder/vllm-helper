//go:build darwin

package service

import "testing"

func TestHealthFromPrint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		text     string
		state    string
		restarts int
		crashed  bool
	}{
		{
			name:     "serving",
			text:     "state = running\nruns = 1\nlast exit code = 0\n",
			state:    "active",
			restarts: 1,
		},
		{
			// launchd throttles a job that keeps dying, so it sits in
			// "waiting" with the failing exit code still attached.
			name:     "crash looping",
			text:     "state = waiting\nruns = 7\nlast exit code = 1\n",
			state:    "failed",
			restarts: 7,
			crashed:  true,
		},
		{
			name:     "stopped cleanly",
			text:     "state = waiting\nruns = 3\nlast exit code = 0\n",
			state:    "inactive",
			restarts: 3,
		},
		{
			name:     "unrecognized output",
			text:     "something else entirely\n",
			state:    "unknown",
			restarts: -1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := healthFromPrint(tt.text)
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
