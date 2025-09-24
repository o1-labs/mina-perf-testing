package main

import (
	"net/http"

	service "itn_orchestrator/service"
)

// CancelResponse represents the response for cancel endpoint
type CancelResponse struct {
	Result string `json:"result"`
}

// CancelHandler handles experiment cancellation requests
type CancelHandler struct {
	Store *service.Store
}

// Handle processes the cancel request with well-typed input/output
// This function cancels the currently running experiment by stopping its execution
// and updating its status to cancelled.
func (h *CancelHandler) Handle() (*CancelResponse, error) {
	if err := h.Store.Cancel(); err != nil {
		return nil, err
	}
	return &CancelResponse{Result: "canceled"}, nil
}

// ServeHTTP implements the http.Handler interface
func (h *CancelHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	response, err := h.Handle()
	if err != nil {
		writeErrorResponse(w, http.StatusConflict, []string{err.Error()})
		return
	}

	writeJSONResponse(w, http.StatusOK, response)
}
