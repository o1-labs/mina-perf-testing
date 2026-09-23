package itn_orchestrator

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"itn_json_types"
)

func schedTestConfig() Config {
	return Config{Log: logging.Logger("scheduling-test")}
}

// schedTestFeePayers returns DISTINCT keys. Zero-value keys make reuse and
// starvation invisible: every element compares equal, so a node handed the same
// key twice, or a batch handed an empty slice, reads the same as a correct run.
func schedTestFeePayers(n int) []itn_json_types.MinaPrivateKey {
	keys := make([]itn_json_types.MinaPrivateKey, n)
	for i := range keys {
		keys[i] = itn_json_types.MinaPrivateKey(fmt.Sprintf("key-%03d", i))
	}
	return keys
}

// TestScheduleAllNodesFailReturnsError is the regression test for a load run
// that reported success having sent nothing.
//
// selectNodesWithFallback returns no fallback nodes whenever tps/minTps >= node
// count -- the common case -- so when every selected node rejected the request
// the fallback loop never ran and the function fell through to `return nil`.
// orchestrator.go aborts only on a non-nil error, so it advanced to the next
// step with zero receipts emitted.
func TestScheduleAllNodesFailReturnsError(t *testing.T) {
	nodes := []NodeAddress{"node-a", "node-b", "node-c"}
	calls, receipts := 0, 0

	_, err := scheduleTransactionBatches(
		schedTestConfig(), "payments", 3.0, 1.0, nodes, schedTestFeePayers(6),
		func(NodeAddress, int, float64, []itn_json_types.MinaPrivateKey) (string, error) {
			calls++
			return "", errors.New("connection refused")
		},
		func(NodeAddress, string) { receipts++ },
	)

	if err == nil {
		t.Fatalf("every node rejected the request but the run was reported as successful "+
			"(calls=%d receipts=%d err=<nil>)", calls, receipts)
	}
	if receipts != 0 {
		t.Errorf("receipts = %d, want 0 -- nothing was scheduled", receipts)
	}
	if calls != len(nodes) {
		t.Errorf("schedule called %d times, want %d (one per selected node)", calls, len(nodes))
	}
}

// TestSchedulePartialSuccessIsNotAnError: one node failing is tolerated, since
// the remaining tps is redistributed over the nodes that are still answering.
func TestSchedulePartialSuccessIsNotAnError(t *testing.T) {
	nodes := []NodeAddress{"node-a", "node-b", "node-c"}
	receipts := 0

	_, err := scheduleTransactionBatches(
		schedTestConfig(), "payments", 3.0, 1.0, nodes, schedTestFeePayers(6),
		func(n NodeAddress, _ int, _ float64, _ []itn_json_types.MinaPrivateKey) (string, error) {
			if n == "node-a" {
				return "", errors.New("connection refused")
			}
			return "handle-" + string(n), nil
		},
		func(NodeAddress, string) { receipts++ },
	)

	if err != nil {
		t.Fatalf("a partial failure must not fail the step: %v", err)
	}
	if receipts == 0 {
		t.Error("no receipts emitted although two nodes accepted the batch")
	}
}

// TestScheduleAllSucceed pins the ordinary path.
func TestScheduleAllSucceed(t *testing.T) {
	nodes := []NodeAddress{"node-a", "node-b", "node-c"}
	receipts := 0

	_, err := scheduleTransactionBatches(
		schedTestConfig(), "zkapps", 3.0, 1.0, nodes, schedTestFeePayers(6),
		func(n NodeAddress, _ int, _ float64, _ []itn_json_types.MinaPrivateKey) (string, error) {
			return "handle-" + string(n), nil
		},
		func(NodeAddress, string) { receipts++ },
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receipts != len(nodes) {
		t.Errorf("receipts = %d, want %d", receipts, len(nodes))
	}
}

// TestScheduleNoNodes: an empty node list is an error, not a silent no-op.
func TestScheduleNoNodes(t *testing.T) {
	_, err := scheduleTransactionBatches(
		schedTestConfig(), "payments", 3.0, 1.0, nil, schedTestFeePayers(6),
		func(NodeAddress, int, float64, []itn_json_types.MinaPrivateKey) (string, error) {
			t.Fatal("schedule must not be called when there are no nodes")
			return "", nil
		},
		func(NodeAddress, string) {},
	)
	if err == nil {
		t.Fatal("want an error when no nodes are available, got nil")
	}
}

// TestScheduleBelowMinTpsStillReportsTotalFailure covers the shape the early
// `remTps < minTps` return used to hide.
//
// remTps only falls on success, so after a total failure it still equals
// tpsTotal; the early return therefore fired whenever tpsTotal < minTps and
// the "nothing was scheduled" check was never reached. The issue-#3 clamp on
// this stack makes that reachable from the real generator: at zkappRatio 0.99
// the payments step runs at 0.0092 tps against a minTps of 0.01.
func TestScheduleBelowMinTpsStillReportsTotalFailure(t *testing.T) {
	nodes := []NodeAddress{"node-a", "node-b", "node-c"}
	calls, receipts := 0, 0

	scheduled, err := scheduleTransactionBatches(
		schedTestConfig(), "payments", 0.003, 0.01, nodes, schedTestFeePayers(6),
		func(NodeAddress, int, float64, []itn_json_types.MinaPrivateKey) (string, error) {
			calls++
			return "", errors.New("connection refused")
		},
		func(NodeAddress, string) { receipts++ },
	)

	if err == nil {
		t.Fatalf("tps below minTps and every node refusing was reported as success "+
			"(calls=%d receipts=%d scheduled=%v)", calls, receipts, scheduled)
	}
	if receipts != 0 || scheduled != 0 {
		t.Errorf("receipts=%d scheduled=%v, want nothing placed", receipts, scheduled)
	}
}

// TestScheduleNeverReusesFeePayers: a failed attempt is not proof the daemon
// did not queue the batch, so its senders must not be handed to another node.
func TestScheduleNeverReusesFeePayers(t *testing.T) {
	nodes := []NodeAddress{"node-a", "node-b", "node-c"}
	feePayers := []itn_json_types.MinaPrivateKey{"k0", "k1", "k2", "k3", "k4", "k5"}

	seen := map[itn_json_types.MinaPrivateKey]NodeAddress{}
	batchIxs := map[int]bool{}

	_, err := scheduleTransactionBatches(
		schedTestConfig(), "payments", 3.0, 1.0, nodes, feePayers,
		func(node NodeAddress, batchIx int, _ float64, keys []itn_json_types.MinaPrivateKey) (string, error) {
			if batchIxs[batchIx] {
				t.Errorf("batch index %d was used twice; the memo prefix then does not "+
					"distinguish the two batches in results analysis", batchIx)
			}
			batchIxs[batchIx] = true
			for _, k := range keys {
				if other, dup := seen[k]; dup {
					t.Errorf("key %s went to both %s and %s: two daemons then sign from one account",
						k, other, node)
				}
				seen[k] = node
			}
			if node == "node-a" {
				// A lost response looks exactly like a rejection.
				return "", errors.New("i/o timeout")
			}
			return "handle-" + string(node), nil
		},
		func(NodeAddress, string) {},
	)
	if err != nil {
		t.Fatalf("two of three nodes accepted the batch, want success: %v", err)
	}
}

// TestScheduleDoesNotOverloadOneNode: the per-node share is what
// selectNodesWithFallback computed, and a failure elsewhere must not raise it.
func TestScheduleDoesNotOverloadOneNode(t *testing.T) {
	nodes := make([]NodeAddress, 20)
	for i := range nodes {
		nodes[i] = NodeAddress(fmt.Sprintf("node-%02d", i))
	}
	const tpsTotal, minTps = 10.0, 1.0

	maxAsked, maxKeys := 0.0, 0
	failing := 10

	_, err := scheduleTransactionBatches(
		schedTestConfig(), "payments", tpsTotal, minTps, nodes, schedTestFeePayers(40),
		func(_ NodeAddress, _ int, tps float64, keys []itn_json_types.MinaPrivateKey) (string, error) {
			if tps > maxAsked {
				maxAsked = tps
			}
			if len(keys) > maxKeys {
				maxKeys = len(keys)
			}
			if failing > 0 {
				failing--
				return "", errors.New("connection refused")
			}
			return "handle", nil
		},
		func(NodeAddress, string) {},
	)
	if err != nil {
		t.Fatalf("half the nodes answered, want success: %v", err)
	}
	// 10 selected nodes over 10 tps is 1 tps each; the fallback pass spreads
	// what is left the same way, so nothing may be asked for the whole rate.
	if maxAsked > tpsTotal/2 {
		t.Errorf("a single node was asked for %.3f tps of a %.3f total; the spreading the "+
			"selection performs was undone by the failure path", maxAsked, tpsTotal)
	}
	if maxKeys >= 40 {
		t.Errorf("a single node was handed %d of 40 fee payers", maxKeys)
	}
}

// TestScheduleAbortsOnAPermanentRejection: a request the daemon refuses on its
// merits is refused by every node, so trying them all wastes the round and
// buries the cause.
func TestScheduleAbortsOnAPermanentRejection(t *testing.T) {
	nodes := []NodeAddress{"node-a", "node-b", "node-c"}
	calls := 0

	_, err := scheduleTransactionBatches(
		schedTestConfig(), "payments", 3.0, 1.0, nodes, schedTestFeePayers(6),
		func(node NodeAddress, _ int, _ float64, _ []itn_json_types.MinaPrivateKey) (string, error) {
			calls++
			return "", fmt.Errorf("error scheduling payments to %s: %w", node,
				&GqlRequestError{Node: node, StatusCode: 400, Err: errors.New("memoPrefix is longer than 32 characters")})
		},
		func(NodeAddress, string) {},
	)
	if err == nil {
		t.Fatal("a permanent rejection was reported as success")
	}
	if calls != 1 {
		t.Errorf("schedule called %d times, want 1: the same request cannot succeed elsewhere", calls)
	}
	if !strings.Contains(err.Error(), "32 characters") {
		t.Errorf("err = %v, want the daemon's own reason to survive", err)
	}
}

// TestScheduleReportsWhatItPlaced: under-delivery is the load test measuring
// something other than what was asked for, so the caller can see the rate.
func TestScheduleReportsWhatItPlaced(t *testing.T) {
	nodes := []NodeAddress{"node-a", "node-b", "node-c", "node-d"}

	scheduled, err := scheduleTransactionBatches(
		schedTestConfig(), "payments", 4.0, 1.0, nodes, schedTestFeePayers(8),
		func(node NodeAddress, _ int, _ float64, _ []itn_json_types.MinaPrivateKey) (string, error) {
			if node != "node-a" {
				return "", errors.New("connection refused")
			}
			return "handle", nil
		},
		func(NodeAddress, string) {},
	)
	if err != nil {
		t.Fatalf("one node answered, want success: %v", err)
	}
	if math.Abs(scheduled-1.0) > 1e-9 {
		t.Fatalf("scheduled = %v, want 1.0 of the 4.0 requested", scheduled)
	}
}

// TestSelectNodesWithFallbackPartitions: the two returned sets must be the
// whole node list, split, or a node is either used twice or lost.
func TestSelectNodesWithFallbackPartitions(t *testing.T) {
	for _, tc := range []struct{ tps, minTps float64 }{
		{10.0, 1.0}, {3.0, 1.0}, {0.003, 0.01}, {100.0, 1.0},
	} {
		nodes := make([]NodeAddress, 8)
		for i := range nodes {
			nodes[i] = NodeAddress(fmt.Sprintf("node-%d", i))
		}

		_, selected, fallback := selectNodesWithFallback(tc.tps, tc.minTps, nodes)

		seen := map[NodeAddress]int{}
		for _, n := range selected {
			seen[n]++
		}
		for _, n := range fallback {
			seen[n]++
		}
		if len(seen) != len(nodes) {
			t.Errorf("tps=%v minTps=%v: %d distinct nodes across selected+fallback, want %d",
				tc.tps, tc.minTps, len(seen), len(nodes))
		}
		for node, count := range seen {
			if count != 1 {
				t.Errorf("tps=%v minTps=%v: %s appears %d times across the two sets",
					tc.tps, tc.minTps, node, count)
			}
		}
	}
}

// TestRetryOnMultipleServersStopsOnCancel: the backoff must be interruptible.
//
// The pauses are 1, 2, 4 and 8 minutes, and the check at the top of each
// iteration does nothing while a pause is in progress, so a cancel landing
// just after it left the orchestrator unresponsive for up to 8 minutes plus
// one unbounded attempt. DiscoverParticipants has the same shape with a
// 20-minute pause.
func TestRetryOnMultipleServersStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0

	done := make(chan error, 1)
	go func() {
		done <- retryOnMultipleServers([]string{"server-a"}, ctx, 0, "fund-keys",
			logging.Logger("retry-test"), func(string) error {
				attempts++
				cancel() // the operator cancels while the first attempt runs
				return errors.New("connection refused")
			})
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("retryOnMultipleServers is still sleeping 10s after the cancel; " +
			"the backoff is not interruptible")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1: no further attempt after the cancel", attempts)
	}
}

// daemonLikeSchedule models what the daemon actually does with an empty sender
// list: mina_graphql.ml answers "Empty list of senders" / "Empty list of fee
// payers", and graphql_internal.ml returns GraphQL errors as HTTP 200 -- so the
// orchestrator sees a plain transient error, not a 4xx.
//
// The previous mocks accepted an empty slice, which is why a scheduler that
// handed fallbacks zero keys still looked like a success.
// failFirstCalls rather than failing named nodes: selectNodesWithFallback
// shuffles the node slice in place, so "the first N nodes are down" is not
// reproducible from the caller's ordering. Failing the first N *calls* models
// the same situation and is deterministic.
func daemonLikeSchedule(failFirstCalls int) (scheduleBatchFunc, *[]int) {
	var keyCounts []int
	calls := 0
	f := func(n NodeAddress, _ int, _ float64, keys []itn_json_types.MinaPrivateKey) (string, error) {
		keyCounts = append(keyCounts, len(keys))
		calls++
		if len(keys) == 0 {
			return "", errors.New("Empty list of senders")
		}
		if calls <= failFirstCalls {
			return "", errors.New("connection refused")
		}
		return "handle-" + string(n), nil
	}
	return f, &keyCounts
}

func nodesNamed(n int) []NodeAddress {
	out := make([]NodeAddress, n)
	for i := range out {
		out[i] = NodeAddress(fmt.Sprintf("node-%02d", i))
	}
	return out
}

// TestScheduleNeverSendsASenderlessBatch is the regression test for fallback
// nodes being handed zero fee payers.
//
// feePayersPerNode was len(feePayers)/len(selectedNodes), so the selected nodes
// consumed the whole pool; a failed attempt burns its keys, and the fallbacks
// were then handed len(remFeePayers)/batchCount == 0. Against the real daemon
// every one of those is rejected, so the fallback path was dead.
func TestScheduleNeverSendsASenderlessBatch(t *testing.T) {
	for _, tc := range []struct {
		name           string
		nodes, keys    int
		tpsTotal       float64
		minTps         float64
		failFirst      int
		wantSomePlaced bool
	}{
		{"20 nodes, 40 keys, first 10 fail", 20, 40, 10, 1, 10, true},
		{"20 nodes, 40 keys, first 5 fail", 20, 40, 10, 1, 5, true},
		{"tps below minTps", 20, 40, 0.003, 0.01, 0, true},
		{"tight key budget", 3, 7, 0.0092, 0.01, 0, true},
		// The loop-entry half: with tpsTotal < minTps the fallback loop was
		// never entered at all, so when the one selected node failed there was
		// nowhere for the load to go. ~95% of generated payments steps at a
		// high zkapp ratio have this shape.
		{"tps below minTps and the selected node fails", 20, 40, 0.003, 0.01, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nodes := nodesNamed(tc.nodes)
			sched, keyCounts := daemonLikeSchedule(tc.failFirst)

			placed, err := scheduleTransactionBatches(
				schedTestConfig(), "payments", tc.tpsTotal, tc.minTps, nodes,
				schedTestFeePayers(tc.keys), sched, func(NodeAddress, string) {})

			for i, c := range *keyCounts {
				if c == 0 {
					t.Fatalf("call %d was made with 0 fee payers; the daemon rejects that "+
						"(\"Empty list of senders\") and returns it as HTTP 200, so the "+
						"orchestrator treats it as transient and walks the rest keyless. "+
						"key counts: %v", i, *keyCounts)
				}
			}
			if tc.wantSomePlaced && placed <= 0 {
				t.Errorf("placed = %v with err = %v; want some load placed. "+
					"calls: %d, key counts %v", placed, err, len(*keyCounts), *keyCounts)
			}
			// When the selected nodes failed, a fallback must actually have
			// been attempted rather than the loop being skipped entirely.
			if tc.failFirst > 0 && len(*keyCounts) <= tc.failFirst {
				t.Errorf("only %d schedule call(s) made for %d failing node(s); "+
					"no fallback was tried. key counts: %v",
					len(*keyCounts), tc.failFirst, *keyCounts)
			}
		})
	}
}
