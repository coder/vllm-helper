package cli

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// gatherToolchain verifies the build packages vLLM's JIT kernel compilation
// needs (Triton, FlashInfer). Which one bites depends on the first model
// served, so checking up front beats discovering them one crash at a time.
func gatherToolchain() []toolchainCheck {
	pyHeaders, _ := filepath.Glob("/usr/include/python3*/Python.h")
	return []toolchainCheck{
		{Name: "ninja", Package: "ninja-build", OK: hasBins("ninja")},
		{Name: "gcc/g++", Package: "build-essential", OK: hasBins("gcc", "g++")},
		{Name: "Python.h", Package: pythonDevPkg(), OK: len(pyHeaders) > 0},
	}
}

func toolchainInstaller() (string, []string) {
	if !hasBins("apt-get") {
		return "", nil
	}
	return "sudo", []string{"apt-get", "install", "-y"}
}

func pythonDevPkg() string {
	out, err := exec.Command("python3", "-c", "import sys; print(f'python{sys.version_info.major}.{sys.version_info.minor}-dev')").Output()
	if err != nil {
		return "python3-dev"
	}
	return strings.TrimSpace(string(out))
}
