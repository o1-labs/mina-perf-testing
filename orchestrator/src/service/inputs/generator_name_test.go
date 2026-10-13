package inputs

import (
	lib "itn_orchestrator"
	"strings"
	"testing"
)

func TestValidateExperimentNameLength(t *testing.T) {
	ok := strings.Repeat("a", lib.MaxExperimentNameLen)
	long := ok + "a"
	for _, c := range []struct {
		name  string
		valid bool
	}{{ok, true}, {long, false}, {"local-baseline-be3795e-7nodes", false}, {"b-1215049-5n", true}} {
		in := GeneratorInputData{ExperimentName: &c.name}
		if got := in.ValidateExperimentName(nil); got != c.valid {
			t.Errorf("%q (len %d): valid=%v, want %v", c.name, len(c.name), got, c.valid)
		}
	}
	// "<name>-<round>-<batch>" with two-digit round and batch fits a memo.
	if n := len(ok + "-99-99"); n > 32 {
		t.Errorf("memo of a maximal name is %d characters", n)
	}
}
