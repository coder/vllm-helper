package cli

// gatherToolchain checks what a macOS source build of vLLM actually needs.
// There are no Apple Silicon wheels, so vLLM is always compiled locally
// against Apple Clang from the Command Line Tools; Triton and FlashInfer
// are CUDA-only and never enter the picture.
func gatherToolchain() []toolchainCheck {
	return []toolchainCheck{
		{Name: "cmake", Package: "cmake", OK: hasBins("cmake")},
		{Name: "ninja", Package: "ninja", OK: hasBins("ninja")},
		// Clang ships with the Command Line Tools, which brew can't install.
		{Name: "clang", Hint: "xcode-select --install", OK: hasBins("clang")},
	}
}

func toolchainInstaller() (string, []string) {
	if !hasBins("brew") {
		return "", nil
	}
	return "brew", []string{"install"}
}
