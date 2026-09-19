package itn_orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validParams returns a parameter set that passes every validation step, so
// each case below can turn on exactly one thing and attribute the result. One
// step stats the privkey files, so they must exist on disk.
func validParams(t *testing.T) GenParams {
	t.Helper()
	privkey := filepath.Join(t.TempDir(), "key0")
	if err := os.WriteFile(privkey, []byte("dummy"), 0600); err != nil {
		t.Fatalf("writing dummy privkey: %v", err)
	}
	p := DefaultGenParams()
	p.Privkeys = []string{privkey}
	p.ZkappRatio = 0.5
	p.MaxCostMixedTpsRatio = 0
	p.MaxCost = false
	p.NonDefaultToken = false
	return p
}

func TestValidateAndCollectErrors(t *testing.T) {
	const (
		errMaxCost    = "non-default-token has no effect with max-cost"
		errZkappRatio = "non-default-token requires a non-zero zkapp ratio"
	)

	for _, tc := range []struct {
		name string
		// mutate applies the single deviation under test.
		mutate func(p *GenParams)
		// wantErrSubstr is empty when the parameters must be accepted.
		wantErrSubstr string
	}{
		{
			name:   "baseline is valid",
			mutate: func(p *GenParams) {},
		},
		{
			name:   "non-default-token alone is valid",
			mutate: func(p *GenParams) { p.NonDefaultToken = true },
		},
		{
			name: "non-default-token with max-cost is rejected",
			mutate: func(p *GenParams) {
				p.NonDefaultToken = true
				p.MaxCost = true
			},
			wantErrSubstr: errMaxCost,
		},
		{
			name: "non-default-token with zero zkapp ratio is rejected",
			mutate: func(p *GenParams) {
				p.NonDefaultToken = true
				p.ZkappRatio = 0
			},
			wantErrSubstr: errZkappRatio,
		},
		{
			// Generate() computes max-cost per round, so the even rounds still
			// run ordinary zkApp load in the custom token. This combination is
			// deliberately allowed.
			name: "non-default-token with max-cost-mixed is valid",
			mutate: func(p *GenParams) {
				p.NonDefaultToken = true
				p.MaxCostMixedTpsRatio = 0.5
			},
		},
		{
			name:   "max-cost alone is valid",
			mutate: func(p *GenParams) { p.MaxCost = true },
		},
		{
			name:   "zero zkapp ratio without non-default-token is valid",
			mutate: func(p *GenParams) { p.ZkappRatio = 0 },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := validParams(t)
			tc.mutate(&p)
			errs := ValidateAndCollectErrors(&p)

			if tc.wantErrSubstr == "" {
				if len(errs) != 0 {
					t.Fatalf("want no validation errors, got %v", errs)
				}
				return
			}
			for _, e := range errs {
				if strings.Contains(e, tc.wantErrSubstr) {
					return
				}
			}
			t.Fatalf("want an error containing %q, got %v", tc.wantErrSubstr, errs)
		})
	}
}

// TestValidationStepsExitCodes guards the contract ValidateAndExitEarly relies
// on: every step must carry a non-zero exit code, or a rejected experiment
// would exit 0 and look successful.
func TestValidationStepsExitCodes(t *testing.T) {
	p := validParams(t)
	for i, step := range ValidationSteps(&p) {
		if step.ExitCode == 0 {
			t.Errorf("step %d (%q) has exit code 0", i, step.ErrorMsg)
		}
		if step.Check == nil {
			t.Errorf("step %d (%q) has no Check", i, step.ErrorMsg)
		}
	}
}
