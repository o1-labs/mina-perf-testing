package itn_orchestrator

import "testing"

// TestSampleStopRatioStaysInRange pins the clamp, and the pair {1.0, 0.0} in
// particular: with min > max the stddev is negative and the sample can come
// back below zero, which silently disables node stops rather than erroring.
// That combination is rejected by ValidationSteps, but the sampler should not
// depend on validation having run.
func TestSampleStopRatioStaysInRange(t *testing.T) {
	for _, tc := range []struct{ min, max float64 }{
		{0, 1},
		{0.1, 0.25},
		{0.5, 0.5},
		{0, 0},
		{1, 1},
		{1.0, 0.0}, // inverted
	} {
		// The bound is the configured pair, not [0, 1]. Asserting only
		// [0, 1] left the clamp pinned for the pair {0, 1} alone: replacing
		// its bounds with 0 and 1 kept the test green while a configured
		// max of 0.25 overshot to 0.371, and with the shipped default of 0.5
		// that is a stop ratio near 0.87 on a "stop half the nodes" config.
		lo, hi := tc.min, tc.max
		if lo > hi {
			lo, hi = hi, lo
		}
		for i := 0; i < 200000; i++ {
			r := SampleStopRatio(tc.min, tc.max)
			if r < lo || r > hi {
				t.Fatalf("SampleStopRatio(%v, %v) returned %v, outside [%v, %v]; below the low end "+
					"every `> 1e-6` guard in generate.go fails and the round emits no stop command "+
					"at all, above the high end more nodes are stopped than the config allows",
					tc.min, tc.max, r, lo, hi)
			}
		}
	}
}
