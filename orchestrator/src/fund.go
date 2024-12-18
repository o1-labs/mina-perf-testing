package itn_orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type FundParams struct {
	Amount      uint64   `json:"amount"`
	Fee         uint64   `json:"fee"`
	Prefix      string   `json:"prefix"`
	Num         int      `json:"num"`
	Privkeys    []string `json:"privkeys"`
	PasswordEnv string   `json:"passwordEnv,omitempty"`
}

type FundAction struct{}

func launchMultiple(ctx context.Context, perform func(ctx context.Context, spawnAction func(func() error))) error {
	var wg sync.WaitGroup
	errs := make(chan error)
	ctx, cancelF := context.WithCancel(ctx)
	defer cancelF()
	perform(ctx, func(run func() error) {
		wg.Add(1)
		go func() {
			if err := run(); err != nil {
				errs <- err
			} else {
				wg.Done()
			}
		}()
	})
	go func() {
		wg.Wait()
		errs <- nil
	}()
	return <-errs
}

func fundImpl(config Config, ctx context.Context, daemonPort string, params FundParams, amountPerKey uint64, password string) error {
	return launchMultiple(ctx, func(ctx context.Context, spawnAction func(func() error)) {
		for i, privkey := range params.Privkeys {
			num := params.Num / len(params.Privkeys)
			if i < params.Num%len(params.Privkeys) {
				num++
			}
			args := []string{
				"advanced", "itn-create-accounts",
				"--amount", strconv.FormatUint(amountPerKey*uint64(num), 10),
				"--fee", strconv.FormatUint(params.Fee, 10),
				"--key-prefix", fmt.Sprintf("%s-%d", params.Prefix, i),
				"--num-accounts", strconv.Itoa(num),
				"--privkey-path", privkey,
			}
			if daemonPort != "" {
				args = append(args, "--daemon-port", daemonPort)
			}
			cmd := exec.CommandContext(ctx, config.MinaExec, args...)
			cmd.Stderr = os.Stderr
			cmd.Stdout = os.Stderr
			cmd.Env = []string{"MINA_PRIVKEY_PASS=" + password}
			spawnAction(cmd.Run)
		}
	})
}

func fundRunImpl(config Config, ctx context.Context, daemonPortIx int, params FundParams, output OutputF) error {
	amountPerKey := params.Amount / uint64(params.Num)
	password := ""
	if params.PasswordEnv != "" {
		password, _ = os.LookupEnv(params.PasswordEnv)
	}
	return retryOnMultipleServers(config.FundDaemonPorts, daemonPortIx, "fund", config.Log, func(daemonPort string) error {
		return fundImpl(config, ctx, daemonPort, params, amountPerKey, password)
	})
}

func (FundAction) Run(config Config, rawParams json.RawMessage, output OutputF) error {
	var params FundParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return err
	}
	return fundRunImpl(config, config.Ctx, 0, params, output)
}

func (FundAction) Name() string { return "fund-keys" }

func memorize(cache map[string]struct{}, keys []string) bool {
	for _, k := range keys {
		_, has := cache[k]
		if has {
			return false
		}
		cache[k] = struct{}{}
	}
	return true
}

// Run consecutive commands that do not use common private keys in parallel
func (FundAction) RunMany(config Config, actionIOs []ActionIO) error {
	if len(actionIOs) == 0 {
		return nil
	}
	fundParams := make([]FundParams, len(actionIOs))
	for i, aIO := range actionIOs {
		if err := json.Unmarshal(aIO.Params, &fundParams[i]); err != nil {
			return err
		}
	}
	i := 0
	for i < len(actionIOs) {
		daemonPortIx := 0
		if len(config.FundDaemonPorts) > 0 {
			daemonPortIx = i % len(config.FundDaemonPorts)
		}
		usedKeys := map[string]struct{}{}
		err := launchMultiple(config.Ctx, func(ctx context.Context, spawnAction func(func() error)) {
			for ; i < len(actionIOs); i++ {
				fp := fundParams[i]
				out := actionIOs[i].Output
				if memorize(usedKeys, fp.Privkeys) {
					spawnAction(func() error {
						return fundRunImpl(config, ctx, daemonPortIx, fp, out)
					})
				} else {
					break
				}
			}
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (FundAction) Validate(rawParams json.RawMessage) error {
	var params FundParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return fmt.Errorf("failed to unmarshal fund-keys params: %v", err)
	}
	fundKeysBaseDir := extractBaseDir(params.Prefix)
	if pathExists(fundKeysBaseDir) {
		return fmt.Errorf("path '%s' already exists. Please re-generate script using unique experiment name or different '-fund-keys-dir' CLI argument value", fundKeysBaseDir)
	}
	return nil
}

// Helper function to extract the first two levels of the path
func extractBaseDir(prefix string) string {
	parts := strings.Split(filepath.ToSlash(prefix), "/")
	if len(parts) > 3 {
		return strings.Join(parts[:len(parts)-3], "/")
	}
	return prefix
}

// Helper function to check if a path exists
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

var _ BatchAction = FundAction{}
