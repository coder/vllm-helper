package detect

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/coder/vllm-helper/internal/config"
)

// Server is one detected vLLM server and everything we learned about it.
// Cmdline is deliberately kept out of JSON: it can contain --api-key values.
type Server struct {
	PID     int      `json:"pid"`
	Cmdline []string `json:"-"`

	// Parsed from the command line:
	Model          string `json:"model"`
	Port           string `json:"port"`
	MaxModelLen    string `json:"max_model_len,omitempty"`
	MemUtilization string `json:"mem_utilization,omitempty"` // --gpu-memory-utilization
	ExtraArgs      string `json:"extra_args,omitempty"`      // every other serving flag, reassembled
	APIKeySet      bool   `json:"api_key_set"`

	// How it's being run:
	VLLMBin       string `json:"vllm_bin,omitempty"`       // resolved vllm executable ("" if unknown)
	SystemdUnit   string `json:"systemd_unit,omitempty"`   // e.g. "vllm.service" ("" if not under systemd)
	UserScope     bool   `json:"user_scope"`               // true if under the user manager (user@.service slice)
	InContainer   bool   `json:"in_container"`             // cgroup says it's inside a container
	ContainerHint string `json:"container_hint,omitempty"` // raw cgroup fragment for container correlation
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

// valuelessFlags never consume the following argument. Any other flag is
// assumed to take a value when the next argument doesn't look like a flag,
// which is how unrecognized flags get carried over intact.
var valuelessFlags = map[string]bool{
	"--enable-auto-tool-choice": true,
	"--enable-prefix-caching":   true,
	"--trust-remote-code":       true,
}

// looksSecret reports whether a flag carries a credential. Those are left
// out of ExtraArgs entirely: it lands in a profile file and in
// 'doctor --json', which is the same reason Cmdline is withheld from JSON.
// Matching on the trailing word keeps real flags like --tokenizer-mode and
// --ssl-keyfile from being mistaken for secrets.
func looksSecret(flag string) bool {
	for _, suffix := range []string{"-key", "-token", "-secret", "-password"} {
		if strings.HasSuffix(strings.ToLower(flag), suffix) {
			return true
		}
	}
	return false
}

// looksLikeValue distinguishes a flag's value from the next flag. Negative
// numbers are values despite the leading dash (e.g. --seed -1).
func looksLikeValue(s string) bool {
	if s == "" {
		return false
	}
	if !strings.HasPrefix(s, "-") {
		return true
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
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
		case a == "--gpu-memory-utilization":
			s.MemUtilization = next()
		case a == "--api-key":
			next()
			s.APIKeySet = true
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
			case flag == "--gpu-memory-utilization":
				s.MemUtilization = val
			case flag == "--api-key":
				s.APIKeySet = true
			case valuelessFlags[flag]: // e.g. --trust-remote-code=true
				extra = append(extra, flag)
			case looksSecret(flag):
				// Deliberately dropped, like --api-key.
			default:
				extra = append(extra, flag+"="+config.Quote(val))
			}
		case valuelessFlags[a]:
			extra = append(extra, a)
		case strings.HasPrefix(a, "--"):
			// Anything else the server was started with: carry it over so
			// the profile can reproduce this configuration.
			hasValue := i+1 < len(args) && looksLikeValue(args[i+1])
			if looksSecret(a) {
				if hasValue {
					next() // consume it so it can't be read as a flag
				}
				break
			}
			if hasValue {
				extra = append(extra, a+" "+config.Quote(next()))
			} else {
				extra = append(extra, a)
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
