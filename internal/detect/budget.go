package detect

import (
	"math"
	"strconv"
	"strings"
)

// The weights budget answers one question: how big a model can this machine
// load and still have room to serve it? Everything here is derived from vLLM's
// own memory profiler, read out of 14 starts on a DGX Spark spanning
// facebook/opt-125m to a 35B dense model. Those runs say four useful things.
//
// Weights on disk are weights in memory. A 58.82 GiB checkpoint reported
// "Model loading took 58.93 GiB"; a 65.40 GiB one took 65.53 GiB. Within 0.2%
// across a 260x size range, so the Hub's byte count is a sound basis and the
// only care needed is to compare it in the same units.
//
// The runtime overhead does not scale with the model. Non-torch memory was
// 0.60 GiB for a 125M model and 0.05-4.00 GiB for a 30B one -- noise, not a
// function of size. CUDA graphs cost at most 0.98 GiB and were sometimes
// negative. Both are constants, and the percentage-of-RAM rule this replaced
// was wrong in both directions at once: absurdly generous on a 128 GB box and
// far too tight on a 16 GB laptop.
//
// Activation is the one term that does track model size, at 0.05-3.08 GiB over
// that same range, or roughly 5% of weights.
//
// And a model you cannot serve is not a model that fits, so the budget also
// holds back enough for a usable KV cache.
const (
	gibibyte = 1 << 30
	gigabyte = 1e9

	// runtimeReserve covers Python, the CUDA context and everything else
	// vLLM counts as non-torch. Set above the 4.00 GiB high-water mark.
	runtimeReserve = 4.5 * gibibyte

	// graphReserve covers torch.compile artefacts and captured CUDA graphs.
	// Measured max 0.98 GiB.
	graphReserve = 1.5 * gibibyte

	// activationFraction is peak activation as a share of weights. Measured
	// max was 4.7% (3.08 GiB against 65.53 GiB of weights).
	activationFraction = 0.06

	// kvFloor is the smallest KV cache worth calling a served model, capped
	// at a quarter of the pool so a small card is not budgeted to zero.
	kvFloor     = 6 * gibibyte
	kvFloorFrac = 0.25

	// A discrete card holds only what CUDA puts there: the host runtime is
	// somebody else's memory, and the driver's own footprint is small.
	discreteBaseline = 0.5 * gibibyte
	discreteRuntime  = 1.5 * gibibyte
)

// weightsBudget solves for the largest model whose weights, overhead and a
// usable KV cache all fit in pool.
//
//	w + baseline + runtime + graphs + activationFraction*w + kv <= pool
//
// Closed form rather than a search, since every term is either constant or
// linear in w. Returns 0 when the fixed costs already exceed the pool, which
// is a real answer: such a machine cannot serve anything.
func weightsBudget(pool, baseline, runtime float64) float64 {
	kv := math.Min(kvFloor, kvFloorFrac*pool)
	free := pool - baseline - runtime - graphReserve - kv
	if free <= 0 {
		return 0
	}
	return free / (1 + activationFraction)
}

// defaultBaseline estimates what the operating system is holding when we could
// not measure it. Proportional in the middle, because a desktop's footprint
// does grow with the machine it was installed on, but bounded: 6% of a 128 GB
// box would be 7.7 GB, which is about right, while 6% of an 8 GB one would be
// far less than any Linux install actually uses.
func defaultBaseline(pool float64) float64 {
	const (
		frac = 0.06
		lo   = 2 * gibibyte
		hi   = 8 * gibibyte
	)
	switch b := frac * pool; {
	case b < lo:
		return lo
	case b > hi:
		return hi
	default:
		return b
	}
}

// baselineCeiling is the most of the pool a reading may claim before we stop
// believing it describes an idle machine.
//
// The reading we can take cheaply is "memory in use right now", and on the
// machine this matters most for -- unified memory, where the model lives in
// the same pool -- a loaded model dominates it completely. Sampling while our
// own service is up gives 111 GiB of 121 GiB and a budget of nothing. Rather
// than teach this package about service state, treat an implausible baseline
// as no reading at all: an idle machine is under the ceiling, and a busy one
// falls back to the estimate, which is what we would have used anyway.
const baselineCeiling = 0.15

// Budget reports the largest model weights this machine should be asked to
// load, in bytes, with a line explaining the figure.
//
// usedBytes is memory in use now; pass 0 when unknown. A discrete GPU is
// measured against its own VRAM, since that is the pool the weights land in
// and the host's RAM is irrelevant to whether they fit.
func Budget(totalBytes, gpuBytes, usedBytes int64, unified bool) (int64, string) {
	pool, runtime, baseline := float64(totalBytes), float64(runtimeReserve), 0.0
	reason := "unified/system memory, minus the OS and vLLM's own overhead"

	switch {
	case !unified && gpuBytes > 0:
		pool, runtime, baseline = float64(gpuBytes), discreteRuntime, discreteBaseline
		reason = "discrete VRAM, minus CUDA's overhead and room for a KV cache"
	case usedBytes > 0 && float64(usedBytes) < baselineCeiling*pool:
		baseline = float64(usedBytes)
		reason = "unified/system memory, minus a measured OS baseline and vLLM's own overhead"
	default:
		baseline = defaultBaseline(pool)
	}

	if pool <= 0 {
		return 0, "unknown: this machine's memory could not be measured"
	}
	return int64(weightsBudget(pool, baseline, runtime)), reason
}

// BudgetGB reports the weights budget in decimal gigabytes, honouring an
// override from config.env when one is set.
//
// The override exists for machines whose real ceiling we cannot see -- a box
// sharing its GPU with something else, or one whose owner simply knows better.
// An unparseable or non-positive value is treated as absent rather than as
// zero, since a zero budget silently rejects every model on the Hub.
func BudgetGB(override string) float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(override), 64); err == nil && v > 0 {
		return v
	}
	return float64(Probe().BudgetBytes) / gigabyte
}
