// cmd/setup performs the one-time on-chain setup required before running the
// e2e test:
//
//  1. AddOrUpdateService  — registers the TEE signer + pricing on the contract
//  2. Deposit             — funds the user account on the contract
//  3. AcknowledgeTEESigner — user accepts the TEE signer for this provider
//
// Since the e2e test uses a single account for TEE key / provider / user,
// all three transactions are signed by the same private key.
//
// Usage:
//
//	MOCK_TEE=true \
//	MOCK_APP_PRIVATE_KEY=0x<key> \
//	go run ./cmd/setup/ \
//	  --rpc      https://evmrpc-testnet.0g.ai \
//	  --chain-id 16602 \
//	  --contract 0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210 \
//	  --deposit  0.01
package main

import (
	"context"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/0gfoundation/0g-sandbox-billing/internal/chain"
)

func main() {
	rpc := flag.String("rpc", "https://evmrpc-testnet.0g.ai", "RPC endpoint")
	chainID := flag.Int64("chain-id", 16602, "Chain ID")
	contractHex := flag.String("contract", "0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210", "Contract address")
	depositEth := flag.Float64("deposit", 0.01, "0G amount to deposit into the contract")
	serviceURL := flag.String("url", "https://0g-sandbox.io", "Provider service URL")
	pricePerMin := flag.String("price-per-min", "0", "Compute price per minute in neuron")
	createFee := flag.String("create-fee", "0", "Create fee in neuron")
	flag.Parse()

	keyHex := strings.TrimPrefix(os.Getenv("MOCK_APP_PRIVATE_KEY"), "0x")
	if keyHex == "" {
		fmt.Fprintln(os.Stderr, "error: MOCK_APP_PRIVATE_KEY not set")
		os.Exit(1)
	}

	privKey, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		fatalf("parse private key: %v", err)
	}
	addr := crypto.PubkeyToAddress(privKey.PublicKey)
	fmt.Printf("account:  %s\n", addr.Hex())
	fmt.Printf("contract: %s\n", *contractHex)
	fmt.Printf("rpc:      %s\n", *rpc)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	eth, err := ethclient.Dial(*rpc)
	if err != nil {
		fatalf("dial rpc: %v", err)
	}
	defer eth.Close()

	contractAddr := common.HexToAddress(*contractHex)
	contract, err := chain.NewSandboxServing(contractAddr, eth)
	if err != nil {
		fatalf("bind contract: %v", err)
	}

	chainIDBig := big.NewInt(*chainID)
	auth, err := bind.NewKeyedTransactorWithChainID(privKey, chainIDBig)
	if err != nil {
		fatalf("build transactor: %v", err)
	}
	auth.Context = ctx

	// ── 1. AddOrUpdateService ─────────────────────────────────────────────────
	fmt.Println("\n[1/3] AddOrUpdateService...")
	pricePerMinBig, ok := new(big.Int).SetString(*pricePerMin, 10)
	if !ok {
		fatalf("invalid --price-per-min: %s", *pricePerMin)
	}
	createFeeBig, ok := new(big.Int).SetString(*createFee, 10)
	if !ok {
		fatalf("invalid --create-fee: %s", *createFee)
	}

	// Read providerStake from contract; attach it as msg.value on first registration.
	callOpts := &bind.CallOpts{Context: ctx}
	isRegistered, err := contract.ServiceExists(callOpts, addr)
	if err != nil {
		fatalf("ServiceExists: %v", err)
	}
	if !isRegistered {
		requiredStake, err := contract.ProviderStake(callOpts)
		if err != nil {
			fatalf("ProviderStake: %v", err)
		}
		if requiredStake.Sign() > 0 {
			auth.Value = requiredStake
			fmt.Printf("      stake required: %s neuron (first registration)\n", requiredStake.String())
		}
	}

	fmt.Printf("      price/min: %s neuron\n", pricePerMinBig.String())
	fmt.Printf("      create fee: %s neuron\n", createFeeBig.String())
	tx, err := contract.AddOrUpdateService(auth, *serviceURL, addr, pricePerMinBig, createFeeBig)
	auth.Value = big.NewInt(0) // reset after call
	if err != nil {
		fatalf("AddOrUpdateService: %v", err)
	}
	fmt.Printf("      tx: %s\n", tx.Hash().Hex())
	if _, err := bind.WaitMined(ctx, eth, tx); err != nil {
		fatalf("wait mined (AddOrUpdateService): %v", err)
	}
	fmt.Println("      confirmed ✓")

	// ── 2. Deposit ────────────────────────────────────────────────────────────
	// Deposit for self as provider (setup uses a single key for provider/user).
	fmt.Printf("\n[2/3] Deposit %.4f 0G (for provider %s)...\n", *depositEth, addr.Hex())
	depositWei := ogToNeuron(*depositEth)
	auth.Value = depositWei
	tx, err = contract.Deposit(auth, addr, addr)
	if err != nil {
		fatalf("Deposit: %v", err)
	}
	auth.Value = big.NewInt(0) // reset
	fmt.Printf("      tx: %s\n", tx.Hash().Hex())
	if _, err := bind.WaitMined(ctx, eth, tx); err != nil {
		fatalf("wait mined (Deposit): %v", err)
	}
	fmt.Println("      confirmed ✓")

	// ── 3. AcknowledgeTEESigner ───────────────────────────────────────────────
	fmt.Println("\n[3/3] AcknowledgeTEESigner...")
	// User acknowledges the provider (same account) as TEE signer.
	tx, err = contract.AcknowledgeTEESigner(auth, addr, true)
	if err != nil {
		fatalf("AcknowledgeTEESigner: %v", err)
	}
	fmt.Printf("      tx: %s\n", tx.Hash().Hex())
	if _, err := bind.WaitMined(ctx, eth, tx); err != nil {
		fatalf("wait mined (AcknowledgeTEESigner): %v", err)
	}
	fmt.Println("      confirmed ✓")

	// ── Summary ───────────────────────────────────────────────────────────────
	bal, err := contract.GetBalance(&bind.CallOpts{Context: ctx}, addr, addr)
	if err != nil {
		fatalf("GetBalance: %v", err)
	}
	fmt.Printf("\nSetup complete!\n")
	fmt.Printf("  on-chain balance (for self as provider): %s neuron\n", bal.Balance.String())
	fmt.Printf("  provider/user:    %s\n", addr.Hex())
}

func ogToNeuron(og float64) *big.Int {
	// 1 0G = 1e18 neuron; use integer arithmetic to avoid float precision issues.
	ogBig := new(big.Float).SetFloat64(og)
	neuronBig := new(big.Float).Mul(ogBig, new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)))
	neuron, _ := neuronBig.Int(nil)
	return neuron
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
