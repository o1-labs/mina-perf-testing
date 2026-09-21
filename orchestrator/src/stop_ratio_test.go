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
		for i := 0; i < 200000; i++ {
			r := SampleStopRatio(tc.min, tc.max)
			if r < 0 {
				t.Fatalf("SampleStopRatio(%v, %v) returned %v; a negative ratio fails every "+
					"`> 1e-6` guard in generate.go, so the round emits no stop command at all",
					tc.min, tc.max, r)
			}
			if r > 1 {
				t.Fatalf("SampleStopRatio(%v, %v) returned %v, above 1", tc.min, tc.max, r)
			}
		}
	}
}
