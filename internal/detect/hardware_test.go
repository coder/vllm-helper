//go:build linux || darwin

package detect_test

import (
	"testing"

	"github.com/coder/vllm-helper/internal/detect"
)

// The budget drives whether `search` calls a model too big, so a zero here
// would silently rule out every model on the machine.
func TestProbeReportsThisMachine(t *testing.T) {
	t.Parallel()

	hw := detect.Probe()
	if hw.TotalMemGB <= 0 {
		t.Errorf("TotalMemGB = %d, want the real system memory", hw.TotalMemGB)
	}
	if hw.BudgetGB <= 0 {
		t.Errorf("BudgetGB = %d, want a usable weights budget", hw.BudgetGB)
	}
	if hw.BudgetReason == "" {
		t.Error("BudgetReason should explain the budget to the user")
	}
	if hw.GPUMemGB > 0 && !hw.Unified && hw.BudgetGB > hw.GPUMemGB {
		t.Errorf("BudgetGB = %d exceeds the %d GB of discrete VRAM it has to fit in", hw.BudgetGB, hw.GPUMemGB)
	}
}

// fitMemUtilization divides by this, so zero would disable the clamp that
// keeps vLLM's CPU backend from refusing to start.
func TestAvailableMemMB(t *testing.T) {
	t.Parallel()

	avail := detect.AvailableMemMB()
	if avail <= 0 {
		t.Errorf("AvailableMemMB = %d, want the memory that's actually free", avail)
	}
	if total := detect.Probe().TotalMemGB * 1024; avail > total {
		t.Errorf("AvailableMemMB = %d, more than the %d MB the machine has", avail, total)
	}
}
