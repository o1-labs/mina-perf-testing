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
func writeJSONResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(struct {
		Result interface{} `json:"result,omitempty"`
	}{Result: data}); err != nil {
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

// experimentRequest accepts both request shapes.
//
// The canonical body is flat:
//
//	{"experiment_name": "exp-1", "rounds": 2, ...}
//
// Callers written against the older API wrap the same object in an
// "experiment_setup" envelope:
//
//	{"experiment_setup": {"experiment_name": "exp-1", "rounds": 2, ...}}
//
// Both are decoded; the envelope wins when it is present and non-empty, so an
// existing caller keeps working unchanged. Before this, the envelope decoded
// into a struct whose fields were all nil and the handlers dereferenced
// ExperimentName, which panicked instead of returning 400.
type experimentRequest struct {
	ExperimentSetup *service_inputs.GeneratorInputData `json:"experiment_setup,omitempty"`
}

// parseExperimentSetup parses the experiment setup from request body.
//
// It guarantees a non-nil ExperimentName on success, so callers may
// dereference it. An absent or empty name is a 400, never a panic.
func parseExperimentSetup(r *http.Request) (*service_inputs.GeneratorInputData, error) {
	// Limit request body size to prevent abuse
	const maxRequestSize = 1024 * 1024 // 1MB
	if err := validateContentLength(r, maxRequestSize); err != nil {
		return nil, err
	}

	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxRequestSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %v", err)
	}

	// The envelope and the flat form are decoded from the same bytes. Unknown
	// keys are ignored by encoding/json, so decoding the flat form out of an
	// enveloped body simply yields an empty struct, and vice versa.
	var enveloped experimentRequest
	if err := json.Unmarshal(body, &enveloped); err != nil {
		return nil, fmt.Errorf("failed to decode request body: %v", err)
	}

	var flat service_inputs.GeneratorInputData
	if err := json.Unmarshal(body, &flat); err != nil {
		return nil, fmt.Errorf("failed to decode request body: %v", err)
	}

	experimentSetup := &flat
	if enveloped.ExperimentSetup != nil {
		experimentSetup = enveloped.ExperimentSetup
	}

	if experimentSetup.ExperimentName == nil || *experimentSetup.ExperimentName == "" {
		return nil, fmt.Errorf("experiment_name is required")
	}

	return experimentSetup, nil
}
