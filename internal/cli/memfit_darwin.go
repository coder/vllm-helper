package cli

import (
	"fmt"
	"math"
	"strconv"

	"github.com/coder/vllm-helper/internal/detect"
)

// memHeadroom leaves a margin below what's free: vLLM samples available memory
// at startup, and the figure keeps drifting while the engine boots.
const memHeadroom = 0.85

// fitMemUtilization lowers --gpu-memory-utilization to a value this machine can
// actually satisfy. macOS only has vLLM's CPU backend, where the flag reserves
// a fraction of *system* RAM and startup aborts unless that much is free right
// now -- so a number saved in a profile goes stale the moment someone opens a
// browser. Clamping here, immediately before exec, is the only place the real
// figure is known.
func fitMemUtilization(want string) string {
	f, err := strconv.ParseFloat(want, 64)
	if err != nil || f <= 0 {
		return want
	}
	totalMB, availMB := detect.Probe().TotalMemGB*1024, detect.AvailableMemMB()
	if totalMB <= 0 || availMB <= 0 {
		return want
	}

	limit := memHeadroom * float64(availMB) / float64(totalMB)
	if f <= limit {
		return want
	}
	lowered := math.Floor(limit*100) / 100
	if lowered < 0.05 {
		fmt.Printf("warning: only %d MiB of %d MiB is free -- vLLM will likely refuse to start. Close some apps and switch again.\n", availMB, totalMB)
		return want
	}
	fmt.Printf("Lowering --gpu-memory-utilization %s -> %.2f: only %d MiB of %d MiB is free, and on the CPU backend that flag reserves system RAM.\n",
		want, lowered, availMB, totalMB)
	return strconv.FormatFloat(lowered, 'f', 2, 64)
}
