package itn_orchestrator

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	mina "github.com/MinaProtocol/mina-sdk-go"
	"github.com/MinaProtocol/mina-sdk-go/itn"
)

// The daemon's ITN GraphQL server is reached through mina-sdk-go's itn
// package, which signs requests with config.Sk and handles the server
// UUID, the sequence numbers and a new auth after a daemon restart.

func readBody(req *http.Request) ([]byte, error) {
	readCloser, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	defer readCloser.Close()
	return io.ReadAll(readCloser)
}

func NewGqlClient(config Config, addr NodeAddress) (*NodeEntry, error) {
	url := "http://" + string(addr) + "/graphql"
	opts := []itn.Option{itn.WithRetries(1)}
	if config.PrintRequests {
		opts = append(opts, itn.WithHTTPClient(&http.Client{Transport: RoundTripper{logger: config.Log}}))
	}
	client := itn.NewClient(url, itn.KeyFromSeed(config.Sk.Seed()), opts...)
	auth, err := client.Auth(config.Ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to authorize client %s: %v", addr, err)
	}
	entry := &NodeEntry{Client: client}
	entry.setAuth(auth)
	return entry, nil
}

func (e *NodeEntry) setAuth(auth *itn.Auth) {
	e.Libp2pPort = auth.Libp2pPort
	e.PeerId = auth.PeerID
	e.IsBlockProducer = auth.IsBlockProducer
}

func GetGqlClient(config Config, addr NodeAddress) (*itn.Client, error) {
	if entry, has := config.NodeData[addr]; has {
		return entry.Client, nil
	}
	entry, err := NewGqlClient(config, addr)
	if err != nil {
		return nil, err
	}
	config.NodeData[addr] = *entry
	return entry.Client, nil
}

// GqlRequestError carries the HTTP status a node answered with, so a caller
// can tell "this node is unwell" from "this request is wrong".
//
// Without it every failure looked alike, and a deterministic rejection -- a
// memo over the 32-character limit, say -- was retried against every node in
// the cluster before the round gave up.
type GqlRequestError struct {
	Node       NodeAddress
	StatusCode int
	Err        error
}

func (e *GqlRequestError) Error() string {
	if e.StatusCode == 0 {
		return fmt.Sprintf("request to %s failed: %v", e.Node, e.Err)
	}
	return fmt.Sprintf("request to %s failed with status %d: %v", e.Node, e.StatusCode, e.Err)
}

func (e *GqlRequestError) Unwrap() error { return e.Err }

// Permanent reports whether the same request would be refused by any node.
//
// 4xx is the daemon saying the request is wrong, with three exceptions: 408
// and 429 are load, and 412 is the sequence-number rejection the client
// already retries. Anything else, a transport failure included, may be this
// node alone.
func (e *GqlRequestError) Permanent() bool {
	switch e.StatusCode {
	case 408, 412, 429:
		return false
	}
	return e.StatusCode >= 400 && e.StatusCode < 500
}

// statusOf returns the HTTP status behind an itn client error: the one the
// daemon answered with, 200 for a GraphQL error (the daemon reports those
// in a 200 response), or 0 when no answer came.
func statusOf(err error) int {
	var unauthorized *itn.UnauthorizedError
	var sequencing *itn.SequencingError
	var httpErr *itn.HTTPError
	var gqlErr *mina.GraphQLError
	switch {
	case errors.As(err, &unauthorized):
		return http.StatusUnauthorized
	case errors.As(err, &sequencing):
		return http.StatusPreconditionFailed
	case errors.As(err, &httpErr):
		return httpErr.StatusCode
	case errors.As(err, &gqlErr):
		return http.StatusOK
	}
	return 0
}

func wrapGqlRequest[T any](config Config, nodeAddress NodeAddress, perform func(client *itn.Client) (T, error)) (T, error) {
	var zero T
	client, err := GetGqlClient(config, nodeAddress)
	if err != nil {
		return zero, fmt.Errorf("failed to create a client for %s: %v", nodeAddress, err)
	}
	resp, err := perform(client)
	// The client runs auth again by itself after a daemon restart; keep the
	// node's peer ID, libp2p port and role current for the gating actions.
	if auth := client.LastAuth(); auth != nil {
		if entry, has := config.NodeData[nodeAddress]; has && entry.Client == client {
			entry.setAuth(auth)
			config.NodeData[nodeAddress] = entry
		}
	}
	if err != nil {
		return resp, &GqlRequestError{Node: nodeAddress, StatusCode: statusOf(err), Err: err}
	}
	return resp, nil
}

func SchedulePaymentsGql(config Config, nodeAddress NodeAddress, input PaymentsDetails) (string, error) {
	handle, err := wrapGqlRequest(config, nodeAddress, func(client *itn.Client) (string, error) {
		return client.SchedulePayments(config.Ctx, input.toItn())
	})
	if err != nil {
		return "", fmt.Errorf("error scheduling payments to %s: %w", nodeAddress, err)
	}
	return handle, nil
}

func StopTransactionsGql(config Config, nodeAddress NodeAddress, handle string) (string, error) {
	resp, err := wrapGqlRequest(config, nodeAddress, func(client *itn.Client) (string, error) {
		return client.StopScheduledTransactions(config.Ctx, handle)
	})
	if err != nil {
		return "", fmt.Errorf("error stoping transactions at %s on %s: %v", handle, nodeAddress, err)
	}
	return resp, nil
}

func ScheduleZkappCommands(config Config, nodeAddress NodeAddress, input ZkappCommandsDetails) (string, error) {
	handle, err := wrapGqlRequest(config, nodeAddress, func(client *itn.Client) (string, error) {
		return client.ScheduleZkappCommands(config.Ctx, input.toItn())
	})
	if err != nil {
		return "", fmt.Errorf("error scheduling zkapp txs to %s: %w", nodeAddress, err)
	}
	return handle, nil
}

func SlotsWonGql(config Config, nodeAddress NodeAddress) ([]int, bool, error) {
	var isBlockProducer bool
	slots, err := wrapGqlRequest(config, nodeAddress, func(client *itn.Client) ([]int64, error) {
		if !config.NodeData[nodeAddress].IsBlockProducer {
			return nil, nil
		}
		isBlockProducer = true
		return client.SlotsWon(config.Ctx)
	})
	if err != nil {
		return nil, true, fmt.Errorf("failed to get slots for %s: %v", nodeAddress, err)
	}
	if !isBlockProducer {
		return nil, false, nil
	}
	out := make([]int, len(slots))
	for i, s := range slots {
		out[i] = int(s)
	}
	return out, true, nil
}

func UpdateGatingGql(config Config, nodeAddress NodeAddress, input GatingUpdate) error {
	_, err := wrapGqlRequest(config, nodeAddress, func(client *itn.Client) (string, error) {
		return client.UpdateGating(config.Ctx, input.toItn())
	})
	if err != nil {
		return fmt.Errorf("failed to update gating for %s: %v", nodeAddress, err)
	}
	// TODO do something with resp.UpdateGating?
	return nil
}

func StopDaemonGql(config Config, nodeAddress NodeAddress, clean bool, delaySec int) (string, error) {
	resp, err := wrapGqlRequest(config, nodeAddress, func(client *itn.Client) (string, error) {
		return client.StopDaemon(config.Ctx, &delaySec, clean)
	})
	if err != nil {
		return "", fmt.Errorf("error stoping daemon on %s (delay %d): %v", nodeAddress, delaySec, err)
	}
	return resp, nil
}

func SetZkappSoftLimitGql(config Config, nodeAddress NodeAddress, limit *int) (*int, error) {
	resp, err := wrapGqlRequest(config, nodeAddress, func(client *itn.Client) (*int, error) {
		return client.SetZkappCommandLimit(config.Ctx, limit)
	})
	if err != nil {
		return nil, fmt.Errorf("error setting zkapp soft limit on %s: %v", nodeAddress, err)
	}
	return resp, nil
}
