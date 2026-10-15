package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"gorm.io/gorm"

	lib "itn_orchestrator"
)

// AnomalyError is the cause a run is cancelled with when a within-run check
// fails. loadRun tells it from an operator cancel through context.Cause.
type AnomalyError struct{ Message string }

func (e *AnomalyError) Error() string { return "anomaly: " + e.Message }

// anomalyCause returns the anomaly a run's context was cancelled for, if any.
func anomalyCause(ctx context.Context) *AnomalyError {
	var e *AnomalyError
	if errors.As(context.Cause(ctx), &e) {
		return e
	}
	return nil
}

// anomalyProbe reads the network's state. The trace database is the source:
// the fetcher and the consumer put every node's block traces there, with the
// transactions each block carried.
type anomalyProbe interface {
	// LastBlockAt is the newest block any node reported; ok is false when
	// there is none for the current deployment.
	LastBlockAt(ctx context.Context) (at time.Time, ok bool, err error)
	// IncludedSince counts the transactions whose memo starts with prefix
	// that reached a block after since.
	IncludedSince(ctx context.Context, prefix string, since time.Time) (int, error)
	// RestartsSince counts the node restarts recorded after since.
	RestartsSince(ctx context.Context, since time.Time) (int, error)
}

type dbProbe struct{ db *gorm.DB }

func (p dbProbe) LastBlockAt(ctx context.Context) (time.Time, bool, error) {
	var at *float64
	err := p.db.WithContext(ctx).Raw(`SELECT max(trace_started_at) FROM block_trace
		WHERE deployment_id = get_max_deployment_id() AND trace_started_at > 1500000000`).Scan(&at).Error
	if err != nil || at == nil {
		return time.Time{}, false, err
	}
	sec, frac := math.Modf(*at)
	return time.Unix(int64(sec), int64(frac*1e9)), true, nil
}

func (p dbProbe) IncludedSince(ctx context.Context, prefix string, since time.Time) (int, error) {
	var n int
	err := p.db.WithContext(ctx).Raw(`SELECT count(*) FROM block_txs
		WHERE deployment_id = get_max_deployment_id() AND block_received > ? AND memo LIKE ?`,
		since, likePrefix(prefix)).Scan(&n).Error
	return n, err
}

func (p dbProbe) RestartsSince(ctx context.Context, since time.Time) (int, error) {
	var n int
	err := p.db.WithContext(ctx).Raw(`SELECT count(*) FROM node_restarts
		WHERE deployment_id = get_max_deployment_id() AND time > ?`, since).Scan(&n).Error
	return n, err
}

// likePrefix escapes prefix for a LIKE pattern that matches it at the start.
func likePrefix(prefix string) string {
	out := make([]byte, 0, len(prefix)+8)
	for i := 0; i < len(prefix); i++ {
		if c := prefix[i]; c == '%' || c == '_' || c == '\\' {
			out = append(out, '\\')
		}
		out = append(out, prefix[i])
	}
	return string(out) + "%"
}

// runWatcher holds the checks of one run. It learns where the run is from
// the steps the orchestrator reports: a payments or zkapp-txs step opens a
// load window of RoundDurationMin, and the transaction checks apply inside
// it, after a grace period of TxStallMin.
type runWatcher struct {
	probe    anomalyProbe
	params   lib.AnomalyParams
	slot     time.Duration
	round    time.Duration
	offered  float64 // transactions per second the run sends, lower bound
	prefix   string  // memo prefix of the run's transactions
	restarts bool    // whether the restart check applies
	now      func() time.Time

	mu        sync.Mutex
	started   time.Time
	loadStart time.Time
	loadUntil time.Time
}

func newRunWatcher(probe anomalyProbe, p *lib.GenParams, slotMs int) *runWatcher {
	offered := math.Min(p.BaseTps, p.StressTps)
	if p.MaxCostMixedTpsRatio > 1e-3 && p.MaxCostMixedTpsRatio < 1 {
		offered *= p.MaxCostMixedTpsRatio
	}
	w := &runWatcher{
		probe:    probe,
		params:   p.Anomaly,
		slot:     time.Duration(slotMs) * time.Millisecond,
		round:    time.Duration(p.RoundDurationMin) * time.Minute,
		offered:  offered,
		prefix:   p.ExperimentName + "-",
		restarts: p.StopsPerRound == 0,
		now:      time.Now,
	}
	w.started = w.now()
	return w
}

// Describe is the one-line summary logged when the run starts.
func (w *runWatcher) Describe() string {
	p := w.params
	if !p.Checks {
		return "Anomaly checks: off"
	}
	s := fmt.Sprintf("Anomaly checks: chain stall %d slots (%s), transaction stall %d min, throughput at least %.0f%% of %.2f tps",
		p.ChainStallSlots, time.Duration(p.ChainStallSlots)*w.slot, p.TxStallMin, p.MinTpsRatio*100, w.offered)
	if w.restarts {
		s += fmt.Sprintf(", at most %d node restart(s)", p.MaxRestarts)
	} else {
		s += ", restarts not checked (the run stops nodes itself)"
	}
	return s
}

// ObserveStep is called with each step the orchestrator begins.
func (w *runWatcher) ObserveStep(name string) {
	if name != "payments" && name != "zkapp-txs" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	// Both steps of one round open the same window; a later round's step
	// comes after the previous window ended (the generator waits out the
	// round), so it opens a new one.
	if now.After(w.loadUntil) {
		w.loadStart = now
		w.loadUntil = now.Add(w.round)
	}
}

// Check runs every check once and returns the first anomaly, or nil.
func (w *runWatcher) Check(ctx context.Context) (*AnomalyError, error) {
	if !w.params.Checks {
		return nil, nil
	}
	now := w.now()
	w.mu.Lock()
	loadStart, loadUntil := w.loadStart, w.loadUntil
	w.mu.Unlock()

	// Chain progress. A run with no block trace at all is measured from its
	// own start, so a dead pipeline is reported after the same delay.
	last, ok, err := w.probe.LastBlockAt(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the last block: %w", err)
	}
	if !ok || last.Before(w.started) {
		last = w.started
	}
	if stall := time.Duration(w.params.ChainStallSlots) * w.slot; now.Sub(last) > stall {
		return &AnomalyError{Message: fmt.Sprintf("no node reported a new block for %s (more than %d slots): the chain is stalled, or the trace pipeline is down",
			now.Sub(last).Truncate(time.Second), w.params.ChainStallSlots)}, nil
	}

	// The run's transactions, inside a load window and past its grace period.
	window := time.Duration(w.params.TxStallMin) * time.Minute
	if !loadStart.IsZero() && now.Before(loadUntil) && now.Sub(loadStart) >= window {
		n, err := w.probe.IncludedSince(ctx, w.prefix, now.Add(-window))
		if err != nil {
			return nil, fmt.Errorf("counting the included transactions: %w", err)
		}
		if n == 0 {
			return &AnomalyError{Message: fmt.Sprintf("none of the run's transactions reached a block in the last %d min while load is being sent", w.params.TxStallMin)}, nil
		}
		if achieved, floor := float64(n)/window.Seconds(), w.params.MinTpsRatio*w.offered; w.params.MinTpsRatio > 0 && achieved < floor {
			return &AnomalyError{Message: fmt.Sprintf("%.3f tps of the run's transactions reached blocks in the last %d min (%d transactions), below %.0f%% of the %.2f tps offered",
				achieved, w.params.TxStallMin, n, w.params.MinTpsRatio*100, w.offered)}, nil
		}
	}

	if w.restarts {
		n, err := w.probe.RestartsSince(ctx, w.started)
		if err != nil {
			return nil, fmt.Errorf("counting node restarts: %w", err)
		}
		if n > w.params.MaxRestarts {
			return &AnomalyError{Message: fmt.Sprintf("%d node restart(s) recorded during the run, more than the %d allowed", n, w.params.MaxRestarts)}, nil
		}
	}
	return nil, nil
}

// anomalyCheckInterval is how often a run is checked.
const anomalyCheckInterval = time.Minute

// watchRun checks the run until ctx ends and cancels it with the first
// anomaly as the cause. A probe failure (the database being unreachable, for
// example) is logged and the check is tried again at the next tick: the
// orchestrator's own health is not the network's.
func (a *App) watchRun(ctx context.Context, cancel context.CancelCauseFunc, w *runWatcher, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		anomaly, err := w.Check(ctx)
		if err != nil {
			stdoutLog.Printf("Anomaly check skipped: %v", err)
			continue
		}
		if anomaly != nil {
			stdoutLog.Printf("Anomaly: %s; cancelling the run", anomaly.Message)
			cancel(anomaly)
			return
		}
	}
}
