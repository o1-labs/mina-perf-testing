package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gorilla/mux"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	logging "github.com/ipfs/go-log/v2"

	service "itn_orchestrator/service"

	lib "itn_orchestrator"
)

// App holds application-wide dependencies
type App struct {
	Router          *mux.Router
	Store           *service.Store
	Config          *lib.OrchestratorConfig
	WebhookNotifier *WebhookNotifier
}

func (a *App) initializeRoutes() {
	log.Println("Registering routes...")

	// Initialize handlers
	createHandler := &CreateExperimentHandler{
		Store:  a.Store,
		Config: a.Config,
		App:    a,
	}
	infoHandler := &InfoExperimentHandler{
		Store: a.Store,
	}
	statusHandler := &StatusHandler{
		Store: a.Store,
	}
	cancelHandler := &CancelHandler{
		Store: a.Store,
	}

	// Register routes with new handlers
	a.Router.Handle("/api/v0/experiment/run", createHandler).Methods(http.MethodPost)
	a.Router.Handle("/api/v0/experiment/test", infoHandler).Methods(http.MethodPost)
	a.Router.Handle("/api/v0/experiment/status", statusHandler).Methods(http.MethodGet)
	a.Router.Handle("/api/v0/experiment/cancel", cancelHandler).Methods(http.MethodPost)
}

// allowPrivateWebhooks mirrors the -allow-private-webhooks flag. It is a
// package-level switch rather than a config-file field so that the default
// (refuse private destinations) applies even to a config written before the
// option existed.
var allowPrivateWebhooks bool

// Initialize opens the DB and sets up routes.
func (a *App) Initialize(connStr string, config lib.OrchestratorConfig) {
	var err error
	db, err := gorm.Open(postgres.Open(connStr), &gorm.Config{})
	if err != nil {
		log.Fatalf("Cannot open DB: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("Cannot get generic database object: %v", err)
	}
	if err = sqlDB.Ping(); err != nil {
		log.Fatalf("Cannot connect to DB: %v", err)
	}
	a.Router = mux.NewRouter()
	store, err := service.NewStore(db)
	if err != nil {
		log.Fatalf("Cannot initialise experiment store: %v", err)
	}
	a.Store = store
	a.Config = &config
	a.WebhookNotifier = NewWebhookNotifier(logging.Logger("webhook"))
	a.WebhookNotifier.allowPrivate = allowPrivateWebhooks
	a.initializeRoutes()
}

func (a *App) Run(address string) {

	log.Println("Starting orchestrator service...")

	log.Printf("Starting server on %s", address)
	if err := http.ListenAndServe(address, a.Router); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// isCancellation reports whether err is the operator stopping the experiment
// rather than the experiment failing. RunExperiment returns the context error
// unchanged in some paths and wrapped in an *OrchestratorError in others, so
// errors.Is is used rather than an equality check; OrchestratorError.Unwrap
// makes that reach the cause.
//
// DeadlineExceeded is deliberately not treated as a cancel. The experiment
// context carries no deadline today, but if one is ever added, a run that
// times out is a failure and must be reported as one.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled)
}

func (a *App) loadRun(inDecoder *json.Decoder, config lib.Config, log logging.StandardLogger) {
	if err := lib.RunExperiment(inDecoder, config, log); err != nil {
		// POST /cancel cancels the experiment context, which surfaces here as
		// an ordinary error. The operator asked for it, so it is a cancellation
		// and not a failure: no Errors entry, no error webhook, terminal status
		// "cancelled".
		// The context is checked as well as the error: a step that returns
		// its own error after the cancel has landed loses the cause, and the
		// operator still asked for the stop.
		if isCancellation(err) || config.Ctx.Err() != nil {
			a.Store.FinishWithCancel()
			return
		}
		var orchErr *lib.OrchestratorError
		var ok bool
		if orchErr, ok = err.(*lib.OrchestratorError); !ok {
			errMsg := fmt.Sprintf("Experiment failed: %v", err)
			orchErr = &lib.OrchestratorError{
				Message: errMsg,
				Code:    9,
			}
		}
		if experiment := a.Store.FinishWithError(orchErr); experiment.WebhookURL != "" {
			// Send error webhook notification
			go a.WebhookNotifier.SendErrorNotification(
				context.Background(),
				experiment.WebhookURL,
				experiment.Name,
				orchErr.Message,
				experiment.Warnings,
			)
		}
		return
	}
	if experiment := a.Store.FinishWithSuccess(); experiment.WebhookURL != "" {
		// Send success webhook notification
		go a.WebhookNotifier.SendSuccessNotification(
			context.Background(),
			experiment.WebhookURL,
			experiment.Name,
			experiment.Warnings,
		)
	}
}

func main() {

	// Define a -conn flag for the Postgres connection string
	connStr := flag.String("conn", "", "Postgres connection string (e.g. \"host=... user=... password=... dbname=... sslmode=disable\")")
	configFilename := flag.String("config", "", "Path to the config file")
	address := flag.String("address", ":8080", "Address to run the server on")
	flag.BoolVar(&allowPrivateWebhooks, "allow-private-webhooks", false,
		"permit webhook destinations on private (RFC1918/RFC4193) addresses; loopback and link-local stay refused")

	flag.Parse()

	if *connStr == "" {
		fmt.Fprintln(os.Stderr, "Usage: go run main.go -conn=\"<connection string>\"")
		os.Exit(1)
	}

	config := lib.LoadAppConfig(*configFilename)

	logging.SetupLogging(logging.Config{
		Format: logging.ColorizedOutput,
		Stderr: true,
		Stdout: false,
		Level:  logging.LogLevel(config.LogLevel),
		File:   config.LogFile,
	})

	app := &App{}
	app.Initialize(*connStr, config)
	sqlDB, err := app.Store.DB.DB()
	if err != nil {
		log.Fatalf("Failed to get generic database object: %v", err)
	}
	defer sqlDB.Close()

	app.Run(*address)

}
