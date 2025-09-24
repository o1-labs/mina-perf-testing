package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	logging "github.com/ipfs/go-log/v2"

	lib "itn_orchestrator"
	service "itn_orchestrator/service"
	service_inputs "itn_orchestrator/service/inputs"
)

// CreateExperimentHandler handles experiment creation requests
type CreateExperimentHandler struct {
	Store  *service.Store
	Config *lib.OrchestratorConfig
	App    *App
}

// Handle processes the create experiment request with well-typed input/output
// This function creates a new experiment based on the provided setup parameters,
// validates the input, generates the experiment configuration, and starts the experiment execution.
// Returns (statusCode, errors) where statusCode indicates the type of response.
func (h *CreateExperimentHandler) Handle(setup *service_inputs.GeneratorInputData) (int, []string, string) {
	var p lib.GenParams
	setup.ApplyWithDefaults(&p)

	validationErrors := lib.ValidateAndCollectErrors(&p)
	if len(validationErrors) > 0 {
		return http.StatusBadRequest, validationErrors, ""
	}

	var experimentScript string
	{
		var result strings.Builder
		if err := lib.EncodeToWriter(&p, &result); err != nil {
			return http.StatusInternalServerError, []string{err.Error()}, ""
		}
		experimentScript = result.String()
	}

	setup_json, err := p.ToJSON()
	if err != nil {
		return http.StatusInternalServerError, []string{fmt.Sprintf("Error converting to JSON: %v", err)}, ""
	}

	job := &service.ExperimentState{
		Name:      *setup.ExperimentName,
		Status:    "running",
		CreatedAt: time.Now(),
		SetupJSON: setup_json,
	}

	ctx, cancel := context.WithCancel(context.Background())

	orchestratorConfig := *h.Config
	log := service.StoreLogging{Store: h.Store, Log: logging.Logger("orchestrator")}
	config := lib.SetupConfig(ctx, orchestratorConfig, log)

	if err := h.Store.Add(job, cancel); err != nil {
		return http.StatusConflict, []string{fmt.Sprintf("failed to add experiment: %v", err)}, ""
	}

	if err := h.Store.WriteExperimentToDB(*job); err != nil {
		return http.StatusInternalServerError, []string{fmt.Sprintf("failed to write experiment to database: %v", err)}, ""
	}

	decoder := json.NewDecoder(strings.NewReader(experimentScript))
	go h.App.loadRun(decoder, config, log)

	return http.StatusOK, []string{}, experimentScript
}

// ServeHTTP implements the http.Handler interface
func (h *CreateExperimentHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	experimentSetup, err := parseExperimentSetup(r)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, APIResponse{
			Errors: []string{err.Error()},
			Result: "error",
		})
		return
	}
	if !h.Store.CheckExperimentIsUnique(*experimentSetup.ExperimentName) {
		writeResponse(w, http.StatusBadRequest, APIResponse{
			Errors: []string{"experiment with the same name already exists"},
			Result: "error",
		})
		return
	}

	statusCode, errors, experimentScript := h.Handle(experimentSetup)

	// Determine result based on status code
	var result string
	switch statusCode {
	case http.StatusOK:
		result = experimentScript
	case http.StatusBadRequest:
		// Check if it's validation errors (from Handle method)
		if len(errors) > 0 {
			result = "invalid"
		} else {
			result = "error"
		}
	default:
		result = "error"
	}

	writeResponse(w, statusCode, APIResponse{
		Errors: errors,
		Result: result,
	})
}
