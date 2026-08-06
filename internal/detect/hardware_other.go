//go:build !linux && !darwin

package detect

// totalMemGB has no portable implementation; a 0 here just means the budget
// falls back to whatever MODEL_BUDGET_GB says in config.env.
func totalMemGB() int { return 0 }

// AvailableMemMB has no portable implementation.
func AvailableMemMB() int { return 0 }

func describeLocalGPU(*Hardware) {}
