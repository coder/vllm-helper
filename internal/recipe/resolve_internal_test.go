package recipe

import (
	"slices"
	"strings"
	"testing"
)

// sparkTarget is the DGX Spark GB10 profile: an exact hardware id, its GPU
// generation, and the arch the forbidden-flag check keys on.
var sparkTarget = Target{Hardware: "dgx_spark_gb10", Generation: "blackwell", Arch: "sm121"}

const deepseek0731 = "deepseek-ai/DeepSeek-V4-Flash-0731"

func load(t *testing.T) Index {
	t.Helper()
	ix, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return ix
}

// The released checkpoint has no recipe file of its own: it is a variant of the
// DeepSeek-V4-Flash recipe, so lookup has to search variant model_ids.
func TestLookupFindsRecipeByVariantModelID(t *testing.T) {
	r, variant, ok := load(t).Lookup(deepseek0731)
	if !ok {
		t.Fatalf("no recipe for %s", deepseek0731)
	}
	if r.Slug != "deepseek-v4-flash" || variant != "default" {
		t.Fatalf("got slug %q variant %q, want deepseek-v4-flash/default", r.Slug, variant)
	}
}

func TestLookupMissingRepo(t *testing.T) {
	if _, _, ok := load(t).Lookup("QuantTrio/MiniMax-M2-AWQ"); ok {
		t.Fatal("community quant should not resolve to a recipe")
	}
}

func resolveSpark(t *testing.T) Resolved {
	t.Helper()
	ix := load(t)
	r, variant, ok := ix.Lookup(deepseek0731)
	if !ok {
		t.Fatalf("no recipe for %s", deepseek0731)
	}
	got, err := Resolve(r, variant, "multi_node_tp",
		[]string{"tool_calling", "reasoning", "spec_decoding"}, sparkTarget)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return got
}

// Golden: the verified two-node DGX Spark launch. A merge-order regression here
// is what put SM100-only kernels on GB10 before.
func TestResolveSparkMultiNodeTP(t *testing.T) {
	got := resolveSpark(t)

	if got.ModelID != deepseek0731 {
		t.Errorf("ModelID = %q", got.ModelID)
	}
	if got.Image != "eugr/spark-vllm-b12x:latest" {
		t.Errorf("Image = %q", got.Image)
	}
	if got.PipPath {
		t.Error("PipPath = true, want false: there is no wheel path on Spark")
	}
	if !got.Verified {
		t.Error("Verified = false, want true")
	}
	if got.MinVLLM != "0.25.0" {
		t.Errorf("MinVLLM = %q", got.MinVLLM)
	}
	if got.VRAMMinimumGB != 200 {
		t.Errorf("VRAMMinimumGB = %v", got.VRAMMinimumGB)
	}
	if len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "2-node TP launch") {
		t.Errorf("Notes = %v", got.Notes)
	}

	want := []string{
		"--trust-remote-code",
		"--kv-cache-dtype", "fp8",
		"--block-size", "256",
		"--reasoning-parser", "deepseek_v4",
		"--reasoning-config", `{"reasoning_parser":"deepseek_v4","reasoning_start_str":"","reasoning_end_str":""}`,
		"--speculative-config", `{"method":"dspark","num_speculative_tokens":5,"draft_sample_method":"probabilistic","attention_backend":"B12X_MLA_SPARSE"}`,
		"--tokenizer-mode", "deepseek_v4",
		"--tool-call-parser", "deepseek_v4",
		"--enable-auto-tool-choice",
		"--max-num-seqs", "8",
		"--max-num-batched-tokens", "8192",
		"--gpu-memory-utilization", "0.85",
		"--enable-prefix-caching",
		"--default-chat-template-kwargs.thinking=true",
		"--default-chat-template-kwargs.reasoning_effort=high",
		"--load-format", "instanttensor",
		"--moe-backend", "b12x",
		"--linear-backend", "b12x",
		"--attention-backend", "B12X_MLA_SPARSE",
		"--max-cudagraph-capture-size", "64",
		"--compilation-config", `{"cudagraph_mode":"FULL_AND_PIECEWISE","custom_ops":["all"]}`,
	}
	if !slices.Equal(got.Args, want) {
		t.Errorf("args mismatch: got %q want %q", got.Args, want)
	}

	for k, v := range map[string]string{
		"CUTE_DSL_ARCH":     "sm_121a",
		"B12X_MOE_FORCE_A8": "1",
		"NCCL_IB_HCA":       "$IB_IF",
	} {
		if got.Env[k] != v {
			t.Errorf("Env[%s] = %q, want %q", k, got.Env[k], v)
		}
	}
}

// The strategy-level blackwell override must REPLACE the recipe- and
// variant-level ones rather than layer on top, or these two SM100-only flags
// survive onto GB10.
func TestSparkDropsGenerationLevelBlackwellOverrides(t *testing.T) {
	got := resolveSpark(t)
	joined := strings.Join(got.Args, " ")
	for _, bad := range []string{"deep_gemm_mega_moe", "use_fp4_indexer_cache"} {
		if strings.Contains(joined, bad) {
			t.Errorf("resolved args still contain %q: %s", bad, joined)
		}
	}
}

// Fail closed: if a future catalog edit reintroduces an SM100-only kernel on
// Spark, resolution must error instead of shipping a command that aborts.
func TestForbiddenArgsFailClosed(t *testing.T) {
	r := Recipe{
		Slug: "synthetic", ModelID: "acme/model",
		Variants: map[string]Variant{"default": {
			HardwareOverrides: map[string]Override{
				"dgx_spark_gb10": {ExtraArgs: []string{"--moe-backend", "deep_gemm_mega_moe"}},
			},
		}},
		CompatibleStrategies: []string{"multi_node_tp"},
	}
	if _, err := Resolve(r, "default", "multi_node_tp", nil, sparkTarget); err == nil {
		t.Fatal("expected resolution to fail on a forbidden sm121 flag")
	}
}

// multi_node_tp is absent from compatible_strategies and granted only by
// strategy_hardware, so reading the strategy list alone hides the layout the
// two-Spark cluster needs.
func TestStrategyHardwareGrantsAndRevokes(t *testing.T) {
	ix := load(t)
	r, _, ok := ix.Lookup(deepseek0731)
	if !ok {
		t.Fatal("recipe missing")
	}
	if slices.Contains(r.CompatibleStrategies, "multi_node_tp") {
		t.Fatal("fixture drifted: multi_node_tp should not be in compatible_strategies")
	}
	if !r.StrategySupported("multi_node_tp", sparkTarget) {
		t.Error("multi_node_tp should be granted on dgx_spark_gb10")
	}
	npu := Target{Hardware: "ascend_950dt_8x", Generation: "npu"}
	if r.StrategySupported("single_node_tep", npu) {
		t.Error("single_node_tep should be revoked on npu")
	}
}

func TestDedupeArgsLastWinsAndKeepsInlineForm(t *testing.T) {
	got := dedupeArgs([]string{
		"--moe-backend", "deep_gemm_mega_moe",
		"--flag=one",
		"--enable-prefix-caching",
		"--moe-backend", "b12x",
		"--flag=two",
	})
	want := []string{"--enable-prefix-caching", "--moe-backend", "b12x", "--flag=two"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
