package itn_orchestrator

import "sort"

// RunNode is one node of a run's resolved node set: what the orchestrator
// learned about the daemon when it connected. A run records the set so that
// "what was this run sent to, and which build was it running" is answerable
// afterwards, from the run itself.
type RunNode struct {
	Address        NodeAddress `json:"address"`
	PeerID         string      `json:"peer_id,omitempty"`
	BlockProducer  bool        `json:"block_producer"`
	HarnessSupport bool        `json:"harness_support"`
	// Build is the daemon's git commit (ITN commitId; empty without harness
	// support).
	Build string `json:"build,omitempty"`
}

// ResolvedNodes lists the nodes the orchestrator has connected to so far,
// sorted by address.
func (config Config) ResolvedNodes() []RunNode {
	nodes := make([]RunNode, 0, len(config.NodeData))
	for addr, entry := range config.NodeData {
		nodes = append(nodes, RunNode{
			Address:        addr,
			PeerID:         entry.PeerId,
			BlockProducer:  entry.IsBlockProducer,
			HarnessSupport: entry.HarnessSupport,
			Build:          entry.CommitID,
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Address < nodes[j].Address })
	return nodes
}

// DaemonBuilds returns the distinct builds of nodes, sorted; nodes without a
// known build are left out.
func DaemonBuilds(nodes []RunNode) []string {
	seen := map[string]bool{}
	var builds []string
	for _, n := range nodes {
		if n.Build != "" && !seen[n.Build] {
			seen[n.Build] = true
			builds = append(builds, n.Build)
		}
	}
	sort.Strings(builds)
	return builds
}
