package vllm

import (
	"encoding/json"
	"time"
)

// Metal describes the vllm-metal plugin, which runs vLLM on the Apple Silicon
// GPU through MLX. Without it, vLLM on a Mac runs its CPU backend, and the two
// read --gpu-memory-utilization differently: the CPU backend reserves a share
// of system RAM that must be free right now, while vllm-metal sizes its KV
// cache as a share of the Metal working set.
type Metal struct {
	// Installed is whether vllm_metal is importable beside the vllm binary.
	Installed bool `json:"installed"`
	// WorkingSetBytes is MLX's max_recommended_working_set_size: the most
	// unified memory macOS lets the GPU wire, and the figure vllm-metal
	// multiplies --gpu-memory-utilization by. 0 when it could not be read.
	WorkingSetBytes int64 `json:"working_set_bytes"`
}

// metalProbe is run inside vLLM's interpreter. It imports MLX but not vLLM or
// torch, so it stays well under a second.
const metalProbe = `
import importlib.util, json
out = {"installed": importlib.util.find_spec("vllm_metal") is not None, "working_set_bytes": 0}
if out["installed"]:
    try:
        import mlx.core as mx
        out["working_set_bytes"] = int(mx.device_info().get("max_recommended_working_set_size", 0))
    except Exception:
        pass
print("VLLM_HELPER_METAL " + json.dumps(out))
`

const metalMarker = "VLLM_HELPER_METAL "

// metalInstalledProbe asks only whether vllm_metal is importable. A module
// lookup imports nothing, so this stays cheap enough for every 'start'.
const metalInstalledProbe = `
import importlib.util, json
print("VLLM_HELPER_METAL_INSTALLED " + json.dumps(importlib.util.find_spec("vllm_metal") is not None))
`

const metalInstalledMarker = "VLLM_HELPER_METAL_INSTALLED "

// metalTimeout bounds the probe. The first MLX import after an install can
// take a few seconds while macOS verifies the native libraries.
const metalTimeout = 20 * time.Second

// ProbeMetal reports whether the vLLM at binPath has the vllm-metal plugin, and
// the Metal working set it will size against. A failed probe returns a zero
// Metal, which callers treat as the CPU backend's behaviour.
func ProbeMetal(binPath string) Metal {
	if binPath == "" {
		return Metal{}
	}
	out, err := runPython(binPath, metalProbe, metalTimeout)
	if err != nil {
		return Metal{}
	}
	return parseMetal(out)
}

// MetalInstalled reports whether the vLLM at binPath has the vllm-metal
// plugin, without importing MLX. Use it when only the backend matters; the
// working set needs ProbeMetal. A failed probe reports false, the CPU backend.
func MetalInstalled(binPath string) bool {
	if binPath == "" {
		return false
	}
	out, err := runPython(binPath, metalInstalledProbe, envTimeout)
	if err != nil {
		return false
	}
	return parseMetalInstalled(out)
}

func parseMetalInstalled(out []byte) bool {
	line, ok := extractMarked(out, metalInstalledMarker)
	if !ok {
		return false
	}
	var installed bool
	return json.Unmarshal(line, &installed) == nil && installed
}

func parseMetal(out []byte) Metal {
	line, ok := extractMarked(out, metalMarker)
	if !ok {
		return Metal{}
	}
	var m Metal
	if json.Unmarshal(line, &m) != nil {
		return Metal{}
	}
	if m.WorkingSetBytes < 0 {
		m.WorkingSetBytes = 0
	}
	return m
}
