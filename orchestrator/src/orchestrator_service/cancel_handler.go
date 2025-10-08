package main

import (
	"net/http"

	service "itn_orchestrator/service"
)

// CancelHandler handles experiment cancellation requests
type CancelHandler struct {
	Store *service.Store
}

// Handle processes the cancel request with well-typed input/output
// This function cancels the currently running experiment by stopping its execution
// and updating its status to cancelled.
func (h *CancelHandler) Handle() error {
	if err := h.Store.Cancel(); err != nil {
		return err
	}
	return nil
}

// ServeHTTP implements the http.Handler interface
func (h *CancelHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h.Handle(); err != nil {
		writeResponse(w, http.StatusConflict, APIResponse{
			Errors: []string{err.Error()},
			Result: "error",
		})
		return
	}
	writeResponse(w, http.StatusOK, APIResponse{
		Result: "canceled",
	})
}
