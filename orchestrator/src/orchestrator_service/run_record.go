package main

import (
	"fmt"
	"strings"

	lib "itn_orchestrator"
)

// orchestratorBuild is the git commit this service was built from. The
// Makefile sets it with -ldflags "-X main.orchestratorBuild=..."; the
// Dockerfile passes the ORCHESTRATOR_BUILD build argument through.
var orchestratorBuild = "unknown"

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func deploymentString(id *int) string {
	if id == nil {
		return "-"
	}
	return fmt.Sprint(*id)
}

// describeBuilds summarises the daemon builds of nodes for a log line.
func describeBuilds(nodes []lib.RunNode) string {
	builds := lib.DaemonBuilds(nodes)
	unknown := 0
	for _, n := range nodes {
		if n.Build == "" {
			unknown++
		}
	}
	s := strings.Join(builds, ", ")
	if s == "" {
		s = "-"
	}
	if unknown > 0 {
		s += fmt.Sprintf(" (%d node(s) without harness support report no build)", unknown)
	}
	return s
}
