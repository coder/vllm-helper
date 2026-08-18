package cli

import "testing"

func TestLastMeaningfulLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		logs string
		want string
	}{
		{"empty", "", ""},
		{"only blank lines", "\n  \n\t\n", ""},
		{"single line", "boom", "boom"},
		{"trailing newline is ignored", "first\nlast\n", "last"},
		{"trailing blank lines are ignored", "the real error\n\n   \n", "the real error"},
		{
			// The failure from issue #13: the exception is the last line
			// of the traceback, which is what people need to see.
			name: "python traceback ends with the exception",
			logs: "Traceback (most recent call last):\n" +
				"  File \"/usr/lib/vllm/engine.py\", line 1, in <module>\n" +
				"ValueError: User-specified max_model_len (131072) is greater than the derived max_model_len (2048)\n",
			want: "ValueError: User-specified max_model_len (131072) is greater than the derived max_model_len (2048)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := lastMeaningfulLine(tt.logs); got != tt.want {
				t.Errorf("lastMeaningfulLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCrashReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		now, baseline int
		want          string
	}{
		{"counts from the baseline", 5, 2, "the service is crash-looping (3 restarts since the switch)"},
		{"no baseline to count from", 5, -1, "the service is crash-looping"},
		{"counter did not move", 2, 2, "the service is crash-looping"},
		{"counter unreadable", -1, 0, "the service is crash-looping"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := crashReason(tt.now, tt.baseline); got != tt.want {
				t.Errorf("crashReason(%d, %d) = %q, want %q", tt.now, tt.baseline, got, tt.want)
			}
		})
	}
}
