package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestParseExperimentSetup pins the request-body contract. Both the flat body
// and the older {"experiment_setup": {...}} envelope must work, and a body
// carrying neither must produce an error rather than a nil ExperimentName that
// the handlers then dereference.
func TestParseExperimentSetup(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		wantName string // empty means "expect an error"
		wantErr  string
	}{
		{
			name:     "flat body",
			body:     `{"experiment_name":"exp-1","rounds":2}`,
			wantName: "exp-1",
		},
		{
			name:     "enveloped body (legacy callers)",
			body:     `{"experiment_setup":{"experiment_name":"exp-1","rounds":2}}`,
			wantName: "exp-1",
		},
		{
			name:     "envelope wins over flat when both are present",
			body:     `{"experiment_name":"flat","experiment_setup":{"experiment_name":"enveloped"}}`,
			wantName: "enveloped",
		},
		{
			name:    "empty object",
			body:    `{}`,
			wantErr: "experiment_name is required",
		},
		{
			name:    "envelope without a name",
			body:    `{"experiment_setup":{"rounds":2}}`,
			wantErr: "experiment_name is required",
		},
		{
			name:    "explicitly empty name",
			body:    `{"experiment_name":""}`,
			wantErr: "experiment_name is required",
		},
		{
			name:    "malformed json",
			body:    `{"experiment_name":`,
			wantErr: "failed to decode request body",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/v0/experiment/run", strings.NewReader(tc.body))
			setup, err := parseExperimentSetup(r)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got setup %+v", tc.wantErr, setup)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// The handlers dereference this without a guard, so a successful
			// parse must never leave it nil.
			if setup.ExperimentName == nil {
				t.Fatal("ExperimentName is nil after a successful parse")
			}
			if *setup.ExperimentName != tc.wantName {
				t.Fatalf("ExperimentName = %q, want %q", *setup.ExperimentName, tc.wantName)
			}
		})
	}
}

// TestParseExperimentSetupCarriesFields guards against the envelope and the
// flat form disagreeing about anything but the name.
func TestParseExperimentSetupCarriesFields(t *testing.T) {
	for _, body := range []string{
		`{"experiment_name":"exp-1","rounds":7,"zkapp_ratio":0.25}`,
		`{"experiment_setup":{"experiment_name":"exp-1","rounds":7,"zkapp_ratio":0.25}}`,
	} {
		r := httptest.NewRequest("POST", "/x", strings.NewReader(body))
		setup, err := parseExperimentSetup(r)
		if err != nil {
			t.Fatalf("body %s: %v", body, err)
		}
		if setup.Rounds == nil || *setup.Rounds != 7 {
			t.Errorf("body %s: Rounds = %v, want 7", body, setup.Rounds)
		}
		if setup.ZkappRatio == nil || *setup.ZkappRatio != 0.25 {
			t.Errorf("body %s: ZkappRatio = %v, want 0.25", body, setup.ZkappRatio)
		}
	}
}

// TestIsCancellation covers the branch that decides whether a finished
// experiment is reported as "cancelled" or as "error".
func TestIsCancellation(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"context canceled", context.Canceled, true},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"wrapped cancel", fmt.Errorf("scheduling batch 3: %w", context.Canceled), true},
		{"ordinary failure", fmt.Errorf("daemon refused the mutation"), false},
		{
			// A message that merely mentions cancellation is not one.
			"message only mentions cancellation",
			fmt.Errorf("experiment failed: context canceled"),
			false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCancellation(tc.err); got != tc.want {
				t.Fatalf("isCancellation(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
