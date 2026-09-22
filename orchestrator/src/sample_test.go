package itn_orchestrator

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
)

// runSample drives the real action, so the test pins the shipped code path
// rather than a copy of its arithmetic.
func runSample(t *testing.T, ratios []float64, n int) (map[string][]NodeAddress, error) {
	t.Helper()

	group := make([]NodeAddress, n)
	for i := range group {
		group[i] = NodeAddress(fmt.Sprintf("node-%02d", i))
	}
	rawParams, err := json.Marshal(SampleParams{Group: group, Ratios: ratios})
	if err != nil {
		t.Fatalf("marshalling params: %v", err)
	}

	got := map[string][]NodeAddress{}
	output := func(name string, value any, multiple bool, sensitive bool) error {
		nodes, ok := value.([]NodeAddress)
		if !ok {
			t.Fatalf("output %q carries %T, want []NodeAddress", name, value)
		}
		got[name] = nodes
		return nil
	}

	return got, SampleAction{}.Run(Config{}, rawParams, output)
}

// TestSampleActionPartitionsGroup is the regression test for two defects in
// one function.
//
//  1. Independent per-ratio rounding let the groups claim more nodes than the
//     group holds, and SampleAction then sliced past the end:
//     `{0.5, 0.5}` panicked with "slice bounds out of range" at every odd node
//     count. In production a stop ratio of exactly 1.0 -- which the clamp in
//     SampleStopRatio produces for ~0.27% of draws -- reaches this directly.
//  2. The same rounding pushed every over-allocation onto the last bucket, so
//     `{0.2 x5}` over 8 nodes gave [2 2 2 2 0]: the fifth group got nothing
//     while its ideal share was 1.6.
//
// Cumulative boundaries fix both, and the invariant prev <= end <= groupLen
// then holds by construction.
func TestSampleActionPartitionsGroup(t *testing.T) {
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
		// A full ratio takes the whole group and leaves "rest" empty. This is
		// the value a clamped stop ratio produces.
		{"whole group", []float64{1.0}, 5, []int{5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runSample(t, tc.ratios, tc.n)
			if err != nil {
				t.Fatalf("SampleAction: %v", err)
			}

			seen := map[NodeAddress]string{}
			total := 0
			for i, want := range tc.want {
				name := fmt.Sprintf("group%d", i+1)
				nodes := got[name]
				if len(nodes) != want {
					t.Fatalf("%s got %d nodes, want %d (all groups: %v)", name, len(nodes), want, sizes(got, len(tc.want)))
				}
				ideal := tc.ratios[i] * float64(tc.n)
				if math.Abs(float64(len(nodes))-ideal) >= 1 {
					t.Errorf("%s got %d nodes, ideal share %.2f -- off by a whole node", name, len(nodes), ideal)
				}
				for _, node := range nodes {
					if other, dup := seen[node]; dup {
						t.Fatalf("%s is in both %s and %s", node, other, name)
					}
					seen[node] = name
				}
				total += len(nodes)
			}

			// Every node lands in exactly one group, the remainder included.
			total += len(got["rest"])
			for _, node := range got["rest"] {
				if other, dup := seen[node]; dup {
					t.Fatalf("%s is in both %s and rest", node, other)
				}
				seen[node] = "rest"
			}
			if total != tc.n {
				t.Fatalf("groups and rest hold %d nodes, want the whole group of %d", total, tc.n)
			}
		})
	}
}

// TestSampleActionRejectsImpossibleRatios: a ratio list that cannot be
// satisfied is an error, not a partition that silently drops nodes.
func TestSampleActionRejectsImpossibleRatios(t *testing.T) {
	if _, err := runSample(t, []float64{0.6, 0.6}, 10); err == nil {
		t.Fatal("ratios summing above 1 were accepted")
	}
	if _, err := runSample(t, []float64{-0.1, 0.5}, 10); err == nil {
		t.Fatal("a negative ratio was accepted")
	}
}

func sizes(groups map[string][]NodeAddress, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = len(groups[fmt.Sprintf("group%d", i+1)])
	}
	return out
}
