package cli

import (
	"fmt"

	"github.com/coder/vllm-helper/internal/detect"
)

// fitMemUtilization leaves --gpu-memory-utilization alone when there's a real
// NVIDIA GPU to serve from, because then it means VRAM and nothing else on the
// machine competes for it. Without one, vLLM falls back to the CPU backend,
// where the same flag reserves a fraction of *system* RAM -- and the Linux
// default (0.85, chosen for VRAM) asks for 85% of the box, which aborts
// startup on any machine that is also doing something else. Seen for real on a
// 15 GiB WSL2 box; see issue #13.
func fitMemUtilization(want string) string {
	hw := detect.Probe()
	if hw.GPUName != "" || hw.GPUMemGB > 0 {
		return want
	}
	value, msg := clampMemUtilization(want, hw.TotalMemGB*1024, detect.AvailableMemMB())
	if msg != "" {
		fmt.Print(msg)
	}
	return value
}
