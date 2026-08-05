package detect

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// listProcesses shells out to ps, since macOS has no /proc. -ww stops ps from
// truncating long command lines, which vLLM's always are. Argv boundaries are
// lost to whitespace splitting, but no vLLM flag or model id contains a space.
func listProcesses() []rawProc {
	out, err := exec.Command("ps", "-Awwo", "pid=,command=").Output()
	if err != nil {
		return nil
	}
	var procs []rawProc
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		procs = append(procs, rawProc{PID: pid, Args: fields[1:]})
	}
	return procs
}

// enrichPlatform looks for a launchd job owning the process. Containers are
// never visible here: on macOS they run inside a Linux VM, so anything ps can
// see is a host process. ScanContainers covers the rest.
func enrichPlatform(s *Server) {
	if s.VLLMBin == "" {
		for _, a := range s.Cmdline {
			// The venv's python sits next to its console scripts.
			if cand := filepath.Join(filepath.Dir(a), "vllm"); strings.Contains(a, "/bin/") && isExecutable(cand) {
				s.VLLMBin = cand
				break
			}
		}
	}
	if label := launchdLabelFor(s.PID); label != "" {
		s.SystemdUnit = label
		s.UserScope = true // launchctl list only reports the caller's domain
	}
}

// launchdLabelFor returns the launchd job label running pid, or "" if the
// process isn't managed by launchd in this user's domain.
func launchdLabelFor(pid int) string {
	out, err := exec.Command("launchctl", "list").Output()
	if err != nil {
		return ""
	}
	want := strconv.Itoa(pid)
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != want {
			continue
		}
		return fields[2]
	}
	return ""
}
