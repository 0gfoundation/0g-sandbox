package main

import (
	"context"
	"fmt"
	"math/big"
	"os"

	"github.com/0gfoundation/0g-sandbox/internal/chain"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	rpc := getEnv("RPC_URL", "https://evmrpc-testnet.0g.ai")
	contractAddr := getEnv("SETTLEMENT_CONTRACT", "0x3D0F2D62A60c8e62095671FfB23D15Cc4C98ca7c")
	user := common.HexToAddress(getEnv("USER_ADDR", "0x2ff0F380d85543e0Ab6D32eba80DA7F3dB332dcB"))
	provider := common.HexToAddress(getEnv("PROVIDER_ADDR", "0xB831371eb2703305f1d9F8542163633D0675CEd7"))

	eth, err := ethclient.Dial(rpc)
	if err != nil {
		panic(err)
	}
	contract, err := chain.NewSandboxServing(common.HexToAddress(contractAddr), eth)
	if err != nil {
		panic(err)
	}

	balances, err := contract.BalanceOfBatch(&bind.CallOpts{Context: context.Background()}, []common.Address{user}, provider)
	if err != nil {
		panic(err)
	}

	b := balances[0]
	og := new(big.Float).Quo(new(big.Float).SetInt(b), new(big.Float).SetFloat64(1e18))
	fmt.Printf("user:     %s\nprovider: %s\nbalance:  %s neuron (%.6f 0G)\n", user.Hex(), provider.Hex(), b.String(), og)
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
