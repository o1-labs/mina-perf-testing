package itn_orchestrator

import (
	"errors"
	"testing"

	logging "github.com/ipfs/go-log/v2"
	"itn_json_types"
)

func testConfig() Config {
	return Config{Log: logging.Logger("scheduling-test")}
}

func testFeePayers(n int) []itn_json_types.MinaPrivateKey {
	return make([]itn_json_types.MinaPrivateKey, n)
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

	err := scheduleTransactionBatches(
		testConfig(), "payments", 3.0, 1.0, nodes, testFeePayers(6),
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

	err := scheduleTransactionBatches(
		testConfig(), "payments", 3.0, 1.0, nodes, testFeePayers(6),
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

	err := scheduleTransactionBatches(
		testConfig(), "zkapps", 3.0, 1.0, nodes, testFeePayers(6),
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
	err := scheduleTransactionBatches(
		testConfig(), "payments", 3.0, 1.0, nil, testFeePayers(6),
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
