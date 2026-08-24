package detect

import (
	"runtime"
	"testing"
)

// Which machines share one memory pool with the CPU. This decides whether
// --gpu-memory-utilization gets capped, so a wrong answer either leaves a
// Spark uncapped (it can wedge the host) or caps a discrete card for no reason.
func TestIsUnified(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		gpuName      string
		gpuMemGB     int
		totalMemGB   int
		nvidiaDevice bool
		want         bool
	}{
		// nvidia-smi reports "[N/A]" for memory.total on a GB10, so the name
		// is the only thing that identifies it.
		{name: "DGX Spark", gpuName: "NVIDIA GB10", totalMemGB: 121, want: true},
		{name: "GH200", gpuName: "NVIDIA GH200 480GB", totalMemGB: 480, want: true},
		{name: "Grace-Blackwell", gpuName: "NVIDIA GB200", totalMemGB: 384, want: true},
		{name: "Jetson", gpuName: "Orin (nvgpu)", totalMemGB: 62, want: true},
		{name: "Thor", gpuName: "NVIDIA Thor", totalMemGB: 128, want: true},
		{
			name: "a pool the size of system RAM",
			// Anything reporting nearly all of system memory as its own is
			// sharing it, whatever it calls itself.
			gpuName: "Unknown Integrated", gpuMemGB: 120, totalMemGB: 128, want: true,
		},
		{
			name:    "discrete card stays discrete",
			gpuName: "NVIDIA RTX A6000", gpuMemGB: 48, totalMemGB: 256, want: false,
		},
		{
			name:    "consumer Blackwell is discrete",
			gpuName: "NVIDIA GeForce RTX 5090", gpuMemGB: 32, totalMemGB: 128, want: false,
		},
		{name: "no GPU at all", totalMemGB: 15, want: false},
		{
			// The version file outlives a working GPU -- a driver upgrade
			// without a reboot, or a container started without --gpus -- and
			// claiming unified there would skip the clamp issue #13 needs.
			name: "device node absent means CPU backend", gpuName: "", totalMemGB: 15,
			nvidiaDevice: false, want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isUnified(tt.gpuName, tt.gpuMemGB, tt.totalMemGB, tt.nvidiaDevice)
			if got != tt.want {
				t.Errorf("isUnified(%q, %d, %d, %v) = %v, want %v",
					tt.gpuName, tt.gpuMemGB, tt.totalMemGB, tt.nvidiaDevice, got, tt.want)
			}
		})
	}
}

// An NVIDIA device we could not describe says a GPU is present but nothing
// about its memory. Architecture settles it: every NVIDIA part on arm64 is
// integrated, while an undescribed x86 GPU is discrete and keeps the discrete
// treatment.
func TestIsUnifiedUndescribedGPUFollowsArch(t *testing.T) {
	t.Parallel()

	got := isUnified("", 0, 121, true)
	if want := runtime.GOARCH == "arm64"; got != want {
		t.Errorf("isUnified with an undescribed NVIDIA device = %v, want %v on %s",
			got, want, runtime.GOARCH)
	}
}
