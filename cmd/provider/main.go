// cmd/provider — provider-side management CLI
//
// Subcommands:
//
//	register       Register (or update) the service on the settlement contract
//	status         Show provider registration, stake, and earnings
//	withdraw       Withdraw accumulated earnings
//	set-stake      (owner only) Update the minimum stake required for new providers
//	push-image     Load a local Docker image into the internal registry via the runner
//	snapshot       Register a registry image as a named Daytona snapshot
//	snapshots      List all snapshots
//	delete-snapshot Delete a snapshot by name
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
//
//	go run ./cmd/provider/ push-image --image rust-sandbox:1.0.0
//	PROVIDER_KEY=0x<hex> go run ./cmd/provider/ snapshot --api http://... --image registry:6000/daytona/rust-sandbox:1.0.0 --name rust-sandbox
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
		fmt.Fprintln(os.Stderr, "  subcommands: register | status | withdraw | set-stake | push-image | snapshot | snapshots | delete-snapshot")
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
	case "push-image":
		runPushImage(os.Args[2:])
	case "snapshot":
		runSnapshot(os.Args[2:])
	case "snapshots":
		runListSnapshots(os.Args[2:])
	case "delete-snapshot":
		runDeleteSnapshot(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		fmt.Fprintln(os.Stderr, "  subcommands: register | status | withdraw | set-stake | push-image | snapshot | snapshots | delete-snapshot")
		os.Exit(1)
	}
}

// ── register ──────────────────────────────────────────────────────────────────

func runRegister(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	rpc            := fs.String("rpc",           defaultRPC,              "RPC endpoint")
	chainID        := fs.Int64("chain-id",        defaultChainID,          "Chain ID")
	contractHex    := fs.String("contract",       defaultContract,         "Settlement contract address")
	keyHex         := fs.String("key",            "",                      "Provider private key (hex); or set PROVIDER_KEY env")
	teeSigner      := fs.String("tee-signer",     "",                      "TEE signer address (defaults to provider address)")
	serviceURL     := fs.String("url",            "",                      "Provider service URL (required)")
	pricePerCPU    := fs.String("price-per-cpu",  "1000000000000000",      "Price per CPU per minute (neuron)")
	pricePerMemGB  := fs.String("price-per-mem",  "500000000000000",       "Price per GB memory per minute (neuron)")
	createFee      := fs.String("fee",            "60000000000000000",     "Create fee per sandbox (neuron)")
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
	pricePerCPUBig   := parseBigInt(*pricePerCPU, "--price-per-cpu")
	pricePerMemGBBig := parseBigInt(*pricePerMemGB, "--price-per-mem")
	createFeeBig     := parseBigInt(*createFee, "--fee")

	fmt.Printf("Provider:       %s\n", providerAddr.Hex())
	fmt.Printf("TEE signer:     %s\n", teeAddr.Hex())
	fmt.Printf("Contract:       %s\n", *contractHex)
	fmt.Printf("Service URL:    %s\n", *serviceURL)
	fmt.Printf("CPU price/min:  %s neuron\n", pricePerCPUBig.String())
	fmt.Printf("Mem price/min:  %s neuron/GB\n", pricePerMemGBBig.String())
	fmt.Printf("Create fee:     %s neuron\n", createFeeBig.String())

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
			fmt.Printf("Stake:          %s neuron (first registration, attached automatically)\n", requiredStake.String())
		}
	} else {
		fmt.Println("Already registered — updating service (no stake required)")
	}

	fmt.Println("\n[1/1] AddOrUpdateService...")
	tx, err := contract.AddOrUpdateService(auth, *serviceURL, teeAddr, pricePerCPUBig, createFeeBig, pricePerMemGBBig)
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
		fmt.Printf("  URL:              %s\n", svc.Url)
		fmt.Printf("  TEE signer:       %s\n", svc.TeeSignerAddress.Hex())
		fmt.Printf("  CPU price/min:    %s neuron\n", svc.PricePerCPUPerMin.String())
		fmt.Printf("  Mem price/min:    %s neuron/GB\n", svc.PricePerMemGBPerMin.String())
		fmt.Printf("  Create fee:       %s neuron\n", svc.CreateFee.String())
		fmt.Printf("  Signer ver:       %s\n", svc.SignerVersion.String())
		fmt.Printf("  My stake:         %s neuron\n", myStake.String())
		fmt.Printf("  Earnings:         %s neuron\n", earnings.String())
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
