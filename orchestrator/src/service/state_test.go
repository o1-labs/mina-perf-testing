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
