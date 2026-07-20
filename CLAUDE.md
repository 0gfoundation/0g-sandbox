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
  provider/   provider CLI (signed by the appId OWNER key): register (binds signer→appId), remove-service, rotate, status, withdraw, snapshot management
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

### Identity Model (v2: provider IS the TEE signer)
- The **provider address** (services key, voucher payee, `(user, provider)`
  balance bucket, earnings ledger) is the node's TEE signer address, derived
  from the TEE key at runtime. There is no separate provider wallet.
- The **owner** (the appId's TappRegistry owner — resolved on-chain from
  `getAppInfo(BACKEND_APP_NAME).owner`, never configured) does all
  management: `register --signer`, `remove-service`, `withdraw`, `rotate`.
  Owner is always an admin; `ADMIN_ADDRESSES` adds extra operator wallets.
- Settlement requires the voucher to be signed **by its own payee**
  (`recovered == v.provider`) and that address to be an active TappRegistry
  node — one node can never settle vouchers naming another node.
- **Machine rebuild = signer rotation** (service restarts keep the key):
  the settler holds its queue until the new signer is a registered node;
  `cmd/provider rotate` migrates the service entry; users move balances off
  the old signer via the normal refund flow.

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
The current setting is also surfaced through `GET /api/info` as `sealed_only`, so
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

**`publicPorts` (per-port public preview)** — a create request may include
`"publicPorts": [8080, 3000]`: only listed ports are publicly reachable; all other
ports fall back to Daytona's private-sandbox auth (owner preview tokens still work).
Omit for the default all-ports-public behavior. Requires the 0g-daytona fork images
(compose defaults to them via `REGISTRY_PREFIX`); against stock Daytona images
the billing proxy rejects such creates with 502 instead of silently ignoring the
restriction. Rules: max 16 ports, system ports (22222/2280/33333) rejected, immutable
after create, sealed sandboxes must include 8080. Successful creates return
`preview_urls: {"8080": "http://8080-<id>.<PROXY_DOMAIN>"}`.

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

See `docs/TESTING.md` for full test documentation.

---

## Running the Server

```bash
# Copy and fill in .env.example, then:
MOCK_TEE=true \
MOCK_APP_PRIVATE_KEY=0x<hex-key> \
DAYTONA_API_URL=http://localhost:3000 \
DAYTONA_ADMIN_KEY=<key> \
SETTLEMENT_CONTRACT=0x<proxy-addr> \
TAPP_REGISTRY=0x<tapp-registry-addr> \
BACKEND_APP_NAME=<tapp-app-id> \
RPC_URL=https://evmrpc-testnet.0g.ai \
CHAIN_ID=16602 \
PROXY_DOMAIN=<your-ip>.nip.io:4000 \
go run ./cmd/billing/
```

`TAPP_REGISTRY` and `BACKEND_APP_NAME` are required — at startup the billing
server derives its provider identity from the TEE key (**provider IS the TEE
signer** — there is no provider wallet), resolves the app owner from
`getAppInfo(BACKEND_APP_NAME).owner` (standing admin, surfaced in /api/info),
and queries TappRegistry for node + ack state on every voucher.

`PROXY_DOMAIN` controls the URL format for accessing user-defined service ports inside the
sandbox. Format: `http://<port>-<sandboxId>.<PROXY_DOMAIN>/<path>`. The Daytona proxy listens
on port 4000. With a real domain and nginx fronting port 80, omit the port suffix.

The server starts on port 8080 (`PORT` env var) and exposes:

**Public / unauthenticated:**
- `GET /healthz` — liveness probe
- `GET /dashboard` — operator dashboard (embedded HTML)
- `GET /api/info` — provider info (address, contract, pricing, `sealed_only`)
- `GET /api/providers` — list registered providers
- `GET /api/snapshots` — list available snapshots
- `GET /api/sandbox_list` — list all sandboxes (admin view, no auth)
- `GET /api/registry/images` — list images in internal registry

**Authenticated (EIP-191 wallet signature):**
- `POST /api/sandbox` — create sandbox (billing: create-fee voucher)
- `GET /api/sandbox` — list sandboxes (filtered to caller's own)
- `GET /api/sandbox/paginated` — paginated list
- `GET /api/sandbox/:id` — get sandbox (admin or owner)
- `DELETE /api/sandbox/:id` — delete sandbox (admin or owner; billing: final compute voucher)
- `POST /api/sandbox/:id/start` — start a stopped sandbox (owner only)
- `POST /api/sandbox/:id/stop` — stop a running sandbox (admin or owner; billing: OnStop)
- `POST /api/sandbox/:id/archive` — archive a stopped sandbox (admin or owner)
- `POST /api/sandbox/:id/ensure-billing` — idempotent backfill if the create-time billing hook missed
- `POST /api/sandbox/:id/ssh-access` — owner-only; sealed sandboxes return 403
- `PUT /api/sandbox/:id/labels` — owner only (strips `daytona-owner` from the payload)
- `Any /api/sandbox/:id/<other>` — transparent Daytona proxy (owner only)
- `Any /api/toolbox/:id/*` — Daytona toolbox proxy (owner only, sealed sandboxes blocked)
- `GET /api/volumes` — list volumes owned by caller
- `GET /api/snapshots` `POST /api/snapshots` `DELETE /api/snapshots/:id` — snapshot mgmt
- `GET /api/events` — on-chain VoucherSettled events

Most management-plane routes use `withOwnerOrAdmin`: admins skip the owner
check, non-admins still need to own the sandbox. Routes that read or run the
user's workload (`/start`, `/ssh-access`, `/toolbox/*`, transparent
`/sandbox/:id/<other>`) intentionally stay owner-only — exposing them to
admins would let the operator read user code or trigger billable starts,
violating the workload-privacy guarantee.

**Admin-only (caller wallet must be in `ADMIN_ADDRESSES`):**
- `POST /api/registry/pull` — pull image into internal registry
- `POST /api/registry/gc` — garbage-collect orphan derived tags
- `POST /api/archive-all` — archive every running sandbox + clear Redis sessions
- `DELETE /api/sandbox/:id/force` — operator-intent override of `DELETE /api/sandbox/:id`
- `POST /api/sandbox/:id/force-stop` — operator-intent override of `POST /api/sandbox/:id/stop`
- `GET /api/sessions` — list all open billing sessions across owners
- `DELETE /api/sessions/:id` — close one orphan billing session (no Daytona action)
- `GET /api/audit-log` — local Redis billing event log (created/stopped/auto_stopped/settled)
- `GET /api/queue/summary` `GET /api/queue/dlq` — voucher queue depth + DLQ entries
- `POST /api/queue/dlq/discard` — discard a DLQ voucher
- `POST /api/queue/aggregate` — collapse pending vouchers for a `(user, provider)` pair
- `GET /api/observability` — queue depth + recent alert history for the dashboard

The two `/force*` paths predate `withOwnerOrAdmin` and remain as explicit
operator-intent endpoints so log/audit grep is unambiguous.

The appId's TappRegistry owner is **always** an admin — resolved live from
the chain (`getAppInfo(BACKEND_APP_NAME).owner`), never configured;
`ADMIN_ADDRESSES` is an additive list of extra operator wallets. The on-chain
settlement identity (the provider address) is the TEE signer and never
appears in admin config.

### Dashboard

`web/dashboard.html` is embedded into the billing binary at build time via `//go:embed` in
`web/static.go` and served at `GET /dashboard`. Calls live API endpoints (`/api/info`,
`/api/providers`, `/api/sandbox_list`, `/api/snapshots`, etc.).


---

## First-Time Contract Setup

SandboxServing delegates TEE signer identity, per-node stake, and user
acknowledgement to **TappRegistry** (separate contract; lives in the
[0g-tapp](https://github.com/0gfoundation/0g-tapp) repo). First-time setup
spans both contracts.

```bash
# 0. TappRegistry must already be deployed on the target chain.
#    Get its address from the 0g-tapp repo's deployment record.

# 1. Deploy SandboxServing impl + beacon + proxy, bound to TappRegistry.
go run ./cmd/deploy/ \
  --rpc      https://evmrpc-testnet.0g.ai \
  --chain-id 16602 \
  --tapp     0x<tapp-registry-address> \
  --key      0x<deployer-key>
# Outputs proxy address. Set in .env:
#   SETTLEMENT_CONTRACT=<proxy address>
#   TAPP_REGISTRY=<tapp-registry-address>

# 2. Start the app on a tapp server. This provisions the TEE, generates the
#    app's signing key, and exposes the gRPC endpoint that doubles as its
#    on-chain teeUrl. Done from the 0g-tapp repo / tapp-cli.
tapp-cli -s http://<tapp-server>:50051 start-app -f docker-compose.yml --app-id <appId>

# 3. Register the app in TappRegistry on-chain. tapp-cli reads the
#    composeHash / volumesHash / imageHashes and the TEE signerAddress
#    directly from the tapp server in step 2, then sends the registerApp tx.
tapp-cli -s http://<tapp-server>:50051 -k 0x<deployer-key> register-onchain \
  --app-id   <appId> \
  --rpc-url  https://evmrpc-testnet.0g.ai \
  --contract 0x<tapp-registry-address> \
  --stake-wei <amount>

# 4. Authorize SandboxServing as an ack invalidator for this app.
#    Required so price changes in step 5 can invalidate user acks.
#    (No tapp-cli subcommand for this yet — call the contract directly
#     until tapp-cli adds `authorize-invalidator-onchain`.)
cast send 0x<tapp-registry-address> \
  "authorizeInvalidator(string,address)" "<appId>" 0x<SETTLEMENT_CONTRACT> \
  --rpc-url https://evmrpc-testnet.0g.ai \
  --private-key 0x<provider-key>

# 5. Bind the node's service to the appId + set prices. Signed by the appId
#    OWNER key; --signer is the node's TEE address (tapp-cli get-app-key, or
#    the billing server's /api/info `provider_address`).
OWNER_KEY=0x<owner-key> go run ./cmd/provider/ register \
  --app-id        <appId> \
  --signer        0x<node-tee-address> \
  --url           https://<sandbox-host> \
  --price-per-cpu <neuron/cpu/min> \
  --price-per-mem <neuron/memGB/min> \
  --fee           <neuron>

# 6. Check balance/nonce/earnings
go run ./cmd/checkbal/
```

---

## Tapp Production Deployment

Deploying to a 0G Tapp TEE server via `tapp-cli`. The tapp app-id is the same
string used as the `appId` in TappRegistry and in SandboxServing's `services()`
binding — keep them in sync.

### One-time setup

```bash
export TAPP_PRIVATE_KEY=0x<key>
TAPP_SERVER=http://<tapp-server-host>:50051
APP_ID=<app-id>                              # e.g. 0g-sandbox-provider

# Login to container registry (one-time per server)
tapp-cli -s $TAPP_SERVER docker-login \
  -r <registry-host> \
  -u <user> -p <password>
```

### First deploy

```bash
# 1. Prepare env (must be named .env for docker compose to pick it up).
#    Required: SETTLEMENT_CONTRACT, TAPP_REGISTRY,
#              BACKEND_APP_NAME (= $APP_ID).
cp .env.testnet .env

# 2. Deploy — this registers the app in TappRegistry with the composeHash and
#    image hashes derived from docker-compose.yml, and starts the TEE container.
tapp-cli -s $TAPP_SERVER start-app -f docker-compose.yml --app-id $APP_ID
tapp-cli -s $TAPP_SERVER get-task-status --task-id <task-id>

# 3. Authorize the SandboxServing contract to invalidate this app's acks
#    (so a future price change in SandboxServing bumps ackVersion in TappRegistry)
tapp-cli authorize-invalidator-onchain \
  --app-id   $APP_ID \
  --contract 0x<sandbox-serving-proxy>

# 4. Bind commercial terms to the appId in SandboxServing
PROVIDER_KEY=0x<provider-key> go run ./cmd/provider/ register \
  --api            http://<sandbox-host>:8080 \
  --app-id         $APP_ID \
  --price-per-cpu  <neuron/cpu/min> \
  --price-per-mem  <neuron/memGB/min> \
  --create-fee     <neuron>
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

# 2. Redeploy. If docker-compose.yml or any image digest changes, TappRegistry
#    treats this as a TEE-node / composeHash change → ackVersion bumps and
#    every user's prior acknowledgement is invalidated.
tapp-cli -s $TAPP_SERVER stop-app --app-id $APP_ID
tapp-cli -s $TAPP_SERVER start-app -f docker-compose.yml --app-id $APP_ID
tapp-cli -s $TAPP_SERVER get-task-status --task-id <task-id>
```

### Key notes
- `BACKEND_APP_NAME` in `.env` must match the tapp app-id exactly, and that same string is the `appId` registered in both TappRegistry and SandboxServing
- `.env` is uploaded because docker-compose.yml mounts `./.env:/app/.env:ro` — this mount's only purpose is to trigger tapp-cli to upload the file; docker compose on the server reads it from the working directory for `${VAR}` substitution
- The provider wallet only needs enough 0G to pay gas for `addOrUpdateService` and ongoing `settleFeesWithTEE` batches; SandboxServing no longer holds a provider stake
- `cmd/provider register` uses `OWNER_KEY` env var — the appId owner's key (`PROVIDER_KEY` accepted as a legacy alias)
- Updating prices via `cmd/provider register` bumps `ackVersion(appId)` in TappRegistry — every existing user must call `cmd/user acknowledge` again before further vouchers settle
- **`RESOURCE_LIMITS_DISABLED=false` on the runner service is load-bearing.** The `daytonaio/daytona-runner` image bakes `ENV RESOURCE_LIMITS_DISABLED=true` into its Dockerfile, so omitting the var in compose falls back to `true` and the runner skips setting Docker CFS / memory cgroup limits — every sandbox runs unconstrained on host resources regardless of snapshot tier. To verify: `docker exec 0g-sandbox-runner-1 env | grep RESOURCE_LIMITS` should print `=false`, and any sandbox container's `docker inspect` should show non-zero `HostConfig.CpuQuota` / `Memory`.
