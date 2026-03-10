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
  setup/      legacy one-time setup script (superseded by cmd/provider)
  provider/   provider CLI: register, status, withdraw, snapshot management
  user/       user CLI: create/stop/delete sandbox, exec, balance
  checkbal/   quick balance/nonce/earnings check for a private key
internal/
  admin/      /admin/* endpoints (status, sandboxes, events, archive-all)
  auth/       EIP-191 signature verification, nonce replay protection
  billing/    OnCreate/OnStart/OnStop voucher handlers + periodic compute generator
  chain/      go-ethereum binding wrapper; SettleFeesWithTEE, nonce seeding from chain
  config/     env-var config loading (viper)
  daytona/    Daytona HTTP client (create/stop/list sandboxes)
  events/     event log (audit trail for billing actions)
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
   - Compute price = `cpu × PRICE_PER_CPU_PER_SEC + memGB × PRICE_PER_MEM_GB_PER_SEC`
   - Falls back to flat `COMPUTE_PRICE_PER_SEC` if per-resource prices are both 0
   - On-chain `Service` values take priority over env var fallbacks
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

**Public / unauthenticated:**
- `GET /healthz` — liveness probe
- `GET /dashboard` — operator dashboard (embedded HTML)
- `GET /info` — provider info (address, contract, pricing)
- `GET /api/providers` — list registered providers
- `GET /api/snapshots` — list available snapshots
- `GET /api/sandbox_list` — list all sandboxes (admin view, no auth)
- `GET /api/registry/images` — list images in internal registry

**Authenticated (EIP-191 wallet signature):**
- `POST /api/sandbox` — create sandbox (billing: create-fee voucher)
- `GET /api/sandbox` — list sandboxes (filtered to caller's own)
- `GET /api/sandbox/paginated` — paginated list
- `GET /api/sandbox/:id` — get sandbox (403 if not owner)
- `DELETE /api/sandbox/:id` — delete sandbox (billing: final compute voucher)
- `GET /api/volumes` — list volumes owned by caller
- `POST /api/snapshots` — create snapshot (provider only)
- `DELETE /api/snapshots/:id` — delete snapshot (provider only)
- `POST /api/registry/pull` — pull image into internal registry
- `GET /api/sessions` — list open billing sessions
- `GET /api/events` — event log
- `POST /api/archive-all` — archive all stopped sandboxes

**Admin (DAYTONA_ADMIN_KEY header):**
- `GET /admin/status` — server status + stats
- `GET /admin/sandboxes` — all sandboxes across all users
- `GET /admin/events` — full event log
- `POST /admin/archive-all` — force archive all

### Dashboard

`web/dashboard.html` is embedded into the billing binary at build time via `//go:embed` in
`web/static.go` and served at `GET /dashboard`. Calls live API endpoints (`/info`, `/api/providers`,
`/api/sandbox_list`, `/api/snapshots`, etc.).


---

## First-Time Contract Setup

```bash
# 1. Deploy impl + beacon + proxy
go run ./cmd/deploy/ --rpc https://evmrpc-testnet.0g.ai --key 0x<deployer-key> --chain-id 16602
# → set SETTLEMENT_CONTRACT=<proxy address> in .env

# 2. Register provider service on-chain
#    TEE address must be known first (get via `tapp-cli get-app-key` after first deploy)
PROVIDER_KEY=0x<provider-key> go run ./cmd/provider/ register \
  --api http://<billing-host>:8080 \
  --tee-signer <tee-address> \
  --price-per-cpu <neuron/cpu/min> \
  --price-per-mem <neuron/memGB/min> \
  --create-fee <neuron>

# 3. Check balance/nonce/earnings
go run ./cmd/checkbal/
```

---

## Tapp Production Deployment

Deploying to a 0G Tapp TEE server via `tapp-cli`. Server: `43.106.147.28:50051`, app-id: `0g-sandbox`.

### One-time setup

```bash
export TAPP_PRIVATE_KEY=0x<key>
TAPP_SERVER=http://43.106.147.28:50051

# Login to Alibaba Cloud VPC registry (one-time per server)
tapp-cli -s $TAPP_SERVER docker-login \
  -r eliza-registry-vpc.ap-southeast-1.cr.aliyuncs.com \
  -u <user> -p <password>
```

### First deploy (chicken-and-egg order)

TEE address is only known after the app starts — so registration must happen after first deploy.

```bash
# 1. Prepare env (must be named .env for docker compose to pick it up)
cp .env.testnet .env

# 2. Deploy
tapp-cli -s $TAPP_SERVER start-app -f docker-compose.yml --app-id 0g-sandbox
tapp-cli -s $TAPP_SERVER get-task-status --task-id <task-id>

# 3. Get TEE address (needed for on-chain registration)
tapp-cli -s $TAPP_SERVER get-app-key --app-id 0g-sandbox
# → e.g. 0xe29b6f4e65a796d77196faf511e0e0b859503656

# 4. Register provider service on-chain (use PROVIDER_KEY = provider wallet private key)
PROVIDER_KEY=0x<provider-key> go run ./cmd/provider/ register \
  --api http://43.106.147.28:8080 \
  --tee-signer <tee-address-from-step3> \
  --price-per-cpu <neuron/cpu/min> \
  --price-per-mem <neuron/memGB/min> \
  --create-fee <neuron>

# 5. Restart billing service to pick up the new registration
tapp-cli -s $TAPP_SERVER stop-service --app-id 0g-sandbox --service-name billing
tapp-cli -s $TAPP_SERVER start-service --app-id 0g-sandbox --service-name billing
```

### Redeploy after code changes

```bash
# 1. Build and push
docker build -t eliza-registry-vpc.ap-southeast-1.cr.aliyuncs.com/eliza/0g-sandbox:latest .
docker push eliza-registry-vpc.ap-southeast-1.cr.aliyuncs.com/eliza/0g-sandbox:latest

# 2. Redeploy
tapp-cli -s $TAPP_SERVER stop-app --app-id 0g-sandbox
tapp-cli -s $TAPP_SERVER start-app -f docker-compose.yml --app-id 0g-sandbox
tapp-cli -s $TAPP_SERVER get-task-status --task-id <task-id>
```

### Key notes
- `BACKEND_APP_NAME` in `.env` must match the tapp app-id exactly (`0g-sandbox`)
- `.env` is uploaded because docker-compose.yml mounts `./.env:/app/.env:ro` — this mount's only purpose is to trigger tapp-cli to upload the file; docker compose on the server reads it from the working directory for `${VAR}` substitution
- Provider wallet needs 100 0G staked on first registration
- `cmd/provider register` uses `PROVIDER_KEY` env var (not `MOCK_APP_PRIVATE_KEY`)
