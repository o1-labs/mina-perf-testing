package itn_orchestrator

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	mina "github.com/MinaProtocol/mina-sdk-go"
	"github.com/MinaProtocol/mina-sdk-go/itn"
	logging "github.com/ipfs/go-log/v2"

	"itn_json_types"
)

func TestStatusOfMapsSDKErrors(t *testing.T) {
	for _, c := range []struct {
		err       error
		status    int
		permanent bool
	}{
		{&itn.UnauthorizedError{QueryName: "q"}, 401, true},
		{&itn.SequencingError{QueryName: "q"}, 412, false},
		{&mina.ConnectionError{QueryName: "q", Retries: 1, LastError: &itn.HTTPError{StatusCode: 400}}, 400, true},
		{&mina.ConnectionError{QueryName: "q", Retries: 1, LastError: &itn.HTTPError{StatusCode: 429}}, 429, false},
		{&mina.ConnectionError{QueryName: "q", Retries: 1, LastError: &itn.HTTPError{StatusCode: 503}}, 503, false},
		{&mina.ConnectionError{QueryName: "q", Retries: 1, LastError: errors.New("connection refused")}, 0, false},
		{&mina.GraphQLError{QueryName: "q", Errors: []mina.GraphQLErrorEntry{{Message: "Memo too long"}}}, 200, false},
		{context.Canceled, 0, false},
	} {
		wrapped := fmt.Errorf("outer: %w", c.err)
		e := &GqlRequestError{Node: "n:1", StatusCode: statusOf(wrapped), Err: wrapped}
		if e.StatusCode != c.status || e.Permanent() != c.permanent {
			t.Errorf("%T %v: status %d permanent %v; want %d %v", c.err, c.err, e.StatusCode, e.Permanent(), c.status, c.permanent)
		}
	}
}

// fakeItnDaemon answers auth (with a peer ID that changes when restart is
// set) and schedulePayments; a restart makes the next sequenced request
// fail with 412, as a restarted daemon does.
type fakeItnDaemon struct {
	mu      sync.Mutex
	peerID  string
	restart bool
	inputs  []map[string]any
}

func (f *fakeItnDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	_ = json.Unmarshal(body, &req)
	if strings.Contains(req.Query, "auth {") {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"auth": map[string]any{
			"serverUuid": "u-" + f.peerID, "signerSequenceNumber": "0", "libp2pPort": "8302",
			"peerId": f.peerID, "isBlockProducer": false}}})
		return
	}
	if f.restart {
		f.restart = false
		f.peerID = "peer-after-restart"
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	f.inputs = append(f.inputs, req.Variables["input"].(map[string]any))
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"schedulePayments": "handle-1"}})
}

func testConfig(t *testing.T) Config {
	t.Helper()
	_, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		Ctx:      context.Background(),
		Sk:       sk,
		NodeData: map[NodeAddress]NodeEntry{},
		Log:      logging.Logger("test"),
	}
}

// After the daemon restarts, the SDK runs auth again by itself; the node
// data (peer ID for the gating actions) must follow.
func TestNodeDataFollowsReauth(t *testing.T) {
	f := &fakeItnDaemon{peerID: "peer-before"}
	srv := httptest.NewServer(f)
	defer srv.Close()
	config := testConfig(t)
	addr := NodeAddress(strings.TrimPrefix(srv.URL, "http://"))
	input := PaymentsDetails{DurationMin: 1, Tps: 0.5, MemoPrefix: "m", MaxFee: 2, MinFee: 1, Amount: 1000,
		Receiver: "B62qreceiver", Senders: []itn_json_types.MinaPrivateKey{"EKsender"}}

	if h, err := SchedulePaymentsGql(config, addr, input); err != nil || h != "handle-1" {
		t.Fatalf("handle %q, err %v", h, err)
	}
	if got := config.NodeData[addr].PeerId; got != "peer-before" {
		t.Fatalf("peer ID %q, want peer-before", got)
	}
	f.mu.Lock()
	f.restart = true
	f.mu.Unlock()
	if _, err := SchedulePaymentsGql(config, addr, input); err != nil {
		t.Fatal(err)
	}
	if got := config.NodeData[addr].PeerId; got != "peer-after-restart" {
		t.Errorf("peer ID %q after the daemon restarted, want peer-after-restart", got)
	}
	in := f.inputs[0]
	if in["amount"] != "1000" || in["maxFee"] != "2" || in["receiver"] != "B62qreceiver" ||
		fmt.Sprint(in["senders"]) != "[EKsender]" {
		t.Errorf("unexpected input %v", in)
	}
}

func TestZkappInputSendsNonDefaultTokenOnlyWhenTrue(t *testing.T) {
	d := ZkappCommandsDetails{MaxAccountUpdates: 2, Tps: 0.1, MaxFee: 5}
	if got := d.toItn(); got.NonDefaultToken != nil || *got.MaxAccountUpdates != 2 || got.MaxFee.Nanomina() != 5 {
		t.Errorf("unexpected conversion %+v", got)
	}
	d.NonDefaultToken = true
	if got := d.toItn(); got.NonDefaultToken == nil || !*got.NonDefaultToken {
		t.Error("NonDefaultToken=true must be sent")
	}
}
