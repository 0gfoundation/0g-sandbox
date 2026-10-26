package main

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

func TestCheckKmsNodeInfo(t *testing.T) {
	node, _ := crypto.GenerateKey()
	other, _ := crypto.GenerateKey()
	provider := common.HexToAddress("0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC")
	contract := common.HexToAddress("0x5991A2dF15A8F6A256D3Ec51E99254Cd3fb576A9")
	const chainID, appId = int64(16602), "0g-sandbox-provider"

	endorse := func(k []byte) string { return "0x" + hex.EncodeToString(k) }
	sig, err := voucher.EndorseKmsProvider(node, big.NewInt(chainID), contract, provider, appId)
	if err != nil {
		t.Fatal(err)
	}
	good := kmsNodeInfo{
		ProviderAddress:     provider.Hex(),
		ProviderKeySource:   "kms",
		ProviderEndorsement: endorse(sig),
		NodeAddress:         crypto.PubkeyToAddress(node.PublicKey).Hex(),
		ContractAddress:     contract.Hex(),
		AppId:               appId,
		ChainID:             chainID,
	}

	gotProvider, gotSig, err := checkKmsNodeInfo(good, chainID, contract, appId)
	if err != nil || gotProvider != provider || hex.EncodeToString(gotSig) != hex.EncodeToString(sig) {
		t.Fatalf("good info rejected: %v", err)
	}

	otherSig, _ := voucher.EndorseKmsProvider(other, big.NewInt(chainID), contract, provider, appId)
	for name, tc := range map[string]struct {
		mutate func(*kmsNodeInfo)
		want   string
	}{
		"local key source":  {func(i *kmsNodeInfo) { i.ProviderKeySource = "local" }, "PROVIDER_KEY_SOURCE=kms"},
		"other app":         {func(i *kmsNodeInfo) { i.AppId = "other" }, "not --app-id"},
		"other contract":    {func(i *kmsNodeInfo) { i.ContractAddress = "0x0000000000000000000000000000000000000001" }, "configured for contract"},
		"other chain":       {func(i *kmsNodeInfo) { i.ChainID = 16661 }, "configured for contract"},
		"endorsed by other": {func(i *kmsNodeInfo) { i.ProviderEndorsement = endorse(otherSig) }, "not the node"},
		"no endorsement":    {func(i *kmsNodeInfo) { i.ProviderEndorsement = "" }, "65"},
	} {
		info := good
		tc.mutate(&info)
		if _, _, err := checkKmsNodeInfo(info, chainID, contract, appId); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error mentioning %q", name, err, tc.want)
		}
	}
}
