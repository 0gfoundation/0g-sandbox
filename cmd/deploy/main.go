// cmd/deploy/main.go — deploys the SandboxServing beacon-proxy stack.
//
// Three-step deploy:
//   1. Deploy SandboxServing implementation (no constructor args)
//   2. Deploy UpgradeableBeacon(impl, deployer) — beacon owns the upgrade key
//   3. Deploy BeaconProxy(beacon, initialize(tappRegistry)) — stable address
//
// Prerequisite: TappRegistry must already be deployed on the target chain.
// Pass its address via --tapp.
//
// Usage:
//   go run ./cmd/deploy/ --rpc <url> --key <hex> --chain-id <id> --tapp 0x<addr>
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/0gfoundation/0g-sandbox/internal/chain"
)

func main() {
	rpcURL  := flag.String("rpc",      "https://evmrpc-testnet.0g.ai", "EVM RPC endpoint")
	keyHex  := flag.String("key",      "",    "deployer private key (hex, with or without 0x)")
	chainID := flag.Int64("chain-id",  16602, "chain ID")
	tappHex := flag.String("tapp",     "",    "TappRegistry contract address (required)")
	flag.Parse()

	if *keyHex == "" {
		fmt.Fprintln(os.Stderr, "error: --key is required")
		os.Exit(1)
	}
	if *tappHex == "" {
		fmt.Fprintln(os.Stderr, "error: --tapp is required (TappRegistry must be deployed first)")
		os.Exit(1)
	}
	tappAddr := common.HexToAddress(*tappHex)

	// ── private key ───────────────────────────────────────────────────────────
	keyStr := strings.TrimPrefix(*keyHex, "0x")
	privKey, err := crypto.HexToECDSA(keyStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse key: %v\n", err)
		os.Exit(1)
	}
	deployer := crypto.PubkeyToAddress(privKey.PublicKey)
	fmt.Printf("Deployer : %s\n", deployer.Hex())

	// ── chain client ──────────────────────────────────────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client, err := ethclient.DialContext(ctx, *rpcURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial rpc: %v\n", err)
		os.Exit(1)
	}

	// ── transactor ────────────────────────────────────────────────────────────
	auth, err := bind.NewKeyedTransactorWithChainID(privKey, big.NewInt(*chainID))
	if err != nil {
		fmt.Fprintf(os.Stderr, "transactor: %v\n", err)
		os.Exit(1)
	}
	auth.Context = ctx

	// ── helper: load bytecode from Foundry artifact ───────────────────────────
	loadBytecode := func(artifactPath string) []byte {
		raw, err := os.ReadFile(artifactPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read artifact %s: %v\n", artifactPath, err)
			os.Exit(1)
		}
		var artifact struct {
			Bytecode struct {
				Object string `json:"object"`
			} `json:"bytecode"`
		}
		if err := json.Unmarshal(raw, &artifact); err != nil {
			fmt.Fprintf(os.Stderr, "parse artifact %s: %v\n", artifactPath, err)
			os.Exit(1)
		}
		b, err := hex.DecodeString(strings.TrimPrefix(artifact.Bytecode.Object, "0x"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "decode bytecode %s: %v\n", artifactPath, err)
			os.Exit(1)
		}
		return b
	}

	// ── Step 1: Deploy SandboxServing implementation ──────────────────────────
	fmt.Printf("\n[1/3] Deploying SandboxServing implementation (chainID=%d)...\n", *chainID)

	implABI, err := abi.JSON(strings.NewReader(chain.SandboxServingMetaData.ABI))
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse SandboxServing ABI: %v\n", err)
		os.Exit(1)
	}
	implBytecode := loadBytecode("contracts/out/SandboxServing.sol/SandboxServing.json")

	implAddr, implTx, _, err := bind.DeployContract(auth, implABI, implBytecode, client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "deploy impl: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Tx hash : %s\n", implTx.Hash().Hex())
	implReceipt, err := bind.WaitMined(ctx, client, implTx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wait mined (impl): %v\n", err)
		os.Exit(1)
	}
	if implReceipt.Status == 0 {
		fmt.Fprintln(os.Stderr, "impl deploy tx reverted")
		os.Exit(1)
	}
	fmt.Printf("  Impl    : %s\n", implAddr.Hex())

	// ── Step 2: Deploy UpgradeableBeacon(impl, deployer) ─────────────────────
	fmt.Printf("\n[2/3] Deploying UpgradeableBeacon(impl=%s, owner=%s)...\n",
		implAddr.Hex(), deployer.Hex())

	beaconABI, err := abi.JSON(strings.NewReader(chain.UpgradeableBeaconMetaData.ABI))
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse UpgradeableBeacon ABI: %v\n", err)
		os.Exit(1)
	}
	beaconBytecode := loadBytecode("contracts/out/UpgradeableBeacon.sol/UpgradeableBeacon.json")

	beaconAddr, beaconTx, _, err := bind.DeployContract(auth, beaconABI, beaconBytecode, client,
		implAddr, deployer)
	if err != nil {
		fmt.Fprintf(os.Stderr, "deploy beacon: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Tx hash : %s\n", beaconTx.Hash().Hex())
	beaconReceipt, err := bind.WaitMined(ctx, client, beaconTx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wait mined (beacon): %v\n", err)
		os.Exit(1)
	}
	if beaconReceipt.Status == 0 {
		fmt.Fprintln(os.Stderr, "beacon deploy tx reverted")
		os.Exit(1)
	}
	fmt.Printf("  Beacon  : %s\n", beaconAddr.Hex())

	// ── Step 3: Deploy BeaconProxy(beacon, initialize(tappRegistry)) ──────────
	fmt.Printf("\n[3/3] Deploying BeaconProxy(beacon=%s, tappRegistry=%s)...\n",
		beaconAddr.Hex(), tappAddr.Hex())

	// Build initialize(tappRegistry) calldata
	initCalldata, err := implABI.Pack("initialize", tappAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pack initialize calldata: %v\n", err)
		os.Exit(1)
	}

	// BeaconProxy constructor: (address beacon, bytes memory data) payable
	// We use a raw ABI since BeaconProxy has no external functions — just a constructor.
	proxyConstructorABI, err := abi.JSON(strings.NewReader(`[{
		"type": "constructor",
		"inputs": [
			{"name": "beacon", "type": "address"},
			{"name": "data",   "type": "bytes"}
		],
		"stateMutability": "payable"
	}]`))
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse proxy constructor ABI: %v\n", err)
		os.Exit(1)
	}
	proxyBytecode := loadBytecode("contracts/out/BeaconProxy.sol/BeaconProxy.json")

	proxyAddr, proxyTx, _, err := bind.DeployContract(auth, proxyConstructorABI, proxyBytecode, client,
		beaconAddr, initCalldata)
	if err != nil {
		fmt.Fprintf(os.Stderr, "deploy proxy: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Tx hash : %s\n", proxyTx.Hash().Hex())
	proxyReceipt, err := bind.WaitMined(ctx, client, proxyTx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wait mined (proxy): %v\n", err)
		os.Exit(1)
	}
	if proxyReceipt.Status == 0 {
		fmt.Fprintln(os.Stderr, "proxy deploy tx reverted")
		os.Exit(1)
	}
	fmt.Printf("  Proxy   : %s\n", proxyAddr.Hex())

	// ── Summary ───────────────────────────────────────────────────────────────
	fmt.Printf(`
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
DEPLOY COMPLETE
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Implementation : %s
Beacon         : %s
Proxy (stable) : %s
TappRegistry   : %s

Set in .env:
  SETTLEMENT_CONTRACT=%s
  TAPP_REGISTRY=%s

Explorer (proxy):
  https://chainscan-galileo.0g.ai/address/%s
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
`, implAddr.Hex(), beaconAddr.Hex(), proxyAddr.Hex(), tappAddr.Hex(),
		proxyAddr.Hex(), tappAddr.Hex(), proxyAddr.Hex())
}
