package detect

import (
	"strings"
	"testing"
)

// Real-world argv observed on a GB10 box (venv console-script form).
var realArgv = strings.Fields(
	"/home/dell/venvs/vllm-native/bin/python3 /home/dell/venvs/vllm-native/bin/vllm serve poolside/Laguna-S-2.1-NVFP4 " +
		"--port 8001 --max-model-len 131072 --gpu-memory-utilization 0.85 --api-key sk-secret " +
		"--enable-auto-tool-choice --tool-call-parser poolside_v1 --reasoning-parser poolside_v1")

func TestParseServerConsoleScript(t *testing.T) {
	s := parseServer(1, realArgv)
	if s.Model != "poolside/Laguna-S-2.1-NVFP4" {
		t.Errorf("Model = %q", s.Model)
	}
	if s.Port != "8001" {
		t.Errorf("Port = %q", s.Port)
	}
	if s.MaxModelLen != "131072" {
		t.Errorf("MaxModelLen = %q", s.MaxModelLen)
	}
	if !s.APIKeySet {
		t.Error("APIKeySet should be true")
	}
	want := "--enable-auto-tool-choice --tool-call-parser poolside_v1 --reasoning-parser poolside_v1"
	if s.ExtraArgs != want {
		t.Errorf("ExtraArgs = %q, want %q", s.ExtraArgs, want)
	}
	if strings.Contains(s.ExtraArgs, "api-key") {
		t.Error("api-key must never leak into ExtraArgs")
	}
}

func TestParseServerAPIServerForm(t *testing.T) {
	args := strings.Fields(
		"python -m vllm.entrypoints.openai.api_server --model=openai/gpt-oss-120b --port=8000 --max-model-len=131072 --trust-remote-code")
	s := parseServer(2, args)
	if s.Model != "openai/gpt-oss-120b" {
		t.Errorf("Model = %q", s.Model)
	}
	if s.Port != "8000" {
		t.Errorf("Port = %q", s.Port)
	}
	if s.ExtraArgs != "--trust-remote-code" {
		t.Errorf("ExtraArgs = %q", s.ExtraArgs)
	}
}

func TestParseServerDefaultsPort(t *testing.T) {
	s := parseServer(3, strings.Fields("/usr/bin/vllm serve meta-llama/Llama-3-8B"))
	if s.Port != "8000" {
		t.Errorf("default port = %q, want vllm's default 8000", s.Port)
	}
	if !looksLikeVLLM(s.Cmdline) {
		t.Error("looksLikeVLLM failed on a vllm serve command")
	}
}
