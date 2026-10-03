package itn_orchestrator

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	mina "github.com/MinaProtocol/mina-sdk-go"
	"github.com/MinaProtocol/mina-sdk-go/itn"
	"github.com/btcsuite/btcutil/base58"
	logging "github.com/ipfs/go-log/v2"
)

func TestKeyFileRoundTrip(t *testing.T) {
	sk := append([]byte{1}, make([]byte, 32)...)
	sk[5] = 42
	privateKey := base58.CheckEncode(sk, '\x5A')
	fname := filepath.Join(t.TempDir(), "key-0-0")
	if err := WritePrivateKeyFile(fname, privateKey, "B62qpub", []byte("pw")); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPrivateKey(fname, []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if base58.CheckEncode(got, '\x5A') != privateKey {
		t.Fatalf("round trip gave %x, want %x", got, sk)
	}
	if _, err := LoadPrivateKey(fname, []byte("wrong")); err == nil {
		t.Error("a wrong password opened the key file")
	}
	// The daemon (libsodium) accepts only a 16-byte salt.
	raw, _ := os.ReadFile(fname)
	var box secretBox
	if err := json.Unmarshal(raw, &box); err != nil || len(box.Pwsalt) != 16 || len(box.Nonce) != 24 {
		t.Errorf("salt %d bytes, nonce %d bytes, err %v", len(box.Pwsalt), len(box.Nonce), err)
	}
	if pub, _ := os.ReadFile(fname + ".pub"); string(pub) != "B62qpub\n" {
		t.Errorf("public key file = %q", pub)
	}
	// load-keys lists the key file and skips the .pub file.
	files, err := listKeyfiles(filepath.Dir(fname))
	if err != nil || len(files) != 1 {
		t.Errorf("listKeyfiles = %v, %v", files, err)
	}
	if err := WritePrivateKeyFile(fname, base58.CheckEncode(sk, '\x02'), "B62q", nil); err == nil {
		t.Error("a key with the wrong version byte was accepted")
	}
}

func TestWriteAheadHandle(t *testing.T) {
	journal := filepath.Join(t.TempDir(), "handles.jsonl")
	config := Config{
		Log:           logging.Logger("test"),
		HandleJournal: journal,
		NodeData: map[NodeAddress]NodeEntry{
			"new:3086": {HarnessSupport: true},
			"old:3086": {},
		},
	}
	if _, ok := config.writeAheadHandle("old:3086", "payments"); ok {
		t.Error("a node without harness support got a handle")
	}
	h1, ok1 := config.writeAheadHandle("new:3086", "payments")
	h2, ok2 := config.writeAheadHandle("new:3086", "zkapps")
	if !ok1 || !ok2 || h1 == h2 {
		t.Fatalf("handles %q %v, %q %v", h1, ok1, h2, ok2)
	}
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuid.MatchString(h1) {
		t.Errorf("handle %q is not a version 4 UUID", h1)
	}
	f, err := os.Open(journal)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var entries []journalEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e journalEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	if len(entries) != 2 || entries[0].Handle != h1 || entries[1].Kind != "zkapps" || entries[0].Node != "new:3086" {
		t.Errorf("journal = %+v", entries)
	}
}

func TestOnlyTransportErrorsAreRepeated(t *testing.T) {
	for _, c := range []struct {
		err    error
		repeat bool
	}{
		{errors.New("connection reset"), true},
		{&mina.ConnectionError{QueryName: "q", LastError: context.DeadlineExceeded}, true},
		{&mina.GraphQLError{QueryName: "q"}, false},
		{&mina.ConnectionError{QueryName: "q", LastError: &itn.HTTPError{StatusCode: 500}}, false},
		{&itn.UnauthorizedError{QueryName: "q"}, false},
		{&itn.SequencingError{QueryName: "q"}, false},
	} {
		if got := isTransportError(c.err); got != c.repeat {
			t.Errorf("isTransportError(%v) = %v, want %v", c.err, got, c.repeat)
		}
	}
	calls := 0
	_, err := withHandleRetry(func() (string, error) {
		calls++
		return "", &mina.GraphQLError{QueryName: "q"}
	})
	if err == nil || calls != 1 {
		t.Errorf("a GraphQL error was repeated: %d calls, err %v", calls, err)
	}
}
