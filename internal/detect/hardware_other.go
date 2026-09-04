//go:build !linux && !darwin

package detect

// totalMemBytes has no portable implementation; a 0 here just means the budget
// falls back to whatever MODEL_BUDGET_GB says in config.env.
func totalMemBytes() int64 { return 0 }

// AvailableMemMB has no portable implementation.
func AvailableMemMB() int { return 0 }

func describeLocalGPU(*Hardware) {}
