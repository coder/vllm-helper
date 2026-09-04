package detect

import (
	"os"
	"strconv"
	"strings"
)

// totalMemBytes reports installed RAM. Bytes, not gigabytes: the budget
// arithmetic mixes this with sizes read from the Hub, which are decimal, and
// rounding to whole binary gigabytes here is how a 7.4% error crept into the
// fit check.
func totalMemBytes() int64 {
	return meminfoBytes("MemTotal:")
}

func meminfoBytes(key string) int64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, key); ok {
			fields := strings.Fields(rest)
			if len(fields) >= 1 {
				kb, _ := strconv.ParseInt(fields[0], 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}

// AvailableMemMB reports memory that can be allocated right now.
func AvailableMemMB() int {
	return int(meminfoBytes("MemAvailable:") / (1 << 20))
}

// describeLocalGPU is a no-op on Linux: nvidia-smi already covers the GPUs
// vLLM can serve from.
func describeLocalGPU(*Hardware) {}
