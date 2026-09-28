package itn_orchestrator

import "testing"

// TestReportStepNilIsSafe: the standalone generator leaves ReportStep nil, so
// every call site must tolerate that.
func TestReportStepNilIsSafe(t *testing.T) {
	Config{}.reportStep("fund", 3) // must not panic
}

func TestReportStepForwards(t *testing.T) {
	var gotName string
	var gotStep int
	c := Config{ReportStep: func(name string, step int) { gotName, gotStep = name, step }}

	c.reportStep("zkapps", 7)
	if gotName != "zkapps" || gotStep != 7 {
		t.Fatalf("reportStep forwarded (%q, %d), want (\"zkapps\", 7)", gotName, gotStep)
	}
}
