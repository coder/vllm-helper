package detect

import (
	"os/exec"
	"regexp"
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

// totalMemBytes reads hw.memsize. macOS has no /proc/meminfo, and without this
// the weights budget comes out as 0 and `search` calls every model too big.
func totalMemBytes() int64 {
	n, err := strconv.ParseInt(sysctlStr("hw.memsize"), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

var pageSizeRe = regexp.MustCompile(`page size of (\d+) bytes`)

// AvailableMemMB reports memory that can be allocated right now, counted the
// way psutil -- and therefore vLLM's CPU backend -- counts it on macOS: free
// pages plus the reclaimable ones.
func AvailableMemMB() int {
	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0
	}
	text := string(out)

	pageSize := int64(4096)
	if m := pageSizeRe.FindStringSubmatch(text); m != nil {
		if n, err := strconv.ParseInt(m[1], 10, 64); err == nil && n > 0 {
			pageSize = n
		}
	}

	var pages int64
	for _, key := range []string{"Pages free", "Pages inactive", "Pages speculative"} {
		m := regexp.MustCompile(regexp.QuoteMeta(key) + `:\s+(\d+)`).FindStringSubmatch(text)
		if m == nil {
			continue
		}
		if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			pages += n
		}
	}
	return int(pages * pageSize / 1024 / 1024)
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
