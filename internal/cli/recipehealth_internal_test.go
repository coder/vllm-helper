package cli

import (
	"strings"
	"testing"
)

// The watchdog's whole job is deciding when a silent endpoint means trouble.
// A load legitimately takes minutes, so "activating" must never be treated as
// a miss: that would restart the launch it is waiting for, forever.
func TestHealthDecision(t *testing.T) {
	cases := []struct {
		name      string
		state     string
		healthy   bool
		misses    int
		want      healthAction
		wantCount int
	}{
		{"loading is not a failure", "activating", false, 1, healthSkip, 1},
		{"healthy clears the count", "active", true, 1, healthClear, 0},
		{"first miss only counts", "active", false, 0, healthCount, 1},
		{"second miss relaunches", "active", false, 1, healthRestart, 0},
		{"a dead unit still relaunches", "failed", false, 1, healthRestart, 0},
		{"healthy while inactive is still healthy", "inactive", true, 1, healthClear, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, n := healthDecision(c.state, c.healthy, c.misses, 2)
			if got != c.want || n != c.wantCount {
				t.Errorf("healthDecision(%q, %v, %d) = %v/%d, want %v/%d",
					c.state, c.healthy, c.misses, got, n, c.want, c.wantCount)
			}
		})
	}
}

// The counter lives in /run so a reboot clears it, and the names follow the
// unit so a renamed service cannot leave an orphaned timer behind.
func TestHealthNamesFollowTheUnit(t *testing.T) {
	svc, timer := healthUnitNames("vllm-cluster.service")
	if svc != "vllm-cluster-health.service" || timer != "vllm-cluster-health.timer" {
		t.Errorf("names = %q, %q", svc, timer)
	}
	if got := healthStatePath("vllm-cluster.service"); got != "/run/vllm-cluster-misses" {
		t.Errorf("state path = %q", got)
	}
}

func TestHealthUnitsRunTheCheckerAgainstTheirUnit(t *testing.T) {
	svc, timer := renderHealthUnits(
		recipeSystemdUnit{Home: "/home/serve", Self: "/usr/local/bin/vllm-helper"},
		"vllm-cluster.service", 2)
	for _, want := range []string{
		"ExecStart=/usr/local/bin/vllm-helper recipes health-check --unit vllm-cluster.service",
		"Environment=HOME=/home/serve",
		"Type=oneshot",
	} {
		if !strings.Contains(svc, want) {
			t.Errorf("health service missing %q:\n%s", want, svc)
		}
	}
	for _, want := range []string{"OnUnitActiveSec=2min", "WantedBy=timers.target", "OnBootSec=5min"} {
		if !strings.Contains(timer, want) {
			t.Errorf("timer missing %q:\n%s", want, timer)
		}
	}
	// No User= line: the checker restarts the unit, which needs root.
	if strings.Contains(svc, "User=") {
		t.Errorf("health service pins a user, so it could not restart the unit:\n%s", svc)
	}
}
