package detect

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Hardware describes what we can serve models with on this machine.
type Hardware struct {
	TotalMemGB   int    `json:"total_mem_gb"`  // system RAM
	GPUName      string `json:"gpu_name"`      // e.g. "NVIDIA GB10", "" if no NVIDIA GPU visible
	GPUMemGB     int    `json:"gpu_mem_gb"`    // VRAM reported by nvidia-smi; 0 if unknown/none
	Unified      bool   `json:"unified"`       // GPU shares system memory (DGX Spark / GB10 / Grace)
	BudgetGB     int    `json:"budget_gb"`     // recommended max GB of model weights on disk
	BudgetReason string `json:"budget_reason"` // one-liner explaining the budget for the user
}

// Probe inspects /proc/meminfo and nvidia-smi (if present). Never fails --
// missing pieces just degrade the result.
func Probe() Hardware {
	hw := Hardware{TotalMemGB: totalMemGB()}

	out, err := exec.Command("nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits").Output()
	if err == nil {
		line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
		parts := strings.Split(line, ",")
		if len(parts) >= 1 {
			hw.GPUName = strings.TrimSpace(parts[0])
		}
		if len(parts) >= 2 {
			if mb, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
				hw.GPUMemGB = mb / 1024
			}
		}
	}

	// Unified-memory heuristic: the GPU either reports no discrete pool or a
	// pool within ~90% of system RAM (Grace-Blackwell reports the shared
	// pool), or the GPU name is a known unified part.
	unifiedNames := regexp.MustCompile(`(?i)GB10|GB200|GH200|Grace`)
	switch {
	case hw.GPUName != "" && unifiedNames.MatchString(hw.GPUName):
		hw.Unified = true
	case hw.GPUMemGB > 0 && hw.TotalMemGB > 0 && float64(hw.GPUMemGB) > 0.9*float64(hw.TotalMemGB):
		hw.Unified = true
	}

	switch {
	case hw.Unified || hw.GPUMemGB == 0:
		// Unified (or unknown GPU): weights + KV cache + OS all share RAM.
		hw.BudgetGB = hw.TotalMemGB * 7 / 10
		hw.BudgetReason = "~70% of unified/system memory (leaves room for KV cache and the OS)"
	default:
		// Discrete VRAM: weights must fit in VRAM with KV-cache headroom.
		hw.BudgetGB = hw.GPUMemGB * 8 / 10
		hw.BudgetReason = "~80% of discrete VRAM (leaves room for KV cache)"
	}
	return hw
}

func totalMemGB() int {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			fields := strings.Fields(rest)
			if len(fields) >= 1 {
				kb, _ := strconv.ParseInt(fields[0], 10, 64)
				return int(kb / 1024 / 1024)
			}
		}
	}
	return 0
}
