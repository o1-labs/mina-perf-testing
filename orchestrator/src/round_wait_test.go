package itn_orchestrator

import "testing"

// TestLastRoundWaitsForItsLoad pins that every round, the last one included,
// waits out the load it scheduled.
//
// The remainder wait used to be emitted only when round < Rounds-1, so the
// final round returned as soon as it had scheduled its transactions. In the
// service that means RunExperiment finishes -- and the success webhook fires
// -- up to RoundDurationMin minutes before the load ends, while the round's
// own RoundInfo still reports the full duration.
func TestLastRoundWaitsForItsLoad(t *testing.T) {
	p := DefaultGenParams()
	p.Rounds = 3
	p.RoundDurationMin = 50
	p.PauseMin = 15
	p.StopsPerRound = 0
	p.ExperimentName = "wait-probe"

	waitSeconds := func(round int) int {
		total := 0
		for _, cmd := range p.Generate(round).Commands {
			if cmd.Action != (WaitAction{}).Name() {
				continue
			}
			params, ok := cmd.Params.(WaitParams)
			if !ok {
				t.Fatalf("round %d: wait command carries %T", round, cmd.Params)
			}
			total += params.Seconds + params.Minutes*60
		}
		return total
	}

	last := waitSeconds(p.Rounds - 1)
	if last < p.RoundDurationMin*60 {
		t.Fatalf("last round waits %ds, want at least the round duration %ds; the success webhook "+
			"would otherwise fire while the load it scheduled is still running",
			last, p.RoundDurationMin*60)
	}

	// An earlier round still carries its pause on top of the same remainder.
	first := waitSeconds(0)
	if first != p.RoundDurationMin*60+p.PauseMin*60 {
		t.Fatalf("round 0 waits %ds, want duration + pause = %ds",
			first, p.RoundDurationMin*60+p.PauseMin*60)
	}
}

// TestStopsPerRoundDefaultEmitsStops pins the default that decides whether a
// caller omitting stops_per_round gets the chaos half of the load test. At 0
// no stop-daemon command is generated at all and the run still reports
// success, which is silent.
func TestStopsPerRoundDefaultEmitsStops(t *testing.T) {
	p := DefaultGenParams()
	if p.StopsPerRound <= 0 {
		t.Fatalf("StopsPerRound default is %d: an API request that omits the field performs no "+
			"node stops at all", p.StopsPerRound)
	}

	p.Rounds = 2
	p.ExperimentName = "stops-probe"
	var stops int
	for _, cmd := range p.Generate(0).Commands {
		if cmd.Action == (StopDaemonAction{}).Name() || cmd.Action == (RestartAction{}).Name() {
			stops++
		}
	}
	if stops == 0 {
		t.Fatal("no stop or restart command in a round generated with the default StopsPerRound")
	}
}
