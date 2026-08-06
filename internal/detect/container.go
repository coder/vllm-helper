package detect

import (
	"encoding/json"
	"os/exec"
	"strings"
)

// Container is a running Docker/Podman container that looks like a vLLM
// server (by image name or command).
type Container struct {
	Runtime string `json:"runtime"` // "docker" or "podman"
	ID      string `json:"id"`
	Name    string `json:"name"`
	Image   string `json:"image"`
	Command string `json:"command"`
	Ports   string `json:"ports"`
}

// ScanContainers asks docker and podman (whichever are installed) for
// running containers that look vLLM-related. Errors are treated as "none
// found" -- a missing or unreachable runtime is not a failure.
func ScanContainers() []Container {
	var out []Container
	out = append(out, scanRuntime("docker")...)
	out = append(out, scanRuntime("podman")...)
	return out
}

func scanRuntime(runtime string) []Container {
	if _, err := exec.LookPath(runtime); err != nil {
		return nil
	}
	// `ps --format json` emits one JSON object per line on docker; podman
	// emits a JSON array. Handle both.
	raw, err := exec.Command(runtime, "ps", "--no-trunc", "--format", "json").Output()
	if err != nil {
		return nil
	}
	var entries []map[string]any
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		_ = json.Unmarshal([]byte(trimmed), &entries)
	} else {
		for line := range strings.SplitSeq(trimmed, "\n") {
			var e map[string]any
			if json.Unmarshal([]byte(line), &e) == nil && len(e) > 0 {
				entries = append(entries, e)
			}
		}
	}

	str := func(e map[string]any, keys ...string) string {
		for _, k := range keys {
			if v, ok := e[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}

	var out []Container
	for _, e := range entries {
		c := Container{
			Runtime: runtime,
			ID:      str(e, "ID", "Id"),
			Name:    str(e, "Names", "Name"),
			Image:   str(e, "Image"),
			Command: str(e, "Command"),
			Ports:   str(e, "Ports"),
		}
		blob := strings.ToLower(c.Image + " " + c.Command + " " + c.Name)
		if strings.Contains(blob, "vllm") {
			out = append(out, c)
		}
	}
	return out
}

// InspectArgs returns a container's full command args (entrypoint + cmd),
// which is where model/flags live for the official vllm-openai image.
func InspectArgs(runtime, id string) []string {
	raw, err := exec.Command(runtime, "inspect", "--format", "{{json .Config.Cmd}}", id).Output()
	if err != nil {
		return nil
	}
	var args []string
	_ = json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &args)
	return args
}
