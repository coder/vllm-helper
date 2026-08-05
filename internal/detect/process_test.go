package detect

import (
	"reflect"
	"strings"
	"testing"

	"github.com/coder/vllm-helper/internal/config"
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

// Adoption has to reproduce the server it took over, so every serving flag
// on the command line must survive into the profile -- not just the handful
// the parser knows by name.
func TestParseServerCarriesUnrecognizedFlags(t *testing.T) {
	s := parseServer(4, strings.Fields(
		"/usr/bin/vllm serve poolside/Laguna-S-2.1-NVFP4 --tensor-parallel-size 2 "+
			"--dtype bfloat16 --enforce-eager --max-num-seqs 64"))
	want := "--tensor-parallel-size 2 --dtype bfloat16 --enforce-eager --max-num-seqs 64"
	if s.ExtraArgs != want {
		t.Errorf("ExtraArgs = %q, want %q", s.ExtraArgs, want)
	}
}

// --gpu-memory-utilization belongs to the profile, not EXTRA_ARGS: 'start'
// passes it explicitly, so adopting it into both would duplicate the flag.
func TestParseServerCapturesMemUtilization(t *testing.T) {
	for _, argv := range []string{
		"/usr/bin/vllm serve m/x --gpu-memory-utilization 0.92",
		"python -m vllm.entrypoints.openai.api_server --model=m/x --gpu-memory-utilization=0.92",
	} {
		s := parseServer(5, strings.Fields(argv))
		if s.MemUtilization != "0.92" {
			t.Errorf("%s: MemUtilization = %q, want 0.92", argv, s.MemUtilization)
		}
		if strings.Contains(s.ExtraArgs, "gpu-memory-utilization") {
			t.Errorf("%s: memory fraction leaked into ExtraArgs %q", argv, s.ExtraArgs)
		}
	}
}

// A carried-over value containing spaces has to survive the round trip
// through the profile, so it is quoted on the way out.
func TestParseServerQuotesValuesWithSpaces(t *testing.T) {
	s := parseServer(6, []string{
		"/usr/bin/vllm", "serve", "m/x",
		"--override-generation-config", `{"temperature": 0.7}`,
	})
	got, err := config.SplitArgs(s.ExtraArgs)
	if err != nil {
		t.Fatalf("SplitArgs(%q): %v", s.ExtraArgs, err)
	}
	want := []string{"--override-generation-config", `{"temperature": 0.7}`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip = %#v, want %#v", got, want)
	}
}

// The API key must never reach a profile, in either flag form.
func TestParseServerNeverLeaksAPIKey(t *testing.T) {
	for _, argv := range []string{
		"/usr/bin/vllm serve m/x --api-key sk-secret --dtype bfloat16",
		"/usr/bin/vllm serve m/x --api-key=sk-secret --dtype bfloat16",
	} {
		s := parseServer(7, strings.Fields(argv))
		if !s.APIKeySet {
			t.Errorf("%s: APIKeySet should be true", argv)
		}
		if strings.Contains(s.ExtraArgs, "sk-secret") || strings.Contains(s.ExtraArgs, "api-key") {
			t.Errorf("%s: api key leaked into ExtraArgs %q", argv, s.ExtraArgs)
		}
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
