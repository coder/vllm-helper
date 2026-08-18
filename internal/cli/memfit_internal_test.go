package cli

import "testing"

func TestClampMemUtilization(t *testing.T) {
	t.Parallel()

	const gb = 1024
	tests := []struct {
		name             string
		want             string
		totalMB, availMB int
		value            string
		msg              bool
	}{
		{"fits already", "0.30", 16 * gb, 8 * gb, "0.30", false},
		{"exactly at the limit", "0.85", 16 * gb, 16 * gb, "0.85", false},
		// The issue #13 case: the Linux default on a 15 GiB CPU-only box
		// with about half the RAM in use.
		{"linux default on a busy CPU box", "0.85", 15 * gb, 8 * gb, "0.45", true},
		{"almost nothing free warns but keeps the value", "0.85", 16 * gb, 512, "0.85", true},
		{"unknown total passes through", "0.85", 0, 8 * gb, "0.85", false},
		{"unknown available passes through", "0.85", 16 * gb, 0, "0.85", false},
		{"unparseable passes through", "lots", 16 * gb, 1 * gb, "lots", false},
		{"zero passes through", "0", 16 * gb, 1 * gb, "0", false},
		{"negative passes through", "-1", 16 * gb, 1 * gb, "-1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			value, msg := clampMemUtilization(tt.want, tt.totalMB, tt.availMB)
			if value != tt.value {
				t.Errorf("value = %q, want %q", value, tt.value)
			}
			if (msg != "") != tt.msg {
				t.Errorf("msg = %q, want a message: %v", msg, tt.msg)
			}
		})
	}
}

// Whatever we hand back has to survive the round trip into vLLM's flag.
func TestClampMemUtilizationNeverRaisesTheRequest(t *testing.T) {
	t.Parallel()

	const gb = 1024
	for _, avail := range []int{256, 1 * gb, 4 * gb, 8 * gb, 15 * gb} {
		value, _ := clampMemUtilization("0.85", 16*gb, avail)
		if value > "0.85" {
			t.Errorf("clampMemUtilization(0.85, %d MiB free) = %q, which is higher than asked", avail, value)
		}
	}
}
