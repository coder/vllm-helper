package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/coder/vllm-helper/internal/detect"
)

// Two consecutive misses, two minutes apart, before relaunching. One miss is
// not evidence: a single probe can lose to a long request or a restart that
// has not opened the socket yet, and relaunching a working cluster costs
// minutes of loading.
const (
	defaultHealthMisses   = 2
	defaultHealthInterval = 2
)

func newRecipesHealthCheckCmd() *cobra.Command {
	var (
		unit      string
		threshold int
		statePath string
	)
	c := &cobra.Command{
		Use:    "health-check",
		Short:  "Relaunch the cluster when the endpoint stops answering",
		Long:   "Plumbing for the watchdog timer that 'recipes install-service' installs. Safe to run by hand: it only acts after repeated failures.",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if strings.TrimSpace(unit) == "" {
				return fmt.Errorf("--unit is required")
			}
			if statePath == "" {
				statePath = healthStatePath(unit)
			}
			return runHealthCheck(unit, threshold, statePath)
		},
	}
	c.Flags().StringVar(&unit, "unit", defaultRecipeServiceName, "unit to relaunch")
	c.Flags().IntVar(&threshold, "misses", defaultHealthMisses, "consecutive failures before relaunching")
	c.Flags().StringVar(&statePath, "state", "", "where the miss count is kept")
	return c
}

// healthStatePath keeps the counter in /run: it is root-writable, and it is
// cleared by a reboot, which is exactly the lifetime a miss count should have.
func healthStatePath(unit string) string {
	return "/run/" + strings.TrimSuffix(unit, ".service") + "-misses"
}

type healthAction string

const (
	healthSkip    healthAction = "skip"
	healthClear   healthAction = "clear"
	healthCount   healthAction = "count"
	healthRestart healthAction = "restart"
)

// healthDecision is the whole watchdog policy, kept pure so the interesting
// cases can be tested without a systemd host or a model.
//
// "activating" is left alone because a load takes minutes and the endpoint is
// legitimately silent for all of it; restarting then would loop forever.
func healthDecision(unitState string, healthy bool, misses, threshold int) (healthAction, int) {
	if unitState == "activating" {
		return healthSkip, misses
	}
	if healthy {
		return healthClear, 0
	}
	misses++
	if misses >= threshold {
		return healthRestart, 0
	}
	return healthCount, misses
}

func runHealthCheck(unit string, threshold int, statePath string) error {
	state := strings.TrimSpace(systemctlOutput("is-active", unit))
	healthy := endpointHealthy()
	misses := readMisses(statePath)

	action, next := healthDecision(state, healthy, misses, threshold)
	switch action {
	case healthSkip:
		return nil
	case healthClear:
		_ = os.Remove(statePath)
		return nil
	case healthCount:
		writeMisses(statePath, next)
		fmt.Fprintf(os.Stderr, "%s: endpoint did not answer (%d/%d)\n", unit, misses+1, threshold)
		return nil
	default:
		_ = os.Remove(statePath)
		fmt.Fprintf(os.Stderr, "%s: endpoint unhealthy %d times; relaunching\n", unit, threshold)
		return runPoolCommand(os.Stdout, "systemctl", "restart", unit)
	}
}

// endpointHealthy asks the port config names, which is the same check status
// reports, so the watchdog and the operator agree on what healthy means.
func endpointHealthy() bool {
	cfg, err := loadConfig()
	if err != nil {
		return false
	}
	for _, ep := range detect.ProbePorts([]string{cfg.Port()}, cfg.APIKey()) {
		if ep.Healthy {
			return true
		}
	}
	return false
}

func systemctlOutput(args ...string) string {
	// is-active exits non-zero for anything but active, so the output is the
	// answer and the status is not.
	out, _ := exec.Command("systemctl", args...).Output()
	return string(out)
}

func readMisses(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func writeMisses(path string, n int) {
	if err := os.WriteFile(path, []byte(strconv.Itoa(n)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "could not record the miss count:", err)
	}
}

// renderHealthUnits builds the watchdog pair.
//
// The service runs as root so it can restart the unit without a sudo rule,
// and carries HOME so it reads the same config as the user who installed it.
func renderHealthUnits(u recipeSystemdUnit, unitName string, intervalMin int) (service, timer string) {
	service = fmt.Sprintf("[Unit]\n"+
		"Description=Health check for %s\n\n"+
		"[Service]\n"+
		"Type=oneshot\n"+
		"Environment=HOME=%s\n"+
		"ExecStart=%s recipes health-check --unit %s\n",
		unitName, u.Home, u.Self, unitName)
	timer = fmt.Sprintf("[Unit]\n"+
		"Description=Run the health check for %s every %d minutes\n\n"+
		"[Timer]\n"+
		"OnBootSec=5min\n"+
		"OnUnitActiveSec=%dmin\n"+
		"AccuracySec=15s\n\n"+
		"[Install]\n"+
		"WantedBy=timers.target\n",
		unitName, intervalMin, intervalMin)
	return service, timer
}

// healthUnitNames derives the watchdog names from the unit they watch, so a
// renamed service does not leave an orphaned timer behind.
func healthUnitNames(unit string) (service, timer string) {
	base := strings.TrimSuffix(unit, ".service") + "-health"
	return base + ".service", base + ".timer"
}
