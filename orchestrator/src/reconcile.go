package itn_orchestrator

import (
	"context"
	"fmt"
	"time"
)

// StopOrphanedLoad stops every scheduler on the nodes the orchestrator can
// find, with no record of the handles. It exists for a restart: the previous
// process lost its handles with its memory, and the daemons keep generating
// load until something asks them to stop.
//
// The node set is one pass over the uptime data plus FundItnNodes, where the
// account-creation jobs run. DiscoverParticipants is not used because its
// retries wait 10, 20 and 40 minutes, which suits an experiment step and not
// a startup recovery. The uptime read gets discoveryTimeout of its own, so
// an uptime outage (its reader retries for five minutes) cannot use up the
// time the stops need; the configured funding nodes are then still stopped.
func StopOrphanedLoad(config Config, discoveryTimeout time.Duration, output func(NodeAddress, string)) error {
	nodes := append([]NodeAddress{}, config.FundItnNodes...)

	discoveryConfig := config
	ctx, cancel := context.WithTimeout(config.Ctx, discoveryTimeout)
	defer cancel()
	discoveryConfig.Ctx = ctx
	err := discoverParticipantsDo(discoveryConfig, DiscoveryParams{}, func(addr NodeAddress) {
		nodes = append(nodes, addr)
	})
	if err != nil {
		if len(nodes) == 0 {
			return fmt.Errorf("finding the nodes: %w", err)
		}
		config.Log.Warnf("finding the nodes through the uptime data: %v; stopping on the %d configured funding node(s) only", err, len(nodes))
	}
	return StopAllScheduled(config, StopAllScheduledParams{Nodes: nodes}, output)
}
