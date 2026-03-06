// cmd/provider — provider-side management CLI
//
// Subcommands:
//
//	register    Register (or update) the service on the settlement contract
//	status      Show provider registration, stake, and earnings
//	withdraw    Withdraw accumulated earnings
//	set-stake   (owner only) Update the minimum stake required for new providers
//
// Examples:
//
//	PROVIDER_KEY=0x<hex> go run ./cmd/provider/ register \
//	  --contract 0x... \
//	  --url https://provider.example.com \
//	  --price 1000000000000000 \
//	  --fee 60000000000000000
//
//	PROVIDER_KEY=0x<hex> go run ./cmd/provider/ status   --contract 0x...
//	PROVIDER_KEY=0x<hex> go run ./cmd/provider/ withdraw --contract 0x...
//	OWNER_KEY=0x<hex>    go run ./cmd/provider/ set-stake --contract 0x... --stake 100000000000000000
package main

import (
	"context"
	"crypto/ecdsa"
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

const (
	defaultRPC      = "https://evmrpc-testnet.0g.ai"
	defaultChainID  = int64(16602)
	defaultContract = "0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: provider <subcommand> [flags]")
		fmt.Fprintln(os.Stderr, "  subcommands: register | status | withdraw | set-stake")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "register", "init-service":
		runRegister(os.Args[2:])
	case "status":
		runStatus(os.Args[2:])
	case "withdraw":
		runWithdraw(os.Args[2:])
	case "set-stake":
		runSetStake(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		fmt.Fprintln(os.Stderr, "  subcommands: register | status | withdraw | set-stake")
		os.Exit(1)
	}
}

// ── register ──────────────────────────────────────────────────────────────────

func runRegister(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	rpc         := fs.String("rpc",        defaultRPC,              "RPC endpoint")
	chainID     := fs.Int64("chain-id",    defaultChainID,          "Chain ID")
	contractHex := fs.String("contract",   defaultContract,         "Settlement contract address")
	keyHex      := fs.String("key",        "",                      "Provider private key (hex); or set PROVIDER_KEY env")
	teeSigner   := fs.String("tee-signer", "",                      "TEE signer address (defaults to provider address)")
	serviceURL  := fs.String("url",        "",                      "Provider service URL (required)")
	pricePerMin := fs.String("price",      "1000000000000000",      "Compute price per minute (neuron)")
	createFee   := fs.String("fee",        "60000000000000000",     "Create fee per sandbox (neuron)")
	_ = fs.Parse(args)

	if *serviceURL == "" {
		fatalf("--url is required")
	}
	privKey := resolveKey(*keyHex, "PROVIDER_KEY")
	providerAddr := crypto.PubkeyToAddress(privKey.PublicKey)

	teeAddr := providerAddr // default: TEE signer == provider (single-key / dev mode)
	if *teeSigner != "" {
		teeAddr = common.HexToAddress(*teeSigner)
	}
	pricePerMinBig := parseBigInt(*pricePerMin, "--price")
	createFeeBig := parseBigInt(*createFee, "--fee")

	fmt.Printf("Provider:    %s\n", providerAddr.Hex())
	fmt.Printf("TEE signer:  %s\n", teeAddr.Hex())
	fmt.Printf("Contract:    %s\n", *contractHex)
	fmt.Printf("Service URL: %s\n", *serviceURL)
	fmt.Printf("Price/min:   %s neuron\n", pricePerMinBig.String())
	fmt.Printf("Create fee:  %s neuron\n", createFeeBig.String())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	eth, contract := dialContract(ctx, *rpc, *contractHex)
	defer eth.Close()

	callOpts := &bind.CallOpts{Context: ctx}
	isRegistered, err := contract.ServiceExists(callOpts, providerAddr)
	if err != nil {
		fatalf("ServiceExists: %v", err)
	}

	auth := buildAuth(ctx, privKey, *chainID)
	if !isRegistered {
		// First registration: auto-read required stake and attach as msg.value.
		requiredStake, err := contract.ProviderStake(callOpts)
		if err != nil {
			fatalf("ProviderStake: %v", err)
		}
		if requiredStake.Sign() > 0 {
			auth.Value = requiredStake
			fmt.Printf("Stake:       %s neuron (first registration, attached automatically)\n", requiredStake.String())
		}
	} else {
		fmt.Println("Already registered — updating service (no stake required)")
	}

	fmt.Println("\n[1/1] AddOrUpdateService...")
	tx, err := contract.AddOrUpdateService(auth, *serviceURL, teeAddr, pricePerMinBig, createFeeBig)
	auth.Value = big.NewInt(0)
	if err != nil {
		fatalf("AddOrUpdateService: %v", err)
	}
	fmt.Printf("      tx: %s\n", tx.Hash().Hex())
	if _, err := bind.WaitMined(ctx, eth, tx); err != nil {
		fatalf("wait mined: %v", err)
	}
	fmt.Println("      confirmed ✓")
	fmt.Printf("\nDone. Provider address: %s\n", providerAddr.Hex())
}

// ── status ────────────────────────────────────────────────────────────────────

func runStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	rpc         := fs.String("rpc",      defaultRPC,      "RPC endpoint")
	contractHex := fs.String("contract", defaultContract, "Settlement contract address")
	keyHex      := fs.String("key",      "",              "Provider private key; or set PROVIDER_KEY env")
	addrHex     := fs.String("address",  "",              "Provider address (alternative to --key)")
	_ = fs.Parse(args)

	var providerAddr common.Address
	if *addrHex != "" {
		providerAddr = common.HexToAddress(*addrHex)
	} else {
		privKey := resolveKey(*keyHex, "PROVIDER_KEY")
		providerAddr = crypto.PubkeyToAddress(privKey.PublicKey)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	eth, contract := dialContract(ctx, *rpc, *contractHex)
	defer eth.Close()

	opts := &bind.CallOpts{Context: ctx}

	registered, err := contract.ServiceExists(opts, providerAddr)
	if err != nil {
		fatalf("ServiceExists: %v", err)
	}
	requiredStake, err := contract.ProviderStake(opts)
	if err != nil {
		fatalf("ProviderStake: %v", err)
	}
	owner, err := contract.Owner(opts)
	if err != nil {
		fatalf("Owner: %v", err)
	}

	fmt.Printf("Provider:       %s\n", providerAddr.Hex())
	fmt.Printf("Contract:       %s\n", *contractHex)
	fmt.Printf("Registered:     %v\n", registered)
	fmt.Printf("Required stake: %s neuron\n", requiredStake.String())
	fmt.Printf("Contract owner: %s\n", owner.Hex())

	if registered {
		svc, err := contract.Services(opts, providerAddr)
		if err != nil {
			fatalf("Services: %v", err)
		}
		myStake, err := contract.ProviderStakes(opts, providerAddr)
		if err != nil {
			fatalf("ProviderStakes: %v", err)
		}
		earnings, err := contract.ProviderEarnings(opts, providerAddr)
		if err != nil {
			fatalf("ProviderEarnings: %v", err)
		}
		fmt.Printf("\nService:\n")
		fmt.Printf("  URL:          %s\n", svc.Url)
		fmt.Printf("  TEE signer:   %s\n", svc.TeeSignerAddress.Hex())
		fmt.Printf("  Price/min:    %s neuron\n", svc.ComputePricePerMin.String())
		fmt.Printf("  Create fee:   %s neuron\n", svc.CreateFee.String())
		fmt.Printf("  Signer ver:   %s\n", svc.SignerVersion.String())
		fmt.Printf("  My stake:     %s neuron\n", myStake.String())
		fmt.Printf("  Earnings:     %s neuron\n", earnings.String())
	}
}

// ── withdraw ──────────────────────────────────────────────────────────────────

func runWithdraw(args []string) {
	fs := flag.NewFlagSet("withdraw", flag.ExitOnError)
	rpc         := fs.String("rpc",      defaultRPC,      "RPC endpoint")
	chainID     := fs.Int64("chain-id",  defaultChainID,  "Chain ID")
	contractHex := fs.String("contract", defaultContract, "Settlement contract address")
	keyHex      := fs.String("key",      "",              "Provider private key; or set PROVIDER_KEY env")
	_ = fs.Parse(args)

	privKey := resolveKey(*keyHex, "PROVIDER_KEY")
	providerAddr := crypto.PubkeyToAddress(privKey.PublicKey)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	eth, contract := dialContract(ctx, *rpc, *contractHex)
	defer eth.Close()

	opts := &bind.CallOpts{Context: ctx}
	earnings, err := contract.ProviderEarnings(opts, providerAddr)
	if err != nil {
		fatalf("ProviderEarnings: %v", err)
	}
	if earnings.Sign() == 0 {
		fmt.Println("No earnings to withdraw.")
		return
	}
	fmt.Printf("Provider:  %s\n", providerAddr.Hex())
	fmt.Printf("Earnings:  %s neuron\n", earnings.String())

	fmt.Println("\nWithdrawing earnings...")
	tx, err := contract.WithdrawEarnings(buildAuth(ctx, privKey, *chainID))
	if err != nil {
		fatalf("WithdrawEarnings: %v", err)
	}
	fmt.Printf("  tx: %s\n", tx.Hash().Hex())
	if _, err := bind.WaitMined(ctx, eth, tx); err != nil {
		fatalf("wait mined: %v", err)
	}
	fmt.Printf("  confirmed ✓  (%s neuron withdrawn)\n", earnings.String())
}

// ── set-stake ─────────────────────────────────────────────────────────────────

func runSetStake(args []string) {
	fs := flag.NewFlagSet("set-stake", flag.ExitOnError)
	rpc         := fs.String("rpc",      defaultRPC,      "RPC endpoint")
	chainID     := fs.Int64("chain-id",  defaultChainID,  "Chain ID")
	contractHex := fs.String("contract", defaultContract, "Settlement contract address")
	keyHex      := fs.String("key",      "",              "Owner private key; or set OWNER_KEY env")
	stakeStr    := fs.String("stake",    "",              "New providerStake value in neuron (required)")
	_ = fs.Parse(args)

	if *stakeStr == "" {
		fatalf("--stake is required")
	}
	newStake := parseBigInt(*stakeStr, "--stake")
	privKey := resolveKey(*keyHex, "OWNER_KEY")
	ownerAddr := crypto.PubkeyToAddress(privKey.PublicKey)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	eth, contract := dialContract(ctx, *rpc, *contractHex)
	defer eth.Close()

	opts := &bind.CallOpts{Context: ctx}
	currentStake, err := contract.ProviderStake(opts)
	if err != nil {
		fatalf("ProviderStake: %v", err)
	}
	fmt.Printf("Owner:          %s\n", ownerAddr.Hex())
	fmt.Printf("Current stake:  %s neuron\n", currentStake.String())
	fmt.Printf("New stake:      %s neuron\n", newStake.String())

	fmt.Println("\nSetting provider stake...")
	tx, err := contract.SetProviderStake(buildAuth(ctx, privKey, *chainID), newStake)
	if err != nil {
		fatalf("SetProviderStake: %v", err)
	}
	fmt.Printf("  tx: %s\n", tx.Hash().Hex())
	if _, err := bind.WaitMined(ctx, eth, tx); err != nil {
		fatalf("wait mined: %v", err)
	}
	fmt.Println("  confirmed ✓")
}

// ── helpers ───────────────────────────────────────────────────────────────────

func resolveKey(flagVal, envVar string) *ecdsa.PrivateKey {
	hex := flagVal
	if hex == "" {
		hex = os.Getenv(envVar)
	}
	if hex == "" {
		fatalf("private key required: use --key or %s env", envVar)
	}
	privKey, err := crypto.HexToECDSA(strings.TrimPrefix(hex, "0x"))
	if err != nil {
		fatalf("parse private key: %v", err)
	}
	return privKey
}

func parseBigInt(s, name string) *big.Int {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		fatalf("invalid %s value: %s", name, s)
	}
	return v
}

func dialContract(ctx context.Context, rpcURL, contractHex string) (*ethclient.Client, *chain.SandboxServing) {
	eth, err := ethclient.Dial(rpcURL)
	if err != nil {
		fatalf("dial rpc: %v", err)
	}
	contract, err := chain.NewSandboxServing(common.HexToAddress(contractHex), eth)
	if err != nil {
		fatalf("bind contract: %v", err)
	}
	return eth, contract
}

func buildAuth(ctx context.Context, privKey *ecdsa.PrivateKey, chainID int64) *bind.TransactOpts {
	auth, err := bind.NewKeyedTransactorWithChainID(privKey, big.NewInt(chainID))
	if err != nil {
		fatalf("build transactor: %v", err)
	}
	auth.Context = ctx
	return auth
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
