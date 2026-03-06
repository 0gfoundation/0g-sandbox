package main

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/0gfoundation/0g-sandbox-billing/internal/chain"
)

func main() {
	eth, _ := ethclient.Dial("https://evmrpc-testnet.0g.ai")
	privKey, _ := crypto.HexToECDSA("859c3bd1baf85767059b81448d0902d2bb649d137f0df460eb576915d15d58eb")
	addr := crypto.PubkeyToAddress(privKey.PublicKey)
	c, _ := chain.NewSandboxServing(common.HexToAddress("0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210"), eth)
	opts := &bind.CallOpts{Context: context.Background()}

	bal, _ := c.GetBalance(opts, addr, addr)
	nonce, _ := c.GetLastNonce(opts, addr, addr)
	earnings, _ := c.GetProviderEarnings(opts, addr)
	fmt.Printf("balance (self):     %s neuron\n", bal.Balance)
	fmt.Printf("nonce:              %s\n", nonce)
	fmt.Printf("earnings:           %s neuron\n", earnings)

	fmt.Println()
	svc, err := c.Services(opts, addr)
	if err != nil {
		fmt.Println("services error:", err)
		return
	}
	fmt.Printf("computePricePerMin: %s neuron/min\n", svc.ComputePricePerMin)
	perSec := new(big.Int).Div(svc.ComputePricePerMin, big.NewInt(60))
	fmt.Printf("computePricePerSec: %s neuron/sec (÷60)\n", perSec)
	fmt.Printf("expected per 60s:   %s neuron\n", new(big.Int).Mul(perSec, big.NewInt(60)))
	fmt.Printf("createFee:          %s neuron\n", svc.CreateFee)
	fmt.Printf("signerVersion:      %s\n", svc.SignerVersion)

	// Recent settled voucher events
	fmt.Println()
	fmt.Println("=== Recent VoucherSettled events (last 5000 blocks) ===")
	ctx := context.Background()
	chainClient := &chainReader{eth: eth, c: c, addr: common.HexToAddress("0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210")}
	_ = chainClient
	_ = ctx
}

type chainReader struct {
	eth  *ethclient.Client
	c    *chain.SandboxServing
	addr common.Address
}
