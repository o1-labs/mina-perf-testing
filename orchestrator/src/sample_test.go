package itn_orchestrator

import (
	"math"
	"testing"
)

// splitSizes mirrors the boundary arithmetic in SampleAction.Run, so the
// distribution can be tested without building a whole action context.
func splitSizes(ratios []float64, groupLen int) []int {
	cum, prev := 0.0, 0
	sizes := make([]int, 0, len(ratios))
	for _, r := range ratios {
		cum += r
		end := int(math.Round(cum * float64(groupLen)))
		if end > groupLen {
			end = groupLen
		}
		sizes = append(sizes, end-prev)
		prev = end
	}
	return sizes
}

// TestSampleSplitIsBalanced pins the property that made the old arithmetic
// wrong: every over-allocation was absorbed by the last bucket, so it could be
// starved while its ideal share was well above zero.
func TestSampleSplitIsBalanced(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ratios []float64
		n      int
		want   []int
	}{
		// The review's example: independent rounding gave [2 2 2 2 0].
		{"five equal fifths over eight", []float64{0.2, 0.2, 0.2, 0.2, 0.2}, 8, []int{2, 1, 2, 1, 2}},
		{"five equal fifths over seven", []float64{0.2, 0.2, 0.2, 0.2, 0.2}, 7, []int{1, 2, 1, 2, 1}},
		{"thirds over eight", []float64{1.0 / 3, 1.0 / 3, 1.0 / 3}, 8, []int{3, 2, 3}},
		{"halves over seven", []float64{0.5, 0.5}, 7, []int{4, 3}},
		{"exact split", []float64{0.3, 0.3}, 10, []int{3, 3}},
		{"single ratio", []float64{0.5}, 4, []int{2}},
		{"no nodes", []float64{0.5, 0.5}, 0, []int{0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := splitSizes(tc.ratios, tc.n)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}

			// No bucket may exceed its ideal share by a whole node, which is
			// what [2 2 2 2 0] violated at the far end.
			total := 0
			for i, size := range got {
				ideal := tc.ratios[i] * float64(tc.n)
				if math.Abs(float64(size)-ideal) >= 1 {
					t.Errorf("group %d got %d nodes, ideal share %.2f -- off by a whole node",
						i+1, size, ideal)
				}
				total += size
			}
			if total > tc.n {
				t.Errorf("groups total %d nodes, only %d available", total, tc.n)
			}
		})
	}
}
