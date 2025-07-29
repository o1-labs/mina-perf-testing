package itn_orchestrator

import (
	"fmt"
	"os"
)

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func rangeOfValues(value, min, max float64) bool {
	return value < min || value > max
}

func isBetweenZeroAndOneInclusive(value float64) bool {
	return rangeOfValues(value, 0.0, 1.0)
}

type ValidationStep struct {
	ErrorMsg string
	Check    func(p *GenParams) bool
	ExitCode int
}

func simpleRangeCheck(value float64, name string) ValidationStep {
	return ValidationStep{
		ErrorMsg: "Invalid " + name,
		Check: func(p *GenParams) bool {
			return isBetweenZeroAndOneInclusive(value)
		},
		ExitCode: 2,
	}
}

func ValidationSteps(p *GenParams) []ValidationStep {
	return []ValidationStep{
		simpleRangeCheck(p.SenderRatio, "sender ratio"),
		simpleRangeCheck(p.ZkappRatio, "zkapp ratio"),
		simpleRangeCheck(p.MinStopRatio, "min stop ratio"),
		simpleRangeCheck(p.MaxStopRatio, "max stop ratio"),
		simpleRangeCheck(p.StopCleanRatio, "stop-clean ratio"),
		simpleRangeCheck(p.MixMaxCostTpsRatio, "max-cost-mixed ratio"),
		simpleRangeCheck(p.RotationRatio, "rotation ratio"),
		{
			ErrorMsg: "both max-cost-mixed and max-cost specified",
			Check: func(p *GenParams) bool {
				return p.MaxCost && p.MixMaxCostTpsRatio > 1e-3
			},
			ExitCode: 2,
		},
		{
			ErrorMsg: "wrong large-pause-every: should be a positive number",
			Check: func(p *GenParams) bool {
				return p.LargePauseEveryNRounds <= 0
			},
			ExitCode: 2,
		},
		{
			ErrorMsg: "increase round duration: roundDurationMin*60 should be more than gap*4",
			Check: func(p *GenParams) bool {
				return p.RoundDurationMin*60 < p.Gap*4
			},
			ExitCode: 9,
		},
		{
			ErrorMsg: "wrong new account ratio",
			Check: func(p *GenParams) bool {
				return p.NewAccountRatio < 0
			},
			ExitCode: 2,
		},
		{
			ErrorMsg: "Specify funding private key files after all flags (separated by spaces)",
			Check: func(p *GenParams) bool {
				return len(p.Privkeys) == 0
			},
			ExitCode: 4,
		},
		{
			ErrorMsg: "When option -generate-funding-keys is used, only a single private key should be provided",
			Check: func(p *GenParams) bool {
				return p.GenerateFundKeys > 0 && len(p.Privkeys) > 1
			},
			ExitCode: 4,
		},
		{
			ErrorMsg: "Number of private keys is less than -privkeys-per-fund",
			Check: func(p *GenParams) bool {
				return (p.GenerateFundKeys > 0 && p.GenerateFundKeys < p.PrivkeysPerFundCmd) || (p.GenerateFundKeys == 0 && len(p.Privkeys) < p.PrivkeysPerFundCmd)
			},
			ExitCode: 4,
		},
		{
			ErrorMsg: "wrong rotation configuration",
			Check: func(p *GenParams) bool {
				return len(p.RotationServers) != len(p.RotationKeys)
			},
			ExitCode: 5,
		},
		{
			ErrorMsg: "Payment receiver not specified",
			Check: func(p *GenParams) bool {
				return p.PaymentReceiver == "" && p.ZkappRatio < 0.999
			},
			ExitCode: 2,
		},
		{
			ErrorMsg: "File not found or missing permissions for one of privkey",
			Check: func(p *GenParams) bool {
				for _, privkey := range p.Privkeys {
					if _, err := os.Stat(privkey); os.IsNotExist(err) {
						return true
					}
				}
				return false
			},
			ExitCode: 2,
		},
	}
}

func ValidateAndCollectErrors(p *GenParams) []string {
	var errors []string

	for _, step := range ValidationSteps(p) {
		if step.Check(p) {
			errors = append(errors, step.ErrorMsg)
		}
	}
	return errors
}

func ValidateAndExitEarly(p *GenParams) {
	for _, step := range ValidationSteps(p) {
		if step.Check(p) {
			fmt.Fprintln(os.Stderr, step.ErrorMsg)
			os.Exit(step.ExitCode)
		}
	}
}
