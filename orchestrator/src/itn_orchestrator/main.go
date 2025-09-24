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
	inDecoder := json.NewDecoder(os.Stdin)
	
	return lib.RunExperiment(inDecoder, config, log)
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
