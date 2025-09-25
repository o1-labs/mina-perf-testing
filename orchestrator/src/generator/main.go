package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	lib "itn_orchestrator"
)

const maxCostMixedTpsRatioHelp = "when provided, specifies ratio of tps (proportional to total tps) for max cost transactions to be used every other round, zkapps ratio for these rounds is set to 100%"

func main() {
	var rotateKeys, rotateServers string
	var mode string
	var p lib.GenParams
	var defaults = lib.DefaultGenParams()

	flag.Float64Var(&p.BaseTps, "base-tps", defaults.BaseTps, "Base tps rate for the whole network")
	flag.Float64Var(&p.StressTps, "stress-tps", defaults.StressTps, "stress tps rate for the whole network")
	flag.Float64Var(&p.MinTps, "min-tps", defaults.MinTps, "minimal tps per node")
	flag.Float64Var(&p.MinStopRatio, "stop-min-ratio", defaults.MinStopRatio, "float in range [0..1], minimum ratio of nodes to stop at an interval")
	flag.Float64Var(&p.MaxStopRatio, "stop-max-ratio", defaults.MaxStopRatio, "float in range [0..1], maximum ratio of nodes to stop at an interval")
	flag.Float64Var(&p.SenderRatio, "sender-ratio", defaults.SenderRatio, "float in range [0..1], max proportion of nodes selected for transaction sending")
	flag.Float64Var(&p.ZkappRatio, "zkapp-ratio", defaults.ZkappRatio, "float in range [0..1], ratio of zkapp transactions of all transactions generated")
	flag.Float64Var(&p.StopCleanRatio, "stop-clean-ratio", defaults.StopCleanRatio, "float in range [0..1], ratio of stops with cleaning of all stops")
	flag.Float64Var(&p.NewAccountRatio, "new-account-ratio", defaults.NewAccountRatio, "float in range [0..1], ratio of new accounts, in relation to expected number of zkapp txs, ignored for max-cost txs")
	flag.BoolVar(&p.SendFromNonBpsOnly, "send-from-non-bps", defaults.SendFromNonBpsOnly, "send only from non block producers")
	flag.BoolVar(&p.StopOnlyBps, "stop-only-bps", defaults.StopOnlyBps, "stop only block producers")
	flag.BoolVar(&p.UseRestartScript, "use-restart-script", defaults.UseRestartScript, "use restart script instead of stop-daemon command")
	flag.BoolVar(&p.MaxCost, "max-cost", defaults.MaxCost, "send max-cost zkapp commands")
	flag.IntVar(&p.RoundDurationMin, "round-duration", defaults.RoundDurationMin, "duration of a round, minutes")
	flag.IntVar(&p.PauseMin, "pause", defaults.PauseMin, "duration of a pause between rounds, minutes")
	flag.IntVar(&p.Rounds, "rounds", defaults.Rounds, "number of rounds to run experiment")
	flag.IntVar(&p.StopsPerRound, "round-stops", defaults.StopsPerRound, "number of stops to perform within round")
	flag.IntVar(&p.Gap, "gap", defaults.Gap, "gap between related transactions, seconds")
	flag.IntVar(&p.ZkappSoftLimit, "zkapp-soft-limit", defaults.ZkappSoftLimit, "soft limit for number of zkapps to be taken to a block (-2 for no-op, -1 for reset, >=0 for setting a value)")
	flag.StringVar(&mode, "mode", "default", "mode of generation")
	flag.StringVar(&p.FundKeyPrefix, "fund-keys-dir", defaults.FundKeyPrefix, "Dir for generated fund key prefixes")
	flag.StringVar(&p.PasswordEnv, "password-env", defaults.PasswordEnv, "Name of environment variable to read privkey password from")
	flag.StringVar((*string)(&p.PaymentReceiver), "payment-receiver", "", "Mina PK receiving payments")
	flag.StringVar(&p.ExperimentName, "experiment-name", defaults.ExperimentName, "Name of experiment")
	flag.IntVar(&p.PrivkeysPerFundCmd, "privkeys-per-fund", defaults.PrivkeysPerFundCmd, "Number of private keys to use per fund command")
	flag.IntVar(&p.GenerateFundKeys, "generate-privkeys", defaults.GenerateFundKeys, "Number of funding keys to generate from the private key")
	flag.StringVar(&rotateKeys, "rotate-keys", "", "Comma-separated list of public keys to rotate")
	flag.StringVar(&rotateServers, "rotate-servers", "", "Comma-separated list of servers for rotation")
	flag.Float64Var(&p.RotationRatio, "rotate-ratio", defaults.RotationRatio, "Ratio of balance to rotate")
	flag.BoolVar(&p.RotationPermutation, "rotate-permutation", defaults.RotationPermutation, "Whether to generate only permutation mappings for rotation")
	flag.IntVar(&p.LargePauseMin, "large-pause", defaults.LargePauseMin, "duration of the large pause, minutes")
	flag.IntVar(&p.LargePauseEveryNRounds, "large-pause-every", defaults.LargePauseEveryNRounds, "number of rounds in between large pauses")
	flag.Float64Var(&p.MaxCostMixedTpsRatio, "max-cost-mixed", defaults.MaxCostMixedTpsRatio, maxCostMixedTpsRatioHelp)
	flag.Uint64Var(&p.MaxBalanceChange, "max-balance-change", defaults.MaxBalanceChange, "Max balance change for zkapp account update")
	flag.Uint64Var(&p.MinBalanceChange, "min-balance-change", defaults.MinBalanceChange, "Min balance change for zkapp account update")
	flag.Uint64Var(&p.DeploymentFee, "deployment-fee", defaults.DeploymentFee, "Zkapp deployment fee")
	flag.Uint64Var(&p.FundFee, "fund-fee", defaults.FundFee, "Funding tx fee")
	flag.Uint64Var(&p.MinPaymentFee, "min-payment-fee", defaults.MinPaymentFee, "Min payment fee")
	flag.Uint64Var(&p.MaxPaymentFee, "max-payment-fee", defaults.MaxPaymentFee, "Max payment fee")
	flag.Uint64Var(&p.MinZkappFee, "min-zkapp-fee", defaults.MinZkappFee, "Min zkapp tx fee")
	flag.Uint64Var(&p.MaxZkappFee, "max-zkapp-fee", defaults.MaxZkappFee, "Max zkapp tx fee")
	flag.Uint64Var(&p.PaymentAmount, "payment-amount", defaults.PaymentAmount, "Payment amount")
	flag.Parse()
	p.Privkeys = flag.Args()

	if rotateKeys != "" {
		p.RotationKeys = strings.Split(rotateKeys, ",")
	}
	if rotateServers != "" {
		p.RotationServers = strings.Split(rotateServers, ",")
	}

	lib.ValidateAndExitEarly(&p)

	switch mode {
	case "stop-ratio-distribution":
		for i := 0; i < 10000; i++ {
			v := lib.SampleStopRatio(p.MinStopRatio, p.MaxStopRatio)
			fmt.Printf("Sampled stop ratio: %f\n", v)
			fmt.Println(v)
		}
		return
	case "tps-distribution":
		for i := 0; i < 10000; i++ {
			v := lib.SampleTps(p.BaseTps, p.StressTps)
			fmt.Println(v)
		}
		return
	case "default":
	default:
		os.Exit(1)
	}

	if _, err := lib.EncodeToWriter(&p, os.Stdout, false); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding: %v\n", err)
		os.Exit(3)
	}
}
