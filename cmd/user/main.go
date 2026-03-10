// cmd/user — user-side CLI for the 0G sandbox billing system
//
// Chain subcommands (interact with settlement contract directly):
//
//	balance      Show on-chain balance and nonce
//	deposit      Deposit 0G tokens into the contract
//	acknowledge  Acknowledge the TEE signer for a provider
//
// API subcommands (call the billing proxy with EIP-191 signed requests):
//
//	create   Create a new sandbox
//	list     List your sandboxes
//	stop     Stop a running sandbox
//	delete   Delete a sandbox
//
// Private key via --key flag or USER_KEY env var.
//
// Examples:
//
//	# Deposit 0.01 0G for provider
//	go run ./cmd/user/ deposit --provider 0xB831... --amount 0.01
//
//	# Acknowledge TEE signer
//	go run ./cmd/user/ acknowledge --provider 0xB831...
//
//	# Create a sandbox
//	go run ./cmd/user/ create --api http://47.236.111.154:8080
//
//	# List sandboxes
//	go run ./cmd/user/ list --api http://47.236.111.154:8080
//
//	# Stop a sandbox
//	go run ./cmd/user/ stop --api http://47.236.111.154:8080 --id <sandbox-id>
//
//	# Delete a sandbox
//	go run ./cmd/user/ delete --api http://47.236.111.154:8080 --id <sandbox-id>
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
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/0gfoundation/0g-sandbox-billing/internal/chain"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "balance":
		runBalance(os.Args[2:])
	case "deposit":
		runDeposit(os.Args[2:])
	case "acknowledge":
		runAcknowledge(os.Args[2:])
	case "create":
		runCreate(os.Args[2:])
	case "list":
		runList(os.Args[2:])
	case "stop":
		runStop(os.Args[2:])
	case "delete":
		runDelete(os.Args[2:])
	case "exec":
		runExec(os.Args[2:])
	case "toolbox":
		runToolbox(os.Args[2:])
	case "start":
		runStart(os.Args[2:])
	case "ssh-access":
		runSSHAccess(os.Args[2:])
	case "providers":
		runProviders(os.Args[2:])
	case "snapshots":
		runListSnapshots(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: user <subcommand> [flags]")
	fmt.Fprintln(os.Stderr, "  chain:  balance | deposit | acknowledge")
	fmt.Fprintln(os.Stderr, "  api:    providers | create | list | start | stop | delete | exec | toolbox | ssh-access | snapshots")
}

// ── Shared chain flags ───────────────────────────────────────────────────────

type chainFlags struct {
	rpc      string
	chainID  int64
	contract string
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func addChainFlags(fs *flag.FlagSet) *chainFlags {
	cf := &chainFlags{}
	fs.StringVar(&cf.rpc,      "rpc",      envOrDefault("RPC_URL", "https://evmrpc-testnet.0g.ai"),                       "RPC endpoint")
	fs.Int64Var(&cf.chainID,   "chain-id", 16602,                                                                          "Chain ID")
	fs.StringVar(&cf.contract, "contract", envOrDefault("SETTLEMENT_CONTRACT", "0x2024eB0Cc14316fF8Cc425bFB7CC37FD8713E9b3"), "Settlement contract address")
	return cf
}

// ── balance ──────────────────────────────────────────────────────────────────

func runBalance(args []string) {
	fs := flag.NewFlagSet("balance", flag.ExitOnError)
	cf := addChainFlags(fs)
	addrHex := fs.String("address", "", "Wallet address to check (defaults to --key address)")
	keyHex  := fs.String("key",     "", "User private key (hex); or set USER_KEY env")
	providerHex := fs.String("provider", "", "Provider address (optional; shows nonce)")
	_ = fs.Parse(args)

	var walletAddr common.Address
	if *addrHex != "" {
		walletAddr = common.HexToAddress(*addrHex)
	} else {
		privKey := mustLoadKey(*keyHex)
		walletAddr = crypto.PubkeyToAddress(privKey.PublicKey)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	eth, contract := mustDialContract(ctx, cf.rpc, cf.contract)
	defer eth.Close()

	// Native wallet balance (gas)
	nativeBal, err := eth.BalanceAt(ctx, walletAddr, nil)
	if err != nil {
		fatalf("BalanceAt: %v", err)
	}

	opts := &bind.CallOpts{Context: ctx}
	fmt.Printf("Address:         %s\n", walletAddr.Hex())
	fmt.Printf("Wallet balance:  %s neuron  (%.6f 0G)  ← for gas\n", nativeBal, neuronTo0G(nativeBal))

	if *providerHex != "" {
		providerAddr := common.HexToAddress(*providerHex)
		bal, err := contract.GetBalance(opts, walletAddr, providerAddr)
		if err != nil {
			fatalf("GetBalance: %v", err)
		}
		fmt.Printf("Contract balance:%s neuron  (%.6f 0G)  ← for sandbox (provider %s)\n",
			bal.Balance, neuronTo0G(bal.Balance), providerAddr.Hex())

		nonce, err := contract.GetLastNonce(opts, walletAddr, providerAddr)
		if err != nil {
			fatalf("GetLastNonce: %v", err)
		}
		fmt.Printf("Nonce (vs provider): %s\n", nonce)

		earnings, err := contract.GetProviderEarnings(opts, providerAddr)
		if err != nil {
			fatalf("GetProviderEarnings: %v", err)
		}
		fmt.Printf("Provider earnings: %s neuron  (%.6f 0G)\n", earnings, neuronTo0G(earnings))
	} else {
		fmt.Println("(use --provider <addr> to see per-provider balance)")
	}
}

// ── deposit ──────────────────────────────────────────────────────────────────

func runDeposit(args []string) {
	fs := flag.NewFlagSet("deposit", flag.ExitOnError)
	cf := addChainFlags(fs)
	keyHex      := fs.String("key",      "", "User private key (hex); or set USER_KEY env")
	amount      := fs.Float64("amount",  0.01, "Amount to deposit in 0G (e.g. 0.01)")
	providerHex := fs.String("provider", "", "Provider address to deposit for (required)")
	_ = fs.Parse(args)

	if *providerHex == "" {
		fatalf("--provider is required")
	}

	privKey := mustLoadKey(*keyHex)
	userAddr := crypto.PubkeyToAddress(privKey.PublicKey)
	providerAddr := common.HexToAddress(*providerHex)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	eth, contract := mustDialContract(ctx, cf.rpc, cf.contract)
	defer eth.Close()

	auth, err := bind.NewKeyedTransactorWithChainID(privKey, big.NewInt(cf.chainID))
	if err != nil {
		fatalf("build transactor: %v", err)
	}
	auth.Context = ctx

	depositWei := ogToNeuron(*amount)
	auth.Value = depositWei

	fmt.Printf("User:     %s\n", userAddr.Hex())
	fmt.Printf("Provider: %s\n", providerAddr.Hex())
	fmt.Printf("Amount:   %.6f 0G (%s neuron)\n", *amount, depositWei)
	fmt.Printf("Contract: %s\n", cf.contract)

	fmt.Println("\n[1/1] Deposit...")
	tx, err := contract.Deposit(auth, userAddr, providerAddr)
	if err != nil {
		fatalf("Deposit: %v", err)
	}
	auth.Value = big.NewInt(0)
	fmt.Printf("      tx: %s\n", tx.Hash().Hex())
	if _, err := bind.WaitMined(ctx, eth, tx); err != nil {
		fatalf("wait mined: %v", err)
	}

	opts := &bind.CallOpts{Context: ctx}
	bal, _ := contract.GetBalance(opts, userAddr, providerAddr)
	fmt.Println("      confirmed ✓")
	fmt.Printf("\nNew balance (for provider %s): %s neuron  (%.6f 0G)\n",
		providerAddr.Hex(), bal.Balance, neuronTo0G(bal.Balance))
}

// ── acknowledge ──────────────────────────────────────────────────────────────

func runAcknowledge(args []string) {
	fs := flag.NewFlagSet("acknowledge", flag.ExitOnError)
	cf := addChainFlags(fs)
	keyHex      := fs.String("key",      "", "User private key (hex); or set USER_KEY env")
	providerHex := fs.String("provider", "", "Provider address (required)")
	revoke      := fs.Bool("revoke",     false, "Revoke instead of acknowledge")
	_ = fs.Parse(args)

	if *providerHex == "" {
		fatalf("--provider is required")
	}
	privKey := mustLoadKey(*keyHex)
	userAddr := crypto.PubkeyToAddress(privKey.PublicKey)
	providerAddr := common.HexToAddress(*providerHex)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	eth, contract := mustDialContract(ctx, cf.rpc, cf.contract)
	defer eth.Close()

	auth, err := bind.NewKeyedTransactorWithChainID(privKey, big.NewInt(cf.chainID))
	if err != nil {
		fatalf("build transactor: %v", err)
	}
	auth.Context = ctx

	accept := !*revoke
	verb := "AcknowledgeTEESigner"
	if !accept {
		verb = "RevokeTEESigner"
	}
	fmt.Printf("User:     %s\n", userAddr.Hex())
	fmt.Printf("Provider: %s\n", providerAddr.Hex())
	fmt.Printf("\n[1/1] %s (accept=%v)...\n", verb, accept)

	tx, err := contract.AcknowledgeTEESigner(auth, providerAddr, accept)
	if err != nil {
		fatalf("AcknowledgeTEESigner: %v", err)
	}
	fmt.Printf("      tx: %s\n", tx.Hash().Hex())
	if _, err := bind.WaitMined(ctx, eth, tx); err != nil {
		fatalf("wait mined: %v", err)
	}
	fmt.Println("      confirmed ✓")
}

// ── providers ────────────────────────────────────────────────────────────────

// runProviders discovers registered providers by scanning on-chain ServiceUpdated
// events, then reads each provider's current service info from the contract.
// No API URL required — only the RPC endpoint and contract address (both have
// sensible defaults for 0G Galileo testnet).
func runProviders(args []string) {
	fs := flag.NewFlagSet("providers", flag.ExitOnError)
	cf := addChainFlags(fs)
	_ = fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	eth, contract := mustDialContract(ctx, cf.rpc, cf.contract)
	defer eth.Close()

	// Scan ServiceUpdated events from genesis to collect all provider addresses.
	iter, err := contract.FilterServiceUpdated(nil, nil)
	if err != nil {
		fatalf("filter ServiceUpdated: %v", err)
	}
	defer iter.Close()

	seen := map[common.Address]struct{}{}
	var providerAddrs []common.Address
	for iter.Next() {
		addr := iter.Event.Provider
		if _, ok := seen[addr]; !ok {
			seen[addr] = struct{}{}
			providerAddrs = append(providerAddrs, addr)
		}
	}
	if err := iter.Error(); err != nil {
		fatalf("iterate events: %v", err)
	}

	if len(providerAddrs) == 0 {
		fmt.Println("No providers found on-chain.")
		return
	}

	opts := &bind.CallOpts{Context: ctx}
	fmt.Printf("Found %d provider(s) on-chain:\n\n", len(providerAddrs))
	for i, addr := range providerAddrs {
		svc, err := contract.Services(opts, addr)
		if err != nil {
			fmt.Printf("[%d] %s  (error reading service: %v)\n\n", i+1, addr.Hex(), err)
			continue
		}
		cpuPerSec := new(big.Int).Div(svc.PricePerCPUPerMin, big.NewInt(60))
		memPerSec := new(big.Int).Div(svc.PricePerMemGBPerMin, big.NewInt(60))
		fmt.Printf("[%d] %s\n", i+1, addr.Hex())
		fmt.Printf("    URL:         %s\n", svc.Url)
		fmt.Printf("    Create fee:  %.4f 0G\n", neuronTo0G(svc.CreateFee))
		fmt.Printf("    CPU price:   %.6f 0G/CPU/sec  (%.4f 0G/CPU/min)\n",
			neuronTo0G(cpuPerSec), neuronTo0G(svc.PricePerCPUPerMin))
		fmt.Printf("    Mem price:   %.6f 0G/GB/sec   (%.4f 0G/GB/min)\n",
			neuronTo0G(memPerSec), neuronTo0G(svc.PricePerMemGBPerMin))
		fmt.Printf("    TEE signer:  %s (v%s)\n", svc.TeeSignerAddress.Hex(), svc.SignerVersion)
		fmt.Println()
	}
	if len(providerAddrs) == 1 {
		svc, _ := contract.Services(opts, providerAddrs[0])
		fmt.Printf("# To use this provider:\n")
		fmt.Printf("export PROVIDER=%s\n", providerAddrs[0].Hex())
		fmt.Printf("export API=%s\n", svc.Url)
	}
}

func mustParseBigInt(s string) *big.Int {
	n := new(big.Int)
	n.SetString(s, 10)
	return n
}

// ── create ───────────────────────────────────────────────────────────────────

func runCreate(args []string) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	apiURL   := fs.String("api",      "http://localhost:8080", "Billing proxy URL")
	keyHex   := fs.String("key",      "",                     "User private key (hex); or set USER_KEY env")
	snapshot := fs.String("snapshot", "",                     "Snapshot name to use as the sandbox base (optional)")
	name     := fs.String("name",     "",                     "Sandbox display name (optional)")
	class    := fs.String("class",    "",                     "Sandbox class: small | medium | large (optional)")
	cpu      := fs.Int("cpu",         0,                      "CPU cores (optional, overrides class)")
	memory   := fs.Int("memory",      0,                      "Memory in GB (optional, overrides class)")
	disk     := fs.Int("disk",        0,                      "Disk in GB (optional, overrides class)")
	_ = fs.Parse(args)

	if *class != "" && *class != "small" && *class != "medium" && *class != "large" {
		fatalf("--class must be one of: small, medium, large")
	}

	privKey := mustLoadKey(*keyHex)

	body := map[string]any{}
	if *name != "" {
		body["name"] = *name
	}
	if *snapshot != "" {
		body["snapshot"] = *snapshot
	}
	if *class != "" {
		body["class"] = *class
	}
	if *cpu > 0 {
		body["cpu"] = *cpu
	}
	if *memory > 0 {
		body["memory"] = *memory
	}
	if *disk > 0 {
		body["disk"] = *disk
	}
	payloadBytes, _ := json.Marshal(body)

	msg, sig, walletAddr := signRequest(privKey, "create", "", json.RawMessage(payloadBytes))

	var bodyBuf bytes.Buffer
	json.NewEncoder(&bodyBuf).Encode(body) //nolint:errcheck

	req, err := http.NewRequest(http.MethodPost, *apiURL+"/api/sandbox", &bodyBuf)
	if err != nil {
		fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Wallet-Address", walletAddr)
	req.Header.Set("X-Signed-Message", msg)
	req.Header.Set("X-Wallet-Signature", sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatalf("create sandbox: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fatalf("create sandbox: HTTP %d: %s", resp.StatusCode, respBody)
	}

	var result map[string]any
	json.Unmarshal(respBody, &result) //nolint:errcheck
	fmt.Printf("Created sandbox: %s\n", prettyJSON(result))
}

// ── list ─────────────────────────────────────────────────────────────────────

func runList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	apiURL  := fs.String("api", "http://localhost:8080", "Billing proxy URL")
	keyHex  := fs.String("key", "",                     "User private key (hex); or set USER_KEY env")
	_ = fs.Parse(args)

	privKey := mustLoadKey(*keyHex)
	msg, sig, walletAddr := signRequest(privKey, "list", "", json.RawMessage(`{}`))

	req, err := http.NewRequest(http.MethodGet, *apiURL+"/api/sandbox", nil)
	if err != nil {
		fatalf("build request: %v", err)
	}
	req.Header.Set("X-Wallet-Address", walletAddr)
	req.Header.Set("X-Signed-Message", msg)
	req.Header.Set("X-Wallet-Signature", sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatalf("list sandboxes: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fatalf("list sandboxes: HTTP %d: %s", resp.StatusCode, respBody)
	}

	var result []any
	if err := json.Unmarshal(respBody, &result); err != nil {
		fmt.Println(string(respBody))
		return
	}
	if len(result) == 0 {
		fmt.Println("No sandboxes.")
		return
	}
	for _, s := range result {
		fmt.Println(prettyJSON(s))
	}
}

// ── stop ─────────────────────────────────────────────────────────────────────

func runStop(args []string) {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	apiURL := fs.String("api", "http://localhost:8080", "Billing proxy URL")
	keyHex := fs.String("key", "",                     "User private key (hex); or set USER_KEY env")
	id     := fs.String("id",  "",                     "Sandbox ID (required)")
	_ = fs.Parse(args)

	if *id == "" {
		fatalf("--id is required")
	}
	privKey := mustLoadKey(*keyHex)
	msg, sig, walletAddr := signRequest(privKey, "stop", *id, json.RawMessage(`{}`))

	req, err := http.NewRequest(http.MethodPost, *apiURL+"/api/sandbox/"+*id+"/stop", nil)
	if err != nil {
		fatalf("build request: %v", err)
	}
	req.Header.Set("X-Wallet-Address", walletAddr)
	req.Header.Set("X-Signed-Message", msg)
	req.Header.Set("X-Wallet-Signature", sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatalf("stop sandbox: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fatalf("stop sandbox: HTTP %d: %s", resp.StatusCode, respBody)
	}
	fmt.Printf("Stopped sandbox %s\n", *id)
}

// ── delete ───────────────────────────────────────────────────────────────────

func runDelete(args []string) {
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	apiURL := fs.String("api", "http://localhost:8080", "Billing proxy URL")
	keyHex := fs.String("key", "",                     "User private key (hex); or set USER_KEY env")
	id     := fs.String("id",  "",                     "Sandbox ID (required)")
	_ = fs.Parse(args)

	if *id == "" {
		fatalf("--id is required")
	}
	privKey := mustLoadKey(*keyHex)
	msg, sig, walletAddr := signRequest(privKey, "delete", *id, json.RawMessage(`{}`))

	req, err := http.NewRequest(http.MethodDelete, *apiURL+"/api/sandbox/"+*id, nil)
	if err != nil {
		fatalf("build request: %v", err)
	}
	req.Header.Set("X-Wallet-Address", walletAddr)
	req.Header.Set("X-Signed-Message", msg)
	req.Header.Set("X-Wallet-Signature", sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatalf("delete sandbox: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fatalf("delete sandbox: HTTP %d: %s", resp.StatusCode, respBody)
	}
	fmt.Printf("Deleted sandbox %s\n", *id)
}

// ── exec ─────────────────────────────────────────────────────────────────────

// runExec runs a shell command inside a sandbox via the toolbox API and prints stdout/stderr.
func runExec(args []string) {
	fs := flag.NewFlagSet("exec", flag.ExitOnError)
	apiURL  := fs.String("api",     "http://localhost:8080", "Billing proxy URL")
	keyHex  := fs.String("key",     "",                     "User private key (hex); or set USER_KEY env")
	id      := fs.String("id",      "",                     "Sandbox ID (required)")
	command := fs.String("cmd",     "",                     "Shell command to run (required)")
	timeout := fs.Int("timeout",    30,                     "Timeout in seconds")
	_ = fs.Parse(args)

	if *id == "" {
		fatalf("--id is required")
	}
	if *command == "" {
		fatalf("--cmd is required")
	}

	privKey := mustLoadKey(*keyHex)
	msg, sig, walletAddr := signRequest(privKey, "toolbox", *id, json.RawMessage(`{}`))

	body, _ := json.Marshal(map[string]any{"command": *command, "timeout": *timeout})
	url := *apiURL + "/api/toolbox/" + *id + "/toolbox/process/execute"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		fatalf("build request: %v", err)
	}
	req.Header.Set("X-Wallet-Address", walletAddr)
	req.Header.Set("X-Signed-Message", msg)
	req.Header.Set("X-Wallet-Signature", sig)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatalf("exec: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fatalf("exec: HTTP %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		ExitCode int    `json:"exitCode"`
		Result   string `json:"result"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		fmt.Print(string(respBody))
		return
	}
	printSandboxOutput(*id, *command, result.Result, result.ExitCode)
	if result.ExitCode != 0 {
		os.Exit(result.ExitCode)
	}
}

// printSandboxOutput renders sandbox output in a bordered box with ANSI colors.
func printSandboxOutput(id, command, output string, exitCode int) {
	const (
		cyan  = "\033[36m"
		red   = "\033[31m"
		reset = "\033[0m"
		width = 80
		inner = width - 2 // inside the borders
	)

	shortID := id
	if len(id) > 8 {
		shortID = id[:8]
	}

	runeWidth := utf8.RuneCountInString

	// Truncate command if too long for header
	label := fmt.Sprintf(" sandbox:%s  $ %s ", shortID, command)
	for runeWidth(label) > inner {
		runes := []rune(label)
		label = string(runes[:len(runes)-1])
	}
	labelPadded := label + strings.Repeat(" ", inner-runeWidth(label))

	statusMark := " ok "
	borderColor := cyan
	if exitCode != 0 {
		statusMark = fmt.Sprintf(" exit %d ", exitCode)
		borderColor = red
	}
	bottomFill := strings.Repeat("─", inner-runeWidth(statusMark))

	fmt.Print(cyan + "┌" + strings.Repeat("─", inner) + "┐" + reset + "\n")
	fmt.Print(cyan + "│" + reset + labelPadded + cyan + "│" + reset + "\n")

	if output != "" {
		fmt.Print(cyan + "├" + strings.Repeat("─", inner) + "┤" + reset + "\n")
		lineWidth := inner - 2 // 1 space padding each side
		for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
			runes := []rune(line)
			for len(runes) > lineWidth {
				fmt.Print(cyan + "│" + reset + " " + string(runes[:lineWidth]) + " " + cyan + "│" + reset + "\n")
				runes = runes[lineWidth:]
			}
			fmt.Printf("%s│%s %-*s %s│%s\n", cyan, reset, lineWidth, string(runes), cyan, reset)
		}
	}
	fmt.Print(borderColor + "└" + bottomFill + statusMark + "┘" + reset + "\n")
}

// ── toolbox ──────────────────────────────────────────────────────────────────

// runToolbox makes an arbitrary toolbox API call and prints the response.
func runToolbox(args []string) {
	fs := flag.NewFlagSet("toolbox", flag.ExitOnError)
	apiURL  := fs.String("api",    "http://localhost:8080", "Billing proxy URL")
	keyHex  := fs.String("key",    "",                     "User private key (hex); or set USER_KEY env")
	id      := fs.String("id",     "",                     "Sandbox ID (required)")
	action  := fs.String("action", "",                     "Toolbox action path, e.g. files, git/status (required)")
	method  := fs.String("method", "GET",                  "HTTP method")
	body    := fs.String("body",   "",                     "Request body (JSON)")
	_ = fs.Parse(args)

	if *id == "" {
		fatalf("--id is required")
	}
	if *action == "" {
		fatalf("--action is required")
	}

	privKey := mustLoadKey(*keyHex)
	msg, sig, walletAddr := signRequest(privKey, "toolbox", *id, json.RawMessage(`{}`))

	url := *apiURL + "/api/toolbox/" + *id + "/toolbox/" + strings.TrimPrefix(*action, "/")
	req, err := http.NewRequest(strings.ToUpper(*method), url, strings.NewReader(*body))
	if err != nil {
		fatalf("build request: %v", err)
	}
	req.Header.Set("X-Wallet-Address", walletAddr)
	req.Header.Set("X-Signed-Message", msg)
	req.Header.Set("X-Wallet-Signature", sig)
	if *body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatalf("toolbox: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fatalf("toolbox: HTTP %d: %s", resp.StatusCode, respBody)
	}
	fmt.Println(prettyJSON(json.RawMessage(respBody)))
}

// ── start ─────────────────────────────────────────────────────────────────────

func runStart(args []string) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	apiURL := fs.String("api", "http://localhost:8080", "Billing proxy URL")
	keyHex := fs.String("key", "", "User private key (hex); or set USER_KEY env")
	id     := fs.String("id",  "", "Sandbox ID (required)")
	_ = fs.Parse(args)

	if *id == "" {
		fatalf("--id is required")
	}

	privKey := mustLoadKey(*keyHex)
	msg, sig, walletAddr := signRequest(privKey, "start", *id, json.RawMessage(`{}`))

	url := *apiURL + "/api/sandbox/" + *id + "/start"
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		fatalf("build request: %v", err)
	}
	req.Header.Set("X-Wallet-Address", walletAddr)
	req.Header.Set("X-Signed-Message", msg)
	req.Header.Set("X-Wallet-Signature", sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatalf("start: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fatalf("start: HTTP %d: %s", resp.StatusCode, body)
	}
	fmt.Printf("Started sandbox %s\n", *id)
}

// ── ssh-access ────────────────────────────────────────────────────────────────

// runSSHAccess fetches a temporary SSH token and prints the connect command.
func runSSHAccess(args []string) {
	fs := flag.NewFlagSet("ssh-access", flag.ExitOnError)
	apiURL := fs.String("api", "http://localhost:8080", "Billing proxy URL")
	keyHex := fs.String("key", "", "User private key (hex); or set USER_KEY env")
	id     := fs.String("id",  "", "Sandbox ID (required)")
	_ = fs.Parse(args)

	if *id == "" {
		fatalf("--id is required")
	}

	privKey := mustLoadKey(*keyHex)
	msg, sig, walletAddr := signRequest(privKey, "ssh-access", *id, json.RawMessage(`{}`))

	url := *apiURL + "/api/sandbox/" + *id + "/ssh-access"
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		fatalf("build request: %v", err)
	}
	req.Header.Set("X-Wallet-Address", walletAddr)
	req.Header.Set("X-Signed-Message", msg)
	req.Header.Set("X-Wallet-Signature", sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatalf("ssh-access: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fatalf("ssh-access: HTTP %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Token      string `json:"token"`
		ExpiresAt  string `json:"expiresAt"`
		SSHCommand string `json:"sshCommand"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		fatalf("parse response: %v", err)
	}

	// Replace localhost with the actual host from the API URL
	apiHost := strings.TrimPrefix(strings.TrimPrefix(*apiURL, "https://"), "http://")
	apiHost = strings.Split(apiHost, ":")[0]
	sshCmd := strings.ReplaceAll(result.SSHCommand, "localhost", apiHost)

	fmt.Println(sshCmd)
	if result.ExpiresAt != "" {
		fmt.Fprintf(os.Stderr, "Token expires: %s\n", result.ExpiresAt)
	}
	fmt.Fprintf(os.Stderr, "Password: %s\n", result.Token)
}

// runListSnapshots lists available Daytona snapshots.
// The /api/snapshots endpoint is public — no auth required.
func runListSnapshots(args []string) {
	fs := flag.NewFlagSet("snapshots", flag.ExitOnError)
	apiURL := fs.String("api", "http://localhost:8080", "0G Sandbox service URL")
	_ = fs.Parse(args)

	req, err := http.NewRequest(http.MethodGet, *apiURL+"/api/snapshots", nil)
	if err != nil {
		fatalf("build request: %v", err)
	}

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
		Items []any `json:"items"`
		Total int   `json:"total"`
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
		fmt.Println(prettyJSON(s))
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// signRequest builds the three auth headers required by the billing proxy.
// Returns (X-Signed-Message value, X-Wallet-Signature value, X-Wallet-Address value).
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

	reqObj := signedRequest{
		Action:     action,
		ExpiresAt:  time.Now().Add(3 * time.Minute).Unix(),
		Nonce:      nonce,
		Payload:    payload,
		ResourceID: resourceID,
	}
	msgBytes, err := json.Marshal(reqObj)
	if err != nil {
		fatalf("marshal signed request: %v", err)
	}

	// EIP-191: keccak256("\x19Ethereum Signed Message:\n<len><msg>")
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(msgBytes))
	hash := crypto.Keccak256([]byte(prefix), msgBytes)

	sigBytes, err := crypto.Sign(hash, privKey)
	if err != nil {
		fatalf("sign: %v", err)
	}
	sigBytes[64] += 27 // V: 0/1 → 27/28 (Ethereum convention)

	return base64.StdEncoding.EncodeToString(msgBytes),
		"0x" + hex.EncodeToString(sigBytes),
		addr.Hex()
}

// mustLoadKey loads a private key from the flag value or USER_KEY env var.
func mustLoadKey(keyHex string) *ecdsa.PrivateKey {
	if keyHex == "" {
		keyHex = os.Getenv("USER_KEY")
	}
	if keyHex == "" {
		fatalf("private key required: use --key or USER_KEY env")
	}
	privKey, err := crypto.HexToECDSA(strings.TrimPrefix(keyHex, "0x"))
	if err != nil {
		fatalf("parse private key: %v", err)
	}
	return privKey
}

// mustDialContract dials the RPC and binds the SandboxServing contract.
func mustDialContract(ctx context.Context, rpcURL, contractHex string) (*ethclient.Client, *chain.SandboxServing) {
	eth, err := ethclient.Dial(rpcURL)
	if err != nil {
		fatalf("dial rpc: %v", err)
	}
	contract, err := chain.NewSandboxServing(common.HexToAddress(contractHex), eth)
	if err != nil {
		eth.Close()
		fatalf("bind contract: %v", err)
	}
	_ = ctx
	return eth, contract
}

func ogToNeuron(og float64) *big.Int {
	ogBig := new(big.Float).SetFloat64(og)
	neuronBig := new(big.Float).Mul(ogBig,
		new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)))
	n, _ := neuronBig.Int(nil)
	return n
}

func neuronTo0G(neuron *big.Int) float64 {
	f, _ := new(big.Float).Quo(
		new(big.Float).SetInt(neuron),
		new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)),
	).Float64()
	return f
}

func prettyJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
