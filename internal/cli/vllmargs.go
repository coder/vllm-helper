package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/detect"
	"github.com/coder/vllm-helper/internal/profile"
)

type serveSpec struct {
	Bin            string
	Model          string
	Port           string
	MaxModelLen    string
	MemUtilization string
	APIKey         string
	KVCacheBytes   string
	ExtraArgs      string
}

func serveSpecFromActive(active *config.EnvFile) serveSpec {
	return serveSpec{
		Bin:            active.Get("VLLM_BIN"),
		Model:          active.Get("MODEL"),
		Port:           active.Get("PORT"),
		MaxModelLen:    active.Get("MAX_MODEL_LEN"),
		MemUtilization: active.Get("GPU_MEMORY_UTILIZATION"),
		APIKey:         active.Get("VLLM_API_KEY"),
		KVCacheBytes:   active.Get("KV_CACHE_MEMORY_BYTES"),
		ExtraArgs:      active.Get("EXTRA_ARGS"),
	}
}

func serveSpecFromProfile(cfg *config.Config, p *profile.Profile, port string) serveSpec {
	if port == "" {
		port = cfg.Port()
	}
	return serveSpec{
		Bin:            cfg.VLLMBin(),
		Model:          p.Model(),
		Port:           port,
		MaxModelLen:    p.File.Get("MAX_MODEL_LEN"),
		MemUtilization: p.File.Get("GPU_MEMORY_UTILIZATION"),
		APIKey:         cfg.APIKey(),
		KVCacheBytes:   p.File.Get("KV_CACHE_MEMORY_BYTES"),
		ExtraArgs:      p.ExtraArgs(),
	}
}

func (s serveSpec) validate() error {
	if s.Bin == "" {
		return fmt.Errorf("VLLM_BIN is not set in config.env")
	}
	if info, err := os.Stat(s.Bin); err != nil || info.Mode()&0o111 == 0 {
		return fmt.Errorf("VLLM_BIN (%s) does not exist or is not executable", s.Bin)
	}
	if s.Model == "" {
		return fmt.Errorf("MODEL is not set")
	}
	if s.Port == "" {
		return fmt.Errorf("PORT is not set")
	}
	return nil
}

func (s serveSpec) args(extra ...string) ([]string, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	args := []string{s.Bin, "serve", s.Model,
		"--port", s.Port,
		"--max-model-len", s.MaxModelLen,
		"--gpu-memory-utilization", fitMemUtilization(s.MemUtilization, s.KVCacheBytes != ""),
		"--api-key", s.APIKey,
	}
	if s.KVCacheBytes != "" {
		args = append(args, "--kv-cache-memory-bytes", s.KVCacheBytes)
	}
	if s.ExtraArgs != "" {
		words, err := config.SplitArgs(s.ExtraArgs)
		if err != nil {
			return nil, fmt.Errorf("EXTRA_ARGS: %w", err)
		}
		args = append(args, words...)
	}
	args = append(args, extra...)
	return args, nil
}

func serveEnvFromActive(active *config.EnvFile) []string {
	env := os.Environ()
	for _, k := range active.Keys() {
		env = append(env, k+"="+active.Get(k))
	}
	return env
}

func serveEnvFromProfile(cfg *config.Config, p *profile.Profile) []string {
	return append(os.Environ(), config.MergedEnv(cfg.File, cfg.Secrets, p.File)...)
}

// redactServeArgs hides credentials in a command about to be printed. Both
// spellings vLLM accepts have to be covered: EXTRA_ARGS is free text, and
// "--api-key=sk-..." there would otherwise reach a dry run verbatim.
func redactServeArgs(args []string) []string {
	out := append([]string{}, args...)
	for i := 0; i < len(out); i++ {
		if !strings.HasPrefix(out[i], "--") {
			continue
		}
		if flag, _, inline := strings.Cut(out[i], "="); inline {
			if detect.LooksSecret(flag) {
				out[i] = flag + "=<redacted>"
			}
			continue
		}
		if detect.LooksSecret(out[i]) && i+1 < len(out) {
			out[i+1] = "<redacted>"
			i++
		}
	}
	return out
}
