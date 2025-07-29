package inputs

import (
	"itn_json_types"
)

type OrchestratorInputConfig struct {
	Key              itn_json_types.Ed25519Privkey `json:"key"`
	SlotDurationMs   int                           `json:"slot_duration_ms"`
	GenesisTimestamp itn_json_types.Time           `json:"genesis_timestamp"`
	OnlineURL        string                        `json:"online_url"`
	FundDaemonPorts  []string                      `json:"fund_daemon_ports"`
	LogFile          string                        `json:"log_file"`
	URLOverrides     []string                      `json:"url_overrides"`
	MinaExec         string                        `json:"mina_exec"`
}
