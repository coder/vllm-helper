package detect

import "sync"

// MetalMemUtilization is the --gpu-memory-utilization a new profile gets under
// vllm-metal. There the flag is a share of the Metal working set, which macOS
// already holds below installed RAM, so it can sit higher than the 0.85 that
// a DGX Spark needs. It stays a little under vllm-metal's own 0.92 default to
// leave room for the KV commit probe and a second process.
const MetalMemUtilization = 0.9

// metalWorkingSetGuess is the share of RAM taken as the working set when MLX
// could not report it. macOS commonly lets the GPU wire about three quarters
// of RAM; larger Macs get more, so this errs low.
const metalWorkingSetGuess = 0.75

// metalState is one registration of the vllm-metal probes. Hardware alone
// cannot answer whether a Mac serves on its GPU: the same Mac runs vLLM's CPU
// backend or the GPU depending on which vLLM is installed, and the two spend
// memory differently.
//
// The two questions are probed separately because their costs differ. Whether
// vllm_metal is importable takes a module lookup; the working set needs an MLX
// import, which only the commands that size memory should pay for. Each is
// asked at most once per registration, and its answer lands in this state, so
// a probe still running when a newer registration replaces it cannot
// overwrite the newer answer.
type metalState struct {
	installed  func() bool
	workingSet func() int64

	installedOnce sync.Once
	on            bool
	workingOnce   sync.Once
	wsBytes       int64
}

var (
	metalMu sync.Mutex
	metal   *metalState
)

// UseMetalProbe registers how to ask the configured vLLM about vllm-metal:
// installed reports whether the plugin is importable, workingSet returns MLX's
// max_recommended_working_set_size in bytes (0 when unknown). Neither runs
// until a caller needs its answer, because each starts a Python interpreter.
// A nil installed clears the registration.
func UseMetalProbe(installed func() bool, workingSet func() int64) {
	metalMu.Lock()
	defer metalMu.Unlock()
	if installed == nil {
		metal = nil
		return
	}
	metal = &metalState{installed: installed, workingSet: workingSet}
}

// currentMetal returns the registration in force, or nil when there is none.
// Callers that need both answers take one snapshot so they agree.
func currentMetal() *metalState {
	metalMu.Lock()
	defer metalMu.Unlock()
	return metal
}

// runtime reports whether vllm_metal is importable. A nil state is false.
func (s *metalState) runtime() bool {
	if s == nil {
		return false
	}
	s.installedOnce.Do(func() { s.on = s.installed() })
	return s.on
}

// workingSetBytes reads the Metal working set, or 0 when vllm-metal is not
// installed or MLX could not report it. It is not probed without vllm-metal,
// since MLX is not there to ask.
func (s *metalState) workingSetBytes() int64 {
	if !s.runtime() || s.workingSet == nil {
		return 0
	}
	s.workingOnce.Do(func() { s.wsBytes = max(s.workingSet(), 0) })
	return s.wsBytes
}

// MetalRuntime reports whether the configured vLLM serves on the Apple Silicon
// GPU through vllm-metal. It never imports MLX, so it is cheap enough for
// 'start' and for choosing a profile's defaults.
func MetalRuntime() bool { return currentMetal().runtime() }
