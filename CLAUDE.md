# CLAUDE.md — 0G Sandbox

## What This Is

**0G Sandbox** provides private, isolated sandboxes for vibe coding, solving two problems
simultaneously:

1. **Isolation**: local environments aren't isolated enough for running untrusted or
   experimental code — each sandbox is a fully containerized Daytona workspace.
2. **Confidentiality**: remote servers are controlled by the host provider — the billing
   proxy and TEE signing key run inside a hardware TDX enclave managed by 0G Tapp, so
   the host cannot inspect workloads or forge billing vouchers. The provider never sees
   the user's code; sandbox workloads are opaque to the infrastructure operator.

The billing layer is a Go proxy server in front of Daytona that charges users in 0G tokens
via TEE-signed EIP-712 vouchers settled on-chain against a Solidity contract.

---

## Architecture

```
User ──► Billing Proxy (Go HTTP server)
              │
              ├── Auth       EIP-191 wallet signature → identifies caller
              ├── Proxy      forwards sandbox requests to Daytona, injects daytona-owner label
              ├── Billing    emits create-fee voucher on sandbox create; opens compute session
              ├── Generator  ticks every VoucherIntervalSec → emits compute vouchers
              ├── Settler    drains Redis queue, submits SettleFeesWithTEE tx on-chain
              └── StopHandler  calls Daytona stop on INSUFFICIENT_BALANCE, cleans Redis
                         │
              Daytona (sandbox runtime)

Solidity: BeaconProxy ──► UpgradeableBeacon ──► SandboxServing impl
          (stable addr)    (upgrade key)          (pure logic)
```

---

## Directory Structure

```
cmd/
  billing/    main server entry point
  deploy/     deploy beacon-proxy stack (3 steps: impl → beacon → proxy)
  upgrade/    upgrade via beacon.upgradeTo(newImpl)
  verify/     verify contracts on block explorer
  setup/      one-time on-chain setup (AddOrUpdateService + Deposit + AcknowledgeTEESigner)
  checkbal/   quick balance/nonce/earnings check for a private key
internal/
  auth/       EIP-191 signature verification, nonce replay protection
  billing/    OnCreate/OnStart/OnStop voucher handlers + periodic compute generator
  chain/      go-ethereum binding wrapper; SettleFeesWithTEE, nonce seeding from chain
  config/     env-var config loading (viper)
  daytona/    Daytona HTTP client (create/stop/list sandboxes)
  proxy/      gin handler: proxies Daytona, enforces sandbox ownership
  settler/    reads voucher queue from Redis, submits batch settlements
  tee/        TEE key retrieval (TDX gRPC in production, MOCK_TEE in dev)
  voucher/    EIP-712 signing + Redis queue (RPUSH/BLPOP) helpers
contracts/
  src/        SandboxServing.sol, proxy/UpgradeableBeacon.sol, proxy/BeaconProxy.sol
  abi/        extracted ABIs (input to abigen)
  out/        Foundry build artifacts (gitignored — run `make build-contracts` to populate)
```

---

## Key Concepts

### Token Units
- `1 0G = 10^18 neuron` (neuron is the smallest unit, analogous to ETH/wei)
- All on-chain amounts are **neuron** (big.Int)

### Billing Flow
1. User sends EIP-191-signed `POST /api/sandbox` → proxy authenticates, injects `daytona-owner`
   label, forwards to Daytona
2. `billing.OnCreate` emits a create-fee voucher + opens a compute session in Redis
3. `billing.RunGenerator` ticks every `VOUCHER_INTERVAL_SEC` → emits compute vouchers for all
   open sessions
4. `settler.Run` drains the Redis voucher queue, calls `SettleFeesWithTEE` on-chain in batches
5. On `INSUFFICIENT_BALANCE`: settler writes `stop:sandbox:<id>` to Redis
6. `runStopHandler` reads stop keys, calls Daytona stop, cleans up Redis keys

### Voucher (EIP-712)
```
SandboxVoucher(address user, address provider, bytes32 usageHash, uint256 nonce, uint256 totalFee)
```
Signed by the TEE key. Nonce is per `(user, provider)` pair; must be strictly increasing.

### TEE Key
- **Production**: fetched via gRPC from the tapp-daemon inside a TDX enclave
- **Development**: set `MOCK_TEE=true` and `MOCK_APP_PRIVATE_KEY=0x<hex>`

### Redis Keys
| Key | Purpose |
|-----|---------|
| `billing:compute:<sandboxID>` | Open compute session (JSON) |
| `billing:nonce:<user>:<provider>` | In-memory nonce counter (seeded from chain on startup) |
| `voucher:<providerAddr>` | Redis list queue of pending vouchers |
| `stop:sandbox:<sandboxID>` | Pending stop signal (value = reason string) |
| `auth:nonce:<nonce>` | Seen request nonces (replay protection, TTL-based) |

### Contract Upgrade Pattern (Beacon Proxy)
- `BeaconProxy` (stable address) stores all state; delegatecalls to impl via `UpgradeableBeacon`
- To upgrade: deploy new `SandboxServing` impl → call `beacon.upgradeTo(newImpl)`
- All balances, nonces, and service registrations are preserved across upgrades

---

## Build & Test

```bash
export PATH=$PATH:/usr/local/go/bin

# Compile contracts (requires Docker)
make build-contracts

# Regenerate Go bindings from ABI
make abigen

# Build Go
go build ./...

# Unit tests (no external dependencies)
go test ./...

# Chain integration tests (requires make build-contracts)
go test ./internal/chain/... -v

# Component tests — simulated chain + miniredis + mock Daytona (requires make build-contracts)
go test ./cmd/billing/ -v -run TestComponent

# E2E tests — real chain + Redis + Daytona
MOCK_TEE=true MOCK_APP_PRIVATE_KEY=0x<key> \
go test -v -tags e2e ./cmd/billing/ -run TestE2E -timeout 10m
```

See `TESTING.md` for full test documentation.

---

## Running the Server

```bash
# Copy and fill in .env.example, then:
MOCK_TEE=true \
MOCK_APP_PRIVATE_KEY=0x<hex-key> \
DAYTONA_API_URL=http://localhost:3000 \
DAYTONA_ADMIN_KEY=<key> \
SETTLEMENT_CONTRACT=0x<proxy-addr> \
RPC_URL=https://evmrpc-testnet.0g.ai \
CHAIN_ID=16602 \
go run ./cmd/billing/
```

The server starts on port 8080 (`PORT` env var) and exposes:
- `GET /healthz` — liveness probe
- `GET /dashboard` — static operator dashboard (embedded from `web/dashboard.html`)
- `POST /api/sandbox` — create sandbox (billing hook: create-fee voucher)
- `GET /api/sandbox` — list sandboxes (filtered to caller's own)
- `GET /api/sandbox/:id` — get sandbox (403 if not owner)
- `POST /api/sandbox/:id/stop` — stop sandbox (billing hook: final compute voucher)
- `DELETE /api/sandbox/:id` — delete sandbox (billing hook: final compute voucher)

### Dashboard

`web/dashboard.html` is embedded into the billing binary at build time via `//go:embed` in
`web/static.go` and served at `GET /dashboard`. The page currently shows static mock data.

**Style guide**: follows [chainscan-galileo.0g.ai](https://chainscan-galileo.0g.ai/) — light theme,
`#1e3de4` blue primary, `#f5f5f5` background, white cards, `Roboto Mono` for addresses/numbers,
Inter for body text.

**Tracked issue**: [#14 — connect dashboard to real billing data](https://github.com/0gfoundation/0g-sandbox/issues/14)
(add `/admin/*` endpoints returning live Redis data; update dashboard to `fetch()` them).

---

## First-Time Contract Setup

```bash
# 1. Deploy impl + beacon + proxy
go run ./cmd/deploy/ --rpc https://evmrpc-testnet.0g.ai --key 0x<deployer-key> --chain-id 16602
# → set SETTLEMENT_CONTRACT=<proxy address>

# 2. Register provider service and fund the account
MOCK_APP_PRIVATE_KEY=0x<tee-key> go run ./cmd/setup/ \
  --rpc https://evmrpc-testnet.0g.ai \
  --contract 0x<proxy>

# 3. Check balance/nonce
go run ./cmd/checkbal/
```
