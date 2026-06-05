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
		var handle string
		handle, err := schedule(nodeAddress, successfulBatches, remTps, remFeePayers)
		if err != nil {
			config.Log.Warnf("error scheduling %s for fallback node %s: %v", actionName, nodeAddress, err)
			continue
		}
		output(nodeAddress, handle)
		return nil
	}
	if remTps >= minTps {
		config.Log.Warnf("unable to schedule %.6f of %.6f total tps for %s after trying %d fallback nodes", remTps, tpsTotal, actionName, len(fallbackNodes))
	}
	return nil
}
