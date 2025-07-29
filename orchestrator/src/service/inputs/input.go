package inputs

import lib "itn_orchestrator"

type Input struct {
	ExperimentSetup    *GeneratorInputData      `json:"experiment_setup"`
	OrchestratorConfig *OrchestratorInputConfig `json:"orchestrator_config"`
}

func (input *Input) GetOrchestratorConfig(defaults *lib.OrchestratorConfig) lib.OrchestratorConfig {
	if input.OrchestratorConfig == nil {
		return *defaults
	} else {
		config := lib.OrchestratorConfig{}

		lib.SetOrDefault(&config.LogFile, &input.OrchestratorConfig.LogFile, defaults.LogFile)
		lib.SetOrDefault(&config.Key, &input.OrchestratorConfig.Key, defaults.Key)
		lib.SetOrDefault(&config.OnlineURL, &input.OrchestratorConfig.OnlineURL, defaults.OnlineURL)
		lib.SetOrDefault(&config.FundDaemonPorts, &input.OrchestratorConfig.FundDaemonPorts, defaults.FundDaemonPorts)
		lib.SetOrDefault(&config.MinaExec, &input.OrchestratorConfig.MinaExec, defaults.MinaExec)
		lib.SetOrDefault(&config.SlotDurationMs, &input.OrchestratorConfig.SlotDurationMs, defaults.SlotDurationMs)
		lib.SetOrDefault(&config.GenesisTimestamp, &input.OrchestratorConfig.GenesisTimestamp, defaults.GenesisTimestamp)
		lib.SetOrDefault(&config.UrlOverrides, &input.OrchestratorConfig.URLOverrides, defaults.UrlOverrides)

		return config
	}

}
