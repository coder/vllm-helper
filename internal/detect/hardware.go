package detect

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// hasNVIDIADevice reports whether this machine has a GPU we could actually
// serve from, for the case where nvidia-smi told us nothing.
//
// The control device is the signal rather than /proc/driver/nvidia/version,
// because the version file outlives a usable GPU: it is still there after a
// driver upgrade without a reboot (nvidia-smi then fails on a library version
// mismatch), and it is visible inside a container started without --gpus. Both
// are ordinary CPU-backend situations, and treating them as GPU boxes would
// skip the clamp that issue #13 needs.
func hasNVIDIADevice() bool {
	_, err := os.Stat("/dev/nvidiactl")
	return err == nil
}

// Hardware describes what we can serve models with on this machine.
type Hardware struct {
	TotalMemGB   int    `json:"total_mem_gb"`  // system RAM
	GPUName      string `json:"gpu_name"`      // e.g. "NVIDIA GB10", "" if no NVIDIA GPU visible
	GPUMemGB     int    `json:"gpu_mem_gb"`    // VRAM reported by nvidia-smi; 0 if unknown/none
	Unified      bool   `json:"unified"`       // GPU shares system memory (DGX Spark / GB10 / Grace)
	BudgetGB     int    `json:"budget_gb"`     // recommended max GB of model weights on disk
	BudgetReason string `json:"budget_reason"` // one-liner explaining the budget for the user
}

// unifiedNames are GPUs whose memory is the system's memory: Grace-Blackwell
// (GB10 in the DGX Spark, GB200, GB300), Grace-Hopper, and the integrated
// Jetson/Thor parts. Naming them individually beats inferring it, because
// nvidia-smi reports "[N/A]" for memory.total on GB10 and so tells us nothing.
var unifiedNames = regexp.MustCompile(`(?i)GB10|GB200|GB300|GH200|Grace|Orin|Thor|Tegra`)

// isUnified decides whether the GPU shares one memory pool with the CPU. It is
// the pure half of Probe, so the cases below can be tested without the machine.
//
// Only positive evidence counts. A GPU we could not describe is left alone: an
// NVIDIA device node says a GPU exists but nothing about its memory, and there
// is no cheap signal that would say more. Architecture looked like one, but a
// GB10 presents as an ordinary PCI display controller, so it cannot separate
// integrated from discrete on arm64 either. Guessing unified there would hand
// the 0.85 cap to a machine whose real need is the CPU backend's live clamp,
// which is the regression issue #13 describes.
func isUnified(gpuName string, gpuMemGB, totalMemGB int, nvidiaDevice bool) bool {
	if gpuName != "" && unifiedNames.MatchString(gpuName) {
		return true
	}
	// A pool the size of system memory is a shared pool -- but only when it
	// does not exceed it, since an 80 GB card in a 64 GB host is plainly
	// discrete memory rather than a share of the host's.
	return gpuMemGB > 0 && totalMemGB > 0 &&
		gpuMemGB <= totalMemGB &&
		float64(gpuMemGB) > 0.9*float64(totalMemGB)
}

// Probe inspects system memory and nvidia-smi (if present). Never fails --
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

	hw.Unified = isUnified(hw.GPUName, hw.GPUMemGB, hw.TotalMemGB, hasNVIDIADevice())

	// No NVIDIA GPU, but the platform may still have an accelerator worth
	// naming (Apple Silicon shares one memory pool with the CPU).
	if hw.GPUName == "" {
		describeLocalGPU(&hw)
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
