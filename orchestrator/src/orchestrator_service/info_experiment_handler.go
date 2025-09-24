package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
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
	Setup  interface{} `json:"setup"`
	Rounds []Round     `json:"rounds"`
}

// Round represents a single round in the experiment
type Round struct {
	No           int     `json:"no"`
	PaymentsRate float64 `json:"payments_rate"`
	ZkappRate    float64 `json:"zkapp_rate"`
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

	var errors []string
	var rounds []Round
	var result strings.Builder

	if err := lib.EncodeToWriter(&p, &result); err != nil {
		return nil, fmt.Errorf("encoding errors: %v", err)
	}

	setup_json, err := p.ToJSON()
	if err != nil {
		return nil, fmt.Errorf("error converting to JSON: %v", err)
	}

	// Parse rounds information from the generated output
	for _, line := range strings.Split(result.String(), "\n") {
		re := regexp.MustCompile(`Starting round (\d), .*`)
		if re.MatchString(line) {
			m := re.FindStringSubmatch(line)
			if len(m) == 2 {
				roundNo, err := strconv.Atoi(m[1])
				if err != nil {
					errors = append(errors, fmt.Sprintf("Error parsing round number: %v", err))
					continue
				}
				rounds = append(rounds, Round{
					No: roundNo,
				})
			}
		}

		re = regexp.MustCompile(`\b\d+\s+(zkapp|payments)\b.*?\(([\d.]+)\s*txs\/min\)`)
		if re.MatchString(line) {
			m := re.FindStringSubmatch(line)
			if len(m) == 3 && len(rounds) > 0 {
				round := &rounds[len(rounds)-1]
				rate, err := strconv.ParseFloat(m[2], 64)
				if err != nil {
					errors = append(errors, fmt.Sprintf("Error parsing rate: %v", err))
					continue
				}
				switch m[1] {
				case "zkapp":
					round.ZkappRate = rate
				case "payments":
					round.PaymentsRate = rate
				}
			}
		}
	}

	if len(errors) > 0 {
		return nil, fmt.Errorf("parsing errors: %v", errors)
	}

	return &InfoExperimentResponse{
		Setup:  setup_json,
		Rounds: rounds,
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
	if !h.Store.CheckExperimentIsUnique(*experimentSetup.ExperimentName) {
		writeResponse(w, http.StatusBadRequest, APIResponse{
			Errors: []string{"experiment with the same name already exists"},
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

	writeJSONResponse(w, struct{ Result interface{} }{Result: response})
}
