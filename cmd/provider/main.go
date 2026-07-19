// cmd/provider — provider-side management CLI (v2: provider IS the TEE signer)
//
// The provider address in SandboxServing is the node's TEE signer address;
// its key lives inside the enclave and dies with the machine. All on-chain
// management is therefore signed by the appId's TappRegistry OWNER key
// (OWNER_KEY env, PROVIDER_KEY accepted as a legacy alias).
//
// Subcommands:
//
//	register        Register/update a node's service: bind --signer to an appId, set URL + prices
//	remove-service  Remove a node's service (sweeps pending earnings to the owner)
//	rotate          After a machine rebuild: re-register the new signer with the old terms, remove the old
//	status          Show a node's registration and earnings
//	withdraw        Withdraw a node's accumulated earnings to the owner
//	push-image      Load a local Docker image into the internal registry via the runner
//	snapshot        Register a registry image as a named Daytona snapshot
//	snapshots       List all snapshots
//	delete-snapshot Delete a snapshot by name
//
// Examples:
//
//	OWNER_KEY=0x<hex> go run ./cmd/provider/ register \
//	  --contract       0x... \
//	  --signer         0x<node-tee-address> \
//	  --app-id         0g-sandbox-provider \
//	  --url            http://billing-host:8080 \
//	  --price-per-cpu  1000000000000000 \
//	  --price-per-mem   500000000000000 \
//	  --fee           60000000000000000
//
//	go run ./cmd/provider/ status   --contract 0x... --address 0x<signer>
//	OWNER_KEY=0x<hex> go run ./cmd/provider/ withdraw --contract 0x... --signer 0x<signer>
//	OWNER_KEY=0x<hex> go run ./cmd/provider/ rotate   --contract 0x... --old 0x<dead-signer> --new 0x<new-signer>
//
//	go run ./cmd/provider/ push-image --image rust-sandbox:1.0.0
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/0gfoundation/0g-sandbox/internal/chain"
)

const (
	defaultRPC      = "https://evmrpc-testnet.0g.ai"
	defaultChainID  = int64(16602)
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: provider <subcommand> [flags]")
		fmt.Fprintln(os.Stderr, "  subcommands: register | remove-service | rotate | status | withdraw | push-image | snapshot | snapshots | delete-snapshot | gc-images")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "register", "init-service":
		runRegister(os.Args[2:])
	case "remove-service":
		runRemoveService(os.Args[2:])
	case "rotate":
		runRotate(os.Args[2:])
	case "status":
		runStatus(os.Args[2:])
	case "withdraw":
		runWithdraw(os.Args[2:])
	case "push-image":
		runPushImage(os.Args[2:])
	case "snapshot":
		runSnapshot(os.Args[2:])
	case "snapshots":
		runListSnapshots(os.Args[2:])
	case "delete-snapshot":
		runDeleteSnapshot(os.Args[2:])
	case "gc-images":
		runGCImages(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		fmt.Fprintln(os.Stderr, "  subcommands: register | remove-service | rotate | status | withdraw | push-image | snapshot | snapshots | delete-snapshot | gc-images")
		os.Exit(1)
	}
}

// resolveOwnerKey resolves the appId owner's private key: --key flag, then
// OWNER_KEY env, then PROVIDER_KEY env (legacy alias from before v2, when
// the provider wallet did its own management).
func resolveOwnerKey(flagVal string) *ecdsa.PrivateKey {
	hex := flagVal
	if hex == "" {
		hex = os.Getenv("OWNER_KEY")
	}
	if hex == "" {
		hex = os.Getenv("PROVIDER_KEY")
	}
	if hex == "" {
		fatalf("app owner private key required: use --key or OWNER_KEY env")
	}
	privKey, err := crypto.HexToECDSA(strings.TrimPrefix(hex, "0x"))
	if err != nil {
		fatalf("parse private key: %v", err)
	}
	return privKey
}

// ── register ──────────────────────────────────────────────────────────────────

// runRegister registers/updates a node's service in SandboxServing: binds the
// node's TEE signer address to a TappRegistry appId and sets URL + prices.
// Signed by the appId OWNER's key. Prerequisites (done on tapp directly, in
// separate txs; this CLI doesn't currently chain them):
//
//  1. tappRegistry.registerApp(appId, ...)            ← pays stake
//  2. tappRegistry.addNode(appId, signer, teeUrl)     ← one per machine
//  3. tappRegistry.authorizeInvalidator(appId, sandboxServingAddr)
//
// Then this command runs sandbox.addOrUpdateService(signer, url, appId, prices).
func runRegister(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	rpc            := fs.String("rpc",           defaultRPC,              "RPC endpoint")
	chainID        := fs.Int64("chain-id",        defaultChainID,          "Chain ID")
	contractHex    := fs.String("contract",       envOrDefault("SETTLEMENT_CONTRACT", ""), "Settlement contract address (required: --contract or SETTLEMENT_CONTRACT env)")
	keyHex         := fs.String("key",            "",                      "App owner private key (hex); or set OWNER_KEY env")
	signerHex      := fs.String("signer",         "",                      "Node's TEE signer address = the provider address (required; get it from the node's /api/info or tapp-cli get-app-key)")
	appId          := fs.String("app-id",         "",                      "TappRegistry appId to bind (required; signer must already be an active node of it)")
	serviceURL     := fs.String("url",            "",                      "Provider service URL (required)")
	pricePerCPU    := fs.String("price-per-cpu",  "1000000000000000",      "Price per CPU per minute (neuron)")
	pricePerMemGB  := fs.String("price-per-mem",  "500000000000000",       "Price per GB memory per minute (neuron)")
	createFee      := fs.String("fee",            "60000000000000000",     "Create fee per sandbox (neuron)")
	_ = fs.Parse(args)

	if *serviceURL == "" {
		fatalf("--url is required")
	}
	if *appId == "" {
		fatalf("--app-id is required")
	}
	if *signerHex == "" {
		fatalf("--signer is required (the node's TEE address; see its /api/info `provider_address` or tapp-cli get-app-key)")
	}
	privKey := resolveOwnerKey(*keyHex)
	ownerAddr := crypto.PubkeyToAddress(privKey.PublicKey)
	signerAddr := common.HexToAddress(*signerHex)

	pricePerCPUBig   := parseBigInt(*pricePerCPU, "--price-per-cpu")
	pricePerMemGBBig := parseBigInt(*pricePerMemGB, "--price-per-mem")
	createFeeBig     := parseBigInt(*createFee, "--fee")

	fmt.Printf("App owner:          %s\n", ownerAddr.Hex())
	fmt.Printf("Provider (signer):  %s\n", signerAddr.Hex())
	fmt.Printf("AppId:              %s\n", *appId)
	fmt.Printf("Contract:           %s\n", *contractHex)
	fmt.Printf("Service URL:        %s\n", *serviceURL)
	fmt.Printf("CPU price/min:      %s neuron\n", pricePerCPUBig.String())
	fmt.Printf("Mem price/min:      %s neuron/GB\n", pricePerMemGBBig.String())
	fmt.Printf("Create fee:         %s neuron\n", createFeeBig.String())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	eth, contract := dialContract(ctx, *rpc, *contractHex)
	defer eth.Close()

	auth := buildAuth(ctx, privKey, *chainID)
	fmt.Println("\n[1/1] AddOrUpdateService...")
	tx, err := contract.AddOrUpdateService(auth, signerAddr, *serviceURL, *appId, pricePerCPUBig, createFeeBig, pricePerMemGBBig)
	if err != nil {
		fatalf("AddOrUpdateService: %v\n\nReminders:\n  - the caller key must be the TappRegistry owner of %s\n  - %s must already be an active node of %s (tapp-cli add-node-onchain)\n  - tapp.authorizeInvalidator(%s, %s) must have been called", err, *appId, signerAddr.Hex(), *appId, *appId, *contractHex)
	}
	fmt.Printf("      tx: %s\n", tx.Hash().Hex())
	if _, err := bind.WaitMined(ctx, eth, tx); err != nil {
		fatalf("wait mined: %v", err)
	}
	fmt.Println("      confirmed ✓")
	fmt.Printf("\nDone. Provider (signer) address: %s\n", signerAddr.Hex())
}

// ── remove-service ────────────────────────────────────────────────────────────

// runRemoveService removes a node's service entry. Signed by the appId
// OWNER's key — the signer key itself may be gone (it dies with the machine).
// Sweeps pending earnings to the owner in the same tx; user balances stay
// refundable and nonce watermarks stay put.
func runRemoveService(args []string) {
	fs := flag.NewFlagSet("remove-service", flag.ExitOnError)
	rpc         := fs.String("rpc",      defaultRPC,      "RPC endpoint")
	chainID     := fs.Int64("chain-id",  defaultChainID,  "Chain ID")
	contractHex := fs.String("contract", envOrDefault("SETTLEMENT_CONTRACT", ""), "Settlement contract address (required: --contract or SETTLEMENT_CONTRACT env)")
	keyHex      := fs.String("key",      "",              "App owner private key (hex); or set OWNER_KEY env")
	signerHex   := fs.String("signer",   "",              "Node's TEE signer address whose service to remove (required)")
	_ = fs.Parse(args)

	if *signerHex == "" {
		fatalf("--signer is required")
	}
	privKey := resolveOwnerKey(*keyHex)
	ownerAddr := crypto.PubkeyToAddress(privKey.PublicKey)
	signerAddr := common.HexToAddress(*signerHex)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	eth, contract := dialContract(ctx, *rpc, *contractHex)
	defer eth.Close()

	opts := &bind.CallOpts{Context: ctx}
	if exists, err := contract.ServiceExists(opts, signerAddr); err != nil {
		fatalf("ServiceExists: %v", err)
	} else if !exists {
		fatalf("no service registered for %s on %s — nothing to remove", signerAddr.Hex(), *contractHex)
	}
	earnings, err := contract.ProviderEarnings(opts, signerAddr)
	if err != nil {
		fatalf("ProviderEarnings: %v", err)
	}

	fmt.Printf("App owner:          %s\n", ownerAddr.Hex())
	fmt.Printf("Provider (signer):  %s\n", signerAddr.Hex())
	fmt.Printf("Pending earnings:   %s neuron (swept to owner in the same tx)\n", earnings.String())
	fmt.Println("\n[1/1] RemoveService...")
	auth := buildAuth(ctx, privKey, *chainID)
	tx, err := contract.RemoveService(auth, signerAddr)
	if err != nil {
		fatalf("RemoveService: %v", err)
	}
	fmt.Printf("      tx: %s\n", tx.Hash().Hex())
	if _, err := bind.WaitMined(ctx, eth, tx); err != nil {
		fatalf("wait mined: %v", err)
	}
	fmt.Println("      confirmed ✓")
	fmt.Printf("\nDone. Service cleared; %s neuron swept to %s.\nUser balances at %s stay refundable; remind users to requestRefund + re-deposit to the new node.\n", earnings.String(), ownerAddr.Hex(), signerAddr.Hex())
}

// ── rotate ────────────────────────────────────────────────────────────────────

// runRotate handles the SandboxServing side of a machine rebuild: the TEE key
// changed, so a new provider identity must take over the old one's commercial
// terms. It copies the old signer's service entry to the new signer, then
// removes the old entry (sweeping its pending earnings to the owner).
//
// Full rotation runbook (this command is step 4):
//
//  1. machine back up with the new key — the settler holds its queue until
//     the new signer is registered on-chain
//  2. tapp-cli add-node-onchain (ADD the new signer; do NOT replace yet —
//     old and new nodes coexist so both queues settle)
//  3. wait for the OLD signer's voucher queue to drain (/api/queue/summary)
//  4. provider rotate --old 0x<old> --new 0x<new>
//  5. tapp-cli remove-node-onchain for the old signer (stake unlocks ~1 day)
//  6. users requestRefund on the old bucket and re-deposit to the new signer
func runRotate(args []string) {
	fs := flag.NewFlagSet("rotate", flag.ExitOnError)
	rpc         := fs.String("rpc",      defaultRPC,      "RPC endpoint")
	chainID     := fs.Int64("chain-id",  defaultChainID,  "Chain ID")
	contractHex := fs.String("contract", envOrDefault("SETTLEMENT_CONTRACT", ""), "Settlement contract address (required: --contract or SETTLEMENT_CONTRACT env)")
	keyHex      := fs.String("key",      "",              "App owner private key (hex); or set OWNER_KEY env")
	oldHex      := fs.String("old",      "",              "Old (dead) signer address (required)")
	newHex      := fs.String("new",      "",              "New signer address (required; must already be an active node — tapp-cli add-node-onchain)")
	urlOverride := fs.String("url",      "",              "New service URL (default: keep the old service's URL)")
	_ = fs.Parse(args)

	if *oldHex == "" || *newHex == "" {
		fatalf("--old and --new are required")
	}
	privKey := resolveOwnerKey(*keyHex)
	ownerAddr := crypto.PubkeyToAddress(privKey.PublicKey)
	oldAddr := common.HexToAddress(*oldHex)
	newAddr := common.HexToAddress(*newHex)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	eth, contract := dialContract(ctx, *rpc, *contractHex)
	defer eth.Close()

	opts := &bind.CallOpts{Context: ctx}
	oldExists, err := contract.ServiceExists(opts, oldAddr)
	if err != nil {
		fatalf("ServiceExists(old): %v", err)
	}
	if !oldExists {
		fatalf("no service registered for old signer %s — nothing to rotate from", oldAddr.Hex())
	}
	svc, err := contract.Services(opts, oldAddr)
	if err != nil {
		fatalf("Services(old): %v", err)
	}
	earnings, err := contract.ProviderEarnings(opts, oldAddr)
	if err != nil {
		fatalf("ProviderEarnings(old): %v", err)
	}
	newURL := svc.Url
	if *urlOverride != "" {
		newURL = *urlOverride
	}

	fmt.Printf("App owner:     %s\n", ownerAddr.Hex())
	fmt.Printf("Old signer:    %s (earnings %s neuron — swept to owner in step 2)\n", oldAddr.Hex(), earnings.String())
	fmt.Printf("New signer:    %s\n", newAddr.Hex())
	fmt.Printf("AppId:         %s\n", svc.AppId)
	fmt.Printf("Service URL:   %s\n", newURL)
	fmt.Println("\n⚠ Precondition: the old signer's voucher queue must be EMPTY (/api/queue/summary).")
	fmt.Println("  Vouchers still in flight settle fine until remove-node-onchain, but any that")
	fmt.Println("  reference the old service after this rotate will fail PROVIDER_MISMATCH.")

	auth := buildAuth(ctx, privKey, *chainID)

	fmt.Println("\n[1/2] AddOrUpdateService(new signer, old terms)...")
	tx, err := contract.AddOrUpdateService(auth, newAddr, newURL, svc.AppId, svc.PricePerCPUPerMin, svc.CreateFee, svc.PricePerMemGBPerMin)
	if err != nil {
		fatalf("AddOrUpdateService: %v\n\nReminder: %s must already be an active node of %s (tapp-cli add-node-onchain).", err, newAddr.Hex(), svc.AppId)
	}
	fmt.Printf("      tx: %s\n", tx.Hash().Hex())
	if _, err := bind.WaitMined(ctx, eth, tx); err != nil {
		fatalf("wait mined: %v", err)
	}
	fmt.Println("      confirmed ✓  (same prices → no ack invalidation)")

	fmt.Println("[2/2] RemoveService(old signer)...")
	tx, err = contract.RemoveService(auth, oldAddr)
	if err != nil {
		fatalf("RemoveService: %v", err)
	}
	fmt.Printf("      tx: %s\n", tx.Hash().Hex())
	if _, err := bind.WaitMined(ctx, eth, tx); err != nil {
		fatalf("wait mined: %v", err)
	}
	fmt.Println("      confirmed ✓")

	fmt.Printf("\nDone. New provider identity: %s\n", newAddr.Hex())
	fmt.Println("Next steps:")
	fmt.Printf("  - tapp-cli remove-node-onchain for %s (stake unlocks after ~1 day)\n", oldAddr.Hex())
	fmt.Printf("  - users with balance at %s: requestRefund → withdrawRefund (2h lock) → deposit to %s\n", oldAddr.Hex(), newAddr.Hex())
}

// ── status ────────────────────────────────────────────────────────────────────

func runStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	rpc         := fs.String("rpc",      defaultRPC,      "RPC endpoint")
	contractHex := fs.String("contract", envOrDefault("SETTLEMENT_CONTRACT", ""), "Settlement contract address (required: --contract or SETTLEMENT_CONTRACT env)")
	addrHex     := fs.String("address",  "",              "Provider (signer) address to inspect (required; read-only, no key needed)")
	tappHex     := fs.String("tapp",     envOrDefault("TAPP_REGISTRY", ""), "TappRegistry address (optional; shows the appId owner and node state)")
	_ = fs.Parse(args)

	if *addrHex == "" {
		fatalf("--address is required (the node's TEE signer address; see its /api/info `provider_address`)")
	}
	providerAddr := common.HexToAddress(*addrHex)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	eth, contract := dialContract(ctx, *rpc, *contractHex)
	defer eth.Close()

	opts := &bind.CallOpts{Context: ctx}

	registered, err := contract.ServiceExists(opts, providerAddr)
	if err != nil {
		fatalf("ServiceExists: %v", err)
	}
	owner, err := contract.Owner(opts)
	if err != nil {
		fatalf("Owner: %v", err)
	}

	fmt.Printf("Provider:       %s\n", providerAddr.Hex())
	fmt.Printf("Contract:       %s\n", *contractHex)
	fmt.Printf("Registered:     %v\n", registered)
	fmt.Printf("Contract owner: %s\n", owner.Hex())

	if registered {
		svc, err := contract.Services(opts, providerAddr)
		if err != nil {
			fatalf("Services: %v", err)
		}
		earnings, err := contract.ProviderEarnings(opts, providerAddr)
		if err != nil {
			fatalf("ProviderEarnings: %v", err)
		}
		fmt.Printf("\nService:\n")
		fmt.Printf("  URL:              %s\n", svc.Url)
		fmt.Printf("  AppId:            %s\n", svc.AppId)
		fmt.Printf("  CPU price/min:    %s neuron\n", svc.PricePerCPUPerMin.String())
		fmt.Printf("  Mem price/min:    %s neuron/GB\n", svc.PricePerMemGBPerMin.String())
		fmt.Printf("  Create fee:       %s neuron\n", svc.CreateFee.String())
		fmt.Printf("  Earnings:         %s neuron\n", earnings.String())
		if *tappHex != "" && svc.AppId != "" {
			tapp, err := chain.NewTappRegistry(common.HexToAddress(*tappHex), eth)
			if err != nil {
				fatalf("bind tappRegistry: %v", err)
			}
			appInfo, err := tapp.GetAppInfo(opts, svc.AppId)
			if err != nil {
				fatalf("tapp.getAppInfo: %v", err)
			}
			fmt.Printf("  App owner:        %s (manages this service + withdraws earnings)\n", appInfo.Owner.Hex())
			node, err := tapp.GetNode(opts, svc.AppId, providerAddr)
			if err != nil {
				fatalf("tapp.getNode: %v", err)
			}
			if node.AddedAt.Sign() != 0 {
				fmt.Printf("  Node:             active (teeUrl %s, stake %s)\n", node.TeeUrl, node.StakeAmount.String())
			} else {
				fmt.Println("  Node:             NOT an active TappRegistry node — its vouchers cannot settle")
			}
		}
	}
}

// ── withdraw ──────────────────────────────────────────────────────────────────

// runWithdraw withdraws a node's accrued earnings to the app owner. Signed by
// the OWNER's key; the provider (signer) key never leaves the enclave and has
// no payout rights of its own.
func runWithdraw(args []string) {
	fs := flag.NewFlagSet("withdraw", flag.ExitOnError)
	rpc         := fs.String("rpc",      defaultRPC,      "RPC endpoint")
	chainID     := fs.Int64("chain-id",  defaultChainID,  "Chain ID")
	contractHex := fs.String("contract", envOrDefault("SETTLEMENT_CONTRACT", ""), "Settlement contract address (required: --contract or SETTLEMENT_CONTRACT env)")
	keyHex      := fs.String("key",      "",              "App owner private key; or set OWNER_KEY env")
	signerHex   := fs.String("signer",   "",              "Node's TEE signer address whose earnings to withdraw (required)")
	_ = fs.Parse(args)

	if *signerHex == "" {
		fatalf("--signer is required")
	}
	privKey := resolveOwnerKey(*keyHex)
	ownerAddr := crypto.PubkeyToAddress(privKey.PublicKey)
	signerAddr := common.HexToAddress(*signerHex)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	eth, contract := dialContract(ctx, *rpc, *contractHex)
	defer eth.Close()

	opts := &bind.CallOpts{Context: ctx}
	earnings, err := contract.ProviderEarnings(opts, signerAddr)
	if err != nil {
		fatalf("ProviderEarnings: %v", err)
	}
	if earnings.Sign() == 0 {
		fmt.Println("No earnings to withdraw.")
		return
	}
	fmt.Printf("App owner:          %s\n", ownerAddr.Hex())
	fmt.Printf("Provider (signer):  %s\n", signerAddr.Hex())
	fmt.Printf("Earnings:           %s neuron\n", earnings.String())

	fmt.Println("\nWithdrawing earnings to the owner...")
	tx, err := contract.WithdrawEarnings(buildAuth(ctx, privKey, *chainID), signerAddr)
	if err != nil {
		fatalf("WithdrawEarnings: %v\n\nReminder: --key must be the TappRegistry owner of the appId this signer's service is bound to.", err)
	}
	fmt.Printf("  tx: %s\n", tx.Hash().Hex())
	if _, err := bind.WaitMined(ctx, eth, tx); err != nil {
		fatalf("wait mined: %v", err)
	}
	fmt.Printf("  confirmed ✓  (%s neuron paid to %s)\n", earnings.String(), ownerAddr.Hex())
}

// `set-stake` was removed: stake collection moved to TappRegistry (per-node).
// Use the tapp CLI to manage stakes.

// ── push-image ────────────────────────────────────────────────────────────────

// runPushImage loads a local Docker image into the deployment's internal registry
// via the runner container (which has access to registry:6000).
//
// Steps executed:
//
//	docker save <image> | docker exec -i <runner> docker load
//	docker exec <runner> docker tag <image> <registry>/daytona/<name>
//	docker exec <runner> docker push <registry>/daytona/<name>
func runPushImage(args []string) {
	fs := flag.NewFlagSet("push-image", flag.ExitOnError)
	image    := fs.String("image",    "",                               "Local Docker image (e.g. rust-sandbox:1.0.0) (required)")
	name     := fs.String("name",     "",                               "Name in registry (default: same as --image)")
	runner   := fs.String("runner",   "0g-sandbox-billing-runner-1",   "Runner container name")
	registry := fs.String("registry", "registry:6000",                 "Internal registry address")
	_ = fs.Parse(args)

	if *image == "" {
		fatalf("--image is required")
	}
	if !strings.Contains(*image, ":") || strings.HasSuffix(*image, ":latest") {
		fatalf("image must include an explicit version tag, e.g. rust-sandbox:1.0.0 (not :latest)")
	}

	targetName := *name
	if targetName == "" {
		targetName = *image
	}
	registryPath := *registry + "/daytona/" + targetName

	// ── Step 1: docker save | docker exec -i runner docker load ──────────────
	fmt.Printf("[1/3] Loading %s into runner %s...\n", *image, *runner)
	saveCmd := exec.Command("docker", "save", *image)
	loadCmd := exec.Command("docker", "exec", "-i", *runner, "docker", "load")

	pipe, err := saveCmd.StdoutPipe()
	if err != nil {
		fatalf("pipe: %v", err)
	}
	loadCmd.Stdin = pipe
	loadCmd.Stdout = os.Stdout
	loadCmd.Stderr = os.Stderr

	if err := saveCmd.Start(); err != nil {
		fatalf("docker save: %v", err)
	}
	if err := loadCmd.Start(); err != nil {
		fatalf("docker exec load: %v", err)
	}
	if err := saveCmd.Wait(); err != nil {
		fatalf("docker save: %v", err)
	}
	if err := loadCmd.Wait(); err != nil {
		fatalf("docker exec load: %v", err)
	}

	// ── Step 2: docker exec runner docker tag ────────────────────────────────
	fmt.Printf("[2/3] Tagging as %s...\n", registryPath)
	if out, err := exec.Command("docker", "exec", *runner, "docker", "tag", *image, registryPath).CombinedOutput(); err != nil {
		fatalf("docker tag: %v\n%s", err, out)
	}

	// ── Step 3: docker exec runner docker push ───────────────────────────────
	fmt.Printf("[3/3] Pushing %s...\n", registryPath)
	pushCmd := exec.Command("docker", "exec", *runner, "docker", "push", registryPath)
	pushCmd.Stdout = os.Stdout
	pushCmd.Stderr = os.Stderr
	if err := pushCmd.Run(); err != nil {
		fatalf("docker push: %v", err)
	}

	fmt.Printf("\nDone. Register this image as a snapshot with:\n")
	fmt.Printf("  provider snapshot --image %s --name <snapshot-name>\n", registryPath)
}

// ── snapshot ──────────────────────────────────────────────────────────────────

// snapshotTier defines the resource spec for one size variant.
type snapshotTier struct {
	suffix string
	cpu    int
	memory int
	disk   int
}

// defaultTiers are the standard small/medium/large resource tiers.
var defaultTiers = []snapshotTier{
	{"small",  1, 1,  10},
	{"medium", 2, 4,  30},
	{"large",  4, 8,  60},
}

// runSnapshot registers a Docker image as a named Daytona snapshot via the
// billing proxy (provider-only endpoint). No Daytona access needed.
//
// With --tiers: creates three snapshots (<name>-small, <name>-medium, <name>-large).
// Without --tiers: creates a single snapshot with explicit or default resources.
func runSnapshot(args []string) {
	fs := flag.NewFlagSet("snapshot", flag.ExitOnError)
	apiURL := fs.String("api",    "http://localhost:8080", "0G Sandbox service URL")
	keyHex := fs.String("key",    "",                     "Provider private key (hex); or set PROVIDER_KEY env")
	image  := fs.String("image",  "",                     "Docker image name (required)")
	name   := fs.String("name",   "",                     "Snapshot name (defaults to image name)")
	tiers  := fs.Bool("tiers",    false,                  "Create small/medium/large variants automatically")
	cpu    := fs.Int("cpu",       1,                      "CPU cores (ignored when --tiers)")
	memory := fs.Int("memory",    1,                      "Memory in GB (ignored when --tiers)")
	disk   := fs.Int("disk",      3,                      "Disk in GB (ignored when --tiers)")
	_ = fs.Parse(args)

	if *image == "" {
		fatalf("--image is required")
	}
	privKey := resolveKey(*keyHex, "PROVIDER_KEY")

	baseName := *image
	if *name != "" {
		baseName = *name
	}

	if *tiers {
		fmt.Printf("Creating %d tier snapshots for %s...\n\n", len(defaultTiers), baseName)
		for _, tier := range defaultTiers {
			n := baseName + "-" + tier.suffix
			fmt.Printf("[%s] cpu=%d mem=%dGB disk=%dGB\n", n, tier.cpu, tier.memory, tier.disk)
			if err := createSnapshot(privKey, *apiURL, *image, n, tier.cpu, tier.memory, tier.disk); err != nil {
				fmt.Printf("  ✗ %v\n", err)
			} else {
				fmt.Printf("  ✓ registered (state: pending → active in ~30s)\n")
			}
		}
		fmt.Printf("\nUsers can create sandboxes with:\n")
		for _, tier := range defaultTiers {
			fmt.Printf("  user create --snapshot %s-%s\n", baseName, tier.suffix)
		}
		return
	}

	// Single snapshot
	if err := createSnapshot(privKey, *apiURL, *image, baseName, *cpu, *memory, *disk); err != nil {
		fatalf("%v", err)
	}
}

// createSnapshot calls POST /api/snapshots on the billing proxy.
func createSnapshot(privKey *ecdsa.PrivateKey, apiURL, imageName, name string, cpu, memory, disk int) error {
	body := map[string]any{
		"name":      name,
		"imageName": imageName,
		"cpu":       cpu,
		"memory":    memory,
		"disk":      disk,
	}
	payloadBytes, _ := json.Marshal(body)
	msg, sig, walletAddr := signRequest(privKey, "snapshot", "", json.RawMessage(payloadBytes))

	req, err := http.NewRequest(http.MethodPost, apiURL+"/api/snapshots", bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Wallet-Address", walletAddr)
	req.Header.Set("X-Signed-Message", msg)
	req.Header.Set("X-Wallet-Signature", sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, respBody)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		fmt.Println(string(respBody))
		return nil
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(b))
	if n, ok := result["name"].(string); ok {
		fmt.Printf("\nUsers can create from this snapshot with: --snapshot %s\n", n)
	}
	return nil
}

// runListSnapshots lists available snapshots via the billing proxy.
func runListSnapshots(args []string) {
	fs := flag.NewFlagSet("snapshots", flag.ExitOnError)
	apiURL := fs.String("api", "http://localhost:8080", "0G Sandbox service URL")
	keyHex := fs.String("key", "",                     "Provider private key (hex); or set PROVIDER_KEY env")
	_ = fs.Parse(args)

	privKey := resolveKey(*keyHex, "PROVIDER_KEY")
	msg, sig, walletAddr := signRequest(privKey, "list", "", json.RawMessage(`{}`))

	req, err := http.NewRequest(http.MethodGet, *apiURL+"/api/snapshots", nil)
	if err != nil {
		fatalf("build request: %v", err)
	}
	req.Header.Set("X-Wallet-Address", walletAddr)
	req.Header.Set("X-Signed-Message", msg)
	req.Header.Set("X-Wallet-Signature", sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatalf("snapshots: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fatalf("snapshots: HTTP %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		fmt.Println(string(respBody))
		return
	}
	if result.Total == 0 {
		fmt.Println("No snapshots.")
		return
	}
	for _, s := range result.Items {
		b, _ := json.MarshalIndent(s, "", "  ")
		fmt.Println(string(b))
	}
}

// runDeleteSnapshot deletes a snapshot by ID via the billing proxy (provider-only).
func runDeleteSnapshot(args []string) {
	fs := flag.NewFlagSet("delete-snapshot", flag.ExitOnError)
	apiURL := fs.String("api", "http://localhost:8080", "0G Sandbox service URL")
	keyHex := fs.String("key", "", "Provider private key (hex); or set PROVIDER_KEY env")
	id     := fs.String("id", "", "Snapshot ID (required)")
	_ = fs.Parse(args)

	if *id == "" {
		fatalf("--id is required")
	}
	privKey := resolveKey(*keyHex, "PROVIDER_KEY")
	msg, sig, walletAddr := signRequest(privKey, "delete-snapshot", *id, json.RawMessage(`{}`))

	req, err := http.NewRequest(http.MethodDelete, *apiURL+"/api/snapshots/"+*id, nil)
	if err != nil {
		fatalf("build request: %v", err)
	}
	req.Header.Set("X-Wallet-Address", walletAddr)
	req.Header.Set("X-Signed-Message", msg)
	req.Header.Set("X-Wallet-Signature", sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatalf("delete-snapshot: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fatalf("delete-snapshot: HTTP %d: %s", resp.StatusCode, respBody)
	}
	fmt.Printf("Deleted snapshot %s\n", *id)
}

// runGCImages calls POST /api/registry/gc to clean up orphan derived (":d-*")
// tags in the internal registry. Use --dry-run to preview without deleting.
func runGCImages(args []string) {
	fs := flag.NewFlagSet("gc-images", flag.ExitOnError)
	apiURL := fs.String("api", "http://localhost:8080", "0G Sandbox service URL")
	keyHex := fs.String("key", "", "Provider private key (hex); or set PROVIDER_KEY env")
	dryRun := fs.Bool("dry-run", false, "Preview deletions without actually removing tags")
	_ = fs.Parse(args)

	privKey := resolveKey(*keyHex, "PROVIDER_KEY")
	msg, sig, walletAddr := signRequest(privKey, "gc-images", "", json.RawMessage(`{}`))

	url := *apiURL + "/api/registry/gc"
	if *dryRun {
		url += "?dry_run=true"
	}
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		fatalf("build request: %v", err)
	}
	req.Header.Set("X-Wallet-Address", walletAddr)
	req.Header.Set("X-Signed-Message", msg)
	req.Header.Set("X-Wallet-Signature", sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatalf("gc-images: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fatalf("gc-images: HTTP %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		DryRun     bool                `json:"dry_run"`
		Candidates int                 `json:"candidates"`
		Deleted    []string            `json:"deleted"`
		Kept       []string            `json:"kept"`
		Skipped    []map[string]string `json:"skipped"`
		Failed     []map[string]string `json:"failed"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		fmt.Println(string(respBody))
		return
	}

	verb := "deleted"
	if result.DryRun {
		verb = "would delete"
	}
	fmt.Printf("Scanned %d derived tag(s)\n", result.Candidates)
	fmt.Printf("  kept (still in use): %d\n", len(result.Kept))
	fmt.Printf("  %s: %d\n", verb, len(result.Deleted))
	if len(result.Skipped) > 0 {
		fmt.Printf("  skipped (shares manifest): %d\n", len(result.Skipped))
	}
	if len(result.Failed) > 0 {
		fmt.Printf("  failed: %d\n", len(result.Failed))
	}
	for _, ref := range result.Deleted {
		fmt.Printf("    - %s\n", ref)
	}
	for _, s := range result.Skipped {
		fmt.Printf("    ~ %s\n", s["tag"])
	}
	for _, f := range result.Failed {
		fmt.Printf("    ! %s: %s\n", f["tag"], f["error"])
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func signRequest(privKey *ecdsa.PrivateKey, action, resourceID string, payload json.RawMessage) (signedMsg, sig, walletAddr string) {
	addr := crypto.PubkeyToAddress(privKey.PublicKey)
	nonceBuf := make([]byte, 16)
	rand.Read(nonceBuf) //nolint:errcheck
	nonce := hex.EncodeToString(nonceBuf)
	type signedRequest struct {
		Action     string          `json:"action"`
		ExpiresAt  int64           `json:"expires_at"`
		Nonce      string          `json:"nonce"`
		Payload    json.RawMessage `json:"payload"`
		ResourceID string          `json:"resource_id"`
	}
	reqObj := signedRequest{Action: action, ExpiresAt: time.Now().Add(3 * time.Minute).Unix(), Nonce: nonce, Payload: payload, ResourceID: resourceID}
	msgBytes, _ := json.Marshal(reqObj)
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(msgBytes))
	hash := crypto.Keccak256([]byte(prefix), msgBytes)
	sigBytes, err := crypto.Sign(hash, privKey)
	if err != nil {
		fatalf("sign: %v", err)
	}
	sigBytes[64] += 27
	return base64.StdEncoding.EncodeToString(msgBytes), "0x" + hex.EncodeToString(sigBytes), addr.Hex()
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func resolveEnv(flagVal, envVar, label string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	fatalf("%s required: use --%s or %s env", label, strings.ToLower(strings.ReplaceAll(envVar, "_", "-")), envVar)
	return ""
}

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
	if contractHex == "" {
		fatalf("settlement contract is required: set --contract or SETTLEMENT_CONTRACT env var (e.g. from broker GET /api/info)")
	}
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
