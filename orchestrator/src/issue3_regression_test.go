package itn_orchestrator

import (
	"fmt"
	"math"
	"testing"

	"itn_json_types"
)

// Regression test for https://github.com/o1-labs/mina-perf-testing/issues/3
// ("divide by zero on 99% zkApp txns ratio"), fixed by commit 4b56852.
//
// With zkapp-ratio = 0.99 neither `onlyZkapps` (|1-0.99| >= 1e-3) nor
// `onlyPayments` (0.99 >= 1e-3) holds, so GenParams.Generate emits BOTH a
// zkapps and a payments step and gives the payments step
// tps = roundTps - roundTps*0.99 = 0.01*roundTps.
// With the default min-tps = 0.01 and the default tps range [0.3, 1.0] that is
// 0.003..0.01, so math.Floor(tps/minTps) == 0 and selectNodes used to return
// nodes[:0]. SchedulePayments then evaluated
//
//	feePayersPerNode := len(params.FeePayers) / len(nodes)
//
// and the orchestrator died with "panic: runtime error: integer divide by zero".
//
// This test fails against the pre-4b56852 selectNodes and passes after it.
func TestSelectNodesNeverReturnsZeroNodes(t *testing.T) {
	const feePayers = 6
	nodes := []NodeAddress{"node-a", "node-b", "node-c"}

	cases := []struct {
		name   string
		tps    float64
		minTps float64
	}{
		// --- issue #3 proper: the payments half of a 99% zkapp round ---
		{"zkappRatio=0.99, roundTps=0.3 (BaseTps default)", 0.3 * (1 - 0.99), 0.01},
		{"zkappRatio=0.99, roundTps=1.0 (StressTps default)", 1.0 * (1 - 0.99), 0.01},
		// --- the mirror image: the zkapps half of a 0.1% zkapp round ---
		{"zkappRatio=0.0011, roundTps=0.3", 0.3 * 0.0011, 0.01},
		// --- boundaries the fix must also survive ---
		{"tps exactly one node", 0.01, 0.01},
		{"tps just under one node", 0.00999999, 0.01},
		{"tps zero", 0, 0.01},
		{"tps equals number of nodes", 0.03, 0.01},
		{"tps far above number of nodes", 10, 0.01},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// selectNodes shuffles in place, so hand it a copy.
			in := append([]NodeAddress(nil), nodes...)
			perNodeTps, selected, _ := selectNodesWithFallback(c.tps, c.minTps, in)

			if len(selected) == 0 {
				t.Fatalf("selectNodesWithFallback(tps=%v, minTps=%v, %d nodes) returned 0 nodes; "+
					"SchedulePayments/SendZkappCommands would then panic on "+
					"len(feePayers)/len(nodes) (issue #3)", c.tps, c.minTps, len(nodes))
			}
			if len(selected) > len(nodes) {
				t.Fatalf("selectNodes returned %d nodes, more than the %d available",
					len(selected), len(nodes))
			}
			if math.IsNaN(perNodeTps) || math.IsInf(perNodeTps, 0) {
				t.Fatalf("selectNodesWithFallback(tps=%v, minTps=%v) returned per-node tps %v; "+
					"this is serialised into the GraphQL payment/zkapp input",
					c.tps, c.minTps, perNodeTps)
			}
			// Total tps handed to the daemons must not exceed what was asked for.
			if total := perNodeTps * float64(len(selected)); total > c.tps+1e-9 {
				t.Fatalf("selectNodesWithFallback(tps=%v, minTps=%v) over-allocates: %d nodes x %v = %v",
					c.tps, c.minTps, len(selected), perNodeTps, total)
			}
			// The exact expression that panicked in issue #3.
			_ = len(make([]itn_json_types.MinaPrivateKey, feePayers)) / len(selected)
		})
	}
}

// TestGenerateAt99PercentZkappRatioSelectsNodes drives the real generator at the
// ratio from issue #3 and feeds the generated payments/zkapps steps through the
// same selectNodes call the runtime makes, i.e. it reproduces the reported
// scenario end to end rather than only unit-testing the helper.
func TestGenerateAt99PercentZkappRatioSelectsNodes(t *testing.T) {
	nodes := []NodeAddress{"node-a", "node-b", "node-c"}
	feePayers := make([]itn_json_types.MinaPrivateKey, 6)

	for _, ratio := range []float64{0, 0.001, 0.5, 0.99, 0.999, 1} {
		t.Run(fmt.Sprintf("zkappRatio=%g", ratio), func(t *testing.T) {
			params := someParams()
			params.ZkappRatio = ratio
			params.MaxCostMixedTpsRatio = 0 // keep the ratio fixed across rounds
			params.MinTps = 0.01
			params.BaseTps = 0.3
			params.StressTps = 1

			for i := 0; i < 200; i++ {
				for r := 0; r < 4; r++ {
					round := params.Generate(r)
					for _, c := range round.Commands {
						var tps, minTps float64
						switch c.Action {
						case (PaymentsAction{}).Name():
							p := c.Params.(PaymentRefParams).PaymentSubParams
							tps, minTps = p.Tps, p.MinTps
						case (ZkappCommandsAction{}).Name():
							p := c.Params.(ZkappRefParams).ZkappSubParams
							tps, minTps = p.Tps, p.MinTps
						default:
							continue
						}
						in := append([]NodeAddress(nil), nodes...)
						_, selected, _ := selectNodesWithFallback(tps, minTps, in)
						if len(selected) == 0 {
							t.Fatalf("round %d step %q: selectNodesWithFallback(tps=%v, minTps=%v, %d nodes) "+
								"returned 0 nodes -> integer divide by zero (issue #3)",
								r, c.Action, tps, minTps, len(nodes))
						}
						_ = len(feePayers) / len(selected)
					}
				}
			}
		})
	}
}
