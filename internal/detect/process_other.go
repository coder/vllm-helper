//go:build !linux && !darwin

package detect

// listProcesses has no portable implementation; scans just come back empty.
func listProcesses() []rawProc { return nil }

func enrichPlatform(*Server) {}
