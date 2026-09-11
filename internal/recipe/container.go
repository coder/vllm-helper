package recipe

import (
	"fmt"
	"sort"
	"strings"
)

// CacheMounts are the host directories the image expects to find warm. They are
// bind-mounted rather than baked in so a re-launch does not re-download weights
// or re-JIT kernels, which is most of the startup cost.
var CacheMounts = []struct{ Host, Container string }{
	{".cache/huggingface", "/root/.cache/huggingface"},
	{".cache/vllm", "/root/.cache/vllm"},
	{".cache/flashinfer", "/root/.cache/flashinfer"},
	{".triton", "/root/.triton"},
	{".tilelang", "/root/.tilelang"},
}

// ContainerSpec is everything needed to start one node of a recipe.
type ContainerSpec struct {
	Name    string
	Image   string
	HomeDir string
	Env     map[string]string
	Args    []string
	// APIKey is kept out of Env so it can be redacted when the command is
	// printed, and passed as VLLM_API_KEY rather than --api-key so it stays out
	// of the host process command line.
	APIKey string
}

// RunArgs renders the argv for "docker run". Env is emitted in sorted order so
// the rendered command is stable and two launches can be diffed against
// each other.
func (s ContainerSpec) RunArgs() ([]string, error) { return s.runArgs(false) }

// DisplayArgs is RunArgs with the API key redacted, for printing.
func (s ContainerSpec) DisplayArgs() ([]string, error) { return s.runArgs(true) }

func (s ContainerSpec) runArgs(redact bool) ([]string, error) {
	if s.Name == "" || s.Image == "" || s.HomeDir == "" {
		return nil, fmt.Errorf("container spec needs a name, image and home dir")
	}
	if !strings.Contains(s.Image, "@sha256:") {
		return nil, fmt.Errorf("image %q is not pinned by digest", s.Image)
	}
	out := []string{
		"docker", "run", "-d", "--rm",
		"--name", s.Name,
		// Host networking because the ranks address each other directly over the
		// DAC link, and IPC/privileged/nofile because NCCL needs shared memory,
		// RDMA devices and a large descriptor table.
		"--network", "host",
		"--ipc", "host",
		"--privileged",
		"--ulimit", "nofile=1048576:1048576",
		"--gpus", "all",
	}
	for _, m := range CacheMounts {
		out = append(out, "-v", strings.TrimRight(s.HomeDir, "/")+"/"+m.Host+":"+m.Container)
	}
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, "-e", k+"="+s.Env[k])
	}
	if s.APIKey != "" {
		shown := s.APIKey
		if redact {
			shown = "<redacted>"
		}
		out = append(out, "-e", "VLLM_API_KEY="+shown)
	}
	out = append(out, "--entrypoint", "vllm", s.Image)
	return append(out, s.Args...), nil
}

// ShellCommand renders RunArgs as a single command line safe to pipe to a
// remote shell.
func (s ContainerSpec) ShellCommand() (string, error) { return s.shellCommand(false) }

// DisplayCommand is ShellCommand with the API key redacted, for printing.
func (s ContainerSpec) DisplayCommand() (string, error) { return s.shellCommand(true) }

func (s ContainerSpec) shellCommand(redact bool) (string, error) {
	args, err := s.runArgs(redact)
	if err != nil {
		return "", err
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = ShellQuote(a)
	}
	return strings.Join(quoted, " "), nil
}

// ShellQuote wraps a value so a POSIX shell passes it through untouched. The
// compilation-config argument is JSON with braces and quotes, so unquoted
// interpolation would corrupt it.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '=' || r == ',' || r == '@' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
