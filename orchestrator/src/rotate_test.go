package itn_orchestrator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRestServerGraphQL(t *testing.T) {
	for in, want := range map[string]string{
		"":                               "http://127.0.0.1:3085/graphql",
		"3085":                           "http://127.0.0.1:3085/graphql",
		"10.0.0.5:3085":                  "http://10.0.0.5:3085/graphql",
		"http://node-1:3085":             "http://node-1:3085/graphql",
		"http://node-1:3085/":            "http://node-1:3085/graphql",
		"https://node-1.example/graphql": "https://node-1.example/graphql",
		"http://node-1:3085/custom/path": "http://node-1:3085/custom/path",
	} {
		if got := restServerGraphQL(in); got != want {
			t.Errorf("restServerGraphQL(%q) = %q, want %q", in, got, want)
		}
	}
}

// fakePublicDaemon answers the public GraphQL operations rotate uses and
// records the variables it received.
func fakePublicDaemon(t *testing.T) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var seen []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen = append(seen, req.Variables)
		var data any
		switch {
		case strings.Contains(req.Query, "unlockAccount"):
			data = map[string]any{"unlockAccount": map[string]any{"publicKey": "B62qsender"}}
		case strings.Contains(req.Query, "sendPayment"):
			data = map[string]any{"sendPayment": map[string]any{"payment": map[string]any{
				"id": "id", "hash": "hash", "nonce": "1"}}}
		case strings.Contains(req.Query, "account"):
			data = map[string]any{"account": map[string]any{
				"publicKey": "B62qsender", "nonce": "0",
				"balance": map[string]any{"total": "1500000000"}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestRotateUsesPublicGraphQL(t *testing.T) {
	srv, seen := fakePublicDaemon(t)
	server := srv.URL + "/graphql"

	balance, err := getBalance(Config{}, server, "B62qsender")
	if err != nil || balance != 1_500_000_000 {
		t.Fatalf("getBalance = %d, %v; want 1500000000", balance, err)
	}
	if err := unlockPrivkey(Config{}, server, "B62qsender", "secret"); err != nil {
		t.Fatal(err)
	}
	if err := sendPayment(Config{}, server, "B62qsender", "B62qreceiver", 700_000_000, 2_000_000_000); err != nil {
		t.Fatal(err)
	}

	unlock := (*seen)[1]["input"].(map[string]any)
	if unlock["publicKey"] != "B62qsender" || unlock["password"] != "secret" {
		t.Errorf("unlock input %v", unlock)
	}
	pay := (*seen)[2]["input"].(map[string]any)
	if pay["from"] != "B62qsender" || pay["to"] != "B62qreceiver" ||
		pay["amount"] != "700000000" || pay["fee"] != "2000000000" || pay["memo"] != "rotation" {
		t.Errorf("payment input %v", pay)
	}
}
