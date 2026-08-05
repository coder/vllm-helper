package detect

import (
	"os"
	"strconv"
	"strings"
)

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

// AvailableMemMB reports memory that can be allocated right now.
func AvailableMemMB() int {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "MemAvailable:"); ok {
			fields := strings.Fields(rest)
			if len(fields) >= 1 {
				kb, _ := strconv.ParseInt(fields[0], 10, 64)
				return int(kb / 1024)
			}
		}
	}
	return 0
}

// describeLocalGPU is a no-op on Linux: nvidia-smi already covers the GPUs
// vLLM can serve from.
func describeLocalGPU(*Hardware) {}
