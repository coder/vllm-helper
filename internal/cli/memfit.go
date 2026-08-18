package cli

import (
	"fmt"
	"math"
	"strconv"
)

// memHeadroom leaves a margin below what's free: vLLM samples available memory
// at startup, and the figure keeps drifting while the engine boots.
const memHeadroom = 0.85

// clampMemUtilization is the pure (no I/O) half of fitMemUtilization. It caps
// --gpu-memory-utilization at what this machine can actually satisfy right now
// and returns the value to use plus a line to show the user ("" when there is
// nothing worth saying). Non-positive totals mean we learned nothing about the
// machine, so the request passes through untouched.
func clampMemUtilization(want string, totalMB, availMB int) (value, msg string) {
	f, err := strconv.ParseFloat(want, 64)
	if err != nil || f <= 0 {
		return want, ""
	}
	if totalMB <= 0 || availMB <= 0 {
		return want, ""
	}

	limit := memHeadroom * float64(availMB) / float64(totalMB)
	if f <= limit {
		return want, ""
	}
	lowered := math.Floor(limit*100) / 100
	if lowered < 0.05 {
		return want, fmt.Sprintf(
			"warning: only %d MiB of %d MiB is free -- vLLM will likely refuse to start. Close some apps and switch again.\n",
			availMB, totalMB)
	}
	return strconv.FormatFloat(lowered, 'f', 2, 64), fmt.Sprintf(
		"Lowering --gpu-memory-utilization %s -> %.2f: only %d MiB of %d MiB is free, and on the CPU backend that flag reserves system RAM.\n",
		want, lowered, availMB, totalMB)
}
