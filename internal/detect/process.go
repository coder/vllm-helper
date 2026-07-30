package detect

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Server is one detected vLLM server and everything we learned about it.
type Server struct {
	PID     int
	Cmdline []string

	// Parsed from the command line:
	Model       string
	Port        string
	MaxModelLen string
	ExtraArgs   string // recognized per-model serving flags, reassembled
	APIKeySet   bool

	// How it's being run:
	VLLMBin       string // resolved vllm executable ("" if unknown)
	SystemdUnit   string // e.g. "vllm.service" ("" if not under systemd)
	UserScope     bool   // true if under the user manager (user@.service slice)
	InContainer   bool   // cgroup says it's inside a container
	ContainerHint string // raw cgroup fragment for container correlation
}

// vLLM's OpenAI server shows up either as "... vllm serve <model> ..." or
// "python -m vllm.entrypoints.openai.api_server --model <model> ...".
func looksLikeVLLM(args []string) bool {
	joined := strings.Join(args, " ")
	return strings.Contains(joined, "vllm serve") ||
		strings.Contains(joined, "vllm.entrypoints.openai.api_server")
}

// ScanProcesses walks /proc for running vLLM servers. Worker subprocesses
// (spawned engine processes share the same cmdline root) are deduped by
// keeping only the lowest PID per port.
func ScanProcesses() []Server {
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	byPort := map[string]Server{}
	for _, entry := range procs {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		if !looksLikeVLLM(args) {
			continue
		}
		s := parseServer(pid, args)
		enrich(&s)
		key := s.Port
		if prev, ok := byPort[key]; !ok || s.PID < prev.PID {
			byPort[key] = s
		}
	}
	out := make([]Server, 0, len(byPort))
	for _, s := range byPort {
		out = append(out, s)
	}
	return out
}

// perModelFlags are serving flags worth adopting into a profile. Everything
// else on the command line is either deployment topology (port, host,
// parallelism) or handled explicitly (model, max-model-len, api-key).
var perModelBoolFlags = map[string]bool{
	"--enable-auto-tool-choice": true,
	"--enable-prefix-caching":   true,
	"--trust-remote-code":       true,
}
var perModelValuedFlags = map[string]bool{
	"--tool-call-parser": true,
	"--reasoning-parser": true,
	"--quantization":     true,
	"--kv-cache-dtype":   true,
}

func parseServer(pid int, args []string) Server {
	s := Server{PID: pid, Cmdline: args, Port: "8000"} // vllm's default port
	var extra []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch {
		case a == "serve":
			// Positional model follows "serve" unless it's another flag.
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				s.Model = args[i]
			}
		case a == "--model":
			s.Model = next()
		case a == "--port":
			s.Port = next()
		case a == "--max-model-len":
			s.MaxModelLen = next()
		case a == "--api-key":
			next()
			s.APIKeySet = true
		case perModelBoolFlags[a]:
			extra = append(extra, a)
		case perModelValuedFlags[a]:
			extra = append(extra, a+" "+next())
		case strings.HasPrefix(a, "--") && strings.Contains(a, "="):
			// --flag=value form
			flag, val, _ := strings.Cut(a, "=")
			switch {
			case flag == "--model":
				s.Model = val
			case flag == "--port":
				s.Port = val
			case flag == "--max-model-len":
				s.MaxModelLen = val
			case flag == "--api-key":
				s.APIKeySet = true
			case perModelBoolFlags[flag]: // e.g. --trust-remote-code=true
				extra = append(extra, flag)
			case perModelValuedFlags[flag]:
				extra = append(extra, flag+" "+val)
			}
		}
	}
	s.ExtraArgs = strings.Join(extra, " ")
	return s
}

var unitRe = regexp.MustCompile(`/([^/]+\.service)`)

// enrich adds context that isn't on the command line: the vllm binary, the
// systemd unit (if any), and whether the process lives in a container.
func enrich(s *Server) {
	proc := filepath.Join("/proc", strconv.Itoa(s.PID))

	// Find the vllm console script in argv. /proc/<pid>/exe is useless
	// here: venv pythons symlink to the system interpreter.
	for _, a := range s.Cmdline {
		if strings.HasSuffix(a, "/vllm") && isExecutable(a) {
			s.VLLMBin = a
			break
		}
	}
	if s.VLLMBin == "" && len(s.Cmdline) > 0 {
		if cand := filepath.Join(filepath.Dir(s.Cmdline[0]), "vllm"); isExecutable(cand) {
			s.VLLMBin = cand
		}
	}
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

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}
