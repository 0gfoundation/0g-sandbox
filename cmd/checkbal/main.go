package main

import (
	"context"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/0gfoundation/0g-sandbox/internal/chain"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// resolveAddr returns the account to inspect. checkbal is read-only — it never
// signs — so an ADDRESS is all it needs; a private key (CHECK_KEY / USER_KEY
// env) is accepted only as a convenience and is never required. Never hardcode
// a key here: a previous revision embedded one in source, which is a permanent
// secret disclosure (git history keeps it — that key must be treated as burned).
func resolveAddr(addrFlag, keyFlag string) common.Address {
	if addrFlag != "" {
		return common.HexToAddress(addrFlag)
	}
	keyHex := keyFlag
	if keyHex == "" {
		keyHex = os.Getenv("CHECK_KEY")
	}
	if keyHex == "" {
		keyHex = os.Getenv("USER_KEY")
	}
	if keyHex == "" {
		fmt.Fprintln(os.Stderr, "usage: checkbal --addr 0x<address>  (or --key / CHECK_KEY / USER_KEY env)")
		os.Exit(2)
	}
	privKey, err := crypto.HexToECDSA(strings.TrimPrefix(keyHex, "0x"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid private key:", err)
		os.Exit(2)
	}
	return crypto.PubkeyToAddress(privKey.PublicKey)
}

func main() {
	addrFlag := flag.String("addr", "", "account address to inspect (read-only; preferred over a key)")
	keyFlag := flag.String("key", "", "private key (address is derived; never required — checkbal only reads)")
	rpcFlag := flag.String("rpc", "https://evmrpc-testnet.0g.ai", "RPC endpoint")
	contractFlag := flag.String("contract", envOr("SETTLEMENT_CONTRACT", "0x3D0F2D62A60c8e62095671FfB23D15Cc4C98ca7c"), "SandboxServing proxy address")
	flag.Parse()

	eth, err := ethclient.Dial(*rpcFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rpc dial:", err)
		os.Exit(1)
	}
	addr := resolveAddr(*addrFlag, *keyFlag)
	c, _ := chain.NewSandboxServing(common.HexToAddress(*contractFlag), eth)
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
	fmt.Printf("pricePerCPUPerMin:  %s neuron/min\n", svc.PricePerCPUPerMin)
	cpuPerSec := new(big.Int).Div(svc.PricePerCPUPerMin, big.NewInt(60))
	fmt.Printf("pricePerCPUPerSec:  %s neuron/sec (÷60)\n", cpuPerSec)
	fmt.Printf("pricePerMemGBPerMin:%s neuron/GB/min\n", svc.PricePerMemGBPerMin)
	memPerSec := new(big.Int).Div(svc.PricePerMemGBPerMin, big.NewInt(60))
	fmt.Printf("pricePerMemGBPerSec:%s neuron/GB/sec (÷60)\n", memPerSec)
	fmt.Printf("createFee:          %s neuron\n", svc.CreateFee)
	fmt.Printf("appId:              %s\n", svc.AppId)

	// Recent settled voucher events
	fmt.Println()
	fmt.Println("=== Recent VoucherSettled events (last 5000 blocks) ===")
	ctx := context.Background()
	chainClient := &chainReader{eth: eth, c: c, addr: common.HexToAddress(*contractFlag)}
	_ = chainClient
	_ = ctx
}

type chainReader struct {
	eth  *ethclient.Client
	c    *chain.SandboxServing
	addr common.Address
}
