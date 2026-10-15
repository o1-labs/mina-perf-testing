package itn_orchestrator

import "fmt"

// AnomalyParams are the thresholds of the checks the orchestrator service
// runs while an experiment is in progress (orchestrator_service/watch.go).
// They need no history of earlier runs: each one names a state the network
// must not be in during a load test, and the run fails as soon as it is,
// instead of running to the end and reporting success over a stalled chain.
type AnomalyParams struct {
	// Checks turns the checks on. Off, the run is never failed by them.
	Checks bool
	// ChainStallSlots: the run fails when no node reports a new block for
	// this many slots. It also catches a dead trace pipeline, which is the
	// same loss of observability.
	ChainStallSlots int
	// TxStallMin: while load is being sent, the run fails when none of its
	// transactions reached a block in this many minutes. It is also the
	// window of the throughput check and the grace period after the load
	// starts.
	TxStallMin int
	// MinTpsRatio: while load is being sent, the run fails when the rate of
	// its transactions that reached blocks, over the last TxStallMin
	// minutes, is below this fraction of the offered rate (the lower of
	// BaseTps and StressTps). 0 turns the throughput check off.
	MinTpsRatio float64
	// MaxRestarts: the run fails when more node restarts than this are
	// recorded during it. The check is skipped when the run itself stops
	// nodes (StopsPerRound > 0).
	MaxRestarts int
}

func DefaultAnomalyParams() AnomalyParams {
	return AnomalyParams{Checks: true, ChainStallSlots: 10, TxStallMin: 10, MinTpsRatio: 0.2, MaxRestarts: 0}
}

func anomalyValidationSteps(p *GenParams) []ValidationStep {
	return []ValidationStep{
		{
			ErrorMsg: "anomaly_chain_stall_slots must be at least 1",
			Check:    func(p *GenParams) bool { return p.Anomaly.ChainStallSlots < 1 },
			ExitCode: 1,
		},
		{
			ErrorMsg: "anomaly_tx_stall_min must be at least 1",
			Check:    func(p *GenParams) bool { return p.Anomaly.TxStallMin < 1 },
			ExitCode: 1,
		},
		{
			ErrorMsg: fmt.Sprintf("anomaly_min_tps_ratio must be between 0 and 1 (got %v)", p.Anomaly.MinTpsRatio),
			Check:    func(p *GenParams) bool { return p.Anomaly.MinTpsRatio < 0 || p.Anomaly.MinTpsRatio > 1 },
			ExitCode: 1,
		},
		{
			ErrorMsg: "anomaly_max_restarts must not be negative",
			Check:    func(p *GenParams) bool { return p.Anomaly.MaxRestarts < 0 },
			ExitCode: 1,
		},
	}
}
