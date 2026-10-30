package provider_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/coder/vllm-helper/internal/provider"
)

// Every install from before providers existed has no PROVIDER line, and must
// keep serving with vLLM.
func TestLookupDefaultsToVLLM(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", "  ", "vllm", "VLLM"} {
		p, err := provider.Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", name, err)
		}
		if p.Name() != provider.VLLM {
			t.Errorf("Lookup(%q) = %s, want vllm", name, p.Name())
		}
	}
}

func TestLookupRejectsUnknownNamingTheChoices(t *testing.T) {
	t.Parallel()
	_, err := provider.Lookup("ollama")
	if err == nil {
		t.Fatal("an unknown provider must be an error, not a silent fallback to vLLM")
	}
	for _, want := range []string{"PROVIDER", "ollama", "mlx", "vllm"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// The argv vLLM gets is pinned, so moving it behind the seam changes nothing.
func TestVLLMCommand(t *testing.T) {
	t.Parallel()
	p, _ := provider.Lookup(provider.VLLM)
	got, err := p.Command(provider.Spec{
		Bin: "/opt/vllm/bin/vllm", Model: "Qwen/Qwen3-32B", Port: "8001",
		MaxModelLen: "32768", MemUtilization: "0.85", APIKey: "sk-x",
		KVCacheBytes: "1000", ExtraArgs: `--tool-call-parser hermes --chat-template "/a b.jinja"`,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/vllm/bin/vllm", "serve", "Qwen/Qwen3-32B",
		"--port", "8001", "--max-model-len", "32768", "--gpu-memory-utilization", "0.85",
		"--api-key", "sk-x", "--kv-cache-memory-bytes", "1000",
		"--tool-call-parser", "hermes", "--chat-template", "/a b.jinja"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv\n got %q\nwant %q", got, want)
	}
}

// mlx_lm.server has no API key or vLLM sizing flags, and passing any of them
// is an argparse error that crash-loops the service.
func TestMLXCommandCarriesNoVLLMFlags(t *testing.T) {
	t.Parallel()
	p, _ := provider.Lookup(provider.MLX)
	got, err := p.Command(provider.Spec{
		Bin: "/v/bin/mlx_lm.server", Model: "mlx-community/Qwen3-4B-4bit", Port: "8001",
		MaxModelLen: "32768", MemUtilization: "0.85", APIKey: "sk-x", KVCacheBytes: "1000",
		ExtraArgs: "--host 0.0.0.0 --max-tokens 8192",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/v/bin/mlx_lm.server", "--model", "mlx-community/Qwen3-4B-4bit",
		"--port", "8001", "--host", "0.0.0.0", "--max-tokens", "8192"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv\n got %q\nwant %q", got, want)
	}
	if caps := p.Capabilities(); caps.APIKey || caps.VLLMArgs {
		t.Errorf("mlx capabilities = %+v, want none", caps)
	}
}

func TestCommandRejectsUnparseableExtraArgs(t *testing.T) {
	t.Parallel()
	for _, name := range provider.Names() {
		p, _ := provider.Lookup(name)
		if _, err := p.Command(provider.Spec{Bin: "/x", Model: "m", Port: "1", ExtraArgs: `--a "unterminated`}); err == nil {
			t.Errorf("%s: unbalanced quote in EXTRA_ARGS was accepted", name)
		}
	}
}

// The complaint names the provider's own setting, so an MLX install is not
// told to fix VLLM_BIN.
func TestValidateNamesTheProvidersBinKey(t *testing.T) {
	t.Parallel()
	p, _ := provider.Lookup(provider.MLX)
	err := provider.Validate(p, provider.Spec{Model: "m", Port: "1"})
	if err == nil || !strings.Contains(err.Error(), "MLX_LM_BIN") {
		t.Fatalf("Validate with no bin = %v, want it to name MLX_LM_BIN", err)
	}

	notExec := filepath.Join(t.TempDir(), "mlx_lm.server")
	if err := os.WriteFile(notExec, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := provider.Validate(p, provider.Spec{Bin: notExec, Model: "m", Port: "1"}); err == nil {
		t.Fatal("a non-executable bin passed validation")
	}

	bin := filepath.Join(t.TempDir(), "mlx_lm.server")
	if err := os.WriteFile(bin, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := provider.Validate(p, provider.Spec{Bin: bin, Port: "1"}); err == nil || !strings.Contains(err.Error(), "MODEL") {
		t.Fatalf("Validate with no model = %v", err)
	}
	if err := provider.Validate(p, provider.Spec{Bin: bin, Model: "m", Port: "1"}); err != nil {
		t.Fatalf("a complete spec failed validation: %v", err)
	}
}
