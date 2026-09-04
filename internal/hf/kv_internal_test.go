package hf

import "testing"

// Every figure below was read off a live config.json and, where noted,
// confirmed against what vLLM then allocated on real hardware.
func TestKVBytesPerTokenMatchesWhatVLLMAllocates(t *testing.T) {
	tests := []struct {
		name   string
		arch   Arch
		maxLen int
		want   int64
	}{
		{
			// The load-bearing one. vLLM reported 952,784 tokens in 101.77
			// GiB for this model, which is 114,688 bytes per token.
			name: "Qwen3-0.6B",
			arch: Arch{
				NumHiddenLayers: 28, NumAttentionHeads: 16, NumKeyValueHeads: 8,
				HeadDim: 128, HiddenSize: 1024, TorchDtype: "bfloat16",
			},
			maxLen: 40960, want: 114688,
		},
		{
			name: "Qwen3-Coder-30B-A3B (grouped-query)",
			arch: Arch{
				NumHiddenLayers: 48, NumAttentionHeads: 32, NumKeyValueHeads: 4,
				HeadDim: 128, HiddenSize: 2048, TorchDtype: "bfloat16",
			},
			maxLen: 262144, want: 2 * 48 * 4 * 128 * 2,
		},
		{
			name: "float32 weights cache wider",
			arch: Arch{
				NumHiddenLayers: 4, NumKeyValueHeads: 2, HeadDim: 64,
				TorchDtype: "float32",
			},
			maxLen: 4096, want: 2 * 4 * 2 * 64 * 4,
		},
		{
			name: "no grouped-query attention falls back to all heads",
			arch: Arch{
				NumHiddenLayers: 8, NumAttentionHeads: 12, HeadDim: 64,
				TorchDtype: "bfloat16",
			},
			maxLen: 4096, want: 2 * 8 * 12 * 64 * 2,
		},
		{
			name:   "a config that says nothing gives nothing",
			arch:   Arch{ModelType: "llama"},
			maxLen: 4096, want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.arch.KVBytesPerToken(tt.maxLen); got != tt.want {
				t.Errorf("KVBytesPerToken() = %d, want %d", got, tt.want)
			}
		})
	}
}

// Qwen3-0.6B declares head_dim 128 while hidden_size/num_attention_heads is
// 64. Deriving it would halve the cache we ask for, and a KV cache sized at
// half of what the model needs is an OOM partway through serving rather than a
// refusal at startup.
func TestADeclaredHeadDimBeatsTheDerivedOne(t *testing.T) {
	a := Arch{
		NumHiddenLayers: 28, NumAttentionHeads: 16, NumKeyValueHeads: 8,
		HeadDim: 128, HiddenSize: 1024, TorchDtype: "bfloat16",
	}
	derived := int64(2 * 28 * 8 * (1024 / 16) * 2)
	if got := a.KVBytesPerToken(131072); got == derived {
		t.Fatalf("derived head_dim from hidden_size: got %d, the declared value gives %d", got, derived*2)
	}
}

// Nemotron-H is 52 layers of which only a few attend; the rest are Mamba and
// hold no cache at all. Counting them all overstates the cache several times
// over, which on a machine sized to the answer means refusing models that fit.
func TestHybridsCountOnlyTheirAttentionLayers(t *testing.T) {
	types := make([]string, 52)
	for i := range types {
		types[i] = "mamba"
	}
	for _, i := range []int{3, 15, 27, 39} {
		types[i] = "attention"
	}
	a := Arch{
		NumHiddenLayers: 52, LayerTypes: types,
		NumKeyValueHeads: 2, HeadDim: 128, TorchDtype: "bfloat16",
	}
	want := int64(2 * 4 * 2 * 128 * 2)
	if got := a.KVBytesPerToken(131072); got != want {
		t.Errorf("KVBytesPerToken() = %d, want %d (4 attention layers of 52)", got, want)
	}
}

func TestMultimodalReposReadTheTextConfig(t *testing.T) {
	// The top level describes the wrapper; the language model's shape, which
	// is what holds a KV cache, is one level down.
	a := Arch{
		ModelType: "gemma3",
		TextConfig: &Arch{
			NumHiddenLayers: 34, NumKeyValueHeads: 4, HeadDim: 256,
			MaxPositionEmbed: 131072, TorchDtype: "bfloat16",
		},
	}
	if got, want := a.KVBytesPerToken(131072), int64(2*34*4*256*2); got != want {
		t.Errorf("KVBytesPerToken() = %d, want %d", got, want)
	}
	if got := a.MaxContext(); got != 131072 {
		t.Errorf("MaxContext() = %d, want 131072", got)
	}
}

// gpt-oss-120b interleaves 18 full-attention layers with 18 sliding ones at a
// window of 128. Counting all 36 as full doubles the estimate: measured on a
// GB10, vLLM put 1,015,718 tokens in the 38.7 GB this asked for, where the
// doubled figure predicted 524k.
func TestSlidingLayersCostTheirWindowNotTheContext(t *testing.T) {
	types := make([]string, 36)
	for i := range types {
		if i%2 == 0 {
			types[i] = "sliding_attention"
		} else {
			types[i] = "full_attention"
		}
	}
	a := Arch{
		NumHiddenLayers: 36, LayerTypes: types, SlidingWindow: 128,
		NumAttentionHeads: 64, NumKeyValueHeads: 8, HeadDim: 64,
		MaxPositionEmbed: 131072, TorchDtype: "bfloat16",
	}
	const maxLen = 131072
	got := a.KVBytesPerToken(maxLen)

	// 18 full layers at the whole context, 18 sliding at 128 tokens.
	perLayer := int64(2 * 8 * 64 * 2)
	want := perLayer * (18*maxLen + 18*128) / maxLen
	if got != want {
		t.Errorf("KVBytesPerToken(%d) = %d, want %d", maxLen, got, want)
	}

	// Against the machine: 38.7 GB really held 1,015,718 tokens, so the
	// estimate has to land near that and never far below it -- under-
	// reporting hands out a cache smaller than the requests it promised.
	const pinned = 38654705664
	predicted := pinned / got
	if predicted < 950_000 || predicted > 1_150_000 {
		t.Errorf("38.7 GB predicted as %d tokens; vLLM measured 1,015,718", predicted)
	}

	// And the bug: treating every layer as full.
	if allFull := perLayer * 36; got >= allFull {
		t.Errorf("KVBytesPerToken = %d, no cheaper than counting all 36 layers full (%d)", got, allFull)
	}
}

// A model with no sliding layers must be unaffected by the window handling.
func TestADenseModelIgnoresSlidingWindow(t *testing.T) {
	a := Arch{
		NumHiddenLayers: 28, NumKeyValueHeads: 8, HeadDim: 128,
		TorchDtype: "bfloat16",
	}
	if got, want := a.KVBytesPerToken(40960), int64(114688); got != want {
		t.Errorf("KVBytesPerToken = %d, want %d", got, want)
	}
}
