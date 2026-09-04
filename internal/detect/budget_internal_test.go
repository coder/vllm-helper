package detect

import "testing"

// Figures below come from vLLM's memory profiler on a DGX Spark (GB10, 121.69
// GiB visible), read out of the service journal. They are the reason the
// constants are what they are, so they belong in the tests rather than only in
// a commit message.
// unifiedTotal is MemTotal from a 128 GB unified-memory machine: 2 GB is
// carved off before the OS sees it, which is why the figure is not round.
const unifiedTotal = 127600592 * 1024

func gib(f float64) float64 { return f * gibibyte }

func TestTheBudgetLeavesRoomForWhatVLLMActuallyUses(t *testing.T) {
	// Every start this machine logged, as (weights loaded, total consumed
	// including runtime, activation and CUDA graphs) in GiB. If the budget
	// admits a model, the machine has to be able to hold it.
	runs := []struct {
		name              string
		weights, consumed float64
	}{
		{"opt-125m", 0.24, 1.24},
		{"Qwen3-0.6B", 1.12, 2.86},
		{"Nemotron-30B NVFP4", 18.66, 22.49},
		{"Nemotron-30B BF16", 58.93, 64.34},
		{"Ornith-35B", 65.53, 71.24},
	}
	budget, _ := Budget(unifiedTotal, 0, 0, true)
	for _, r := range runs {
		if gib(r.weights) > float64(budget) {
			continue // too big for this machine; nothing to check
		}
		// The overhead we predict for it must cover what it really used.
		predicted := gib(r.weights)*(1+activationFraction) + runtimeReserve + graphReserve
		if predicted < gib(r.consumed) {
			t.Errorf("%s: budgeted %.2f GiB of memory for a model that used %.2f GiB",
				r.name, predicted/gibibyte, r.consumed)
		}
		// And it must still leave a KV cache worth having.
		spare := unifiedTotal - gib(r.consumed) - defaultBaseline(unifiedTotal)
		if spare < kvFloor {
			t.Errorf("%s: only %.1f GiB left for KV cache", r.name, spare/gibibyte)
		}
	}
}

func TestTheBudgetFitsInTheMachine(t *testing.T) {
	// A budget that cannot itself be served is the bug this replaced: at the
	// old 70%, a model exactly at budget plus overhead overflowed the box.
	for _, total := range []int64{
		8 * gibibyte, 16 * gibibyte, 32 * gibibyte, 64 * gibibyte, unifiedTotal, 512 * gibibyte,
	} {
		budget, _ := Budget(total, 0, 0, true)
		w := float64(budget)
		committed := w*(1+activationFraction) + runtimeReserve + graphReserve + defaultBaseline(float64(total))
		if committed > float64(total) {
			t.Errorf("total %.0f GiB: budget %.1f GiB commits %.1f GiB",
				float64(total)/gibibyte, w/gibibyte, committed/gibibyte)
		}
	}
}

func TestABusyMachineDoesNotGetABudgetOfNothing(t *testing.T) {
	// Probing while our own service is up reads 111 of 121 GiB in use. Taken
	// as an OS baseline that would leave nothing, and every model on the Hub
	// would be reported as too big -- on the machine that was serving one.
	busy, _ := Budget(unifiedTotal, 0, 111*gibibyte, true)
	idle, _ := Budget(unifiedTotal, 0, 0, true)
	if busy != idle {
		t.Errorf("a loaded model changed the budget: busy %.1f GiB, idle %.1f GiB",
			float64(busy)/gibibyte, float64(idle)/gibibyte)
	}
}

func TestAMeasuredBaselineIsUsedWhenItLooksIdle(t *testing.T) {
	// vLLM reported 5.5-6.0 GiB in use before loading anything. A machine
	// that lean should be rewarded for it.
	lean, _ := Budget(unifiedTotal, 0, 5.5*gibibyte, true)
	estimated, _ := Budget(unifiedTotal, 0, 0, true)
	if lean <= estimated {
		t.Errorf("measured-idle budget %.1f GiB should beat the %.1f GiB estimate",
			float64(lean)/gibibyte, float64(estimated)/gibibyte)
	}
}

func TestDiscreteVRAMIsBudgetedAgainstTheCardNotTheHost(t *testing.T) {
	// 24 GiB card in a 128 GiB host. The weights land in VRAM, so the host's
	// memory says nothing about whether they fit.
	got, reason := Budget(128*gibibyte, 24*gibibyte, 0, false)
	if float64(got) >= 24*gibibyte {
		t.Errorf("budget %.1f GiB does not fit a 24 GiB card", float64(got)/gibibyte)
	}
	if float64(got) < 10*gibibyte {
		t.Errorf("budget %.1f GiB is uselessly small for a 24 GiB card", float64(got)/gibibyte)
	}
	if reason == "" {
		t.Error("no reason given")
	}
}

func TestASmallMachineIsNotPromisedMoreThanItHas(t *testing.T) {
	// 70% of 8 GiB was 5.6 GB, which no CUDA context and Python runtime will
	// ever leave room for. Refusing outright is the honest answer.
	got, _ := Budget(8*gibibyte, 0, 0, true)
	if float64(got) > 2*gibibyte {
		t.Errorf("8 GiB machine offered a %.1f GiB budget", float64(got)/gibibyte)
	}
}

func TestAnUnmeasurableMachineSaysSoRatherThanGuessing(t *testing.T) {
	got, reason := Budget(0, 0, 0, true)
	if got != 0 || reason == "" {
		t.Errorf("got %d, %q; want 0 and an explanation", got, reason)
	}
}

func TestTheOverrideWins(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want float64
	}{{"42", 42}, {" 42 ", 42}} {
		if got := BudgetGB(tc.in); got != tc.want {
			t.Errorf("BudgetGB(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	// Garbage and zero fall back to the machine rather than rejecting
	// everything on the Hub.
	for _, in := range []string{"", "  ", "abc", "0", "-5"} {
		if got := BudgetGB(in); got != float64(Probe().BudgetBytes)/gigabyte {
			t.Errorf("BudgetGB(%q) = %v, want the probed budget", in, got)
		}
	}
}
