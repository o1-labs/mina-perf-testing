package service

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/lib/pq"

	lib "itn_orchestrator"
)

func testDB(t *testing.T) *gorm.DB {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return db
}

// Reproduces the reviewer's scenario: pod A is mid-experiment, pod B starts.
func TestReconcileDoesNotReapALiveRun(t *testing.T) {
	db := testDB(t)
	db.Exec(`DROP TABLE IF EXISTS experiment_state`)

	// --- pod A boots and starts an experiment ---
	a, err := NewStore(db)
	if err != nil {
		t.Fatalf("pod A NewStore: %v", err)
	}
	defer a.Close()
	if a.lockConn == nil {
		t.Fatal("pod A did not acquire the orchestrator lock")
	}

	now := time.Now()
	if err := db.Exec(`INSERT INTO experiment_state (name, status, created_at, updated_at)
	                   VALUES (?, ?, ?, ?)`, "itn2-load-live", string(Running), now, now).Error; err != nil {
		t.Fatalf("seeding the live run: %v", err)
	}

	// --- pod B boots against the same database ---
	db2 := testDB(t)
	b, err := NewStore(db2)
	if err != nil {
		t.Fatalf("pod B NewStore: %v", err)
	}
	defer b.Close()
	if b.lockConn != nil {
		t.Error("pod B acquired the lock while pod A holds it")
	}

	var status string
	if err := db.Raw(`SELECT status FROM experiment_state WHERE name = ?`, "itn2-load-live").
		Scan(&status).Error; err != nil {
		t.Fatalf("reading status: %v", err)
	}
	if status != string(Running) {
		t.Fatalf("pod B reaped pod A's live run: status = %q, want %q", status, Running)
	}
	t.Logf("pod A holds the lock; pod B skipped reconcile; live run still %q", status)
}

// The other half: a genuinely orphaned run must still be closed out.
func TestReconcileClosesOrphanedRun(t *testing.T) {
	db := testDB(t)
	db.Exec(`DROP TABLE IF EXISTS experiment_state`)

	// A previous process left a row behind and is gone (no lock held).
	first, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := db.Exec(`INSERT INTO experiment_state (name, status, created_at, updated_at)
	                   VALUES (?, ?, ?, ?)`, "itn2-load-orphan", string(Running), old, old).Error; err != nil {
		t.Fatalf("seeding the orphan: %v", err)
	}
	first.Close() // the process exits, releasing the lock

	db2 := testDB(t)
	second, err := NewStore(db2)
	if err != nil {
		t.Fatalf("restart NewStore: %v", err)
	}
	defer second.Close()
	if second.lockConn == nil {
		t.Fatal("the restarted process could not take the lock the dead one released")
	}

	var status string
	var errs []string
	db.Raw(`SELECT status FROM experiment_state WHERE name = ?`, "itn2-load-orphan").Scan(&status)
	db.Raw(`SELECT unnest(errors) FROM experiment_state WHERE name = ?`, "itn2-load-orphan").Scan(&errs)
	if status != "error" {
		t.Fatalf("orphaned run not closed out: status = %q", status)
	}
	if len(errs) == 0 {
		t.Fatal("orphaned run closed out with no note explaining why")
	}
	t.Logf("orphan closed out: status=%q note=%q", status, errs[0])
}

// waitFor polls until cond holds, so the tests do not depend on the
// supervisor's tick landing at a particular moment.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestLockIsTakenAfterARollingUpdate covers the shape a Deployment produces by
// default: maxSurge >= 1, so pod B starts while pod A is still alive.
//
// With a single attempt at startup B failed to take the lock and never tried
// again, so it never reconciled: A's row stayed `running` in the database for
// good, while B's in-memory slot was free and would happily start a second
// experiment beside a row claiming one was in progress.
func TestLockIsTakenAfterARollingUpdate(t *testing.T) {
	db := testDB(t)
	db.Exec(`DROP TABLE IF EXISTS experiment_state`)

	restore := lockPollInterval
	lockPollInterval = 50 * time.Millisecond
	defer func() { lockPollInterval = restore }()

	a, err := NewStore(db)
	if err != nil {
		t.Fatalf("pod A NewStore: %v", err)
	}
	if !a.HasLock() {
		t.Fatal("pod A did not acquire the orchestrator lock")
	}

	now := time.Now()
	if err := db.Exec(`INSERT INTO experiment_state (name, status, created_at, updated_at)
	                   VALUES (?, ?, ?, ?)`, "itn2-load-live", string(Running), now, now).Error; err != nil {
		t.Fatalf("seeding the live run: %v", err)
	}

	db2 := testDB(t)
	b, err := NewStore(db2)
	if err != nil {
		t.Fatalf("pod B NewStore: %v", err)
	}
	defer b.Close()
	if b.HasLock() {
		t.Fatal("pod B took the lock while pod A held it")
	}

	// The old pod terminates, as it does at the end of a rolling update.
	if err := a.Close(); err != nil {
		t.Fatalf("pod A Close: %v", err)
	}

	waitFor(t, "pod B to take the lock the old pod released", b.HasLock)

	// Having taken it, B reconciles: the run nobody is driving is closed out.
	waitFor(t, "pod B to close out the orphaned run", func() bool {
		var status string
		db2.Raw(`SELECT status FROM experiment_state WHERE name = ?`, "itn2-load-live").Scan(&status)
		return status == "error"
	})
}

// TestLockIsReleasedWhenItsConnectionDies: database/sql does not reconnect a
// *sql.Conn, and Postgres drops a session lock with its session. After a
// transient drop or a failover the process held nothing while still believing
// it held the lock, which is the table-wide reap restored after one blip.
//
// The supervisor interval is left long here, so each step is driven
// explicitly and the assertions cannot race a background tick.
func TestLockIsReleasedWhenItsConnectionDies(t *testing.T) {
	db := testDB(t)
	db.Exec(`DROP TABLE IF EXISTS experiment_state`)

	restore := lockPollInterval
	lockPollInterval = time.Hour
	defer func() { lockPollInterval = restore }()

	a, err := NewStore(db)
	if err != nil {
		t.Fatalf("pod A NewStore: %v", err)
	}
	defer a.Close()
	if !a.HasLock() {
		t.Fatal("pod A did not acquire the orchestrator lock")
	}

	// Kill the backend holding the lock, which is what a Postgres restart,
	// failover or idle-connection reaper does.
	db2 := testDB(t)
	var killed []bool
	if err := db2.Raw(`
		SELECT pg_terminate_backend(pid)
		FROM pg_locks
		WHERE locktype = 'advisory' AND granted AND pid <> pg_backend_pid()`).Scan(&killed).Error; err != nil {
		t.Fatalf("terminating the lock backend: %v", err)
	}
	if len(killed) == 0 {
		t.Fatal("no backend held an advisory lock; the test did not reproduce the drop")
	}

	// Postgres released the lock with the session, so another pod takes it --
	// which is the failure this guards against, and proves the loss is real
	// rather than only a bookkeeping change here.
	b, err := NewStore(db2)
	if err != nil {
		t.Fatalf("pod B NewStore: %v", err)
	}
	if !b.HasLock() {
		t.Fatal("pod B could not take the lock Postgres had released; the test setup is wrong")
	}

	// The next supervisor step must notice. Before this, the field alone said
	// "held" and nothing ever asked Postgres, so both pods believed they were
	// the single orchestrator.
	a.pollOrchestratorLock()
	if a.HasLock() {
		t.Fatal("pod A still believes it holds the lock that pod B now holds")
	}

	// Once the other pod goes away, A takes it again rather than staying
	// locked out for the life of the process.
	if err := b.Close(); err != nil {
		t.Fatalf("pod B Close: %v", err)
	}
	a.pollOrchestratorLock()
	if !a.HasLock() {
		t.Fatal("pod A did not take the lock again after it became free")
	}
}

// TestCancelReachesTheExperimentContext is the regression test for POST
// /cancel becoming a no-op.
//
// Add takes `cancel context.CancelFunc` as a parameter, and parameters share
// the function body's scope, so `ctx, cancel := ...` inside it declared only
// ctx and REASSIGNED cancel to the write timeout's func -- which the defer then
// fired immediately. s.cancel held a spent CancelFunc for a finished DB write,
// so nothing ever cancelled the experiment. go vet's lostcancel does not fire
// on a parameter.
func TestCancelReachesTheExperimentContext(t *testing.T) {
	db := testDB(t)
	db.Exec(`DROP TABLE IF EXISTS experiment_state`)
	s, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	expCtx, expCancel := context.WithCancel(context.Background())
	defer expCancel()

	now := time.Now()
	if err := s.Add(&ExperimentState{
		Name: "cancel-reaches-ctx", Status: Running, CreatedAt: now, UpdatedAt: now,
	}, expCancel); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	select {
	case <-expCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Cancel() did not reach the experiment context: the run keeps going, " +
			"the row sits in `cancelling`, and holdsSlot() then 409s every later POST /run")
	}
}

// TestReconcileDoesNotReapThisPodsOwnRun: the supervisor reconciles on every
// lock acquisition, so a connection blip on a single pod re-acquires and
// reconciles mid-run. The UPDATE is scoped by status alone, so it marked the
// pod's own live experiment `error`.
func TestReconcileDoesNotReapThisPodsOwnRun(t *testing.T) {
	db := testDB(t)
	db.Exec(`DROP TABLE IF EXISTS experiment_state`)
	s, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Now()
	if err := s.Add(&ExperimentState{
		Name: "my-own-live-run", Status: Running, CreatedAt: now, UpdatedAt: now,
	}, cancel); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// An orphan from a previous process, which must still be closed out.
	old := time.Now().Add(-time.Hour)
	if err := db.Exec(`INSERT INTO experiment_state (name, status, created_at, updated_at)
	                   VALUES (?, ?, ?, ?)`, "someone-elses-orphan", string(Running), old, old).Error; err != nil {
		t.Fatalf("seeding the orphan: %v", err)
	}

	// What the supervisor does on every re-acquisition.
	s.reconcileInterruptedExperiments()

	var mine, orphan string
	db.Raw(`SELECT status FROM experiment_state WHERE name = ?`, "my-own-live-run").Scan(&mine)
	db.Raw(`SELECT status FROM experiment_state WHERE name = ?`, "someone-elses-orphan").Scan(&orphan)

	if mine != string(Running) {
		t.Errorf("this pod's own live run was reaped: status = %q, want %q", mine, Running)
	}
	if orphan != "error" {
		t.Errorf("a genuine orphan was not closed out: status = %q, want \"error\"", orphan)
	}
}

// TestAppendErrorFDoesNotSetATerminalState: recording an error must not free
// the slot. Inferring "cancelled" from the message set a terminal status from
// inside RunExperiment while the worker was still writing, so the outgoing
// run's flushed comments landed on the next experiment's row.
func TestAppendErrorFDoesNotSetATerminalState(t *testing.T) {
	db := testDB(t)
	db.Exec(`DROP TABLE IF EXISTS experiment_state`)
	s, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Now()
	if err := s.Add(&ExperimentState{
		Name: "still-winding-down", Status: Running, CreatedAt: now, UpdatedAt: now,
	}, cancel); err != nil {
		t.Fatalf("Add: %v", err)
	}

	s.AppendErrorF("Error running step 3: %v", context.Canceled)

	got := s.AtomicGet()
	if got.EndedAt != nil {
		t.Errorf("AppendErrorF set EndedAt; the terminal state belongs to loadRun, "+
			"after RunExperiment has returned (status now %q)", got.Status)
	}
	if len(got.Errors) == 0 {
		t.Error("AppendErrorF did not record the error")
	}
	// The slot must still be held: a second Add would otherwise start a run
	// alongside a worker that is still writing to the first one's row.
	second := &ExperimentState{Name: "the-next-one", Status: Running, CreatedAt: now, UpdatedAt: now}
	if err := s.Add(second, cancel); !errors.Is(err, ErrExperimentRunning) {
		t.Errorf("Add during wind-down returned %v, want ErrExperimentRunning", err)
	}
}

// The hook receives the runs that were closed out, and only then.
func TestInterruptedRunHookReceivesTheClosedRuns(t *testing.T) {
	db := testDB(t)
	db.Exec(`DROP TABLE IF EXISTS experiment_state`)
	if err := db.AutoMigrate(&ExperimentState{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Exec(`INSERT INTO experiment_state (name, status, created_at, updated_at) VALUES (?, ?, ?, ?), (?, ?, ?, ?)`,
		"lost-run", string(Running), now, now, "done-run", string(Ended), now, now).Error; err != nil {
		t.Fatal(err)
	}
	var got []string
	var hookStore *Store
	s, err := NewStore(db, WithInterruptedRunHook(func(st *Store, names []string) { hookStore, got = st, names }))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(got) != 1 || got[0] != "lost-run" {
		t.Fatalf("hook got %v, want [lost-run]", got)
	}
	if hookStore != s {
		t.Error("hook did not receive the store it was registered on")
	}

	if err := s.AppendWarning(got, "stopped 2 schedulers"); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	if err := db.Raw(`SELECT unnest(warnings) FROM experiment_state WHERE name = ?`, "lost-run").Scan(&warnings).Error; err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || warnings[0] != "stopped 2 schedulers" {
		t.Errorf("warnings %v", warnings)
	}
}

// The hook stays quiet when nothing was interrupted.
func TestInterruptedRunHookIsQuietWithNothingToClose(t *testing.T) {
	db := testDB(t)
	db.Exec(`DROP TABLE IF EXISTS experiment_state`)
	called := false
	s, err := NewStore(db, WithInterruptedRunHook(func(*Store, []string) { called = true }))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if called {
		t.Error("hook ran with no interrupted run")
	}
}

// While orphaned load is being stopped no run may start, or its own
// schedulers would be stopped with the orphans; and the cleanup yields to a
// run that already holds the slot.
func TestCleanupAndTheRunSlotExcludeEachOther(t *testing.T) {
	db := testDB(t)
	db.Exec(`DROP TABLE IF EXISTS experiment_state`)
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if !s.BeginCleanup() {
		t.Fatal("cleanup refused on an idle store")
	}
	if s.BeginCleanup() {
		t.Fatal("a second cleanup was admitted during the first")
	}
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = s.Add(&ExperimentState{Name: "during-cleanup", Status: Running, CreatedAt: time.Now()}, cancel)
	if !errors.Is(err, ErrLoadCleanup) {
		t.Fatalf("Add during cleanup: %v, want ErrLoadCleanup", err)
	}
	s.EndCleanup()

	if err := s.Add(&ExperimentState{Name: "after-cleanup", Status: Running, CreatedAt: time.Now()}, cancel); err != nil {
		t.Fatalf("Add after cleanup: %v", err)
	}
	if s.BeginCleanup() {
		t.Error("cleanup was admitted while a run holds the slot")
	}
}

// The run record round-trips through the database: the fields set at
// creation and the node set that follows discovery.
func TestRunRecordIsPersisted(t *testing.T) {
	db := testDB(t)
	db.Exec(`DROP TABLE IF EXISTS experiment_state`)
	db.Exec(`DROP FUNCTION IF EXISTS get_max_deployment_id()`)
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if id := s.CurrentDeploymentID(context.Background()); id != nil {
		t.Errorf("deployment id %d without the function, want nil", *id)
	}
	db.Exec(`CREATE FUNCTION get_max_deployment_id() RETURNS INT AS $$ SELECT 7 $$ LANGUAGE sql`)
	defer db.Exec(`DROP FUNCTION IF EXISTS get_max_deployment_id()`)
	id := s.CurrentDeploymentID(context.Background())
	if id == nil || *id != 7 {
		t.Fatalf("deployment id %v, want 7", id)
	}

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Add(&ExperimentState{
		Name: "recorded", Status: Running, CreatedAt: time.Now(),
		RequestedBy: "n8n", DeploymentID: id, OrchestratorBuild: "abc1234", Script: `{"action":"wait"}`,
	}, cancel); err != nil {
		t.Fatal(err)
	}
	nodes := []lib.RunNode{
		{Address: "10.0.0.1:3086", PeerID: "p1", HarnessSupport: true, Build: "bbb"},
		{Address: "10.0.0.2:3086", PeerID: "p2", BlockProducer: true, HarnessSupport: true, Build: "aaa"},
	}
	if s.UpdateNodes(nil) || s.UpdateNodes([]lib.RunNode{}) {
		t.Fatal("an empty node set counted as a change")
	}
	if !s.UpdateNodes(nodes) {
		t.Fatal("the first node set did not count as a change")
	}
	if s.UpdateNodes(nodes) {
		t.Fatal("the same node set counted as a change")
	}

	var row struct {
		RequestedBy, OrchestratorBuild, Script string
		DeploymentID                           int
		Nodes                                  string
		DaemonBuilds                           pq.StringArray `gorm:"type:text[]"`
	}
	if err := db.Raw(`SELECT requested_by, orchestrator_build, script, deployment_id, nodes_json::text AS nodes, daemon_builds
		FROM experiment_state WHERE name = ?`, "recorded").Scan(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.RequestedBy != "n8n" || row.OrchestratorBuild != "abc1234" || row.Script != `{"action":"wait"}` || row.DeploymentID != 7 {
		t.Errorf("row %+v", row)
	}
	var stored RunNodes
	if err := stored.Scan(row.Nodes); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual([]lib.RunNode(stored), nodes) {
		t.Errorf("nodes %+v, want %+v", stored, nodes)
	}
	if got := []string(row.DaemonBuilds); !reflect.DeepEqual(got, []string{"aaa", "bbb"}) {
		t.Errorf("daemon builds %v", got)
	}
}
