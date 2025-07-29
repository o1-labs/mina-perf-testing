package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	logging "github.com/ipfs/go-log/v2"

	lib "itn_orchestrator"
)

func run(configFilename string) error {
	orchestratorConfig := lib.LoadAppConfig(configFilename)
	logging.SetupLogging(logging.Config{
		Format: logging.ColorizedOutput,
		Stderr: true,
		Stdout: false,
		Level:  logging.LogLevel(orchestratorConfig.LogLevel),
		File:   orchestratorConfig.LogFile,
	})
	log := logging.Logger("itn orchestrator")
	log.Infof("Launching logging: %v", logging.GetSubsystems())
	config := lib.SetupConfig(context.Background(), orchestratorConfig, log)
	outCache := lib.EmptyOutputCache()
	rconfig := lib.ResolutionConfig{
		OutputCache: outCache,
	}
	inDecoder := json.NewDecoder(os.Stdin)
	step := 0
	var prevAction lib.BatchAction
	var actionAccum []lib.ActionIO
	handlePrevAction := func() error {
		log.Infof("Performing steps %s (%d-%d)", prevAction.Name(), step, len(actionAccum)-step)
		err := prevAction.RunMany(config, actionAccum)
		if err != nil {
			return &lib.OrchestratorError{
				Message: fmt.Sprintf("Error running steps %d-%d: %v", step, len(actionAccum)-step, err),
				Code:    9,
			}
		}
		prevAction = nil
		actionAccum = nil
		return nil
	}

	lib.RunActions(inDecoder, config, outCache, log, step,
		handlePrevAction, &actionAccum, rconfig, &prevAction)
	if prevAction != nil {
		if err := handlePrevAction(); err != nil {
			return &lib.OrchestratorError{
				Message: fmt.Sprintf("Error running previous action: %v", err),
				Code:    9,
			}
		}
	}
	return nil
}

func main() {
	if len(os.Args) < 2 {
		os.Stderr.WriteString("No config provided")
		os.Exit(1)
		return
	}
	configFilename := os.Args[1]
	if err := run(configFilename); err != nil {
		os.Stderr.WriteString(fmt.Sprintf("Error: %v", err))
		os.Exit(1)
	}
	os.Exit(0)
}
