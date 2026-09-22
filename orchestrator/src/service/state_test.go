package service

import (
	"os"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
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
