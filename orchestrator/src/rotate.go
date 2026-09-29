package itn_orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	mina "github.com/MinaProtocol/mina-sdk-go"
)

type RotateParams struct {
	Pubkeys []string `json:"pubkeys"`

	RestServers []string `json:"servers"`

	// Ratio of each private key's balance to be used in the rotation
	Ratio float64 `json:"ratio"`

	// Mapping is an array of receiver indexes:
	//   - size of array equals `n`
	//   - each index is from `0` to `n - 1` inclusive
	//   - value `j` at index `i` means that in this rotation key `i` sends a payment to key `j`
	Mapping []int `json:"mapping"`

	Fee uint64 `json:"fee,omitempty"`

	PasswordEnv string `json:"passwordEnv,omitempty"`
}

type RotateAction struct{}

func (RotateAction) Run(config Config, rawParams json.RawMessage, output OutputF) error {
	var params RotateParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return err
	}
	if len(params.RestServers) != len(params.Pubkeys) {
		return errors.New("length of list of rest servers is not equal to number of key files")
	}
	if len(params.Mapping) != len(params.Pubkeys) {
		return errors.New("length of mapping is not equal to number of key files")
	}
	if params.Ratio < 1e-3 {
		return errors.New("ratio too small")
	}
	password := ""
	if params.PasswordEnv != "" {
		password, _ = os.LookupEnv(params.PasswordEnv)
	}
	for _, m := range params.Mapping {
		if m < 0 || m >= len(params.Pubkeys) {
			return errors.New("wrong index in the mapping")
		}
	}
	balances := make([]uint64, len(params.Pubkeys))
	for i, pk := range params.Pubkeys {
		err := retryOnMultipleServers(params.RestServers, config.Ctx, i, "rotate-get-balance", config.Log, func(restServer string) error {
			var err error
			balances[i], err = getBalance(config, restServer, pk)
			return err
		})
		if err != nil {
			return fmt.Errorf("failed to get balance of public key %s: %s", pk, err)
		}
	}
	config.Log.Infof("Retrieved balances for rotation: %v", balances)
	fee := params.Fee
	if fee == 0 {
		fee = 2e9
	}
	for senderIx, receiverIx := range params.Mapping {
		senderPk := params.Pubkeys[senderIx]
		restServer := params.RestServers[senderIx]
		err := unlockPrivkey(config, restServer, senderPk, password)
		if err != nil {
			config.Log.Warnf("Failed to unlock key %s on server %s", senderPk, restServer)
			continue
		}
		amount := uint64(float64(balances[senderIx]-fee) * params.Ratio)
		receiverPk := params.Pubkeys[receiverIx]
		err = sendPayment(config, restServer, senderPk, receiverPk, amount, fee)
		if err == nil {
			config.Log.Infof("Rotated: %s -> %s (%d nanomina)", senderPk, receiverPk, amount)
		} else {
			config.Log.Warnf("Failed to rotate key %s on server %s", senderPk, restServer)
		}
	}
	return nil
}

func (RotateAction) Name() string { return "rotate-balance" }

// restServerGraphQL turns a rotation server, given in the form of the mina
// CLI's --rest-server, into a GraphQL endpoint: a port number means
// http://127.0.0.1:<port>/graphql, a URL is used as it is (with /graphql
// added when it has no path), and host:port means http://host:port/graphql.
// An empty value is the daemon's default endpoint.
func restServerGraphQL(restServer string) string {
	switch {
	case restServer == "":
		return mina.DefaultGraphQLURI
	case isDigits(restServer):
		return "http://127.0.0.1:" + restServer + "/graphql"
	case strings.Contains(restServer, "://"):
		if u, err := url.Parse(restServer); err == nil && (u.Path == "" || u.Path == "/") {
			u.Path = "/graphql"
			return u.String()
		}
		return restServer
	default:
		return "http://" + restServer + "/graphql"
	}
}

func isDigits(s string) bool {
	_, err := strconv.ParseUint(s, 10, 16)
	return err == nil
}

// publicClient is a client for a daemon's public GraphQL. Like the mina CLI
// it replaced, it makes one attempt; the callers retry across servers.
func publicClient(restServer string) *mina.Client {
	return mina.NewClient(mina.WithGraphQLURI(restServerGraphQL(restServer)), mina.WithRetries(1))
}

func getBalance(_ Config, restServer, pubkey string) (uint64, error) {
	client := publicClient(restServer)
	defer client.Close()
	account, err := client.GetAccount(pubkey, "")
	if err != nil {
		return 0, err
	}
	return account.Balance.Total.Nanomina(), nil
}

func sendPayment(_ Config, restServer, senderPk, receiverPk string, amount, fee uint64) error {
	client := publicClient(restServer)
	defer client.Close()
	_, err := client.SendPayment(mina.SendPaymentParams{
		Sender:   senderPk,
		Receiver: receiverPk,
		Amount:   mina.CurrencyFromNanomina(amount),
		Fee:      mina.CurrencyFromNanomina(fee),
		Memo:     "rotation",
	})
	return err
}

func unlockPrivkey(_ Config, restServer, pubkey, password string) error {
	client := publicClient(restServer)
	defer client.Close()
	_, err := client.UnlockAccount(pubkey, password)
	return err
}
