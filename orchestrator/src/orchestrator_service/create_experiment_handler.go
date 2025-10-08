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
func (h *CreateExperimentHandler) Handle(setup *service_inputs.GeneratorInputData) (int, []string, *InfoExperimentResponse) {
	var p lib.GenParams
	setup.ApplyWithDefaults(&p)

	validationErrors := lib.ValidateAndCollectErrors(&p)
	if len(validationErrors) > 0 {
		return http.StatusBadRequest, validationErrors, nil
	}

	var experimentScript string
	var experimentInfo lib.ExperimentInfo
	{
		var result strings.Builder
		var err error
		if experimentInfo, err = lib.EncodeToWriter(&p, &result, setup); err != nil {
			return http.StatusInternalServerError, []string{err.Error()}, nil
		}
		experimentScript = result.String()
	}

	var webhookURL string
	if setup.WebhookURL != nil {
		webhookURL = *setup.WebhookURL
	}

	job := &service.ExperimentState{
		Name:       *setup.ExperimentName,
		Status:     "running",
		CreatedAt:  time.Now(),
		Setup:      p,
		WebhookURL: webhookURL,
	}

	ctx, cancel := context.WithCancel(context.Background())

	orchestratorConfig := *h.Config
	log := service.StoreLogging{Store: h.Store, Log: logging.Logger("orchestrator")}
	config := lib.SetupConfig(ctx, orchestratorConfig, log)

	if err := h.Store.Add(job, cancel); err != nil {
		return http.StatusConflict, []string{fmt.Sprintf("failed to add experiment: %v", err)}, nil
	}

	if err := h.Store.WriteExperimentToDB(*job); err != nil {
		return http.StatusInternalServerError, []string{fmt.Sprintf("failed to write experiment to database: %v", err)}, nil
	}

	decoder := json.NewDecoder(strings.NewReader(experimentScript))
	go h.App.loadRun(decoder, config, log)

	return http.StatusOK, []string{}, &InfoExperimentResponse{
		Setup:  p,
		Rounds: experimentInfo,
		Script: experimentScript,
	}
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
	if !h.Store.NameIsUnique(*experimentSetup.ExperimentName) {
		writeResponse(w, http.StatusBadRequest, APIResponse{
			Errors: []string{"experiment with the same name already exists"},
			Result: "error",
		})
		return
	}

	statusCode, errors, info := h.Handle(experimentSetup)

	// Determine result based on status code
	var result string
	switch statusCode {
	case http.StatusOK:
		{
			writeJSONResponse(w, info)
			return
		}
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
