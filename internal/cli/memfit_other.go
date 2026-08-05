//go:build !darwin

package cli

// fitMemUtilization is a no-op wherever vLLM has a real GPU backend:
// --gpu-memory-utilization refers to VRAM, which nothing else on the machine
// is competing for.
func fitMemUtilization(want string) string { return want }
