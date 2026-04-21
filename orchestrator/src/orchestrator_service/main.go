package main

import (
	"context"
	"encoding/json"
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

// Initialize opens the DB and sets up routes
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
	a.Store = service.NewStore(db)
	a.Config = &config
	a.WebhookNotifier = NewWebhookNotifier(logging.Logger("webhook"))
	a.initializeRoutes()
}

func (a *App) Run(address string) {

	log.Println("Starting orchestrator service...")

	log.Printf("Starting server on %s", address)
	if err := http.ListenAndServe(address, a.Router); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func (a *App) loadRun(inDecoder *json.Decoder, config lib.Config, log logging.StandardLogger) {
	// Get and set the Mina executable path from deployment metadata
	minaExecPath, err := getMinaExecutablePath(a.Store.DB, log)
	if err != nil {
		// Log the error and add to warnings, but don't fail the experiment
		warningMsg := fmt.Sprintf("Failed to extract Mina executable from deployment metadata: %v. Using existing MinaExec from config.", err)
		log.Warnf(warningMsg)
		a.Store.AppendWarningF(warningMsg)
	} else {
		// Update config with the extracted Mina executable path
		config.MinaExec = minaExecPath
		log.Infof("Using extracted Mina executable: %s", minaExecPath)
	}
	
	if err := lib.RunExperiment(inDecoder, config, log); err != nil {
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
