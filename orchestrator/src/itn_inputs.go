package itn_orchestrator

import (
	"itn_json_types"

	mina "github.com/MinaProtocol/mina-sdk-go"
	"github.com/MinaProtocol/mina-sdk-go/itn"
)

// Inputs of the daemon's ITN mutations. They keep the names and fields of
// the types genqlient used to generate, so the actions that build them are
// unchanged; the Gql wrappers convert them to mina-sdk-go's itn types.
// Fees and amounts are in nanomina.

type PaymentsDetails struct {
	DurationMin int
	Tps         float64
	MemoPrefix  string
	MaxFee      uint64
	MinFee      uint64
	Amount      uint64
	Receiver    itn_json_types.MinaPublicKey
	Senders     []itn_json_types.MinaPrivateKey
}

type ZkappCommandsDetails struct {
	MaxAccountUpdates  int
	MaxCost            bool
	NonDefaultToken    bool
	AccountQueueSize   int
	DeploymentFee      uint64
	MaxFee             uint64
	MinFee             uint64
	InitBalance        uint64
	MaxNewZkappBalance uint64
	MinNewZkappBalance uint64
	MaxBalanceChange   uint64
	MinBalanceChange   uint64
	NoPrecondition     bool
	MemoPrefix         string
	DurationMin        int
	Tps                float64
	NumNewAccounts     int
	NumZkappsToDeploy  int
	FeePayers          []itn_json_types.MinaPrivateKey
}

type NetworkPeer struct {
	Libp2pPort int
	Host       string
	PeerId     string
}

type GatingUpdate struct {
	AddedPeers      []NetworkPeer
	CleanAddedPeers bool
	Isolate         bool
	BannedPeers     []NetworkPeer
	TrustedPeers    []NetworkPeer
}

func privateKeys(keys []itn_json_types.MinaPrivateKey) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = string(k)
	}
	return out
}

func (d PaymentsDetails) toItn() itn.PaymentsDetails {
	return itn.PaymentsDetails{
		DurationMin: d.DurationMin,
		TPS:         d.Tps,
		MemoPrefix:  d.MemoPrefix,
		MaxFee:      mina.CurrencyFromNanomina(d.MaxFee),
		MinFee:      mina.CurrencyFromNanomina(d.MinFee),
		Amount:      mina.CurrencyFromNanomina(d.Amount),
		Receiver:    string(d.Receiver),
		Senders:     privateKeys(d.Senders),
	}
}

func (d ZkappCommandsDetails) toItn() itn.ZkappCommandsDetails {
	maxAccountUpdates := d.MaxAccountUpdates
	out := itn.ZkappCommandsDetails{
		MaxAccountUpdates:  &maxAccountUpdates,
		MaxCost:            d.MaxCost,
		AccountQueueSize:   d.AccountQueueSize,
		DeploymentFee:      mina.CurrencyFromNanomina(d.DeploymentFee),
		MaxFee:             mina.CurrencyFromNanomina(d.MaxFee),
		MinFee:             mina.CurrencyFromNanomina(d.MinFee),
		InitBalance:        mina.CurrencyFromNanomina(d.InitBalance),
		MaxNewZkappBalance: mina.CurrencyFromNanomina(d.MaxNewZkappBalance),
		MinNewZkappBalance: mina.CurrencyFromNanomina(d.MinNewZkappBalance),
		MaxBalanceChange:   mina.CurrencyFromNanomina(d.MaxBalanceChange),
		MinBalanceChange:   mina.CurrencyFromNanomina(d.MinBalanceChange),
		NoPrecondition:     d.NoPrecondition,
		MemoPrefix:         d.MemoPrefix,
		DurationMin:        d.DurationMin,
		TPS:                d.Tps,
		NumNewAccounts:     d.NumNewAccounts,
		NumZkappsToDeploy:  d.NumZkappsToDeploy,
		FeePayers:          privateKeys(d.FeePayers),
	}
	// Only a daemon built from georgeee/itn-arbitrary-cap-custom-token has
	// this field; others ignore it. Send it only when the load asks for it.
	if d.NonDefaultToken {
		nonDefaultToken := true
		out.NonDefaultToken = &nonDefaultToken
	}
	return out
}

func peersToItn(ps []NetworkPeer) []itn.NetworkPeer {
	out := make([]itn.NetworkPeer, len(ps))
	for i, p := range ps {
		out[i] = itn.NetworkPeer{Host: p.Host, Libp2pPort: p.Libp2pPort, PeerID: p.PeerId}
	}
	return out
}

func (g GatingUpdate) toItn() itn.GatingUpdate {
	return itn.GatingUpdate{
		AddedPeers:      peersToItn(g.AddedPeers),
		CleanAddedPeers: g.CleanAddedPeers,
		Isolate:         g.Isolate,
		BannedPeers:     peersToItn(g.BannedPeers),
		TrustedPeers:    peersToItn(g.TrustedPeers),
	}
}
