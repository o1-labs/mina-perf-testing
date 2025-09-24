package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	logging "github.com/ipfs/go-log/v2"

	service "itn_orchestrator/service"

	service_inputs "itn_orchestrator/service/inputs"

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

	a.Router.HandleFunc("/api/v0/experiment/run", a.createExperimentHandler).Methods(http.MethodPost)
	a.Router.HandleFunc("/api/v0/experiment/test", a.infoExperimentHandler).Methods(http.MethodPost)
	a.Router.HandleFunc("/api/v0/experiment/status", a.statusHandler).Methods(http.MethodGet)
	a.Router.HandleFunc("/api/v0/experiment/cancel", a.cancelHandler()).Methods(http.MethodPost)

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

func (a *App) infoExperimentHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	experimentSetup, err := a.GetExperimentSetup(*r)

	if err != nil {
		Error([]string{err.Error()}, w)
		return
	}

	var p lib.GenParams
	experimentSetup.ApplyWithDefaults(&p)

	validationErrors := lib.ValidateAndCollectErrors(&p)

	if len(validationErrors) > 0 {
		ValidationError(validationErrors, w)
		return
	}

	var errors []string

	type Round struct {
		No           int
		PaymentsRate float64
		ZkappRate    float64
	}

	var rounds []Round
	var result strings.Builder

	encoder := json.NewEncoder(&result)
	writeComment := func(comment string) {
		if err := encoder.Encode(comment); err != nil {
			errors = append(errors, fmt.Sprintf("Error writing comment: %v", err))
		}
	}
	writeCommand := func(cmd lib.GeneratedCommand) {
		comment := cmd.Comment()
		if comment != "" {
			writeComment(comment)
		}
		if err := encoder.Encode(cmd); err != nil {
			errors = append(errors, fmt.Sprintf("Error writing command: %v", err))
		}
	}

	if len(errors) > 0 {
		Error(errors, w)
		return
	}

	lib.Encode(&p, writeCommand, writeComment)

	setup_json, err := p.ToJSON()
	if err != nil {
		Error([]string{fmt.Sprintf("Error converting to JSON: %v", err)}, w)
		return
	}

	for _, line := range strings.Split(result.String(), "\n") {

		re := regexp.MustCompile(`Starting round (\d), .*`)

		if re.MatchString(line) {
			m := re.FindStringSubmatch(line)
			if len(m) == 2 {
				rounds = append(rounds, Round{
					No: func() int {
						no, err := strconv.Atoi(m[1])
						if err != nil {
							errors = append(errors, fmt.Sprintf("Error parsing round number: %v", err))
							return 0
						}
						return no
					}(),
				})
			}
		}

		re = regexp.MustCompile(`\b\d+\s+(zkapp|payments)\b.*?\(([\d.]+)\s*txs\/min\)`)

		if re.MatchString(line) {
			m := re.FindStringSubmatch(line)
			if len(m) == 3 {
				round := &rounds[len(rounds)-1]
				rate, err := strconv.ParseFloat(m[2], 64)
				if err != nil {
					errors = append(errors, fmt.Sprintf("Error parsing rate: %v", err))
					return
				}
				switch m[1] {
				case "zkapp":
					round.ZkappRate = rate
				case "payments":
					round.PaymentsRate = rate
				}
			}
		}
	}

	if len(errors) > 0 {
		Error(errors, w)
		return
	}

	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(
		map[string]interface{}{
			"setup":  setup_json,
			"rounds": rounds,
		},
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

// statusHandler returns the current job's status
func (a *App) statusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	job := a.Store.AtomicGet()
	if job == nil {
		http.Error(w, "No experiment running", http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(job)
}

// cancelHandler stops a running job
func (a *App) cancelHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := a.Store.Cancel(); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"result": "canceled"})
	}
}

type APIResponse struct {
	Errors           []string `json:"errors,omitempty"`
	ValidationErrors []string `json:"validation_errors,omitempty"`
	Result           string   `json:"result,omitempty"`
}

func ValidationError(validationErrors []string, w http.ResponseWriter) {
	w.WriteHeader(http.StatusBadRequest)
	if err := json.NewEncoder(w).Encode(
		APIResponse{
			Errors:           []string{},
			ValidationErrors: validationErrors,
			Result:           "validation_failed",
		},
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func Error(errors []string, w http.ResponseWriter) {
	w.WriteHeader(http.StatusBadRequest)
	if err := json.NewEncoder(w).Encode(
		APIResponse{
			Errors:           errors,
			ValidationErrors: []string{},
			Result:           "error",
		},
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func Success(w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(
		APIResponse{
			Errors:           []string{},
			ValidationErrors: []string{},
			Result:           "success",
		},
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (a *App) GetExperimentSetup(r http.Request) (*service_inputs.GeneratorInputData, error) {
	var experimentSetup service_inputs.GeneratorInputData
	if err := json.NewDecoder(r.Body).Decode(&experimentSetup); err != nil {
		return nil, fmt.Errorf("Failed to decode request body: %v", err)
	}

	if !a.Store.CheckExperimentIsUnique(*experimentSetup.ExperimentName) {
		return nil, fmt.Errorf("Experiment with the same name already exists")
	}
	return &experimentSetup, nil
}

func (a *App) createExperimentHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	experimentSetup, err := a.GetExperimentSetup(*r)

	if err != nil {
		Error([]string{err.Error()}, w)
		return
	}

	var p lib.GenParams
	experimentSetup.ApplyWithDefaults(&p)

	validationErrors := lib.ValidateAndCollectErrors(&p)

	if len(validationErrors) > 0 {
		ValidationError(validationErrors, w)
		return
	}

	var errors []string
	var result strings.Builder

	encoder := json.NewEncoder(&result)
	writeComment := func(comment string) {
		if err := encoder.Encode(comment); err != nil {
			errors = append(errors, fmt.Sprintf("Error writing comment: %v", err))
		}
	}
	writeCommand := func(cmd lib.GeneratedCommand) {
		comment := cmd.Comment()
		if comment != "" {
			writeComment(comment)
		}
		if err := encoder.Encode(cmd); err != nil {
			errors = append(errors, fmt.Sprintf("Error writing command: %v", err))
		}
	}

	if len(errors) > 0 {
		Error(errors, w)
		return
	}

	lib.Encode(&p, writeCommand, writeComment)

	setup_json, err := p.ToJSON()
	if err != nil {
		Error([]string{fmt.Sprintf("Error converting to JSON: %v", err)}, w)
		return
	}

	job := &service.ExperimentState{Name: *experimentSetup.ExperimentName, Status: "running", CreatedAt: time.Now(),
		SetupJSON: setup_json,
	}

	ctx, cancel := context.WithCancel(context.Background())

	orchestratorConfig := *a.Config

	log := service.StoreLogging{Store: a.Store, Log: logging.Logger("orchestrator")}
	config := lib.SetupConfig(ctx, orchestratorConfig, log)

	if err := a.Store.Add(job, cancel); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	if err := a.Store.WriteExperimentToDB(*job); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	decoder := json.NewDecoder(strings.NewReader(result.String()))

	println("Starting experiment with setup: ", result.String())

	go a.loadRun(decoder, config, log)

	Success(w)
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
