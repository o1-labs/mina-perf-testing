package itn_orchestrator

import (
	"encoding/json"
	"fmt"
	"itn_json_types"
	"math"
)

type PaymentSubParams struct {
	ExperimentName string                       `json:"experimentName"`
	Tps            float64                      `json:"tps"`
	MinTps         float64                      `json:"minTps"`
	DurationMin    int                          `json:"durationMin"`
	MaxFee         uint64                       `json:"maxFee"`
	MinFee         uint64                       `json:"minFee"`
	Amount         uint64                       `json:"amount"`
	Receiver       itn_json_types.MinaPublicKey `json:"receiver"`
}

type PaymentParams struct {
	PaymentSubParams
	FeePayers []itn_json_types.MinaPrivateKey `json:"feePayers"`
	Nodes     []NodeAddress                   `json:"nodes"`
}

type ScheduledPaymentsReceipt struct {
	Address NodeAddress `json:"address"`
	Handle  string      `json:"handle"`
}

func PaymentKeygenRequirements(gap int, params PaymentSubParams) (int, uint64) {
	maxParticipants := int(math.Ceil(params.Tps / params.MinTps))
	txCost := params.MaxFee + params.Amount
	tpsGap := uint64(math.Ceil(params.Tps * float64(gap)))
	totalTxs := uint64(math.Ceil(float64(params.DurationMin) * 60 * params.Tps))
	balance := 3 * txCost * totalTxs
	keys := maxParticipants + int(tpsGap)*2

	// Add funding fees for account creation (1 MINA per account by default)
	// This ensures we have enough funds to cover both the account balances AND the creation fees
	fundingFees := uint64(keys) * 1e9 // 1 MINA per account creation
	balance += fundingFees

	return keys, balance
}

func paymentInput(params PaymentSubParams, batchIx int, tps float64) PaymentsDetails {
	return PaymentsDetails{
		DurationMin: params.DurationMin,
		Tps:         tps,
		MemoPrefix:  fmt.Sprintf("%s-%d", params.ExperimentName, batchIx),
		MaxFee:      params.MaxFee,
		MinFee:      params.MinFee,
		Amount:      params.Amount,
		Receiver:    params.Receiver,
	}
}

func schedulePaymentsDo(config Config, params PaymentSubParams, nodeAddress NodeAddress, batchIx int, tps float64, feePayers []itn_json_types.MinaPrivateKey) (string, error) {
	paymentInput := paymentInput(params, batchIx, tps)
	paymentInput.Senders = feePayers
	handle, err := SchedulePaymentsGql(config, nodeAddress, paymentInput)
	if err == nil {
		config.Log.Infof("scheduled payment batch %d with tps %f for %s: %s", batchIx, tps, nodeAddress, handle)
	}
	return handle, err
}

func SchedulePayments(config Config, params PaymentParams, output func(ScheduledPaymentsReceipt)) error {
	return scheduleTransactionBatches(
		config,
		"payments",
		params.Tps,
		params.MinTps,
		params.Nodes,
		params.FeePayers,
		func(nodeAddress NodeAddress, batchIx int, tps float64, feePayers []itn_json_types.MinaPrivateKey) (string, error) {
			return schedulePaymentsDo(config, params.PaymentSubParams, nodeAddress, batchIx, tps, feePayers)
		},
		func(nodeAddress NodeAddress, handle string) {
			output(ScheduledPaymentsReceipt{
				Address: nodeAddress,
				Handle:  handle,
			})
		},
	)
}

type PaymentsAction struct{}

func (PaymentsAction) Run(config Config, rawParams json.RawMessage, output OutputF) error {
	var params PaymentParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return err
	}
	return SchedulePayments(config, params, func(receipt ScheduledPaymentsReceipt) {
		output("receipt", receipt, true, false)
		output("participant", receipt.Address, true, false)
	})
}

func (PaymentsAction) Name() string { return "payments" }

var _ Action = PaymentsAction{}
