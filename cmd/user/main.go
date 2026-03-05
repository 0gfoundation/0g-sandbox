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
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: user <subcommand> [flags]")
	fmt.Fprintln(os.Stderr, "  chain:  balance | deposit | acknowledge")
	fmt.Fprintln(os.Stderr, "  api:    create | list | stop | delete | exec | toolbox")
}

// ── Shared chain flags ───────────────────────────────────────────────────────

type chainFlags struct {
	rpc      string
	chainID  int64
	contract string
}

func addChainFlags(fs *flag.FlagSet) *chainFlags {
	cf := &chainFlags{}
	fs.StringVar(&cf.rpc,      "rpc",      "https://evmrpc-testnet.0g.ai",                "RPC endpoint")
	fs.Int64Var(&cf.chainID,   "chain-id", 16602,                                          "Chain ID")
	fs.StringVar(&cf.contract, "contract", "0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210", "Settlement contract address")
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

	opts := &bind.CallOpts{Context: ctx}
	acct, err := contract.GetAccount(opts, walletAddr)
	if err != nil {
		fatalf("GetAccount: %v", err)
	}
	fmt.Printf("Address: %s\n", walletAddr.Hex())
	fmt.Printf("Balance: %s neuron  (%.6f 0G)\n", acct.Balance, neuronTo0G(acct.Balance))

	if *providerHex != "" {
		providerAddr := common.HexToAddress(*providerHex)
		nonce, err := contract.GetLastNonce(opts, walletAddr, providerAddr)
		if err != nil {
			fatalf("GetLastNonce: %v", err)
		}
		fmt.Printf("Nonce (vs provider %s): %s\n", providerAddr.Hex(), nonce)

		earnings, err := contract.GetProviderEarnings(opts, providerAddr)
		if err != nil {
			fatalf("GetProviderEarnings: %v", err)
		}
		fmt.Printf("Provider earnings: %s neuron  (%.6f 0G)\n", earnings, neuronTo0G(earnings))
	}
}

// ── deposit ──────────────────────────────────────────────────────────────────

func runDeposit(args []string) {
	fs := flag.NewFlagSet("deposit", flag.ExitOnError)
	cf := addChainFlags(fs)
	keyHex   := fs.String("key",      "", "User private key (hex); or set USER_KEY env")
	amount   := fs.Float64("amount",  0.01, "Amount to deposit in 0G (e.g. 0.01)")
	_ = fs.Parse(args)

	privKey := mustLoadKey(*keyHex)
	userAddr := crypto.PubkeyToAddress(privKey.PublicKey)

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
	fmt.Printf("Amount:   %.6f 0G (%s neuron)\n", *amount, depositWei)
	fmt.Printf("Contract: %s\n", cf.contract)

	fmt.Println("\n[1/1] Deposit...")
	tx, err := contract.Deposit(auth, userAddr)
	if err != nil {
		fatalf("Deposit: %v", err)
	}
	auth.Value = big.NewInt(0)
	fmt.Printf("      tx: %s\n", tx.Hash().Hex())
	if _, err := bind.WaitMined(ctx, eth, tx); err != nil {
		fatalf("wait mined: %v", err)
	}

	opts := &bind.CallOpts{Context: ctx}
	acct, _ := contract.GetAccount(opts, userAddr)
	fmt.Println("      confirmed ✓")
	fmt.Printf("\nNew balance: %s neuron  (%.6f 0G)\n", acct.Balance, neuronTo0G(acct.Balance))
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

// ── create ───────────────────────────────────────────────────────────────────

func runCreate(args []string) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	apiURL  := fs.String("api", "http://localhost:8080", "Billing proxy URL")
	keyHex  := fs.String("key", "",                     "User private key (hex); or set USER_KEY env")
	image   := fs.String("image", "",                   "Sandbox image (optional)")
	_ = fs.Parse(args)

	privKey := mustLoadKey(*keyHex)

	body := map[string]any{}
	if *image != "" {
		body["image"] = *image
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
	fmt.Print(result.Result)
	if result.ExitCode != 0 {
		os.Exit(result.ExitCode)
	}
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
