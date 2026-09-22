package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
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
	// lockMu guards lockConn and stopLock. It is separate from mu so that a
	// slow lock query cannot block GET /status or POST /cancel.
	lockMu sync.Mutex
	// lockConn holds the session-level advisory lock that makes this process
	// the single orchestrator. Postgres releases a session lock when its
	// connection dies, so a crashed pod frees it without anyone cleaning up --
	// and, for the same reason, a dropped connection silently releases it
	// while this process is still running, which is why the supervisor below
	// re-proves the connection instead of trusting the field.
	lockConn *sql.Conn
	// stopLock ends the supervisor goroutine.
	stopLock context.CancelFunc
}

// orchestratorLockID is the key for the advisory lock that admits one
// orchestrator at a time.
//
// The value is the FNV-1a hash of the string below rather than a readable
// constant: an advisory lock key is global to the database, and "mina" spelt
// in ASCII (0x6d696e61) is exactly what another Mina-adjacent tool sharing
// this database would reach for first.
//
// The lock is session-level, so it requires a direct connection or a pooler in
// session mode. Through PgBouncer in transaction or statement mode the lock is
// taken on a connection the pooler may hand to somebody else, and the whole
// design fails silently.
var orchestratorLockID = fnvLockID("o1labs/mina-perf-testing:orchestrator-experiment-slot")

func fnvLockID(name string) int64 {
	h := fnv.New64a()
	h.Write([]byte(name))
	return int64(h.Sum64())
}

// lockPollInterval is how often the supervisor retries the lock and re-proves
// the one it holds. It is a variable so the tests do not wait on it.
var lockPollInterval = 30 * time.Second

// lockQueryTimeout bounds every lock statement, so a reachable but hung
// Postgres cannot block startup or the supervisor forever.
const lockQueryTimeout = 10 * time.Second

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
	//
	// The lock is taken by a supervisor rather than once here. A single
	// attempt at startup failed in both directions: a rolling update starts
	// the new pod while the old one still holds the lock, and with no retry
	// that pod never reconciled at all, so the old row stayed `running`
	// forever; and a connection drop releases the lock in Postgres while this
	// process still believes it holds it, which is the table-wide reap
	// restored after one blip.
	ctx, cancel := context.WithCancel(context.Background())
	store.stopLock = cancel
	store.superviseOrchestratorLock(ctx)
	return store, nil
}

// HasLock reports whether this process currently holds the single-orchestrator
// lock.
func (s *Store) HasLock() bool {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	return s.lockConn != nil
}

// superviseOrchestratorLock takes the lock, reconciles on every acquisition,
// and keeps proving that the connection holding it is still alive.
func (s *Store) superviseOrchestratorLock(ctx context.Context) {
	s.pollOrchestratorLock()

	go func() {
		ticker := time.NewTicker(lockPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.pollOrchestratorLock()
			}
		}
	}()
}

// pollOrchestratorLock is one supervisor step: prove the lock we hold, or try
// to take one we do not.
func (s *Store) pollOrchestratorLock() {
	if s.holdsLiveLock() {
		return
	}
	acquired, err := s.acquireOrchestratorLock()
	switch {
	case err != nil:
		// Distinct from "somebody else holds it": reporting a failed query as
		// a held lock said something untrue about another pod.
		log.Printf("Could not determine the orchestrator lock: %v", err)
	case !acquired:
		log.Printf("Another orchestrator holds the experiment lock; not reconciling")
	default:
		log.Printf("Took the orchestrator lock")
		s.reconcileInterruptedExperiments()
	}
}

// holdsLiveLock reports whether the pinned connection is still usable. A
// session lock lives on its connection: when that connection dies, Postgres
// drops the lock and database/sql does not reconnect a *sql.Conn, so without
// this check the process holds nothing and knows nothing.
func (s *Store) holdsLiveLock() bool {
	s.lockMu.Lock()
	conn := s.lockConn
	s.lockMu.Unlock()
	if conn == nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), lockQueryTimeout)
	defer cancel()

	// The question asked of the pinned connection is "does this session still
	// hold an advisory lock", not "is the connection alive". A ping is not
	// enough: the driver can answer it from a fresh connection, which holds
	// nothing, and Postgres has already dropped the lock with the session that
	// died.
	var held bool
	err := conn.QueryRowContext(ctx, `
		SELECT count(*) > 0
		FROM pg_locks
		WHERE locktype = 'advisory' AND granted AND pid = pg_backend_pid()`).Scan(&held)
	if err != nil || !held {
		if err != nil {
			log.Printf("Lost the connection holding the orchestrator lock (%v); another orchestrator may take it", err)
		} else {
			log.Printf("The orchestrator lock is no longer held on this session; another orchestrator may take it")
		}
		s.lockMu.Lock()
		if s.lockConn == conn {
			s.lockConn = nil
		}
		s.lockMu.Unlock()
		conn.Close()
		return false
	}
	return true
}

// acquireOrchestratorLock takes the session-level advisory lock, pinned to one
// connection that is then held for the lifetime of the process.
//
// Holding it only across the reconcile would not help: the case to prevent is
// pod B starting while pod A is mid-experiment, and A is long past its own
// startup by then. A keeps the lock, so B cannot take it and does not reconcile.
func (s *Store) acquireOrchestratorLock() (bool, error) {
	sqlDB, err := s.DB.DB()
	if err != nil {
		return false, fmt.Errorf("obtaining database handle: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), lockQueryTimeout)
	defer cancel()

	// One connection is pinned for the lifetime of the lock. This is free
	// while SetMaxOpenConns is unset, as it is here and on main; setting it to
	// 1 would deadlock the service against its own lock.
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("pinning a connection: %w", err)
	}

	var acquired bool
	if err := conn.QueryRowContext(ctx,
		`SELECT pg_try_advisory_lock($1)`, orchestratorLockID).Scan(&acquired); err != nil {
		conn.Close()
		return false, fmt.Errorf("taking the lock: %w", err)
	}
	if !acquired {
		conn.Close()
		return false, nil
	}

	// Kept open deliberately: closing it would return the connection to the
	// pool and release the lock.
	s.lockMu.Lock()
	s.lockConn = conn
	s.lockMu.Unlock()
	return true, nil
}

// Close stops the lock supervisor and releases the orchestrator lock.
// Postgres would release it anyway when the connection drops, so this is for
// orderly shutdown rather than safety; main.go calls it from the signal
// handler, which is what makes an orderly shutdown happen at all.
func (s *Store) Close() error {
	if s.stopLock != nil {
		s.stopLock()
		s.stopLock = nil
	}

	s.lockMu.Lock()
	conn := s.lockConn
	s.lockConn = nil
	s.lockMu.Unlock()
	if conn == nil {
		return nil
	}
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

// dbWriteTimeout bounds the writes taken under s.mu.
const dbWriteTimeout = 30 * time.Second

func (a *Store) WriteExperimentToDB(state ExperimentState) error {
	return a.WriteExperimentToDBContext(context.Background(), state)
}

func (a *Store) WriteExperimentToDBContext(ctx context.Context, state ExperimentState) error {
	err := a.DB.WithContext(ctx).Create(&state).Error
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
	// Bounded for the same reason as the INSERT in Add: this runs from
	// AtomicSet, i.e. once per appended log line, and AtomicSet holds s.mu
	// throughout, so an unbounded write against a hung Postgres would block
	// GET /status and POST /cancel with it.
	ctx, cancel := context.WithTimeout(context.Background(), dbWriteTimeout)
	defer cancel()
	err := a.DB.WithContext(ctx).Model(&ExperimentState{}).Where("name = ?", state.Name).Updates(map[string]interface{}{
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
	// The INSERT happens under s.mu, which is what makes claiming the slot and
	// persisting the experiment one step. It is bounded, because that mutex
	// also serialises AtomicGet and Cancel: an unbounded write against a hung
	// Postgres would block GET /status and POST /cancel with it, and a
	// liveness probe on the status endpoint would then restart the pod.
	ctx, cancel := context.WithTimeout(context.Background(), dbWriteTimeout)
	defer cancel()
	if err := s.WriteExperimentToDBContext(ctx, *experiment); err != nil {
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
		// The current step is reported through Config.ReportStep, wired to
		// UpdateCurrentStep. This function used to prefix-match the log format
		// string and type-assert positional args out of it, which read the
		// batch *end* for "Performing steps %s (%d-%d)" and the *start* for
		// "Performing step %s (%d)" -- so the reported step jumped forward for
		// batched steps and not for single ones.
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
