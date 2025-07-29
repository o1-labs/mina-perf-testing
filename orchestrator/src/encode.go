package itn_orchestrator

import (
	"fmt"
	"os"
	"strings"
)

func fund(p FundParams) GeneratedCommand {
	return GeneratedCommand{Action: FundAction{}.Name(), Params: p}
}

func Encode(p *GenParams, writeCommand func(GeneratedCommand), writeComment func(string)) {

	writeComment("Generated with: " + strings.Join(os.Args, " "))
	if p.ZkappSoftLimit > -2 {
		writeCommand(Discovery(DiscoveryParams{}))
		writeComment(fmt.Sprintf("Setting zkapp soft limit to %d", p.ZkappSoftLimit))
		writeCommand(ZkappSoftLimit(-1, "participant", p.ZkappSoftLimit))
	}
	cmds := []GeneratedCommand{}
	fundCmds := []FundParams{}
	writeComment("Funding keys for the experiment")
	for r := 0; r < p.Rounds; r++ {
		round := p.Generate(r)
		cmds = append(cmds, round.Commands...)
		if round.PaymentFundCommand != nil {
			fundCmds = append(fundCmds, *round.PaymentFundCommand)
		}
		if round.ZkappFundCommand != nil {
			fundCmds = append(fundCmds, *round.ZkappFundCommand)
		}
	}
	privkeys := p.Privkeys
	if p.GenerateFundKeys > 0 {
		fundKeysDir := fmt.Sprintf("%s/%s", p.FundKeyPrefix, p.ExperimentName)
		privkeys = make([]string, p.GenerateFundKeys)
		privkeyAmounts := make([]uint64, p.GenerateFundKeys)
		for i := range privkeys {
			privkeys[i] = fmt.Sprintf("%s/key-0-%d", fundKeysDir, i)
		}
		for i, f := range fundCmds {
			i_ := (i * p.PrivkeysPerFundCmd) % p.GenerateFundKeys
			itemsPerFundKey := f.Num/p.PrivkeysPerFundCmd + 1
			perGeneratedKey := f.Amount / uint64(f.Num) * uint64(itemsPerFundKey)
			for j := i_; j < (i_ + p.PrivkeysPerFundCmd); j++ {
				j_ := j % p.GenerateFundKeys
				privkeyAmounts[j_] += perGeneratedKey
			}
		}
		perKeyAmount := privkeyAmounts[0]
		for _, a := range privkeyAmounts[1:] {
			if perKeyAmount < a {
				perKeyAmount = a
			}
		}
		// Generate funding keys
		writeCommand(fund(FundParams{
			PasswordEnv: p.PasswordEnv,
			Privkeys:    p.Privkeys,
			Prefix:      fundKeysDir + "/key",
			Amount:      perKeyAmount*uint64(p.GenerateFundKeys)*3/2 + 2e9,
			Fee:         p.FundFee,
			Num:         p.GenerateFundKeys,
		}))
		writeCommand(GenWait(1))
	}
	privkeysExt := append(privkeys, privkeys...)
	for i, cmd := range fundCmds {
		i_ := (i * p.PrivkeysPerFundCmd) % len(privkeys)
		cmd.Privkeys = privkeysExt[i_:(i_ + p.PrivkeysPerFundCmd)]
		writeCommand(fund(cmd))
	}
	for _, cmd := range cmds {
		writeCommand(cmd)
	}
}
