package itn_orchestrator

import (
	"math"
	"strings"
	"testing"
)

// TestValidationRejectsNaNRatios pins that a ratio which is not a number is a
// validation error.
//
// Every float comparison against NaN is false, so both the range check and the
// min-versus-max check passed it through: `-stop-min-ratio NaN` exited 0 and
// produced a plan whose rounds carry no stop-daemon command at all, because
// each `> 1e-6` guard in Generate is false for NaN as well. That is the silent
// zero-stop outcome the clamp work set out to remove, reached by a different
// route.
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
			p := DefaultGenParams()
			p.ExperimentName = "nan-probe"
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

// TestValidationRejectsInvertedStopRatios keeps the ordered-pair check, which
// is what stops a negative draw reaching Generate.
func TestValidationRejectsInvertedStopRatios(t *testing.T) {
	p := DefaultGenParams()
	p.ExperimentName = "inverted-probe"
	p.MinStopRatio = 1.0
	p.MaxStopRatio = 0.0

	errs := ValidateAndCollectErrors(&p)
	if len(errs) == 0 {
		t.Fatal("an inverted stop-ratio pair was accepted")
	}
}
