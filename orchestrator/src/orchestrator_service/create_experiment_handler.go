package main

import (
	"context"
	"encoding/json"
	"errors"
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

	// Add both claims the experiment slot and persists the experiment, and it
	// releases the claim if the write fails. A held slot is the caller's
	// problem (409); a failed write is ours (500). These were previously two
	// steps, and a write that failed between them left the slot held forever.
	if err := h.Store.Add(job, cancel); err != nil {
		cancel()
		if errors.Is(err, service.ErrExperimentRunning) {
			return http.StatusConflict, []string{err.Error()}, nil
		}
		return http.StatusInternalServerError, []string{err.Error()}, nil
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
	// experiment_name is dereferenced from here on. Omitting it used to panic
	// on the nil pointer, which killed the connection and reached the caller as
	// a 502 rather than as the bad request it is.
	if experimentSetup.ExperimentName == nil || *experimentSetup.ExperimentName == "" {
		writeResponse(w, http.StatusBadRequest, APIResponse{
			Errors: []string{"experiment_name is required"},
			Result: "invalid",
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
