package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	service "itn_orchestrator/service"
	service_inputs "itn_orchestrator/service/inputs"
)

// APIResponse represents the standard API response format
type APIResponse struct {
	Errors           []string `json:"errors,omitempty"`
	ValidationErrors []string `json:"validation_errors,omitempty"`
	Result           string   `json:"result,omitempty"`
}

// InfoExperimentResponse represents the response for experiment info endpoint
type InfoExperimentResponse struct {
	Setup  interface{} `json:"setup"`
	Rounds []Round     `json:"rounds"`
}

// Round represents a single round in the experiment
type Round struct {
	No           int     `json:"no"`
	PaymentsRate float64 `json:"payments_rate"`
	ZkappRate    float64 `json:"zkapp_rate"`
}

// StatusResponse represents the response for status endpoint
type StatusResponse struct {
	*service.ExperimentState
}

// writeErrorResponse writes an error response with the given status code
func writeErrorResponse(w http.ResponseWriter, statusCode int, errors []string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
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

// writeErrorResponseWithStatus writes an error response with default bad request status
func writeErrorResponseWithStatus(w http.ResponseWriter, errors []string) {
	writeErrorResponse(w, http.StatusBadRequest, errors)
}

// writeValidationErrorResponse writes a validation error response
func writeValidationErrorResponse(w http.ResponseWriter, validationErrors []string) {
	w.Header().Set("Content-Type", "application/json")
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

// writeSuccessResponse writes a success response
func writeSuccessResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
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

// writeJSONResponse writes a JSON response with the given data
func writeJSONResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// validateContentLength validates the request content length
func validateContentLength(r *http.Request, maxSize int64) error {
	if r.ContentLength > maxSize {
		return fmt.Errorf("request body too large: %d bytes (max %d)", r.ContentLength, maxSize)
	}
	return nil
}

// parseExperimentSetup parses the experiment setup from request body
func parseExperimentSetup(r *http.Request, store *service.Store) (*service_inputs.GeneratorInputData, error) {
	// Limit request body size to prevent abuse
	const maxRequestSize = 1024 * 1024 // 1MB
	if err := validateContentLength(r, maxRequestSize); err != nil {
		return nil, err
	}

	var experimentSetup service_inputs.GeneratorInputData
	limitedReader := io.LimitReader(r.Body, maxRequestSize)
	if err := json.NewDecoder(limitedReader).Decode(&experimentSetup); err != nil {
		return nil, fmt.Errorf("failed to decode request body: %v", err)
	}

	if !store.CheckExperimentIsUnique(*experimentSetup.ExperimentName) {
		return nil, fmt.Errorf("experiment with the same name already exists")
	}

	return &experimentSetup, nil
}

// Legacy API response functions for backward compatibility
func ValidationError(validationErrors []string, w http.ResponseWriter) {
	writeValidationErrorResponse(w, validationErrors)
}

func Error(errors []string, w http.ResponseWriter) {
	writeErrorResponseWithStatus(w, errors)
}

func Success(w http.ResponseWriter) {
	writeSuccessResponse(w)
}
