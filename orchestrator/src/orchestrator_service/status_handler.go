package main

import (
	"fmt"
	"net/http"

	service "itn_orchestrator/service"
)

// StatusHandler handles experiment status requests
type StatusHandler struct {
	Store *service.Store
}

// Handle processes the status request with well-typed input/output
// This function returns the current status of the running experiment,
// including all experiment details, current step, logs, errors, and warnings.
func (h *StatusHandler) Handle() (*service.ExperimentState, error) {
	job := h.Store.AtomicGet()
	if job == nil {
		return nil, fmt.Errorf("no experiment running")
	}
	return job, nil
}

// ServeHTTP implements the http.Handler interface
func (h *StatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	job, err := h.Handle()
	if err != nil {
		writeErrorResponse(w, http.StatusNotFound, []string{err.Error()})
		return
	}
	writeJSONResponse(w, http.StatusOK, job)
}
