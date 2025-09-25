package service

import (
	"context"
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
	Name            string           `json:"name"`
	Description     string           `json:"description"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	EndedAt         *time.Time       `json:"ended_at,omitempty"`
	Status          ExperimentStatus `json:"status"`
	Comment         *string          `json:"comment,omitempty"`
	CurrentStepNo   int              `json:"step"`
	CurrentStepName string           `json:"step_name"`
	Setup           lib.GenParams    `gorm:"serializer:json" json:"setup_json"`
	Warnings        pq.StringArray   `gorm:"type:text[]" json:"warnings,omitempty"`
	Errors          pq.StringArray   `gorm:"type:text[]" json:"errors,omitempty"`
	Logs            pq.StringArray   `gorm:"type:text[]" json:"logs,omitempty"`
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
	return &Store{
		DB: db,
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

	err := a.DB.Model(&ExperimentState{}).Where("name = ?", state.Name).Updates(map[string]interface{}{
		"updated_at":        state.UpdatedAt,
		"ended_at":          state.EndedAt,
		"status":            state.Status,
		"setup_json":        state.Setup,
		"current_step_no":   state.CurrentStepNo,
		"current_step_name": state.CurrentStepName,
		"warnings":          state.Warnings,
		"errors":            state.Errors,
		"logs":              state.Logs,
	}).Error
	if err != nil {
		log.Printf("Error updating experiment in DB: %v", err)
		return err
	}
	return nil

}

func (s *Store) AtomicSet(f func(experiment *ExperimentState)) {
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
}

func (s *Store) AtomicGet() *ExperimentState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.experiment == nil {
		return nil
	}
	return s.experiment
}

// FinishWithError sets the experiment status to "error" and appends the error message
func (s *Store) FinishWithError(err *lib.OrchestratorError) {
	s.AtomicSet(func(experiment *ExperimentState) {
		experiment.Status = "error"
		experiment.Errors = append(experiment.Errors, err.Message)
		experiment.EndedAt = &time.Time{}
	})
}

// FinishWithSuccess sets the experiment status to "success" and marks it as completed
func (s *Store) FinishWithSuccess() {
	s.AtomicSet(func(experiment *ExperimentState) {
		experiment.Status = "success"
		experiment.EndedAt = &time.Time{}
	})
}

// Add sets the single job if none is running
func (s *Store) Add(experiment *ExperimentState, cancel context.CancelFunc) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.experiment != nil && s.experiment.Status == "running" {
		return fmt.Errorf("an experiment is already running")
	}
	s.experiment = experiment
	s.cancel = cancel
	return nil
}

// Cancel stops the running job
func (s *Store) Cancel() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.experiment == nil {
		return fmt.Errorf("not experiment running")
	}

	s.experiment.Status = Cancelling
	s.experiment.UpdatedAt = time.Now()
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
