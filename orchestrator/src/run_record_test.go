package itn_orchestrator

import (
	"reflect"
	"testing"
)

func TestResolvedNodesAndBuilds(t *testing.T) {
	config := Config{NodeData: map[NodeAddress]NodeEntry{
		"10.0.0.2:3086": {PeerId: "p2", IsBlockProducer: true, HarnessSupport: true, CommitID: "bbb"},
		"10.0.0.1:3086": {PeerId: "p1", HarnessSupport: true, CommitID: "aaa"},
		"10.0.0.3:3086": {PeerId: "p3"},
		"10.0.0.4:3086": {PeerId: "p4", HarnessSupport: true, CommitID: "aaa"},
	}}
	nodes := config.ResolvedNodes()
	want := []RunNode{
		{Address: "10.0.0.1:3086", PeerID: "p1", HarnessSupport: true, Build: "aaa"},
		{Address: "10.0.0.2:3086", PeerID: "p2", BlockProducer: true, HarnessSupport: true, Build: "bbb"},
		{Address: "10.0.0.3:3086", PeerID: "p3"},
		{Address: "10.0.0.4:3086", PeerID: "p4", HarnessSupport: true, Build: "aaa"},
	}
	if !reflect.DeepEqual(nodes, want) {
		t.Errorf("ResolvedNodes:\n%+v\nwant\n%+v", nodes, want)
	}
	if got := DaemonBuilds(nodes); !reflect.DeepEqual(got, []string{"aaa", "bbb"}) {
		t.Errorf("DaemonBuilds: %v", got)
	}
	if got := DaemonBuilds(nil); got != nil {
		t.Errorf("DaemonBuilds(nil): %v", got)
	}
}
