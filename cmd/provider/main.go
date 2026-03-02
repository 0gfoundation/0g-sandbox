// cmd/provider — provider-side management CLI
//
// Subcommands:
//
//	init-service   Register (or update) the service on the settlement contract
//
// Examples:
//
//	go run ./cmd/provider/ init-service \
//	  --tee-signer 0x61BEb835D1935Eec8cC04efa2f4e2B3cC8B8B6E3 \
//	  --url http://47.236.111.154:8080 \
//	  --price 1000020 \
//	  --fee 5000000
//
//	# Private key via env:
//	PROVIDER_KEY=0x<hex> go run ./cmd/provider/ init-service --tee-signer 0x... --url http://...
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
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: provider <subcommand> [flags]")
		fmt.Fprintln(os.Stderr, "  subcommands: init-service")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "init-service":
		runInitService(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		fmt.Fprintln(os.Stderr, "  subcommands: init-service")
		os.Exit(1)
	}
}

func runInitService(args []string) {
	fs := flag.NewFlagSet("init-service", flag.ExitOnError)
	rpc        := fs.String("rpc",        "https://evmrpc-testnet.0g.ai",                "RPC endpoint")
	chainID    := fs.Int64("chain-id",    16602,                                          "Chain ID")
	contractHex := fs.String("contract",  "0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210", "Settlement contract address")
	keyHex     := fs.String("key",        "",                                             "Provider private key (hex); or set PROVIDER_KEY env")
	teeSigner  := fs.String("tee-signer", "",                                             "TEE signer Ethereum address (required)")
	serviceURL := fs.String("url",        "",                                             "Provider service URL (required)")
	pricePerMin := fs.Int64("price",      1_000_020,                                      "Compute price per minute (neuron)")
	createFee  := fs.Int64("fee",         5_000_000,                                      "Create fee (neuron)")
	_ = fs.Parse(args)

	// Resolve private key: flag > env
	key := *keyHex
	if key == "" {
		key = os.Getenv("PROVIDER_KEY")
	}
	if key == "" {
		fatalf("provider private key required: use --key or PROVIDER_KEY env")
	}
	if *teeSigner == "" {
		fatalf("--tee-signer is required")
	}
	if *serviceURL == "" {
		fatalf("--url is required")
	}

	privKey, err := crypto.HexToECDSA(strings.TrimPrefix(key, "0x"))
	if err != nil {
		fatalf("parse private key: %v", err)
	}
	providerAddr := crypto.PubkeyToAddress(privKey.PublicKey)
	teeAddr := common.HexToAddress(*teeSigner)

	fmt.Printf("Provider:    %s\n", providerAddr.Hex())
	fmt.Printf("TEE signer:  %s\n", teeAddr.Hex())
	fmt.Printf("Contract:    %s\n", *contractHex)
	fmt.Printf("Service URL: %s\n", *serviceURL)
	fmt.Printf("Price/min:   %d neuron\n", *pricePerMin)
	fmt.Printf("Create fee:  %d neuron\n", *createFee)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	eth, err := ethclient.Dial(*rpc)
	if err != nil {
		fatalf("dial rpc: %v", err)
	}
	defer eth.Close()

	contract, err := chain.NewSandboxServing(common.HexToAddress(*contractHex), eth)
	if err != nil {
		fatalf("bind contract: %v", err)
	}

	auth, err := bind.NewKeyedTransactorWithChainID(privKey, big.NewInt(*chainID))
	if err != nil {
		fatalf("build transactor: %v", err)
	}
	auth.Context = ctx

	fmt.Println("\n[1/1] AddOrUpdateService...")
	tx, err := contract.AddOrUpdateService(auth,
		*serviceURL,
		teeAddr,
		big.NewInt(*pricePerMin),
		big.NewInt(*createFee),
	)
	if err != nil {
		fatalf("AddOrUpdateService: %v", err)
	}
	fmt.Printf("      tx: %s\n", tx.Hash().Hex())
	if _, err := bind.WaitMined(ctx, eth, tx); err != nil {
		fatalf("wait mined: %v", err)
	}
	fmt.Println("      confirmed ✓")

	fmt.Printf("\nDone. Set PROVIDER_ADDRESS=%s in your .env.\n", providerAddr.Hex())
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
