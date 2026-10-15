package itn_orchestrator

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// harnessDaemon answers auth, commitId, scheduledTransactions and
// stopScheduledTransactions, as a daemon with mina#19616 does.
type harnessDaemon struct {
	mu      sync.Mutex
	handles []string
	stopped []string
}

func (d *harnessDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	_ = json.Unmarshal(body, &req)
	reply := func(data map[string]any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": data}) }
	switch {
	case strings.Contains(req.Query, "commitId"):
		reply(map[string]any{"auth": map[string]any{"commitId": "abc1234"}})
	case strings.Contains(req.Query, "auth {"):
		reply(map[string]any{"auth": map[string]any{
			"serverUuid": "u-1", "signerSequenceNumber": "0", "libp2pPort": "8302",
			"peerId": "peer-1", "isBlockProducer": false}})
	case strings.Contains(req.Query, "scheduledTransactions") && !strings.Contains(req.Query, "stop"):
		reply(map[string]any{"scheduledTransactions": append([]string{}, d.handles...)})
	case strings.Contains(req.Query, "stopScheduledTransactions"):
		handle := req.Variables["handle"].(string)
		d.stopped = append(d.stopped, handle)
		kept := d.handles[:0]
		for _, h := range d.handles {
			if h != handle {
				kept = append(kept, h)
			}
		}
		d.handles = kept
		reply(map[string]any{"stopScheduledTransactions": "stopped " + handle})
	default:
		http.Error(w, "unexpected query: "+req.Query, http.StatusBadRequest)
	}
}

// uptimeListing serves the online-nodes list the orchestrator discovers from.
func uptimeListing(t *testing.T, nodes ...NodeAddress) *httptest.Server {
	t.Helper()
	var list []MiniMetaToBeSaved
	for _, n := range nodes {
		host, port, _ := strings.Cut(string(n), ":")
		var p uint16
		for _, c := range port {
			p = p*10 + uint16(c-'0')
		}
		list = append(list, MiniMetaToBeSaved{RemoteAddr: host, GraphqlControlPort: p, Submitter: "B62q" + host})
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(list)
	}))
}

// A restart lost the handles; the schedulers the daemons list are stopped,
// once each, although the daemon is known both from the uptime data and as
// a funding node.
func TestStopOrphanedLoadStopsWhatTheNodesList(t *testing.T) {
	d := &harnessDaemon{handles: []string{"h-payments", "h-zkapps"}}
	srv := httptest.NewServer(d)
	defer srv.Close()
	addr := NodeAddress(strings.TrimPrefix(srv.URL, "http://"))
	uptime := uptimeListing(t, addr)
	defer uptime.Close()

	config := testConfig(t)
	config.OnlineURL = uptime.URL
	config.FundItnNodes = []NodeAddress{addr}

	var reported []string
	err := StopOrphanedLoad(config, time.Minute, func(n NodeAddress, h string) { reported = append(reported, h) })
	if err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if got := strings.Join(d.stopped, ","); got != "h-payments,h-zkapps" {
		t.Errorf("daemon received stops %q, want h-payments,h-zkapps once each", got)
	}
	if got := strings.Join(reported, ","); got != "h-payments,h-zkapps" {
		t.Errorf("reported %q", got)
	}
}

// Without uptime data the configured funding nodes are still cleaned, so an
// orphaned account-creation job does not survive an uptime outage.
func TestStopOrphanedLoadWithoutUptimeDataUsesFundingNodes(t *testing.T) {
	d := &harnessDaemon{handles: []string{"h-fund"}}
	srv := httptest.NewServer(d)
	defer srv.Close()
	addr := NodeAddress(strings.TrimPrefix(srv.URL, "http://"))
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer down.Close()

	config := testConfig(t)
	config.OnlineURL = down.URL
	config.FundItnNodes = []NodeAddress{addr}
	// retryGetURL waits a minute between attempts; the discovery budget ends
	// the wait, and the stops must still have time after it.
	if err := StopOrphanedLoad(config, time.Second, func(NodeAddress, string) {}); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if got := strings.Join(d.stopped, ","); got != "h-fund" {
		t.Errorf("daemon received stops %q, want h-fund", got)
	}
}

// With neither uptime data nor funding nodes there is nothing to stop on,
// and that is an error rather than a silent success.
func TestStopOrphanedLoadWithNoNodesFails(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer down.Close()
	config := testConfig(t)
	config.OnlineURL = down.URL
	if err := StopOrphanedLoad(config, time.Second, func(NodeAddress, string) {}); err == nil {
		t.Fatal("no nodes at all reported success")
	}
}
