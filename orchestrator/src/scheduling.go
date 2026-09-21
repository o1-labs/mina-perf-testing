package itn_orchestrator

import (
	"fmt"
	"itn_json_types"
)

type scheduleBatchFunc func(nodeAddress NodeAddress, batchIx int, tps float64, feePayers []itn_json_types.MinaPrivateKey) (string, error)

func scheduleTransactionBatches(
	config Config,
	actionName string,
	tpsTotal float64,
	minTps float64,
	nodes []NodeAddress,
	feePayers []itn_json_types.MinaPrivateKey,
	schedule scheduleBatchFunc,
	output func(NodeAddress, string),
) error {
	tps, selectedNodes, fallbackNodes := selectNodesWithFallback(tpsTotal, minTps, nodes)
	if len(selectedNodes) == 0 {
		return fmt.Errorf("no nodes selected for %s execution (tps=%.6f, minTps=%.6f, available nodes=%d)",
			actionName, tpsTotal, minTps, len(nodes))
	}
	feePayersPerNode := len(feePayers) / len(selectedNodes)
	successfulBatches := 0
	remTps := tpsTotal
	remFeePayers := feePayers
	for nodeIx, nodeAddress := range selectedNodes {
		batchFeePayers := remFeePayers[:feePayersPerNode]
		handle, err := schedule(nodeAddress, successfulBatches, tps, batchFeePayers)
		if err != nil {
			config.Log.Warnf("error scheduling %s for %s: %v", actionName, nodeAddress, err)
			n := len(selectedNodes) - nodeIx - 1
			if n > 0 {
				tps = remTps / float64(n)
				feePayersPerNode = len(remFeePayers) / n
			}
			continue
		}
		successfulBatches++
		remFeePayers = remFeePayers[feePayersPerNode:]
		remTps -= tps
		output(nodeAddress, handle)
	}
	if remTps < minTps {
		return nil
	}
	for _, nodeAddress := range fallbackNodes {
		handle, err := schedule(nodeAddress, successfulBatches, remTps, remFeePayers)
		if err != nil {
			config.Log.Warnf("error scheduling %s for fallback node %s: %v", actionName, nodeAddress, err)
			continue
		}
		output(nodeAddress, handle)
		return nil
	}
	// Reaching here means the fallback nodes did not absorb the remainder
	// either. `remTps >= minTps` is always true at this point -- the only way
	// past the check above is remTps >= minTps, and nothing modifies it in
	// between -- so the warning is unconditional.
	config.Log.Warnf("unable to schedule %.6f of %.6f total tps for %s after trying %d fallback nodes",
		remTps, tpsTotal, actionName, len(fallbackNodes))

	// A total failure must not be reported as success. selectNodesWithFallback
	// returns no fallback nodes whenever tps/minTps >= node count, which is the
	// common case, so when every selected node rejects the request control fell
	// straight through to `return nil`. orchestrator.go aborts only on a
	// non-nil error, so it advanced to the next step and the load test reported
	// success having sent nothing -- and emitted no receipts, leaving the later
	// stop step with no handles either.
	if successfulBatches == 0 {
		return fmt.Errorf("failed to schedule any %s batch across %d nodes and %d fallback nodes",
			actionName, len(selectedNodes), len(fallbackNodes))
	}
	return nil
}
