package main

import (
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
	Router *mux.Router
	Store  *service.Store
	Config *lib.OrchestratorConfig
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
	a.Store = &service.Store{DB: db}
	a.Config = &config
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

	outCache := lib.EmptyOutputCache()
	rconfig := lib.ResolutionConfig{
		OutputCache: outCache,
	}
	step := 0
	var prevAction lib.BatchAction
	var actionAccum []lib.ActionIO
	handlePrevAction := func() error {
		var start int
		if step-len(actionAccum) > 0 {
			start = step - len(actionAccum)
		} else {
			start = 0
		}
		var end int
		if step-1 > 0 {
			end = step - 1
		} else {
			end = 0
		}
		log.Infof("Performing steps %s (%d-%d)", prevAction.Name(), start, end)
		err := prevAction.RunMany(config, actionAccum)
		if err != nil {
			return &lib.OrchestratorError{
				Message: fmt.Sprintf("Error running steps %d-%d: %v", start, end, err),
				Code:    9,
			}
		}
		prevAction = nil
		actionAccum = nil
		return nil
	}
	err := lib.RunActions(inDecoder, config, outCache, log, step,
		handlePrevAction, &actionAccum, rconfig, &prevAction)
	if err != nil {
		if err, ok := err.(*lib.OrchestratorError); ok {
			log.Errorf("Experiment finished with error: %v", err)
			a.Store.FinishWithError(err)
			return
		}
	}

	if prevAction != nil {
		if err := handlePrevAction(); err != nil {
			log.Errorf("Error running action: %s due to: %v", prevAction.Name(), err)
			// If context is canceled, we don't want to finish with error
			// because it means the user canceled the experiment
			if config.Ctx.Err() == nil {
				a.Store.FinishWithError(&lib.OrchestratorError{
					Message: fmt.Sprintf("Error running previous action: %v", err),
					Code:    9,
				})
			}
			return

		}
	}
	a.Store.FinishWithSuccess()
	return
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
