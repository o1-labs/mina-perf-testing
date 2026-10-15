package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	lib "itn_orchestrator"
)

type fakeProbe struct {
	lastBlock time.Time
	noBlock   bool
	included  int
	restarts  int
	err       error
}

func (f *fakeProbe) LastBlockAt(context.Context) (time.Time, bool, error) {
	return f.lastBlock, !f.noBlock, f.err
}
func (f *fakeProbe) IncludedSince(context.Context, string, time.Time) (int, error) {
	return f.included, f.err
}
func (f *fakeProbe) RestartsSince(context.Context, time.Time) (int, error) { return f.restarts, f.err }

func testWatcher(t *testing.T, probe anomalyProbe) (*runWatcher, *time.Time) {
	t.Helper()
	p := lib.DefaultGenParams()
	p.ExperimentName = "exp"
	p.BaseTps, p.StressTps = 1, 2
	p.RoundDurationMin = 30
	p.StopsPerRound = 0
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	clock := &now
	w := newRunWatcher(probe, &p, 20_000) // 20 s slots, as the local env
	w.now = func() time.Time { return *clock }
	w.started = now.Add(-time.Hour) // the run has been going for a while
	return w, clock
}

func wantAnomaly(t *testing.T, w *runWatcher, contains string) {
	t.Helper()
	a, err := w.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if contains == "" {
		if a != nil {
			t.Fatalf("unexpected anomaly: %s", a.Message)
		}
		return
	}
	if a == nil || !strings.Contains(a.Message, contains) {
		t.Fatalf("anomaly %v, want one containing %q", a, contains)
	}
}

func TestChainStall(t *testing.T) {
	f := &fakeProbe{included: 100}
	w, clock := testWatcher(t, f)
	f.lastBlock = clock.Add(-3 * time.Minute)
	wantAnomaly(t, w, "") // 9 slots: within the 10 allowed
	f.lastBlock = clock.Add(-3*time.Minute - 30*time.Second)
	wantAnomaly(t, w, "new block")
	// No trace at all: measured from the run start, so a dead pipeline is
	// reported after the same delay and not at the first tick.
	f.noBlock = true
	w.started = *clock
	*clock = w.started.Add(2 * time.Minute)
	wantAnomaly(t, w, "")
	*clock = w.started.Add(4 * time.Minute)
	wantAnomaly(t, w, "trace pipeline")
}

func TestTransactionChecksApplyInsideTheLoadWindowOnly(t *testing.T) {
	f := &fakeProbe{included: 0}
	w, clock := testWatcher(t, f)
	f.lastBlock = *clock
	wantAnomaly(t, w, "") // no load scheduled yet: nothing to include

	w.ObserveStep("discovery")
	wantAnomaly(t, w, "")
	w.ObserveStep("zkapp-txs")
	w.ObserveStep("payments") // the same round, the same window
	*clock = clock.Add(5 * time.Minute)
	f.lastBlock = *clock
	wantAnomaly(t, w, "") // grace period: 10 min
	*clock = clock.Add(5 * time.Minute)
	f.lastBlock = *clock
	wantAnomaly(t, w, "none of the run's transactions")

	f.included = 1 // 1 in 10 min = 0.0017 tps, under 20% of 1 tps
	wantAnomaly(t, w, "below 20%")
	f.included = 150 // 0.25 tps
	wantAnomaly(t, w, "")

	// After the round the pause has no load; the checks stop.
	*clock = clock.Add(25 * time.Minute)
	f.lastBlock = *clock
	f.included = 0
	wantAnomaly(t, w, "")
	// The next round opens a new window with a new grace period.
	w.ObserveStep("payments")
	*clock = clock.Add(9 * time.Minute)
	f.lastBlock = *clock
	wantAnomaly(t, w, "")
	*clock = clock.Add(2 * time.Minute)
	f.lastBlock = *clock
	wantAnomaly(t, w, "none of the run's transactions")
}

func TestRestartsAreCheckedOnlyWhenTheRunDoesNotStopNodes(t *testing.T) {
	f := &fakeProbe{included: 100, restarts: 1}
	w, clock := testWatcher(t, f)
	f.lastBlock = *clock
	wantAnomaly(t, w, "node restart")
	w.restarts = false // StopsPerRound > 0
	wantAnomaly(t, w, "")
	w.restarts = true
	w.params.MaxRestarts = 1
	wantAnomaly(t, w, "")
}

func TestChecksOffAndProbeErrors(t *testing.T) {
	f := &fakeProbe{err: errors.New("db down")}
	w, _ := testWatcher(t, f)
	if _, err := w.Check(context.Background()); err == nil {
		t.Fatal("a probe error was swallowed")
	}
	w.params.Checks = false
	if a, err := w.Check(context.Background()); a != nil || err != nil {
		t.Fatalf("checks off still ran: %v %v", a, err)
	}
	if got := w.Describe(); got != "Anomaly checks: off" {
		t.Errorf("Describe: %q", got)
	}
}

// watchRun cancels the run with the anomaly as the cause, which loadRun
// tells from an operator cancel.
func TestWatchRunCancelsWithTheAnomalyAsCause(t *testing.T) {
	f := &fakeProbe{noBlock: true}
	w, clock := testWatcher(t, f)
	*clock = w.started.Add(time.Hour)
	ctx, cancel := context.WithCancelCause(context.Background())
	a := &App{}
	go a.watchRun(ctx, cancel, w, 5*time.Millisecond)
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the run was not cancelled")
	}
	if anomalyCause(ctx) == nil {
		t.Fatalf("cause %v is not an anomaly", context.Cause(ctx))
	}
	opCtx, opCancel := context.WithCancelCause(context.Background())
	opCancel(nil)
	if anomalyCause(opCtx) != nil {
		t.Error("an operator cancel reads as an anomaly")
	}
}

func TestLikePrefix(t *testing.T) {
	if got := likePrefix("a_b%c"); got != `a\_b\%c%` {
		t.Errorf("likePrefix: %q", got)
	}
}

// The SQL of the probe against the trace tables (TEST_PG_DSN).
func TestDbProbe(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DROP TABLE IF EXISTS block_trace, block_txs, node_restarts, deployment`,
		`CREATE TABLE deployment (deployment_id SERIAL PRIMARY KEY)`,
		`INSERT INTO deployment DEFAULT VALUES`, `INSERT INTO deployment DEFAULT VALUES`,
		`CREATE OR REPLACE FUNCTION get_max_deployment_id() RETURNS INT AS $$ SELECT max(deployment_id) FROM deployment $$ LANGUAGE sql`,
		`CREATE TABLE block_trace (deployment_id INT, node_name VARCHAR, trace_started_at FLOAT)`,
		`CREATE TABLE block_txs (hash VARCHAR PRIMARY KEY, deployment_id INT, memo TEXT, block_received TIMESTAMPTZ)`,
		`CREATE TABLE node_restarts (deployment_id INT, node_name VARCHAR, time TIMESTAMPTZ)`,
		// An older deployment's rows must not count.
		`INSERT INTO block_trace VALUES (1, 'old', 1900000000), (2, 'seed', 1800000000.5), (2, 'bp-1', 1800000100.25), (2, 'bp-1', 0)`,
		`INSERT INTO block_txs VALUES ('h1', 2, 'exp-0-1', '2026-10-15 12:00:00+00'), ('h2', 2, 'exp-0-1', '2026-10-15 12:30:00+00'),
		   ('h3', 2, 'other-0-1', '2026-10-15 12:30:00+00'), ('h4', 1, 'exp-0-1', '2026-10-15 12:30:00+00')`,
		`INSERT INTO node_restarts VALUES (2, 'seed', '2026-10-15 12:30:00+00'), (1, 'seed', '2026-10-15 12:30:00+00')`,
	} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	p := dbProbe{db: db}
	ctx := context.Background()
	at, ok, err := p.LastBlockAt(ctx)
	if err != nil || !ok || at.Unix() != 1800000100 {
		t.Errorf("LastBlockAt: %v %v %v", at, ok, err)
	}
	since := time.Date(2026, 10, 15, 12, 15, 0, 0, time.UTC)
	if n, err := p.IncludedSince(ctx, "exp-", since); err != nil || n != 1 {
		t.Errorf("IncludedSince: %d %v, want 1", n, err)
	}
	if n, err := p.RestartsSince(ctx, since); err != nil || n != 1 {
		t.Errorf("RestartsSince: %d %v, want 1", n, err)
	}
	if err := db.Exec(`DELETE FROM block_trace`).Error; err != nil {
		t.Fatal(err)
	}
	if _, ok, err := p.LastBlockAt(ctx); ok || err != nil {
		t.Errorf("LastBlockAt with no rows: ok=%v err=%v", ok, err)
	}
}
