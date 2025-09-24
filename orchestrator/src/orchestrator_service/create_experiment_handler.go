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
func (h *CreateExperimentHandler) Handle(setup *service_inputs.GeneratorInputData) (*APIResponse, error) {
	var p lib.GenParams
	setup.ApplyWithDefaults(&p)

	validationErrors := lib.ValidateAndCollectErrors(&p)
	if len(validationErrors) > 0 {
		return &APIResponse{
			Errors:           []string{},
			ValidationErrors: validationErrors,
			Result:           "validation_failed",
		}, nil
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
		return &APIResponse{
			Errors: errors,
			Result: "error",
		}, nil
	}

	lib.Encode(&p, writeCommand, writeComment)

	setup_json, err := p.ToJSON()
	if err != nil {
		return &APIResponse{
			Errors: []string{fmt.Sprintf("Error converting to JSON: %v", err)},
			Result: "error",
		}, nil
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
		return nil, fmt.Errorf("failed to add experiment: %v", err)
	}

	if err := h.Store.WriteExperimentToDB(*job); err != nil {
		return nil, fmt.Errorf("failed to write experiment to database: %v", err)
	}

	decoder := json.NewDecoder(strings.NewReader(result.String()))
	go h.App.loadRun(decoder, config, log)

	return &APIResponse{
		Errors:           []string{},
		ValidationErrors: []string{},
		Result:           "success",
	}, nil
}

// ServeHTTP implements the http.Handler interface
func (h *CreateExperimentHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	experimentSetup, err := parseExperimentSetup(r, h.Store)
	if err != nil {
		writeErrorResponseWithStatus(w, []string{err.Error()})
		return
	}

	response, err := h.Handle(experimentSetup)
	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, []string{err.Error()})
		return
	}

	if len(response.ValidationErrors) > 0 {
		writeValidationErrorResponse(w, response.ValidationErrors)
		return
	}

	if len(response.Errors) > 0 {
		writeErrorResponseWithStatus(w, response.Errors)
		return
	}

	writeSuccessResponse(w)
}
