package main

import (
	"fmt"
	"net/http"
	"strings"

	lib "itn_orchestrator"
	service "itn_orchestrator/service"
	service_inputs "itn_orchestrator/service/inputs"
)

// InfoExperimentHandler handles experiment info requests
type InfoExperimentHandler struct {
	Store *service.Store
}

// InfoExperimentResponse represents the response for experiment info endpoint
type InfoExperimentResponse struct {
	Setup  lib.GenParams   `json:"setup"`
	Rounds []lib.RoundInfo `json:"rounds"`
	Script string          `json:"script"`
}

// Handle processes the experiment info request with well-typed input/output
// This function validates the experiment setup parameters and returns detailed information
// about the experiment configuration including setup JSON and round information.
func (h *InfoExperimentHandler) Handle(setup *service_inputs.GeneratorInputData) (*InfoExperimentResponse, error) {
	var p lib.GenParams
	setup.ApplyWithDefaults(&p)

	validationErrors := lib.ValidateAndCollectErrors(&p)
	if len(validationErrors) > 0 {
		return nil, fmt.Errorf("validation failed: %v", validationErrors)
	}

	// Get experiment info directly from EncodeToWriter
	var result strings.Builder
	experimentInfo, err := lib.EncodeToWriter(&p, &result, setup)
	if err != nil {
		return nil, fmt.Errorf("encoding errors: %v", err)
	}

	return &InfoExperimentResponse{
		Setup:  p,
		Rounds: experimentInfo,
		Script: result.String(),
	}, nil
}

// ServeHTTP implements the http.Handler interface
func (h *InfoExperimentHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	experimentSetup, err := parseExperimentSetup(r)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, APIResponse{
			Errors: []string{err.Error()},
			Result: "error",
		})
		return
	}

	if expName := *experimentSetup.ExperimentName; !h.Store.NameIsUnique(expName) {
		writeResponse(w, http.StatusBadRequest, APIResponse{
			Errors: []string{
				fmt.Sprintf("experiment with name %s already exists", expName),
			},
			Result: "error",
		})
		return
	}

	response, err := h.Handle(experimentSetup)
	if err != nil {
		// Check if it's a validation error
		if strings.Contains(err.Error(), "validation failed") {
			// Extract validation errors from the error message
			errorMsg := err.Error()
			if strings.Contains(errorMsg, "validation failed: ") {
				validationErrorsStr := strings.TrimPrefix(errorMsg, "validation failed: ")
				writeResponse(w, http.StatusBadRequest, APIResponse{
					Errors: []string{validationErrorsStr},
					Result: "invalid",
				})
				return
			}
		}
		writeResponse(w, http.StatusBadRequest, APIResponse{
			Errors: []string{err.Error()},
			Result: "error",
		})
		return
	}

	writeJSONResponse(w, response)
}
