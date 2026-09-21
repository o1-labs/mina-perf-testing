package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	lib "itn_orchestrator"
	"log"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	logging "github.com/ipfs/go-log/v2"
	"github.com/lib/pq"
)

type ExperimentStatus string

const (
	NotRunned  ExperimentStatus = "not_runned"
	Running    ExperimentStatus = "running"
	Cancelling ExperimentStatus = "cancelling"
	Cancelled  ExperimentStatus = "cancelled"
	Ended      ExperimentStatus = "ended"
)

type ExperimentState struct {
	// Name is the primary key in 001-init-schema.sql. Declaring it here too is
	// what keeps AutoMigrate alive: without the tag GORM believes the table has
	// no key, tries to make the column nullable, and Postgres rejects that with
	// SQLSTATE 42P16 — aborting the whole migration before any later column is
	// considered.
	Name            string           `gorm:"primaryKey" json:"name"`
	Description     string           `json:"description"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	EndedAt         *time.Time       `json:"ended_at,omitempty"`
	Status          ExperimentStatus `json:"status"`
	Comment         *string          `json:"comment,omitempty"`
	CurrentStepNo   int              `json:"step"`
	CurrentStepName string           `json:"step_name"`
	Setup           lib.GenParams    `gorm:"column:setup_json;type:jsonb" json:"setup_json"`
	Warnings        pq.StringArray   `gorm:"type:text[]" json:"warnings,omitempty"`
	Errors          pq.StringArray   `gorm:"type:text[]" json:"errors,omitempty"`
	Logs            pq.StringArray   `gorm:"type:text[]" json:"logs,omitempty"`
	// WebhookURL is never serialised to API clients: for Slack, Discord and
	// Teams the URL *is* the credential, and GET /api/v0/experiment/status
	// is unauthenticated. Persistence is driven by the gorm tag and by the
	// explicit column map in updateExperimentInDB, so the DB column is
	// unaffected by `json:"-"`.
	WebhookURL string `gorm:"column:webhook_url" json:"-"`
}

func (ExperimentState) TableName() string {
	return "experiment_state"
}

type Store struct {
	mu         sync.Mutex
	experiment *ExperimentState
	DB         *gorm.DB
	cancel     context.CancelFunc
	// lockConn holds the session-level advisory lock that makes this process
	// the single orchestrator. It is kept open for the lifetime of the
	// process: Postgres releases a session lock when its connection dies, so
	// a crashed pod frees it without anyone cleaning up.
	lockConn *sql.Conn
}

// orchestratorLockID is the key for the advisory lock that admits one
// orchestrator at a time. Arbitrary but fixed; every instance must use it.
const orchestratorLockID int64 = 0x6d696e61 // "mina"

// NewStore opens the store and creates experiment_state if it is missing.
//
// The error is returned rather than logged because this AutoMigrate is the only
// thing that creates the table -- load-tests-cluster/init-sql has no DDL for it.
// If it fails (for example, a role without CREATE), the service would bind and
// look healthy, and the first POST /run would fail with
// `relation "experiment_state" does not exist` after Store.Add had already
// taken the in-process slot. Failing at boot is the honest outcome.
func NewStore(db *gorm.DB) (*Store, error) {
	log.Printf("Starting auto-migration for ExperimentState table...")
	if err := db.AutoMigrate(&ExperimentState{}); err != nil {
		return nil, fmt.Errorf("auto-migrating ExperimentState table: %w", err)
	}
	log.Printf("Auto-migration completed successfully")
	store := &Store{
		DB: db,
	}

	// Reconcile only while holding the single-orchestrator lock. Without it,
	// a second pod -- which a rolling update creates by design -- would reap
	// the *live* run of the first: the UPDATE below is scoped by status
	// alone, and ExperimentState carries no instance, host or pid column to
	// scope it by.
	if store.acquireOrchestratorLock() {
		store.reconcileInterruptedExperiments()
	} else {
		log.Printf("another orchestrator holds the experiment lock; skipping reconcile")
	}
	return store, nil
}

// acquireOrchestratorLock takes the session-level advisory lock, pinned to one
// connection that is then held for the lifetime of the process.
//
// Holding it only across the reconcile would not help: the case to prevent is
// pod B starting while pod A is mid-experiment, and A is long past its own
// startup by then. A keeps the lock, so B cannot take it and does not reconcile.
func (s *Store) acquireOrchestratorLock() bool {
	sqlDB, err := s.DB.DB()
	if err != nil {
		log.Printf("Error obtaining database handle for the orchestrator lock: %v", err)
		return false
	}

	ctx := context.Background()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		log.Printf("Error pinning a connection for the orchestrator lock: %v", err)
		return false
	}

	var acquired bool
	if err := conn.QueryRowContext(ctx,
		`SELECT pg_try_advisory_lock($1)`, orchestratorLockID).Scan(&acquired); err != nil {
		log.Printf("Error taking the orchestrator lock: %v", err)
		conn.Close()
		return false
	}
	if !acquired {
		conn.Close()
		return false
	}

	// Kept open deliberately: closing it would return the connection to the
	// pool and release the lock.
	s.lockConn = conn
	return true
}

// Close releases the orchestrator lock. Postgres would release it anyway when
// the connection drops, so this is for orderly shutdown rather than safety.
func (s *Store) Close() error {
	if s.lockConn == nil {
		return nil
	}
	conn := s.lockConn
	s.lockConn = nil
	if _, err := conn.ExecContext(context.Background(),
		`SELECT pg_advisory_unlock($1)`, orchestratorLockID); err != nil {
		log.Printf("Error releasing the orchestrator lock: %v", err)
	}
	return conn.Close()
}

// reconcileInterruptedExperiments closes out experiments the previous process
// was driving when it stopped.
//
// A worker never survives a restart, so any row still `running` or `cancelling`
// at startup is being driven by nobody. Leaving those rows alone made a pod
// restart an amnesia trick: the in-memory slot came back free and the run
// history kept a row that claimed to be in progress forever. Marking them
// terminal here is what makes a restart a real recovery — and it is also the
// only record that a run was lost, since the process that knew about it is gone.
// This runs as one UPDATE rather than a read-modify-write loop, because
// ExperimentState cannot be read back through GORM at all: Setup is a
// lib.GenParams, which implements no sql.Scanner, so any SELECT into the struct
// fails with "unsupported Scan, storing driver.Value type []uint8". The same
// reason updateExperimentInDB marshals Setup by hand on the way out.
func (s *Store) reconcileInterruptedExperiments() {
	const note = "Experiment interrupted: the orchestrator restarted while this run was in progress"

	// RETURNING name so the log names what was lost. Selecting a text column
	// sidesteps the GenParams scan problem entirely.
	var names []string
	err := s.DB.Raw(`
		UPDATE experiment_state
		SET status = 'error',
		    ended_at = now(),
		    updated_at = now(),
		    errors = array_append(coalesce(errors, ARRAY[]::text[]), ?)
		WHERE status IN (?, ?)
		RETURNING name`, note, string(Running), string(Cancelling)).Scan(&names).Error
	if err != nil {
		log.Printf("Error closing out interrupted experiments: %v", err)
		return
	}
	if len(names) > 0 {
		log.Printf("Closed out %d experiment(s) left in progress when the orchestrator last stopped: %s",
			len(names), strings.Join(names, ", "))
	}
}

func (a *Store) NameIsUnique(name string) bool {
	var count int64
	err := a.DB.Where("name = ?", name).Model(&ExperimentState{}).Count(&count).Error
	if err != nil {
		log.Printf("Error checking experiment uniqueness for name '%s': %v", name, err)
		return false
	}
	return count == 0
}

func (a *Store) WriteExperimentToDB(state ExperimentState) error {
	err := a.DB.Create(&state).Error
	if err != nil {
		log.Printf("Error writing experiment to DB: %v", err)
		return err
	}
	return nil
}

// updateExperimentInDB persists the mutable half of the state.
//
// setup_json is deliberately absent: it never changes after creation, and
// WriteExperimentToDB already writes it once. This function is called from
// AtomicSet, i.e. once per appended log line, so including it re-marshalled the
// whole GenParams -- Privkeys and RotationKeys among them -- on every log line.
func (a *Store) updateExperimentInDB(state *ExperimentState) error {
	err := a.DB.Model(&ExperimentState{}).Where("name = ?", state.Name).Updates(map[string]interface{}{
		"updated_at":        state.UpdatedAt,
		"ended_at":          state.EndedAt,
		"status":            state.Status,
		"current_step_no":   state.CurrentStepNo,
		"current_step_name": state.CurrentStepName,
		"warnings":          state.Warnings,
		"errors":            state.Errors,
		"logs":              state.Logs,
		"webhook_url":       state.WebhookURL,
	}).Error
	if err != nil {
		log.Printf("Error updating experiment in DB: %v", err)
		return err
	}
	return nil

}

func (s *Store) AtomicSet(f func(experiment *ExperimentState)) *ExperimentState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.experiment != nil {
		f(s.experiment)
		if s.experiment.EndedAt == nil {
			s.experiment.UpdatedAt = time.Now()
		}
	}

	if s.experiment != nil {
		err := s.updateExperimentInDB(s.experiment)
		if err != nil {
			log.Printf("Error updating experiment in DB: %v", err)
		}
	}
	return s.experiment
}

func (s *Store) AtomicGet() *ExperimentState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.experiment == nil {
		return nil
	}
	return s.experiment
}

// markEnded stamps the end time with the wall clock. It exists because the
// obvious `experiment.EndedAt = &time.Time{}` takes the address of the *zero*
// time, which is how finished experiments came to report an ended_at of
// 0001-01-01 00:00:00.
func markEnded(experiment *ExperimentState) {
	now := time.Now()
	experiment.EndedAt = &now
}

// detachWorker records that no goroutine is driving the experiment any more.
// Cancel relies on this to tell "winding down" from "nothing left to wind
// down"; without it a cancel with no worker attached parks in `cancelling`
// forever and only a pod restart clears it. Caller must hold s.mu.
func (s *Store) detachWorker() {
	s.cancel = nil
}

// FinishWithError sets the experiment status to "error" and appends the error message
func (s *Store) FinishWithError(err *lib.OrchestratorError) *ExperimentState {
	experiment := s.AtomicSet(func(experiment *ExperimentState) {
		experiment.Status = "error"
		experiment.Errors = append(experiment.Errors, err.Message)
		markEnded(experiment)
	})
	s.mu.Lock()
	s.detachWorker()
	s.mu.Unlock()
	return experiment
}

// FinishWithSuccess sets the experiment status to "success" and marks it as completed
func (s *Store) FinishWithSuccess() *ExperimentState {
	experiment := s.AtomicSet(func(experiment *ExperimentState) {
		experiment.Status = "success"
		markEnded(experiment)
	})
	s.mu.Lock()
	s.detachWorker()
	s.mu.Unlock()
	return experiment
}

// FinishWithCancel marks an experiment the operator asked to stop as cancelled.
//
// A cancellation is not a failure: it must not add to Errors and must not fire
// the error webhook. Without this the context cancellation surfaces as an
// ordinary error from RunExperiment and the experiment is reported as "error".
func (s *Store) FinishWithCancel() *ExperimentState {
	return s.AtomicSet(func(experiment *ExperimentState) {
		experiment.Status = Cancelled
		markEnded(experiment)
	})
}

// ErrExperimentRunning reports that the single-experiment slot is taken. It is
// a sentinel so the HTTP layer can answer 409 for a held slot and 500 for a
// failed write, which used to be indistinguishable at the call site.
var ErrExperimentRunning = errors.New("an experiment is already running")

// holdsSlot reports whether the in-memory experiment still occupies the single
// experiment slot. Cancelling counts: a run being wound down is not finished,
// and starting another one alongside it would have two orchestrators driving
// the same network. Caller must hold s.mu.
func (s *Store) holdsSlot() bool {
	if s.experiment == nil {
		return false
	}
	return s.experiment.Status == Running || s.experiment.Status == Cancelling
}

// Add claims the single experiment slot and persists the experiment.
//
// The write happens under the same lock as the claim, and a failed write
// releases the claim. Previously the claim was made first and the write ran
// afterwards in the handler: when the write failed — as it did for every
// experiment while `webhook_url` was missing from the schema — the slot stayed
// held by a run that existed nowhere but in this process's memory, and no
// cancel could reach it. One such lock was held for 14 days.
func (s *Store) Add(experiment *ExperimentState, cancel context.CancelFunc) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.holdsSlot() {
		return fmt.Errorf("%w: %q, started %s ago, at step %d (%s)",
			ErrExperimentRunning, s.experiment.Name,
			time.Since(s.experiment.CreatedAt).Truncate(time.Second),
			s.experiment.CurrentStepNo, s.experiment.CurrentStepName)
	}
	if err := s.WriteExperimentToDB(*experiment); err != nil {
		return fmt.Errorf("failed to write experiment to database: %w", err)
	}
	s.experiment = experiment
	s.cancel = cancel
	return nil
}

// Cancel stops the running job.
//
// Cancel is terminal in both directions. With a worker attached it asks the
// worker to stop and leaves the experiment `cancelling` — the worker moves it
// on when it unwinds. With no worker attached there is nothing left to unwind,
// so the experiment goes straight to `cancelled` rather than waiting on a
// goroutine that already exited.
func (s *Store) Cancel() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.holdsSlot() {
		return fmt.Errorf("no experiment running")
	}

	if s.cancel == nil {
		s.experiment.Status = Cancelled
		s.experiment.UpdatedAt = time.Now()
		markEnded(s.experiment)
		if err := s.updateExperimentInDB(s.experiment); err != nil {
			log.Printf("Error persisting cancelled experiment: %v", err)
		}
		return nil
	}

	s.experiment.Status = Cancelling
	s.experiment.UpdatedAt = time.Now()
	if err := s.updateExperimentInDB(s.experiment); err != nil {
		log.Printf("Error persisting cancelling experiment: %v", err)
	}
	s.cancel()
	return nil
}

// UpdateStatus updates the single job's status
func (s *Store) UpdateStatus(status ExperimentStatus) error {
	s.AtomicSet(func(experiment *ExperimentState) {
		experiment.Status = status
		experiment.UpdatedAt = time.Now()
	})
	return nil
}

// UpdateCurrentStep updates the single job's current step
func (s *Store) UpdateCurrentStep(name string, number int) error {
	s.AtomicSet(func(experiment *ExperimentState) {
		experiment.CurrentStepName = name
		experiment.CurrentStepNo = number
		experiment.UpdatedAt = time.Now()
	})
	return nil
}

func (s *Store) AppendWarningF(format string, args ...interface{}) error {
	message := fmt.Sprintf(format, args...)
	s.AtomicSet(func(experiment *ExperimentState) {
		experiment.Warnings = append(experiment.Warnings, message)
		experiment.UpdatedAt = time.Now()
	})
	return nil
}

func (s *Store) AppendErrorF(format string, args ...interface{}) error {
	message := fmt.Sprintf(format, args...)
	s.AtomicSet(func(experiment *ExperimentState) {
		if strings.Contains(message, "context canceled") {
			experiment.Status = Cancelled
			now := time.Now()
			experiment.EndedAt = &now
		} else {
			experiment.Errors = append(experiment.Errors, message)
		}
	})
	return nil
}

func (s *Store) AppendLogF(format string, args ...interface{}) error {
	message := fmt.Sprintf(format, args...)
	s.AtomicSet(func(experiment *ExperimentState) {
		if strings.HasPrefix(format, "Performing steps") {
			experiment.CurrentStepName = args[0].(string)
			experiment.CurrentStepNo = args[2].(int)
		} else if strings.HasPrefix(format, "Performing step") {
			experiment.CurrentStepName = args[0].(string)
			experiment.CurrentStepNo = args[1].(int)
		}
		experiment.Logs = append(experiment.Logs, message)
		experiment.UpdatedAt = time.Now()
	})
	return nil
}

type StoreLogging struct {
	Store *Store
	Log   *logging.ZapEventLogger
}

func (s StoreLogging) Infof(format string, args ...interface{}) {
	s.Log.Infof(format, args...)
	s.Store.AppendLogF(format, args...)
}

func (s StoreLogging) Errorf(format string, args ...interface{}) {
	s.Log.Errorf(format, args...)
	s.Store.AppendErrorF(format, args...)
}

func (s StoreLogging) Debugf(format string, args ...interface{}) {
	s.Log.Debugf(format, args...)
	s.Store.AppendLogF(format, args...)
}

func (s StoreLogging) Debug(args ...interface{}) {
	s.Log.Debug(args...)
	s.Store.AppendLogF("%v", args...)
}

func (s StoreLogging) Info(args ...interface{}) {
	s.Log.Info(args...)
	s.Store.AppendLogF("%v", args...)
}
func (s StoreLogging) Error(args ...interface{}) {
	s.Log.Error(args...)
	s.Store.AppendErrorF("%v", args...)
}
func (s StoreLogging) Fatal(args ...interface{}) {
	s.Log.Fatal(args...)
	s.Store.AppendLogF("%v", args...)
}

func (s StoreLogging) Fatalf(format string, args ...interface{}) {
	s.Log.Fatalf(format, args...)
	s.Store.AppendLogF(format, args...)
}

func (s StoreLogging) Warnf(format string, args ...interface{}) {
	s.Log.Warnf(format, args...)
	s.Store.AppendWarningF(format, args...)
}

func (s StoreLogging) Warn(args ...interface{}) {
	s.Log.Warn(args...)
	s.Store.AppendWarningF("%v", args...)
}

func (s StoreLogging) Panic(args ...interface{}) {
	s.Log.Panic(args...)
	s.Store.AppendLogF("%v", args...)
}

func (s StoreLogging) Panicf(format string, args ...interface{}) {
	s.Log.Panicf(format, args...)
	s.Store.AppendLogF(format, args...)
}
