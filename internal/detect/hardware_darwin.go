package detect

import (
	"os/exec"
	"strconv"
	"strings"
)

func sysctlStr(key string) string {
	out, err := exec.Command("sysctl", "-n", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// totalMemGB reads hw.memsize. macOS has no /proc/meminfo, and without this
// the weights budget comes out as 0 GB and `search` calls every model too big.
func totalMemGB() int {
	n, err := strconv.ParseInt(sysctlStr("hw.memsize"), 10, 64)
	if err != nil {
		return 0
	}
	return int(n / 1024 / 1024 / 1024)
}

// describeLocalGPU names the Apple Silicon SoC and marks its memory unified:
// the GPU draws from the same pool as the CPU, so the weights budget has to
// come out of system RAM.
func describeLocalGPU(hw *Hardware) {
	name := sysctlStr("machdep.cpu.brand_string")
	if !strings.HasPrefix(name, "Apple ") {
		return
	}
	hw.GPUName = name
	hw.Unified = true
}
