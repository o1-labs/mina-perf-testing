package itn_orchestrator

// Support for daemons with the ITN harness operations of
// MinaProtocol/mina#19616: caller-chosen handles written ahead to a journal,
// stopping every running scheduler, and a preflight check of the builds.

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	mina "github.com/MinaProtocol/mina-sdk-go"
	"github.com/MinaProtocol/mina-sdk-go/itn"
)

// newHandle returns a random (version 4) UUID, the handle format the daemon
// accepts.
func newHandle() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// journalEntry is one line of the handle journal.
type journalEntry struct {
	Time   time.Time   `json:"time"`
	Node   NodeAddress `json:"node"`
	Kind   string      `json:"kind"`
	Handle string      `json:"handle"`
}

// writeAheadHandle chooses the handle for a scheduling request to node and
// records it before the request is sent. It returns false when the node has
// no harness support, so the caller uses the old mutation.
//
// A failed journal write does not stop the run: scheduledTransactions still
// lists the handle on the daemon, so stop-all-scheduled can find the load.
func (config Config) writeAheadHandle(node NodeAddress, kind string) (string, bool) {
	if !config.NodeData[node].HarnessSupport {
		return "", false
	}
	handle, err := newHandle()
	if err != nil {
		config.Log.Errorf("cannot create a handle for %s: %v; using the old mutation", node, err)
		return "", false
	}
	if config.HandleJournal != "" {
		if err := appendJournal(config.HandleJournal, journalEntry{Time: time.Now(), Node: node, Kind: kind, Handle: handle}); err != nil {
			config.Log.Errorf("cannot write handle %s to the journal %s: %v", handle, config.HandleJournal, err)
		}
	}
	return handle, true
}

func appendJournal(path string, e journalEntry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// withHandleRetry repeats a mutation with a caller-chosen handle after a
// transport error. That is safe because the daemon starts nothing for a handle
// that is already running. Answers from the daemon (GraphQL errors, HTTP error
// statuses, a rejected signature) are not repeated.
func withHandleRetry(call func() (string, error)) (string, error) {
	const attempts = 3
	var err error
	for i := 0; i < attempts; i++ {
		var handle string
		handle, err = call()
		if err == nil || !isTransportError(err) {
			return handle, err
		}
		time.Sleep(2 * time.Second)
	}
	return "", err
}

func isTransportError(err error) bool {
	var gqlErr *mina.GraphQLError
	var httpErr *itn.HTTPError
	var unauthorized *itn.UnauthorizedError
	var sequencing *itn.SequencingError
	return !errors.As(err, &gqlErr) && !errors.As(err, &httpErr) &&
		!errors.As(err, &unauthorized) && !errors.As(err, &sequencing)
}

// nodesOrAll returns nodes, or every node the orchestrator knows when nodes is
// empty, in a stable order.
func (config Config) nodesOrAll(nodes []NodeAddress) []NodeAddress {
	if len(nodes) > 0 {
		return nodes
	}
	all := make([]NodeAddress, 0, len(config.NodeData))
	for addr := range config.NodeData {
		all = append(all, addr)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	return all
}

type StopAllScheduledParams struct {
	// Nodes to stop; empty for every node the orchestrator knows.
	Nodes []NodeAddress `json:"nodes,omitempty"`
}

// StopAllScheduled stops every scheduler that each node lists, whoever
// started it. It needs no record of the handles, so it also stops load whose
// handles were lost, for example by a restart.
func StopAllScheduled(config Config, params StopAllScheduledParams, output func(NodeAddress, string)) error {
	errs := []error{}
	for _, addr := range config.nodesOrAll(params.Nodes) {
		client, err := GetGqlClient(config, addr)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !config.NodeData[addr].HarnessSupport {
			config.Log.Warnf("node %s cannot list its schedulers (no harness support); skipped", addr)
			continue
		}
		handles, err := client.ScheduledTransactions(config.Ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("listing schedulers on %s: %w", addr, err))
			continue
		}
		for _, handle := range handles {
			if _, err := client.StopScheduledTransactions(config.Ctx, handle); err != nil {
				errs = append(errs, fmt.Errorf("stopping %s on %s: %w", handle, addr, err))
				continue
			}
			config.Log.Infof("stopped scheduler %s on %s", handle, addr)
			output(addr, handle)
		}
	}
	return errors.Join(errs...)
}

type StopAllScheduledAction struct{}

func (StopAllScheduledAction) Name() string { return "stop-all-scheduled" }

func (StopAllScheduledAction) Run(config Config, rawParams json.RawMessage, output OutputF) error {
	var params StopAllScheduledParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return err
	}
	return StopAllScheduled(config, params, func(addr NodeAddress, handle string) {
		_ = output("receipt", ScheduledPaymentsReceipt{Address: addr, Handle: handle}, true, false)
	})
}

var _ Action = StopAllScheduledAction{}

type PreflightParams struct {
	// Nodes to check; empty for every node the orchestrator knows.
	Nodes []NodeAddress `json:"nodes,omitempty"`
	// ExpectCommit, when set, is the Mina commit (or a prefix of it) every
	// node must run.
	ExpectCommit string `json:"expectCommit,omitempty"`
}

// Preflight checks, before load is sent, that every node has the harness
// operations and that all nodes run the same build. Components of different
// builds do not fail when they talk to each other; they hang.
func Preflight(config Config, params PreflightParams) (string, error) {
	problems := []string{}
	commits := map[string][]NodeAddress{}
	for _, addr := range config.nodesOrAll(params.Nodes) {
		if _, err := GetGqlClient(config, addr); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", addr, err))
			continue
		}
		entry := config.NodeData[addr]
		if !entry.HarnessSupport {
			problems = append(problems, fmt.Sprintf("%s: no ITN harness support (needs MinaProtocol/mina#19616)", addr))
			continue
		}
		commits[entry.CommitID] = append(commits[entry.CommitID], addr)
		if params.ExpectCommit != "" && !strings.HasPrefix(entry.CommitID, params.ExpectCommit) {
			problems = append(problems, fmt.Sprintf("%s: runs %s, expected %s", addr, entry.CommitID, params.ExpectCommit))
		}
	}
	if len(commits) > 1 {
		parts := []string{}
		for commit, addrs := range commits {
			parts = append(parts, fmt.Sprintf("%s on %d node(s)", commit, len(addrs)))
		}
		sort.Strings(parts)
		problems = append(problems, "nodes run different builds: "+strings.Join(parts, ", "))
	}
	if len(problems) > 0 {
		return "", fmt.Errorf("preflight failed:\n  %s", strings.Join(problems, "\n  "))
	}
	for commit := range commits {
		return commit, nil
	}
	return "", errors.New("preflight: no nodes to check")
}

type PreflightAction struct{}

func (PreflightAction) Name() string { return "preflight" }

func (PreflightAction) Run(config Config, rawParams json.RawMessage, output OutputF) error {
	var params PreflightParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return err
	}
	commit, err := Preflight(config, params)
	if err != nil {
		return err
	}
	config.Log.Infof("preflight passed: all nodes run %s", commit)
	return output("commit", commit, false, false)
}

var _ Action = PreflightAction{}
