package itn_orchestrator

import (
	"encoding/json"
	"fmt"
	"itn_json_types"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"

	"gorm.io/datatypes"
)

type GenParams struct {
	MinTps, BaseTps, StressTps, SenderRatio, ZkappRatio, NewAccountRatio float64
	StopCleanRatio, MinStopRatio, MaxStopRatio                           float64
	RoundDurationMin, PauseMin, Rounds, StopsPerRound, Gap               int
	SendFromNonBpsOnly, StopOnlyBps, UseRestartScript, MaxCost           bool
	ExperimentName, PasswordEnv, FundKeyPrefix                           string
	Privkeys                                                             []string
	PaymentReceiver                                                      itn_json_types.MinaPublicKey
	PrivkeysPerFundCmd                                                   int
	GenerateFundKeys                                                     int
	RotationKeys, RotationServers                                        []string
	RotationPermutation                                                  bool
	RotationRatio                                                        float64
	MixMaxCostTpsRatio                                                   float64
	LargePauseEveryNRounds, LargePauseMin                                int
	MinBalanceChange, MaxBalanceChange, DeploymentFee                    uint64
	PaymentAmount, MinZkappFee, MaxZkappFee, FundFee                     uint64
	MinPaymentFee, MaxPaymentFee                                         uint64
	ZkappSoftLimit                                                       int
}

func (p *GenParams) ToJSON() (datatypes.JSON, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("Failed to marshal setup to JSON: %v", err)
	}
	return data, nil
}

func DefaultGenParams() GenParams {
	return GenParams{
		MinTps:                 0.01,
		BaseTps:                0.3,
		StressTps:              1,
		SenderRatio:            0.5,
		ZkappRatio:             0.5,
		NewAccountRatio:        0,
		StopCleanRatio:         0.1,
		MinStopRatio:           0.0,
		MaxStopRatio:           0.5,
		RoundDurationMin:       30,
		PauseMin:               15,
		Rounds:                 4,
		StopsPerRound:          2,
		Gap:                    180,
		SendFromNonBpsOnly:     false,
		StopOnlyBps:            false,
		UseRestartScript:       false,
		MaxCost:                false,
		ExperimentName:         "exp-0",
		PasswordEnv:            "",
		FundKeyPrefix:          "./fund_keys",
		Privkeys:               []string{},
		PaymentReceiver:        "B62qn7v4x5g3Z1h8k2j6f9c5z5v5v5v5v5v5v5v5v5v5v5v5v5",
		PrivkeysPerFundCmd:     1,
		GenerateFundKeys:       20,
		RotationKeys:           []string{},
		RotationServers:        []string{},
		RotationPermutation:    false,
		RotationRatio:          0.3,
		MixMaxCostTpsRatio:     0.0,
		LargePauseEveryNRounds: 8,
		LargePauseMin:          0,
		MinBalanceChange:       0,
		MaxBalanceChange:       1e3,
		DeploymentFee:          1e9,
		PaymentAmount:          1e5,
		MinZkappFee:            1e9,
		MaxZkappFee:            2e9,
		FundFee:                1e9,
		MinPaymentFee:          1e8,
		MaxPaymentFee:          2e8,
		ZkappSoftLimit:         -2,
	}
}

type GeneratedCommand struct {
	Action  string `json:"action"`
	Params  any    `json:"params"`
	comment string
}

func (cmd *GeneratedCommand) Comment() string {
	return cmd.comment
}

type GeneratedRound struct {
	Commands           []GeneratedCommand
	PaymentFundCommand *FundParams
	ZkappFundCommand   *FundParams
}

func withComment(comment string, cmd GeneratedCommand) GeneratedCommand {
	cmd.comment = comment
	return cmd
}

func formatDur(min, sec int) string {
	sec += min * 60
	min = sec / 60
	sec %= 60
	hour := min / 60
	min %= 60
	day := hour / 24
	hour %= 24
	parts := []string{}
	if day > 0 {
		parts = append(parts, strconv.Itoa(day), "days")
	}
	if hour > 0 {
		parts = append(parts, strconv.Itoa(hour), "hours")
	}
	if min > 0 {
		parts = append(parts, strconv.Itoa(min), "mins")
	}
	if sec > 0 {
		parts = append(parts, strconv.Itoa(sec), "secs")
	}
	if len(parts) == 0 {
		return "immediately"
	}
	return strings.Join(parts, " ")
}

func rotate(p RotateParams) GeneratedCommand {
	return GeneratedCommand{Action: RotateAction{}.Name(), Params: p}
}

func loadKeys(p KeyloaderParams) GeneratedCommand {
	return GeneratedCommand{Action: KeyloaderAction{}.Name(), Params: p}
}

func Discovery(p DiscoveryParams) GeneratedCommand {
	return GeneratedCommand{Action: DiscoveryAction{}.Name(), Params: p}
}

type SetZkappSoftLimitRefParams struct {
	Nodes ComplexValue `json:"nodes"`
	Limit *int         `json:"limit"`
}

func ZkappSoftLimit(nodesRef int, nodesName string, zkappSoftLimit int) GeneratedCommand {
	var limit *int
	if zkappSoftLimit >= 0 {
		limit = &zkappSoftLimit
	}
	nodes := LocalComplexValue(nodesRef, nodesName)
	nodes.OnEmpty = emptyArrayRawMessage
	return GeneratedCommand{Action: SetZkappSoftLimitAction{}.Name(), Params: SetZkappSoftLimitRefParams{
		Nodes: nodes,
		Limit: limit,
	}}
}

type SampleRefParams struct {
	Group  ComplexValue `json:"group"`
	Ratios []float64    `json:"ratios"`
}

func sample(groupRef int, groupName string, ratios []float64) GeneratedCommand {
	group := LocalComplexValue(groupRef, groupName)
	group.OnEmpty = emptyArrayRawMessage
	return GeneratedCommand{Action: SampleAction{}.Name(), Params: SampleRefParams{
		Group:  group,
		Ratios: ratios,
	}}
}

type ZkappRefParams struct {
	ZkappSubParams
	FeePayers ComplexValue `json:"feePayers"`
	Nodes     ComplexValue `json:"nodes"`
}

func zkapps(feePayersRef int, nodesRef int, nodesName string, params ZkappSubParams) GeneratedCommand {
	cmd := GeneratedCommand{Action: ZkappCommandsAction{}.Name(), Params: ZkappRefParams{
		ZkappSubParams: params,
		FeePayers:      LocalComplexValue(feePayersRef, "key"),
		Nodes:          LocalComplexValue(nodesRef, nodesName),
	}}
	maxCostStr := ""
	if params.MaxCost {
		maxCostStr = "max-cost "
	}
	comment := fmt.Sprintf("Scheduling %d %szkapp transactions to be sent over period of %d minutes (%.2f txs/min)",
		int(params.Tps*float64(params.DurationMin)*60), maxCostStr, params.DurationMin, params.Tps*60,
	)
	return withComment(comment, cmd)
}

type PaymentRefParams struct {
	PaymentSubParams
	FeePayers ComplexValue `json:"feePayers"`
	Nodes     ComplexValue `json:"nodes"`
}

func payments(feePayersRef int, nodesRef int, nodesName string, params PaymentSubParams) GeneratedCommand {
	cmd := GeneratedCommand{Action: PaymentsAction{}.Name(), Params: PaymentRefParams{
		PaymentSubParams: params,
		FeePayers:        LocalComplexValue(feePayersRef, "key"),
		Nodes:            LocalComplexValue(nodesRef, nodesName),
	}}
	comment := fmt.Sprintf("Scheduling %d payments to be sent over period of %d minutes (%.2f txs/min)",
		int(params.Tps*float64(params.DurationMin)*60), params.DurationMin, params.Tps*60,
	)
	return withComment(comment, cmd)
}

func waitMin(min int) GeneratedCommand {
	return GeneratedCommand{Action: WaitAction{}.Name(), Params: WaitParams{
		Minutes: min,
	}}
}

func GenWait(sec int) GeneratedCommand {
	return GeneratedCommand{Action: WaitAction{}.Name(), Params: WaitParams{
		Seconds: sec,
	}}
}

type RestartRefParams struct {
	Nodes ComplexValue `json:"nodes"`
	Clean bool         `json:"clean,omitempty"`
}

/*
 * Returns random number in normal distribution centering on 0.
 * ~95% of numbers returned should fall between -2 and 2
 * ie within two standard deviations
 */
func gaussRandom() float64 {
	u := 2*rand.Float64() - 1
	v := 2*rand.Float64() - 1
	r := u*u + v*v
	// if outside interval [0,1] start over
	if r == 0 || r >= 1 {
		return gaussRandom()
	}

	c := math.Sqrt(-2 * math.Log(r) / r)
	return u * c
}

func SampleTps(baseTps, stressTps float64) float64 {
	tpsStddev := (stressTps - baseTps) / 2
	return tpsStddev*math.Abs(gaussRandom()) + baseTps
}

func SampleStopRatio(minRatio, maxRatio float64) float64 {
	stddev := (maxRatio - minRatio) / 3
	return stddev*math.Abs(gaussRandom()) + minRatio
}

func genStopDaemon(useRestartScript bool, nodesRef int, nodesName string, clean bool) GeneratedCommand {
	var name string
	if useRestartScript {
		name = RestartAction{}.Name()
	} else {
		name = StopDaemonAction{}.Name()
	}
	return GeneratedCommand{Action: name, Params: RestartRefParams{
		Nodes: LocalComplexValue(nodesRef, nodesName),
		Clean: clean,
	}}
}

type JoinRefParams struct {
	Group1 ComplexValue `json:"group1"`
	Group2 ComplexValue `json:"group2"`
}

func join(g1Ref int, g1Name string, g2Ref int, g2Name string) GeneratedCommand {
	return GeneratedCommand{Action: JoinAction{}.Name(), Params: JoinRefParams{
		Group1: LocalComplexValue(g1Ref, g1Name),
		Group2: LocalComplexValue(g2Ref, g2Name),
	}}
}

type ExceptRefParams struct {
	Group  ComplexValue `json:"group"`
	Except ComplexValue `json:"except"`
}

var emptyArrayRawMessage json.RawMessage

func init() {
	emptyArrayRawMessage, _ = json.Marshal([]string{})
}

func except(groupRef int, groupName string, exceptRef int, exceptName string) GeneratedCommand {
	group := LocalComplexValue(groupRef, groupName)
	group.OnEmpty = emptyArrayRawMessage
	except := LocalComplexValue(exceptRef, exceptName)
	except.OnEmpty = emptyArrayRawMessage
	return GeneratedCommand{Action: ExceptAction{}.Name(), Params: ExceptRefParams{
		Group:  group,
		Except: except,
	}}
}
func (p *GenParams) Generate(round int) GeneratedRound {
	zkappsKeysDir := fmt.Sprintf("%s/%s/round-%d/zkapps", p.FundKeyPrefix, p.ExperimentName, round)
	paymentsKeysDir := fmt.Sprintf("%s/%s/round-%d/payments", p.FundKeyPrefix, p.ExperimentName, round)
	tps := SampleTps(p.BaseTps, p.StressTps)
	maxCost := p.MaxCost
	zkappRatio := p.ZkappRatio
	if p.MixMaxCostTpsRatio > 1e-3 && (round&1) == 1 {
		maxCost = true
		zkappRatio = 1
		tps *= p.MixMaxCostTpsRatio
	}
	experimentName := fmt.Sprintf("%s-%d", p.ExperimentName, round)
	onlyZkapps := math.Abs(1-zkappRatio) < 1e-3
	onlyPayments := zkappRatio < 1e-3
	zkappTps := tps * zkappRatio
	zkappParams := ZkappSubParams{
		ExperimentName:   experimentName,
		Tps:              zkappTps,
		MinTps:           p.MinTps,
		DurationMin:      p.RoundDurationMin,
		Gap:              p.Gap,
		MinBalanceChange: p.MinBalanceChange,
		MaxBalanceChange: p.MaxBalanceChange,
		MinFee:           p.MinZkappFee,
		MaxFee:           p.MaxZkappFee,
		DeploymentFee:    p.DeploymentFee,
		MaxCost:          maxCost,
		NewAccountRatio:  p.NewAccountRatio,
	}
	if maxCost {
		// This can be set to arbitrary value as for max-cost it only
		// matters that total zkapps deployed is above 5
		// We need to set it this way to override setting accountQueueSize
		// by the orchestrator
		zkappParams.ZkappsToDeploy = 8
		zkappParams.NewAccountRatio = 0
	}
	paymentParams := PaymentSubParams{
		ExperimentName: experimentName,
		Tps:            tps - zkappTps,
		MinTps:         p.MinTps,
		DurationMin:    p.RoundDurationMin,
		MinFee:         p.MinPaymentFee,
		MaxFee:         p.MaxPaymentFee,
		Amount:         p.PaymentAmount,
		Receiver:       p.PaymentReceiver,
	}
	cmds := []GeneratedCommand{}
	roundStartMin := round*(p.RoundDurationMin+p.PauseMin) + round/p.LargePauseEveryNRounds*p.LargePauseMin
	if len(p.RotationKeys) > 0 {
		var mapping []int
		nKeys := len(p.RotationKeys)
		if p.RotationPermutation {
			mapping = rand.Perm(nKeys)
		} else {
			mapping = make([]int, nKeys)
			for i := range mapping {
				mapping[i] = rand.Intn(len(p.RotationKeys))
			}
		}
		cmds = append(cmds, rotate(RotateParams{
			Pubkeys:     p.RotationKeys,
			RestServers: p.RotationServers,
			Mapping:     mapping,
			Ratio:       p.RotationRatio,
			PasswordEnv: p.PasswordEnv,
		}))
	}
	roundStartMsg := fmt.Sprintf("Starting round %d, %s after start", round, formatDur(roundStartMin, 0))
	cmds = append(cmds, withComment(roundStartMsg, Discovery(DiscoveryParams{
		NoBlockProducers: p.SendFromNonBpsOnly,
	})))
	participantsName := "participant"
	participantsRef := -1
	if p.ZkappSoftLimit > -2 {
		if p.SendFromNonBpsOnly {
			// Need to perform wide discovery for zkapp soft limit
			cmds = append(cmds, Discovery(DiscoveryParams{}))
			participantsRef--
		}
		msg := fmt.Sprintf("Setting zkapp soft limit to %d", p.ZkappSoftLimit)
		cmds = append(cmds, withComment(msg, ZkappSoftLimit(-1, "participant", p.ZkappSoftLimit)))
		participantsRef--
	}
	if 1-p.SenderRatio > 1e-6 {
		cmds = append(cmds, sample(participantsRef, participantsName, []float64{p.SenderRatio}))
		participantsName = "group1"
		participantsRef = -1
	}
	if onlyPayments {
		cmds = append(cmds, loadKeys(KeyloaderParams{Dir: paymentsKeysDir}))
		cmds = append(cmds, payments(-1, participantsRef-1, participantsName, paymentParams))
	} else if onlyZkapps {
		cmds = append(cmds, loadKeys(KeyloaderParams{Dir: zkappsKeysDir}))
		cmds = append(cmds, zkapps(-1, participantsRef-1, participantsName, zkappParams))
	} else {
		cmds = append(cmds, loadKeys(KeyloaderParams{Dir: zkappsKeysDir}))
		cmds = append(cmds, loadKeys(KeyloaderParams{Dir: paymentsKeysDir}))
		cmds = append(cmds, zkapps(-2, participantsRef-2, participantsName, zkappParams))
		cmds = append(cmds, payments(-2, participantsRef-3, participantsName, paymentParams))
		cmds = append(cmds, join(-1, "participant", -2, "participant"))
	}
	sendersCmdId := len(cmds)
	stopWaits := make([]int, p.StopsPerRound)
	for i := 0; i < p.StopsPerRound; i++ {
		stopWaits[i] = rand.Intn(60 * p.RoundDurationMin)
	}
	sort.Ints(stopWaits)
	for i := p.StopsPerRound - 1; i > 0; i-- {
		stopWaits[i] -= stopWaits[i-1]
	}
	stopRatio := SampleStopRatio(p.MinStopRatio, p.MaxStopRatio)
	elapsed := 0
	for _, waitSec := range stopWaits {
		cmds = append(cmds, withComment(fmt.Sprintf("Running round %d, %s after start, waiting for %s", round, formatDur(roundStartMin, elapsed), formatDur(0, waitSec)), GenWait(waitSec)))
		cmds = append(cmds, Discovery(DiscoveryParams{
			OnlyBlockProducers: p.StopOnlyBps,
		}))
		exceptRefName := "group"
		if onlyPayments || onlyZkapps {
			exceptRefName = "participant"
		}
		cmds = append(cmds, except(-1, "participant", sendersCmdId-len(cmds)-1, exceptRefName))
		stopCleanRatio := p.StopCleanRatio * stopRatio
		stopNoCleanRatio := (1 - p.StopCleanRatio) * stopRatio
		nodesOrBps := "nodes"
		if p.StopOnlyBps {
			nodesOrBps = "block producers"
		}
		if stopCleanRatio > 1e-6 && stopNoCleanRatio > 1e-6 {
			cmds = append(cmds, sample(-1, "group", []float64{stopCleanRatio, stopNoCleanRatio}))
			comment1 := fmt.Sprintf("Stopping %.1f%% %s with cleaning", stopCleanRatio*100, nodesOrBps)
			cmds = append(cmds, withComment(comment1, genStopDaemon(p.UseRestartScript, -1, "group1", true)))
			comment2 := fmt.Sprintf("Stopping %.1f%% %s without cleaning", stopNoCleanRatio*100, nodesOrBps)
			cmds = append(cmds, withComment(comment2, genStopDaemon(p.UseRestartScript, -2, "group2", false)))
		} else if stopCleanRatio > 1e-6 {
			comment := fmt.Sprintf("Stopping %.1f%% %s with cleaning", stopCleanRatio*100, nodesOrBps)
			cmds = append(cmds, sample(-1, "group", []float64{stopCleanRatio}))
			cmds = append(cmds, withComment(comment, genStopDaemon(p.UseRestartScript, -1, "group1", true)))
		} else if stopNoCleanRatio > 1e-6 {
			comment := fmt.Sprintf("Stopping %.1f%% %s without cleaning", stopNoCleanRatio*100, nodesOrBps)
			cmds = append(cmds, sample(-1, "group", []float64{stopNoCleanRatio}))
			cmds = append(cmds, withComment(comment, genStopDaemon(p.UseRestartScript, -1, "group1", false)))
		}
		elapsed += waitSec
	}
	if round < p.Rounds-1 {
		comment1 := fmt.Sprintf("Waiting for remainder of round %d, %s after start", round, formatDur(roundStartMin, elapsed))
		cmds = append(cmds, withComment(comment1, GenWait(p.RoundDurationMin*60-elapsed)))
		if p.PauseMin > 0 {
			comment2 := fmt.Sprintf("Pause after round %d, %s after start", round, formatDur(roundStartMin+p.RoundDurationMin, 0))
			cmds = append(cmds, withComment(comment2, waitMin(p.PauseMin)))
		}
		if p.LargePauseMin > 0 && (round+1)%p.LargePauseEveryNRounds == 0 {
			comment3 := fmt.Sprintf("Large pause after round %d, %s after start", round, formatDur(roundStartMin+p.RoundDurationMin+p.PauseMin, 0))
			cmds = append(cmds, withComment(comment3, waitMin(p.LargePauseMin)))
		}
	}
	res := GeneratedRound{Commands: cmds}
	if !onlyPayments {
		_, _, _, initBalance := ZkappBalanceRequirements(zkappTps, zkappParams)
		zkappKeysNum, zkappAmount := ZkappKeygenRequirements(initBalance, zkappParams)
		res.ZkappFundCommand = &FundParams{
			PasswordEnv: p.PasswordEnv,
			Prefix:      zkappsKeysDir + "/key",
			Amount:      zkappAmount,
			Fee:         p.FundFee,
			Num:         zkappKeysNum,
		}
	}
	if !onlyZkapps {
		paymentKeysNum, paymentAmount := PaymentKeygenRequirements(p.Gap, paymentParams)
		res.PaymentFundCommand = &FundParams{
			PasswordEnv: p.PasswordEnv,
			Prefix:      paymentsKeysDir + "/key",
			Amount:      paymentAmount,
			Fee:         p.FundFee,
			Num:         paymentKeysNum,
		}
	}
	return res
}
