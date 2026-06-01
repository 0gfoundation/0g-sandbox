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
  auth/       EIP-191 signature verification, nonce replay protection
  billing/    OnCreate/OnStart/OnStop voucher handlers + periodic compute generator
  chain/      go-ethereum binding wrapper; SettleFeesWithTEE, nonce seeding from chain
  config/     env-var config loading (viper)
  daytona/    Daytona HTTP client (create/stop/list sandboxes)
  events/     event log (audit trail for billing actions)
  proxy/      gin handler: proxies Daytona, enforces sandbox ownership
    seal.go             InjectSeal, stripSealKey — sealed container attestation
    sealdebug_off.go    production: sealed → blocks SSH/toolbox
    sealdebug_on.go     sealdebug build: sealed → attestation injected, SSH/toolbox open
  registry/
    digest.go           GetDigest — resolves image ref to sha256 content digest
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

### Sealed Containers (`sealed: true`)

When a sandbox create request includes `"sealed": true`, the proxy:

1. Resolves the image/snapshot reference to its content digest via `registry.GetDigest`
   (uses `crane.Digest`; insecure mode for `registry:` or `localhost:` hosts)
2. Generates an ephemeral secp256k1 keypair for the container's signing identity
3. Builds a TEE-signed attestation over `{sealId, pubkey, imageHash, ts}`:
   ```
   message = keccak256("ImageAttestation:" + sealId + ":" + pubkey + ":" + imageHash + ":" + ts)
   ```
   Signature V is normalised to 27/28 (Ethereum `ecrecover` convention)
4. Injects two env vars into the container:
   - `SANDBOX_SEAL_KEY` — hex private key; the container's signing identity.
     Never returned through the API; stripped from the create response.
   - `SANDBOX_SEAL_ATTESTATION` — JSON: `{seal_id, pubkey, image_hash, signature, ts}`
5. Sets label `0g-sealed: "true"` → blocks SSH and toolbox access for the sandbox lifetime
6. Sets label `0g-seal-id: <32-char hex>` → operators can correlate sandbox ↔ attestation

Sealing requires the image to be present in the internal registry (needed to resolve the
content digest). A non-resolvable image reference is a hard failure; the create request is
rejected.

The container-side runtime that consumes `SANDBOX_SEAL_KEY` + `SANDBOX_SEAL_ATTESTATION`
(provision via attestor, mount agent_seal_priv, run the agent-fronting proxy at :8080,
emit serve-proof, expose `/sign/*` via unix socket) lives in the
[0g-agentic-id](https://github.com/0gfoundation/0g-agentic-id) repo under `sealed/`.

**`SEALED_ONLY=true`** — provider-wide gate. When this env is set, every
`POST /api/sandbox` request that does not carry `"sealed": true` is rejected with
HTTP 400 before any work happens (no balance reservation, no Daytona call). Use this
for providers that only host attested workloads (e.g. an AgenticID-only operator).
The current setting is also surfaced through `GET /info` as `sealed_only`, so
clients can pre-check.

**sealdebug build tag** — for development/inspection of sealed containers:
- Default (production) build: `sealed: true` → blocks SSH and toolbox
- `go build -tags sealdebug`: `sealed: true` → TEE attestation injected but SSH/toolbox remain open
- Dockerfile: `--build-arg BUILD_TAGS=sealdebug`

### `public: true` for All Sandboxes

`InjectOwner` always injects `"public": true` into every sandbox create request. Daytona OIDC
is not used in 0G — sandbox management is controlled via EIP-191 (billing proxy). With
`public: true`, user-defined service ports (e.g. 8080, 9090) are accessible via the Daytona
proxy URL without an OIDC session. System ports (22222/TERMINAL, 2280/TOOLBOX, 33333/RECORDING)
remain protected by Daytona regardless of this flag.

**Proxy URL format:** `http://<port>-<sandboxId>.<PROXY_DOMAIN>/<path>`

The `PROXY_DOMAIN` env var controls the URL format. Examples:
- nip.io (no real domain): `PROXY_DOMAIN=<your-ip>.nip.io:4000`
  → `http://8080-<sandboxId>.<your-ip>.nip.io:4000/result`
- Real domain with nginx: `PROXY_DOMAIN=sandbox.yourdomain.com`
  (nginx listens on 80, proxies to Daytona port 4000 with `proxy_set_header Host $host`)

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

# sealdebug build — attestation injected but SSH/toolbox remain open (dev/inspection)
go build -tags sealdebug ./...
go test -tags sealdebug ./...
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
PROXY_DOMAIN=<your-ip>.nip.io:4000 \
go run ./cmd/billing/
```

`PROXY_DOMAIN` controls the URL format for accessing user-defined service ports inside the
sandbox. Format: `http://<port>-<sandboxId>.<PROXY_DOMAIN>/<path>`. The Daytona proxy listens
on port 4000. With a real domain and nginx fronting port 80, omit the port suffix.

The server starts on port 8080 (`PORT` env var) and exposes:

**Public / unauthenticated:**
- `GET /healthz` — liveness probe
- `GET /dashboard` — operator dashboard (embedded HTML)
- `GET /info` — provider info (address, contract, pricing, `sealed_only`)
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
- `GET /api/events` — on-chain VoucherSettled events

**Admin-only (caller wallet must be in `ADMIN_ADDRESSES`):**
- `POST /api/snapshots` — create snapshot
- `DELETE /api/snapshots/:id` — delete snapshot
- `POST /api/registry/pull` — pull image into internal registry
- `POST /api/registry/gc` — garbage-collect orphan derived tags
- `POST /api/archive-all` — archive every running sandbox + clears Redis sessions
- `DELETE /api/sandbox/force/:id` — delete any sandbox regardless of owner
- `GET /api/sessions` — list all open billing sessions across owners
- `GET /api/audit-log` — local Redis billing event log (created/stopped/auto_stopped/settled)

`ADMIN_ADDRESSES` is comma-separated. When unset, defaults to `[PROVIDER_ADDRESS]` for
backward compatibility with single-key deployments. Distinct from `PROVIDER_ADDRESS`
(the on-chain settlement identity), so multiple operators can manage infrastructure
without holding the provider's settlement key.

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

Deploying to a 0G Tapp TEE server via `tapp-cli`. app-id: `0g-sandbox`.

### One-time setup

```bash
export TAPP_PRIVATE_KEY=0x<key>
TAPP_SERVER=http://<tapp-server-host>:50051

# Login to container registry (one-time per server)
tapp-cli -s $TAPP_SERVER docker-login \
  -r <registry-host> \
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
  --api http://<sandbox-host>:8080 \
  --tee-signer <tee-address-from-step3> \
  --price-per-cpu <neuron/cpu/min> \
  --price-per-mem <neuron/memGB/min> \
  --create-fee <neuron>

# 5. Restart to pick up the new registration (must use stop-app/start-app, not stop-service/start-service)
tapp-cli -s $TAPP_SERVER stop-app --app-id 0g-sandbox
tapp-cli -s $TAPP_SERVER start-app -f docker-compose.yml --app-id 0g-sandbox
```

### Redeploy after code changes

```bash
# 1. Build and push (production — sealed sandboxes block SSH/toolbox)
docker build --target sandbox -t <registry>/<image>:latest .
docker push <registry>/<image>:latest

# 1a. Debug build — sealed sandboxes get attestation but SSH/toolbox remain open
docker build --target sandbox --build-arg BUILD_TAGS=sealdebug \
  -t <registry>/<image>:sealdebug .
docker push <registry>/<image>:sealdebug

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
- **`RESOURCE_LIMITS_DISABLED=false` on the runner service is load-bearing.** The `daytonaio/daytona-runner` image bakes `ENV RESOURCE_LIMITS_DISABLED=true` into its Dockerfile, so omitting the var in compose falls back to `true` and the runner skips setting Docker CFS / memory cgroup limits — every sandbox runs unconstrained on host resources regardless of snapshot tier. To verify: `docker exec 0g-sandbox-runner-1 env | grep RESOURCE_LIMITS` should print `=false`, and any sandbox container's `docker inspect` should show non-zero `HostConfig.CpuQuota` / `Memory`.
