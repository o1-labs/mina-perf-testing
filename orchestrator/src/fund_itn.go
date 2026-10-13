package itn_orchestrator

// Funding with the ITN createAccounts mutation (MinaProtocol/mina#19616): the
// daemon builds and sends the zkApp commands itself, so no mina client binary
// of the daemon's exact RPC version is needed.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	mina "github.com/MinaProtocol/mina-sdk-go"
	"github.com/MinaProtocol/mina-sdk-go/itn"
	"github.com/btcsuite/btcutil/base58"
)

// fundViaItn creates the accounts of params with createAccounts on the
// FundItnNodes, one job per funding key, and writes them to key files named
// as `mina advanced itn-create-accounts` names them (<prefix>-<i>-<n>), so
// the load-keys step reads them unchanged.
func fundViaItn(config Config, ctx context.Context, params FundParams, amountPerKey uint64, password string) error {
	if err := os.MkdirAll(filepath.Dir(params.Prefix), 0o700); err != nil {
		return err
	}
	return launchMultiple(ctx, func(ctx context.Context, spawnAction func(func() error)) {
		for i, privkeyPath := range params.Privkeys {
			num := params.Num / len(params.Privkeys)
			if i < params.Num%len(params.Privkeys) {
				num++
			}
			node := config.FundItnNodes[i%len(config.FundItnNodes)]
			i, privkeyPath := i, privkeyPath
			spawnAction(func() error {
				return createAccountsJob(config, ctx, node, privkeyPath, password,
					fmt.Sprintf("%s-%d", params.Prefix, i), num, params.Fee, amountPerKey*uint64(num))
			})
		}
	})
}

func createAccountsJob(config Config, ctx context.Context, node NodeAddress, privkeyPath, password, keyPrefix string, num int, fee, amount uint64) error {
	sk, err := LoadPrivateKey(privkeyPath, []byte(password))
	if err != nil {
		return err
	}
	client, err := GetGqlClient(config, node)
	if err != nil {
		return err
	}
	if !config.NodeData[node].HarnessSupport {
		return fmt.Errorf("fund node %s has no ITN harness support (needs MinaProtocol/mina#19616)", node)
	}
	handle, ok := config.writeAheadHandle(node, "create-accounts")
	if !ok {
		return fmt.Errorf("cannot create a handle for %s", node)
	}
	details := itn.CreateAccountsDetails{
		FeePayer:    base58.CheckEncode(sk, '\x5A'),
		NumAccounts: num,
		Fee:         mina.CurrencyFromNanomina(fee),
		Amount:      mina.CurrencyFromNanomina(amount),
	}
	var created *itn.CreatedAccounts
	if _, err := withHandleRetry(func() (string, error) {
		var err error
		created, err = client.CreateAccounts(ctx, details, handle)
		return "", err
	}); err != nil {
		return fmt.Errorf("createAccounts on %s: %w", node, err)
	}
	config.Log.Infof("fund-keys: %d accounts in job %s on %s", len(created.Accounts), handle, node)
	for n, account := range created.Accounts {
		fname := fmt.Sprintf("%s-%d", keyPrefix, n)
		if err := WritePrivateKeyFile(fname, account.PrivateKey, account.PublicKey, []byte(password)); err != nil {
			return fmt.Errorf("writing key file %s: %w", fname, err)
		}
	}
	return waitForJob(config, ctx, client, node, handle, 60*time.Minute)
}

// waitForJob waits until node no longer lists handle: the background job
// funded its accounts, or failed (the daemon logs why).
func waitForJob(config Config, ctx context.Context, client *itn.Client, node NodeAddress, handle string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		handles, err := client.ScheduledTransactions(ctx)
		if err != nil {
			return fmt.Errorf("listing schedulers on %s: %w", node, err)
		}
		listed := false
		for _, h := range handles {
			listed = listed || h == handle
		}
		if !listed {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("account creation %s on %s still runs after %s", handle, node, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}
