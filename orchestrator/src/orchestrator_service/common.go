package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	service_inputs "itn_orchestrator/service/inputs"
)

// APIResponse represents the standard API response format
type APIResponse struct {
	Errors []string `json:"errors,omitempty"`
	Result string   `json:"result,omitempty"`
}

// writeResponse writes a unified response with the given status code and APIResponse
func writeResponse(w http.ResponseWriter, statusCode int, resp APIResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// writeJSONResponse writes a JSON response with the given data (for non-APIResponse data)
func writeJSONResponse(w http.ResponseWriter, data struct{ Result interface{} }) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
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
func parseExperimentSetup(r *http.Request) (*service_inputs.GeneratorInputData, error) {
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

	return &experimentSetup, nil
}

// Legacy API response functions for backward compatibility
func ValidationError(validationErrors []string, w http.ResponseWriter) {
	writeResponse(w, http.StatusBadRequest, APIResponse{
		Errors: validationErrors,
		Result: "invalid",
	})
}

func Error(errors []string, w http.ResponseWriter) {
	writeResponse(w, http.StatusBadRequest, APIResponse{
		Errors: errors,
		Result: "error",
	})
}

func Success(w http.ResponseWriter) {
	writeResponse(w, http.StatusOK, APIResponse{
		Result: "success",
	})
}
