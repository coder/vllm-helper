package detect

// DefaultContextTarget is how much context a profile is sized for, expressed
// as a multiple of one full-length request.
//
// Four rather than one because a server holding exactly one context can serve
// exactly one request, and rather than "whatever is left" because that is the
// behaviour this replaced: a 1.1 GB model was given 101.77 GiB of KV cache --
// 23 full contexts -- on a 121 GiB machine, and spent the difference pushing
// the desktop into swap.
//
// It is a default rather than a rule because the right number is a property of
// the workload, not the hardware: a single-user box wants fewer and a shared
// endpoint wants more, and neither can be read off the machine.
const DefaultContextTarget = 4

// Plan is how a specific model should be launched on this machine.
type Plan struct {
	// KVCacheBytes is what to pass to --kv-cache-memory-bytes, or 0 when the
	// model's shape could not be read and vLLM's own profiling should stand.
	KVCacheBytes int64
	// MemUtilization is the ceiling for --gpu-memory-utilization.
	MemUtilization float64
	// Tokens is how much context KVCacheBytes buys, for explaining the
	// choice. 0 when unknown.
	Tokens int64
	// Note explains the sizing in one line, or is empty when there is
	// nothing worth saying.
	Note string
}

// PlanFor sizes the KV cache and the memory ceiling for one model.
//
// weightBytes and kvPerToken come from the Hub; maxLen is the context the
// profile will be started with. A zero kvPerToken means the config did not say
// enough, and the plan then sets no KV size at all rather than guessing: vLLM's
// own profiling is a worse answer than this one but a far better answer than a
// wrong number.
func (h Hardware) PlanFor(weightBytes, kvPerToken int64, maxLen, contextTarget int) Plan {
	if contextTarget <= 0 {
		contextTarget = DefaultContextTarget
	}
	pool := float64(h.TotalMemBytes)
	if !h.Unified && h.GPUMemBytes > 0 {
		pool = float64(h.GPUMemBytes)
	}
	if pool <= 0 {
		return Plan{}
	}

	// The ceiling is everything vLLM may touch: the machine minus what the
	// OS is holding, minus a margin for the drift between our reading and
	// vLLM's own at startup. This is what --gpu-memory-utilization should
	// have always been -- a safety limit -- rather than the KV sizing knob
	// it became by default.
	baseline := defaultBaseline(pool)
	if h.BaselineGB > 0 && h.BaselineGB*gigabyte < baselineCeiling*pool {
		baseline = h.BaselineGB * gigabyte
	}
	const drift = 2 * gibibyte
	util := (pool - baseline - drift) / pool
	switch {
	case util > 0.95:
		util = 0.95
	case util < 0.30:
		util = 0.30
	}
	p := Plan{MemUtilization: util}

	if kvPerToken <= 0 || maxLen <= 0 {
		return p
	}

	// Everything vLLM needs that is not KV cache. Weights are known exactly
	// here, so the estimate is much tighter than the budget's.
	overhead := float64(weightBytes)*(1+activationFraction) + runtimeReserve + graphReserve
	spare := pool*util - overhead
	if spare < float64(kvFloor) {
		// Not enough room for the cache we would want. Say so and leave
		// vLLM to profile it, which at least fails loudly rather than
		// quietly serving one truncated request.
		p.Note = "this model leaves little room for a KV cache -- vLLM will size it, and may not start"
		return p
	}

	want := float64(contextTarget) * float64(maxLen) * float64(kvPerToken)
	if want > spare {
		want = spare
	}
	p.KVCacheBytes = int64(want)
	p.Tokens = p.KVCacheBytes / kvPerToken

	switch n := float64(p.Tokens) / float64(maxLen); {
	case n < 1:
		p.Note = "only enough KV cache for a partial context -- lower MAX_MODEL_LEN if requests get preempted"
	default:
		p.Note = ""
	}
	return p
}
