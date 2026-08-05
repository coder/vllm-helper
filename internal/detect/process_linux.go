package detect

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// listProcesses walks /proc, which gives us NUL-separated argv exactly as
// the kernel recorded it.
func listProcesses() []rawProc {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []rawProc
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		out = append(out, rawProc{PID: pid, Args: args})
	}
	return out
}

var unitRe = regexp.MustCompile(`/([^/]+\.service)`)

// enrichPlatform resolves the vllm binary via /proc/<pid>/exe and reads the
// cgroup to learn the systemd unit and container membership.
func enrichPlatform(s *Server) {
	proc := filepath.Join("/proc", strconv.Itoa(s.PID))

	if s.VLLMBin == "" {
		if exe, err := os.Readlink(filepath.Join(proc, "exe")); err == nil {
			if cand := filepath.Join(filepath.Dir(exe), "vllm"); isExecutable(cand) {
				s.VLLMBin = cand
			}
		}
	}

	cg, err := os.ReadFile(filepath.Join(proc, "cgroup"))
	if err != nil {
		return
	}
	cgs := string(cg)
	for _, marker := range []string{"docker", "containerd", "libpod", "crio", "kubepods"} {
		if strings.Contains(cgs, marker) {
			s.InContainer = true
			s.ContainerHint = marker
			break
		}
	}
	if !s.InContainer {
		if m := unitRe.FindStringSubmatch(cgs); m != nil && m[1] != "user@"+os.Getenv("UID")+".service" && !strings.HasPrefix(m[1], "user@") {
			s.SystemdUnit = m[1]
		}
		s.UserScope = strings.Contains(cgs, "/user.slice/") && s.SystemdUnit != ""
	}
}
