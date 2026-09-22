package itn_orchestrator

import (
	"math"
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

// TestValidationRejectsInvertedStopRatios keeps the ordered-pair check, which
// is what stops a negative draw reaching Generate.
func TestValidationRejectsInvertedStopRatios(t *testing.T) {
	p := validParams(t)
	p.MinStopRatio = 1.0
	p.MaxStopRatio = 0.0

	errs := ValidateAndCollectErrors(&p)
	if len(errs) == 0 {
		t.Fatal("an inverted stop-ratio pair was accepted")
	}
}

// TestValidationRejectsNaNRatios pins that a ratio which is not a number is a
// validation error.
//
// Every float comparison against NaN is false, so both the range check and the
// ordered-pair check passed it through: `-stop-min-ratio NaN` exited 0 and
// produced a plan whose rounds carry no stop-daemon command at all, because
// each `> 1e-6` guard in Generate is false for NaN as well. That is the silent
// zero-stop outcome the clamp work set out to remove, reached by another route.
func TestValidationRejectsNaNRatios(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(p *GenParams)
		want  string
	}{
		{"min stop ratio", func(p *GenParams) { p.MinStopRatio = math.NaN() }, "min stop ratio"},
		{"max stop ratio", func(p *GenParams) { p.MaxStopRatio = math.NaN() }, "max stop ratio"},
		{"zkapp ratio", func(p *GenParams) { p.ZkappRatio = math.NaN() }, "zkapp ratio"},
		{"stop clean ratio", func(p *GenParams) { p.StopCleanRatio = math.NaN() }, "stop-clean ratio"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := validParams(t)
			tc.apply(&p)

			errs := ValidateAndCollectErrors(&p)
			if len(errs) == 0 {
				t.Fatalf("NaN %s was accepted; the generated plan then performs no node stops and still reports success", tc.name)
			}
			joined := strings.ToLower(strings.Join(errs, "; "))
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("errors = %v, want one naming %q", errs, tc.want)
			}
		})
	}
}
