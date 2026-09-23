package itn_orchestrator

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"time"

	"github.com/Khan/genqlient/graphql"
	logging "github.com/ipfs/go-log/v2"
)

type MiniMetaToBeSaved struct {
	GraphqlControlPort uint16 `json:"graphql_control_port,omitempty"`
	RemoteAddr         string `json:"remote_addr"`
	Submitter          string `json:"submitter"` // is base58check-encoded submitter's public key
}

type MetaToBeSaved struct {
	MiniMetaToBeSaved
	CreatedAt string `json:"created_at"`
	PeerId    string `json:"peer_id"`
	SnarkWork string `json:"snark_work,omitempty"`
	BlockHash string `json:"block_hash"` // is base58check-encoded hash of a block
}

type RawParams map[string]json.RawMessage

type Command struct {
	Action string
	Params RawParams
}

type NodeAddress string

type NodeEntry struct {
	Client          graphql.Client
	Libp2pPort      uint16
	PeerId          string
	IsBlockProducer bool
	LastStatusCode  *int
}

type Config struct {
	Ctx                context.Context
	AwsContext         *AwsContext
	OnlineURL          string
	Sk                 ed25519.PrivateKey
	Log                logging.StandardLogger
	MinaExec           string
	NodeData           map[NodeAddress]NodeEntry
	SlotDurationMs     int
	GenesisTimestamp   time.Time
	ControlExec        string
	StopDaemonDelaySec int
	FundDaemonPorts    []string
	UrlOverrides       []string
	PrintRequests      bool

	// AllowUnverifiedMinaExec — see OrchestratorConfig.
	AllowUnverifiedMinaExec bool

	// ReportStep, when set, is called as each step or batch begins. It exists
	// so the orchestrator can report progress directly instead of the service
	// prefix-matching log format strings and type-asserting positional args --
	// which reported the batch *end* for a batch and the *start* for a single
	// step, so the number jumped differently depending on batch size. Nil for
	// the standalone generator, which has nowhere to report to.
	//
	// It is a func rather than an interface because Config lives in the
	// orchestrator package and the Store lives below it; the reverse import
	// would be a cycle.
	ReportStep func(name string, step int)
}

// reportStep invokes the hook when one is configured.
func (c Config) reportStep(name string, step int) {
	if c.ReportStep != nil {
		c.ReportStep(name, step)
	}
}

type OutputF = func(name string, value any, multiple bool, sensitive bool) error

type ActionIO struct {
	Params json.RawMessage
	Output OutputF
}

type Action interface {
	Run(config Config, params json.RawMessage, output OutputF) error
	Name() string
}

type BatchAction interface {
	Action
	RunMany(config Config, actionIOs []ActionIO) error
	Validate(params json.RawMessage) error
}
