//go:build !linux && !darwin

package cli

func gatherToolchain() []toolchainCheck { return nil }

func toolchainInstaller() (string, []string) { return "", nil }
