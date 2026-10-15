package coder

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/plugin"
	"github.com/coder/vllm-helper/internal/profile"
)

func profileWith(t *testing.T, body string) *profile.Profile {
	t.Helper()
	dir := t.TempDir()
	dirs := config.Dirs{Config: dir, Profiles: filepath.Join(dir, "models")}
	if err := os.MkdirAll(dirs.Profiles, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dirs.ProfileFile("ornith"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := profile.Load(dirs, "ornith")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestModelConfigFromProfile(t *testing.T) {
	p := profileWith(t, "MODEL=deepreinforce-ai/Ornith-1.0-35B-FP8\nMAX_MODEL_LEN=65536\n")

	enabled, err := modelConfig(nil, p, true)
	if err != nil {
		t.Fatalf("modelConfig: %v", err)
	}
	if enabled.Model != "deepreinforce-ai/Ornith-1.0-35B-FP8" {
		t.Errorf("Model = %q", enabled.Model)
	}
	if enabled.DisplayName != "Ornith 1.0 35B FP8" {
		t.Errorf("DisplayName = %q", enabled.DisplayName)
	}
	if enabled.ContextLimit != 65536 {
		t.Errorf("ContextLimit = %d, want 65536", enabled.ContextLimit)
	}
	if !enabled.Enabled || !enabled.IsDefault {
		t.Error("the active model should be both enabled and the default")
	}

	disabled, err := modelConfig(nil, p, false)
	if err != nil {
		t.Fatalf("modelConfig: %v", err)
	}
	if disabled.Enabled || disabled.IsDefault {
		t.Error("a backfilled entry should be neither enabled nor the default")
	}
}

// Coder rejects context_limit <= 0, so a MAX_MODEL_LEN we can't turn into
// one has to be reported here. Sending it produced an HTTP 400 that said
// nothing about which local setting was wrong.
func TestModelConfigRejectsAContextLimitCoderWouldRefuse(t *testing.T) {
	for _, length := range []string{"", "lots", "0", "-1"} {
		t.Run(length, func(t *testing.T) {
			p := profileWith(t, "MODEL=some/model\nMAX_MODEL_LEN="+length+"\n")
			if _, err := modelConfig(nil, p, true); err == nil {
				t.Errorf("MAX_MODEL_LEN=%q was accepted, want an error naming the setting", length)
			}
		})
	}
}

// Opting into an event without handling it would be a silent no-op, so every
// event this plugin advertises has to reach a real branch. An unconfigured
// Config means the handlers bail immediately, which is enough to tell "no
// handler" apart from "handled, then declined to do anything".
func TestEveryAdvertisedEventIsHandled(t *testing.T) {
	p := Plugin{}
	for _, e := range p.Events() {
		if err := p.Handle(context.Background(), e, plugin.State{Config: &config.Config{File: &config.EnvFile{}}}); err != nil {
			t.Errorf("Events() advertises %q but Handle rejected it: %v", e, err)
		}
	}
	if err := p.Handle(context.Background(), plugin.Event("invented"), plugin.State{}); err == nil {
		t.Error("an event with no handler was accepted, which would be a silent no-op")
	}
}
