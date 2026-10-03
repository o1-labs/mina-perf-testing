package main

import (
	"testing"

	lib "itn_orchestrator"

	logging "github.com/ipfs/go-log/v2"
)

// With FundItnNodes, fund-keys runs no mina client, so the service neither
// looks for one (the App has no Store here: any lookup would panic) nor
// changes MinaExec.
func TestVerifyMinaClientIsSkippedWithItnFunding(t *testing.T) {
	a := &App{}
	config := lib.Config{FundItnNodes: []lib.NodeAddress{"seed:3086"}, MinaExec: "/no/such/mina"}
	if err := a.verifyMinaClient(&config, logging.Logger("test")); err != nil {
		t.Fatal(err)
	}
	if config.MinaExec != "/no/such/mina" {
		t.Errorf("MinaExec changed to %q", config.MinaExec)
	}
}
