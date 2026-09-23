package itn_orchestrator

import (
	"errors"
	"fmt"
	"math"

	"itn_json_types"
)

type scheduleBatchFunc func(nodeAddress NodeAddress, batchIx int, tps float64, feePayers []itn_json_types.MinaPrivateKey) (string, error)

// takeFeePayers removes the first n keys from the pool and returns them.
//
// The pool is advanced whether or not the batch is accepted. A failed attempt
// is not proof that the daemon did not queue it -- a lost response looks
// exactly like a rejection -- so handing the same senders to the next node
// raced two daemons on one account's nonce sequence. The result is not a
// double spend, because the daemon derives the nonce and Mina rejects or
// fee-replaces the collision, but the load test then delivers well under the
// requested rate and reports success: it corrupts its own measurement.
func takeFeePayers(pool *[]itn_json_types.MinaPrivateKey, n int) []itn_json_types.MinaPrivateKey {
	if n > len(*pool) {
		n = len(*pool)
	}
	batch := (*pool)[:n]
	*pool = (*pool)[n:]
	return batch
}

// scheduleTransactionBatches spreads tpsTotal over nodes, in batches of at most
// the per-node share the selection computed.
//
// It returns the rate it actually placed. A caller that asked for 4 tps and got
// 1 has a load test that ran at a quarter of its configured rate, which is
// worth a line in the log rather than a silent pass.
func scheduleTransactionBatches(
	config Config,
	actionName string,
	tpsTotal float64,
	minTps float64,
	nodes []NodeAddress,
	feePayers []itn_json_types.MinaPrivateKey,
	schedule scheduleBatchFunc,
	output func(NodeAddress, string),
) (float64, error) {
	tps, selectedNodes, fallbackNodes := selectNodesWithFallback(tpsTotal, minTps, nodes)
	if len(selectedNodes) == 0 {
		return 0, fmt.Errorf("no nodes selected for %s execution (tps=%.6f, minTps=%.6f, available nodes=%d)",
			actionName, tpsTotal, minTps, len(nodes))
	}

	// Budget keys over the selected nodes *and* the fallbacks up front.
	// Dividing by len(selectedNodes) alone handed the whole pool to the
	// selected nodes, and since a failed attempt burns its keys, fallbacks
	// were then handed len(remFeePayers)/batchCount == 0. The daemon rejects
	// an empty sender list ("Empty list of senders", "Empty list of fee
	// payers") and returns it as HTTP 200, so the orchestrator read it as
	// transient and walked the remaining keyless fallbacks one by one.
	//
	// The trade-off is fewer senders per selected node. PaymentKeygenRequirements
	// budgets roughly ceil(tps/minTps) + 2*tpsGap, i.e. about 2x headroom, so
	// the reserve fits; if it does not, the fall-back below keeps the previous
	// behaviour rather than starving the selected nodes.
	feePayersPerNode := len(feePayers) / (len(selectedNodes) + len(fallbackNodes))
	if feePayersPerNode == 0 {
		feePayersPerNode = len(feePayers) / len(selectedNodes)
	}
	if feePayersPerNode == 0 {
		return 0, fmt.Errorf("%s: %d fee payers cannot cover %d selected nodes",
			actionName, len(feePayers), len(selectedNodes))
	}
	remFeePayers := feePayers
	scheduled := 0.0
	batchIx := 0

	// Each selected node is asked for the share the selection computed and no
	// more. Redistributing a failed node's share over the nodes still to come
	// defeated that spreading: with 20 nodes at 10 tps and the first ten
	// failing, the tenth node was asked for 10 tps and all 40 keys -- ten times
	// the rate the algorithm had just decided a single node should carry.
	for _, nodeAddress := range selectedNodes {
		batchFeePayers := takeFeePayers(&remFeePayers, feePayersPerNode)
		handle, err := schedule(nodeAddress, batchIx, tps, batchFeePayers)
		batchIx++
		if err != nil {
			if permanentGqlError(err) {
				// A deterministic rejection -- a memo over 32 characters, a
				// malformed receiver, minFee above maxFee -- fails identically
				// on every node. Walking the whole cluster to collect the same
				// refusal wastes the round and buries the cause.
				return scheduled, fmt.Errorf("%s rejected by %s and the cause is permanent: %w",
					actionName, nodeAddress, err)
			}
			config.Log.Warnf("error scheduling %s for %s: %v", actionName, nodeAddress, err)
			continue
		}
		scheduled += tps
		output(nodeAddress, handle)
	}

	// Whatever the selected nodes did not take is spread over the fallback
	// nodes with the same arithmetic, rather than being pushed onto the first
	// one that answers.
	remTps := tpsTotal - scheduled
	// `remTps >= minTps` alone never entered the loop when tpsTotal < minTps,
	// which is ~95% of generated payments steps at a high zkapp ratio -- the
	// shape the issue-#3 clamp makes reachable. A total failure was then
	// reported correctly but no fallback was ever tried.
	for len(fallbackNodes) > 0 && remTps > 0 && remTps >= math.Min(minTps, tpsTotal) {
		batchCount := int(math.Floor(remTps / minTps))
		if batchCount > len(fallbackNodes) {
			batchCount = len(fallbackNodes)
		}
		if batchCount < 1 {
			batchCount = 1
		}
		fbTps := remTps / float64(batchCount)
		fbFeePayers := len(remFeePayers) / batchCount
		if fbFeePayers > feePayersPerNode {
			fbFeePayers = feePayersPerNode
		}
		if fbFeePayers == 0 {
			// A senderless batch is rejected by the daemon, so walking the
			// remaining fallbacks would only collect the same refusal.
			config.Log.Warnf("no fee payers left for %s; %d fallback node(s) not tried, %.6f tps unplaced",
				actionName, len(fallbackNodes), remTps)
			break
		}

		batch := fallbackNodes[:batchCount]
		fallbackNodes = fallbackNodes[batchCount:]

		for _, nodeAddress := range batch {
			batchFeePayers := takeFeePayers(&remFeePayers, fbFeePayers)
			handle, err := schedule(nodeAddress, batchIx, fbTps, batchFeePayers)
			batchIx++
			if err != nil {
				if permanentGqlError(err) {
					return scheduled, fmt.Errorf("%s rejected by fallback node %s and the cause is permanent: %w",
						actionName, nodeAddress, err)
				}
				config.Log.Warnf("error scheduling %s for fallback node %s: %v", actionName, nodeAddress, err)
				continue
			}
			scheduled += fbTps
			output(nodeAddress, handle)
		}
		remTps = tpsTotal - scheduled
	}

	// A total failure must not be reported as success. This check comes before
	// any "nothing left to place" return: remTps only falls on success, so
	// after a total failure it still equals tpsTotal, and an early
	// `remTps < minTps` return -- which fires whenever tpsTotal < minTps, the
	// shape the issue-#3 clamp makes reachable -- skipped the check entirely.
	// orchestrator.go aborts only on a non-nil error, so the load test then
	// reported success having sent nothing, with no receipts for the later
	// stop step either.
	if scheduled <= 0 {
		return 0, fmt.Errorf("failed to schedule any %s batch across %d nodes and %d fallback nodes",
			actionName, len(selectedNodes), len(fallbackNodes))
	}

	if tpsTotal-scheduled > 1e-9 {
		config.Log.Warnf("scheduled %.6f of %.6f requested tps for %s; the remaining %.6f was not placed on any node",
			scheduled, tpsTotal, actionName, tpsTotal-scheduled)
	}
	return scheduled, nil
}

// permanentGqlError reports whether retrying the same request on another node
// is pointless.
func permanentGqlError(err error) bool {
	var gqlErr *GqlRequestError
	if !errors.As(err, &gqlErr) {
		return false
	}
	return gqlErr.Permanent()
}
