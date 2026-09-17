package cli

import "testing"

// status reads this vocabulary, so a mistranslation here is what makes a
// healthy cluster look broken or a crash-looping one look idle.
func TestContainerStateSpeaksTheInitVocabulary(t *testing.T) {
	cases := []struct {
		status string
		code   int
		want   string
		why    string
	}{
		{"running", 0, "active", "serving"},
		{"created", 0, "activating", "starting, not yet up"},
		{"restarting", 0, "activating", "docker is bringing it back"},
		{"", 0, "inactive", "no container at all"},
		{"exited", 0, "inactive", "stopped on purpose is not a failure"},
		{"exited", 137, "failed", "SIGKILL, which is how an OOM shows up"},
		{"dead", 1, "failed", "docker could not clean it up after a fault"},
		{"paused", 0, "paused", "unknown states pass through rather than lie"},
	}
	for _, c := range cases {
		if got := containerState(c.status, c.code); got != c.want {
			t.Errorf("containerState(%q, %d) = %q, want %q (%s)", c.status, c.code, got, c.want, c.why)
		}
	}
}
