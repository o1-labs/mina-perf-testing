package service

import (
	"context"
	"encoding/json"
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
	WebhookURL      string           `json:"webhook_url,omitempty"`
}

func (ExperimentState) TableName() string {
	return "experiment_state"
}

type Store struct {
	mu         sync.Mutex
	experiment *ExperimentState
	DB         *gorm.DB
	cancel     context.CancelFunc
}

func NewStore(db *gorm.DB) *Store {
	// Auto-migrate the schema
	log.Printf("Starting auto-migration for ExperimentState table...")
	err := db.AutoMigrate(&ExperimentState{})
	if err != nil {
		log.Printf("Error auto-migrating ExperimentState table: %v", err)
	} else {
		log.Printf("Auto-migration completed successfully")
	}
	store := &Store{
		DB: db,
	}
	store.reconcileInterruptedExperiments()
	return store
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
func (s *Store) reconcileInterruptedExperiments() {
	var interrupted []ExperimentState
	err := s.DB.Where("status IN ?", []ExperimentStatus{Running, Cancelling}).Find(&interrupted).Error
	if err != nil {
		log.Printf("Error looking for interrupted experiments: %v", err)
		return
	}
	for i := range interrupted {
		experiment := &interrupted[i]
		log.Printf("Experiment %q was %s when the orchestrator last stopped; marking it interrupted",
			experiment.Name, experiment.Status)
		experiment.Status = "error"
		experiment.Errors = append(experiment.Errors,
			"Experiment interrupted: the orchestrator restarted while this run was in progress")
		experiment.UpdatedAt = time.Now()
		markEnded(experiment)
		if err := s.updateExperimentInDB(experiment); err != nil {
			log.Printf("Error closing out interrupted experiment %q: %v", experiment.Name, err)
		}
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

func (a *Store) updateExperimentInDB(state *ExperimentState) error {
	// Convert Setup to JSON bytes to avoid GORM serialization issues
	setupJSON, err := json.Marshal(state.Setup)
	if err != nil {
		log.Printf("Error marshaling setup JSON: %v", err)
		return err
	}

	err = a.DB.Model(&ExperimentState{}).Where("name = ?", state.Name).Updates(map[string]interface{}{
		"updated_at":        state.UpdatedAt,
		"ended_at":          state.EndedAt,
		"status":            state.Status,
		"setup_json":        string(setupJSON),
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
