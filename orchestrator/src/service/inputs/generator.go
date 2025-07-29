package inputs

import (
	"fmt"
	"itn_json_types"
	lib "itn_orchestrator"
	"strings"
)

type GeneratorInputData struct {
	BaseTps                *float64                      `json:"base_tps,omitempty"`
	StressTps              *float64                      `json:"stress_tps,omitempty"`
	MinTps                 *float64                      `json:"min_tps,omitempty"`
	MixMaxCostTpsRatio     *float64                      `json:"mix_max_cost_tps_ratio,omitempty"`
	MinStopRatio           *float64                      `json:"min_stop_ratio,omitempty"`
	MaxStopRatio           *float64                      `json:"max_stop_ratio,omitempty"`
	SenderRatio            *float64                      `json:"sender_ratio,omitempty"`
	ZkappRatio             *float64                      `json:"zkapp_ratio,omitempty"`
	StopCleanRatio         *float64                      `json:"stop_clean_ratio,omitempty"`
	NewAccountRatio        *float64                      `json:"new_account_ratio,omitempty"`
	SendFromNonBpsOnly     *bool                         `json:"send_from_non_bps_only,omitempty"`
	StopOnlyBps            *bool                         `json:"stop_only_bps,omitempty"`
	UseRestartScript       *bool                         `json:"use_restart_script,omitempty"`
	MaxCost                *bool                         `json:"max_cost,omitempty"`
	RoundDurationMin       *int                          `json:"round_duration_min,omitempty"`
	PauseMin               *int                          `json:"pause_min,omitempty"`
	Rounds                 *int                          `json:"rounds,omitempty"`
	StopsPerRound          *int                          `json:"stops_per_round,omitempty"`
	Gap                    *int                          `json:"gap,omitempty"`
	ZkappSoftLimit         *int                          `json:"zkapp_soft_limit,omitempty"`
	Mode                   *string                       `json:"mode,omitempty"`
	FundKeyPrefix          *string                       `json:"fund_key_prefix,omitempty"`
	PasswordEnv            *string                       `json:"password_env,omitempty"`
	PaymentReceiver        *itn_json_types.MinaPublicKey `json:"payment_receiver,omitempty"`
	ExperimentName         *string                       `json:"experiment_name,omitempty"`
	PrivkeysPerFundCmd     *int                          `json:"privkeys_per_fund_cmd,omitempty"`
	GenerateFundKeys       *int                          `json:"generate_fund_keys,omitempty"`
	RotateKeys             *string                       `json:"rotate_keys,omitempty"`
	RotateServers          *string                       `json:"rotate_servers,omitempty"`
	RotationRatio          *float64                      `json:"rotation_ratio,omitempty"`
	RotationPermutation    *bool                         `json:"rotation_permutation,omitempty"`
	LargePauseMin          *int                          `json:"large_pause_min,omitempty"`
	LargePauseEveryNRounds *int                          `json:"large_pause_every_n_rounds,omitempty"`
	MaxBalanceChange       *uint64                       `json:"max_balance_change,omitempty"`
	MinBalanceChange       *uint64                       `json:"min_balance_change,omitempty"`
	PaymentAmount          *uint64                       `json:"payment_amount,omitempty"`
	Privkeys               []string                      `json:"priv_keys,omitempty"`
	Fees                   struct {
		Deployment *uint64 `json:"deployment,omitempty"`
		Fund       *uint64 `json:"fund,omitempty"`
		MinPayment *uint64 `json:"min_payment,omitempty"`
		MaxPayment *uint64 `json:"max_payment,omitempty"`
		MinZkapp   *uint64 `json:"min_zkapp,omitempty"`
		MaxZkapp   *uint64 `json:"max_zkapp,omitempty"`
	} `json:"fees,omitempty"`
}

const mixMaxCostTpsRatioHelp = "when provided, specifies ratio of tps (proportional to total tps) for max cost transactions to be used every other round, zkapps ratio for these rounds is set to 100%"

func (inputData *GeneratorInputData) ApplyWithDefaults(p *lib.GenParams) {

	var defaults = lib.DefaultGenParams()
	var rotateKeys string
	var rotateServers string

	// Example usage for setting values in p based on inputData
	lib.SetOrDefault(inputData.BaseTps, &p.BaseTps, defaults.BaseTps)
	lib.SetOrDefault(inputData.StressTps, &p.StressTps, defaults.StressTps)
	lib.SetOrDefault(inputData.MinTps, &p.MinTps, defaults.MinTps)
	lib.SetOrDefault(inputData.MixMaxCostTpsRatio, &p.MixMaxCostTpsRatio, defaults.MixMaxCostTpsRatio)
	lib.SetOrDefault(inputData.MinStopRatio, &p.MinStopRatio, defaults.MinStopRatio)
	lib.SetOrDefault(inputData.MaxStopRatio, &p.MaxStopRatio, defaults.MaxStopRatio)
	lib.SetOrDefault(inputData.SenderRatio, &p.SenderRatio, defaults.SenderRatio)
	lib.SetOrDefault(inputData.ZkappRatio, &p.ZkappRatio, defaults.ZkappRatio)
	lib.SetOrDefault(inputData.StopCleanRatio, &p.StopCleanRatio, defaults.StopCleanRatio)
	lib.SetOrDefault(inputData.NewAccountRatio, &p.NewAccountRatio, defaults.NewAccountRatio)
	lib.SetOrDefault(inputData.SendFromNonBpsOnly, &p.SendFromNonBpsOnly, defaults.SendFromNonBpsOnly)
	lib.SetOrDefault(inputData.StopOnlyBps, &p.StopOnlyBps, defaults.StopOnlyBps)
	lib.SetOrDefault(inputData.UseRestartScript, &p.UseRestartScript, defaults.UseRestartScript)
	lib.SetOrDefault(inputData.MaxCost, &p.MaxCost, defaults.MaxCost)
	lib.SetOrDefault(inputData.RoundDurationMin, &p.RoundDurationMin, defaults.RoundDurationMin)
	lib.SetOrDefault(inputData.PauseMin, &p.PauseMin, defaults.PauseMin)
	lib.SetOrDefault(inputData.Rounds, &p.Rounds, defaults.Rounds)
	lib.SetOrDefault(inputData.StopsPerRound, &p.StopsPerRound, defaults.StopsPerRound)
	lib.SetOrDefault(inputData.Gap, &p.Gap, defaults.Gap)
	lib.SetOrDefault(inputData.ZkappSoftLimit, &p.ZkappSoftLimit, defaults.ZkappSoftLimit)
	lib.SetOrDefault(inputData.FundKeyPrefix, &p.FundKeyPrefix, defaults.FundKeyPrefix)
	lib.SetOrDefault(inputData.PasswordEnv, &p.PasswordEnv, defaults.PasswordEnv)
	lib.SetOrDefault(inputData.PaymentReceiver, &p.PaymentReceiver, defaults.PaymentReceiver)
	lib.SetOrDefault(inputData.ExperimentName, &p.ExperimentName, defaults.ExperimentName)
	lib.SetOrDefault(inputData.PrivkeysPerFundCmd, &p.PrivkeysPerFundCmd, defaults.PrivkeysPerFundCmd)
	lib.SetOrDefault(inputData.GenerateFundKeys, &p.GenerateFundKeys, defaults.GenerateFundKeys)
	lib.SetOrDefault(inputData.RotateKeys, &rotateKeys, "")
	if rotateKeys != "" {
		p.RotationKeys = strings.Split(rotateKeys, ",")
	}
	lib.SetOrDefault(inputData.RotateServers, &rotateServers, "")
	if rotateServers != "" {
		p.RotationServers = strings.Split(rotateServers, ",")
	}
	lib.SetOrDefault(inputData.RotationRatio, &p.RotationRatio, defaults.RotationRatio)
	lib.SetOrDefault(inputData.RotationPermutation, &p.RotationPermutation, defaults.RotationPermutation)
	lib.SetOrDefault(inputData.LargePauseMin, &p.LargePauseMin, defaults.LargePauseMin)
	lib.SetOrDefault(inputData.LargePauseEveryNRounds, &p.LargePauseEveryNRounds, defaults.LargePauseEveryNRounds)
	lib.SetOrDefault(inputData.MaxBalanceChange, &p.MaxBalanceChange, defaults.MaxBalanceChange)
	lib.SetOrDefault(inputData.MinBalanceChange, &p.MinBalanceChange, defaults.MinBalanceChange)
	lib.SetOrDefault(inputData.PaymentAmount, &p.PaymentAmount, defaults.PaymentAmount)

	lib.SetOrDefault(inputData.Fees.Deployment, &p.DeploymentFee, 1e9)
	lib.SetOrDefault(inputData.Fees.Fund, &p.FundFee, 1e9)
	lib.SetOrDefault(inputData.Fees.MinPayment, &p.MinPaymentFee, 1e8)
	lib.SetOrDefault(inputData.Fees.MaxPayment, &p.MaxPaymentFee, 2e8)
	lib.SetOrDefault(inputData.Fees.MinZkapp, &p.MinZkappFee, 1e9)
	lib.SetOrDefault(inputData.Fees.MaxZkapp, &p.MaxZkappFee, 2e9)

	p.Privkeys = inputData.Privkeys

}

func (inputData *GeneratorInputData) ValidateExperimentName(validationErrors []string) bool {
	illegalChars := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	valid := true
	if inputData.ExperimentName == nil || *inputData.ExperimentName == "" {
		validationErrors = append(validationErrors, "Experiment name is required")
		valid = false
	}
	if len(*inputData.ExperimentName) > 50 {
		validationErrors = append(validationErrors, "Experiment name must be less than 50 characters")
		valid = false
	}
	if strings.Contains(*inputData.ExperimentName, " ") {
		validationErrors = append(validationErrors, "Experiment name must not contain spaces")
		valid = false
	}

	for _, char := range illegalChars {
		if strings.Contains(*inputData.ExperimentName, char) {
			validationErrors = append(validationErrors, fmt.Sprintf("Experiment name '%s' contains illegal character '%s'", *inputData.ExperimentName, char))
		}
		valid = false
	}
	return valid
}
