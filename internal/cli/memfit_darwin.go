package cli

import (
	"fmt"

	"github.com/coder/vllm-helper/internal/detect"
)

// fitMemUtilization lowers --gpu-memory-utilization to a value this machine can
// actually satisfy. macOS only has vLLM's CPU backend, where the flag reserves
// a fraction of *system* RAM and startup aborts unless that much is free right
// now -- so a number saved in a profile goes stale the moment someone opens a
// browser. Clamping here, immediately before exec, is the only place the real
// figure is known.
//
// Apple Silicon shares one memory pool too, but it does not take the cap that
// linux gives unified NVIDIA parts: there the flag reserves a GPU pool that the
// outgoing server has not released yet, while here it reserves ordinary RAM
// that really does have to be free before vLLM will start.
func fitMemUtilization(want string) string {
	value, msg := clampMemUtilization(want, detect.Probe().TotalMemGB*1024, detect.AvailableMemMB())
	if msg != "" {
		fmt.Print(msg)
	}
	return value
}
